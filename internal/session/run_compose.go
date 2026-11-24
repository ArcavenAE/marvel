package session

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// ErrRunProfileRefused is wrapped by every refusal of a requested run profile.
var ErrRunProfileRefused = errors.New("run profile refused")

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRunProfileRefused, fmt.Sprintf(format, a...))
}

// sexp is one node of a read profile: a list, an atom or a string literal.
type sexp struct {
	list  []sexp
	atom  string
	str   string
	isStr bool
	isLst bool
}

var atomPattern = regexp.MustCompile(`^[A-Za-z0-9*_.:/+-]+$`)

// readForms reads profile text into top-level forms. Comments are dropped.
// Anything unbalanced, an unterminated string, a bare atom at the top, a
// backslash or control byte in a string, or a byte outside an atom's alphabet is
// an error.
func readForms(text string) ([]sexp, error) {
	var stack [][]sexp
	var top []sexp
	push := func(n sexp) {
		if len(stack) == 0 {
			top = append(top, n)
			return
		}
		stack[len(stack)-1] = append(stack[len(stack)-1], n)
	}
	for i := 0; i < len(text); {
		c := text[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			i++
		case ';':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case '(':
			stack = append(stack, nil)
			i++
		case ')':
			if len(stack) == 0 {
				return nil, refuse("a closing parenthesis with no open form")
			}
			n := sexp{isLst: true, list: stack[len(stack)-1]}
			stack = stack[:len(stack)-1]
			push(n)
			i++
		case '"':
			j := i + 1
			for j < len(text) && text[j] != '"' {
				if text[j] == '\\' || text[j] < 0x20 || text[j] == 0x7f {
					return nil, refuse("a string holds a backslash or control byte")
				}
				j++
			}
			if j >= len(text) {
				return nil, refuse("an unterminated string")
			}
			push(sexp{isStr: true, str: text[i+1 : j]})
			i = j + 1
		default:
			j := i
			for j < len(text) && !strings.ContainsRune(" \t\n\r();\"", rune(text[j])) {
				j++
			}
			a := text[i:j]
			if !atomPattern.MatchString(a) {
				return nil, refuse("%q is not a plain atom", a)
			}
			push(sexp{atom: a})
			i = j
		}
	}
	if len(stack) != 0 {
		return nil, refuse("an unterminated form")
	}
	for _, f := range top {
		if !f.isLst {
			return nil, refuse("a bare %s at the top level", describeAtom(f))
		}
	}
	return top, nil
}

func describeAtom(n sexp) string {
	if n.isStr {
		return "string"
	}
	return "atom " + n.atom
}

func (n sexp) String() string {
	switch {
	case n.isStr:
		return `"` + n.str + `"`
	case n.isLst:
		parts := make([]string, len(n.list))
		for i, c := range n.list {
			parts[i] = c.String()
		}
		return "(" + strings.Join(parts, " ") + ")"
	}
	return n.atom
}

func (n sexp) head() string {
	if n.isLst && len(n.list) > 0 && !n.list[0].isStr && !n.list[0].isLst {
		return n.list[0].atom
	}
	return ""
}

// netAllowForms are the only allow forms a run profile may carry: the localhost
// network, bind and inbound allows of the run-conditions profile form.
var netAllowForms = map[string]bool{
	`(allow network* (remote ip "localhost:*"))`:       true,
	`(allow network-bind (local ip "localhost:*"))`:    true,
	`(allow network-inbound (local ip "localhost:*"))`: true,
}

// checkFilter accepts the closed set of deny filters: path filters over a
// string literal, the require combinators, and an address filter.
func checkFilter(n sexp) error {
	if !n.isLst || len(n.list) == 0 {
		return refuse("a filter is not a form")
	}
	switch h := n.head(); h {
	case "subpath", "literal", "prefix":
		if len(n.list) != 2 || !n.list[1].isStr {
			return refuse("(%s ...) takes one string literal", h)
		}
	case "require-not", "require-all", "require-any":
		if len(n.list) < 2 {
			return refuse("(%s) is empty", h)
		}
		for _, c := range n.list[1:] {
			if err := checkFilter(c); err != nil {
				return err
			}
		}
	case "remote", "local":
		if len(n.list) != 3 || n.list[1].isLst || n.list[1].isStr || n.list[2].isLst || !n.list[2].isStr ||
			(n.list[1].atom != "ip" && n.list[1].atom != "tcp" && n.list[1].atom != "udp") {
			return refuse("(%s ...) takes ip, tcp or udp and one string literal", h)
		}
	default:
		return refuse("the filter %q is outside the allowed set", h)
	}
	return nil
}

func checkForm(f sexp) error {
	switch f.head() {
	case "deny":
		if len(f.list) < 2 || f.list[1].isLst || f.list[1].isStr {
			return refuse("(deny ...) must name an operation first")
		}
		for _, c := range f.list[1:] {
			switch {
			case c.isStr:
				return refuse("(deny ...) holds a bare string")
			case c.isLst:
				if err := checkFilter(c); err != nil {
					return err
				}
			}
		}
		return nil
	case "allow":
		if netAllowForms[f.String()] {
			return nil
		}
		return refuse("%s is an allow outside the localhost network form", f.String())
	case "":
		return refuse("a form with no head")
	default:
		return refuse("(%s ...) is outside the run-profile grammar", f.head())
	}
}

var (
	roleNetworkWord   = regexp.MustCompile(`network`)
	roleAllowsDefault = regexp.MustCompile(`\(\s*allow\s+default\s*\)`)
	roleVersionForm   = regexp.MustCompile(`\(\s*version\s+1\s*\)`)
	roleParamForm     = regexp.MustCompile(`\(\s*param\s+"([A-Za-z0-9_]+)"\s*\)`)
)

// ComposeRunProfile joins the parts of a sandboxed run's profile into one text:
// the role's own profile, then the requested run profile, then the deny block.
// The request is read into forms and re-serialized, never copied as text, and
// refused unless every form is a deny or one of the three localhost network
// allows the run conditions use (seat-tcc-deny.md, sections 5 and 14; marvel#1596
// rulings Q1 and Q3). The network allows are also refused unless the role has no
// profile or is (allow default) with no network rule, since they would widen it. A role
// with no profile gets (version 1)(allow default).
func ComposeRunProfile(role, requested, block string) (string, error) {
	forms, err := readForms(requested)
	if err != nil {
		return "", err
	}
	roleCode := sbplCode([]byte(role))
	// The network allows are accepted only where they cannot widen the role: a
	// role with no profile (the base allows everything), or one that is
	// (allow default) and names no network operation. A deny-default role, a role
	// with a network rule, and a role naming neither default are refused.
	networkAllowOK := strings.TrimSpace(roleCode) == "" ||
		(roleAllowsDefault.MatchString(roleCode) && !roleNetworkWord.MatchString(roleCode))
	var b strings.Builder
	switch {
	case strings.TrimSpace(roleCode) == "":
		b.WriteString("(version 1)\n(allow default)\n")
	default:
		if !roleVersionForm.MatchString(roleCode) {
			b.WriteString("(version 1)\n")
		}
		b.WriteString(role)
		if !strings.HasSuffix(role, "\n") {
			b.WriteString("\n")
		}
	}
	for _, f := range forms {
		if err := checkForm(f); err != nil {
			return "", err
		}
		if f.head() == "allow" && !networkAllowOK {
			return "", refuse("%s would widen the role's own profile, which is not (allow default) without network rules", f.String())
		}
		b.WriteString(f.String())
		b.WriteString("\n")
	}
	b.WriteString(block)
	if !strings.HasSuffix(block, "\n") {
		b.WriteString("\n")
	}
	return b.String(), nil
}

// RunSandboxParams returns the -D arguments a composed run needs: one KEY=VALUE
// for each (param "KEY") the role's code names, taken from vals. A named
// parameter with no value is an error.
func RunSandboxParams(role string, vals map[string]string) ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for _, m := range roleParamForm.FindAllStringSubmatch(sbplCode([]byte(role)), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		v, ok := vals[n]
		if !ok || v == "" {
			return nil, fmt.Errorf("the role's profile names the parameter %s and the run has no value for it", n)
		}
		out = append(out, "-D", n+"="+v)
	}
	return out, nil
}

// CheckRunHost refuses a host that cannot run a composed profile: not darwin,
// or no sandbox-exec at the path. A run is never started unwrapped.
func CheckRunHost(goos, sandboxExecPath string) error {
	if goos != "darwin" {
		return fmt.Errorf("sandboxed runs need darwin, this host is %s", goos)
	}
	info, err := os.Stat(sandboxExecPath)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("sandbox-exec is not at %s", sandboxExecPath)
	}
	return nil
}
