package hostvitals

import (
	"errors"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxArgsBytes is how many bytes of a process's arguments a report keeps.
const MaxArgsBytes = 120

// MaxDetailReadsPerPass caps how many processes have their arguments and
// directory read on one pass.
const MaxDetailReadsPerPass = 5

const redacted = "<redacted>"

// secretName matches a name that suggests a secret value. It is matched on
// names only, case-insensitively, so the redaction is best effort: a positional
// secret, a short flag such as -p, and a secret inside a JSON argument are not
// recognised.
const secretName = `(?:token|secret|password|passwd|key|auth|bearer)`

var (
	// name=value anywhere in an argument: --token=x, API_KEY=x, ?token=x&n=1.
	assignRE = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*` + secretName + `[A-Za-z0-9_.-]*=)([^&]*)`)
	// Name: value, as in an HTTP header such as Authorization: Bearer x.
	headerRE = regexp.MustCompile(`(?i)^([A-Za-z0-9_-]*` + secretName + `[A-Za-z0-9_-]*:\s*)(.*)$`)
	// A flag whose name matches, with its value in the next argument.
	flagRE = regexp.MustCompile(`(?i)^--?[A-Za-z0-9_.-]*` + secretName + `[A-Za-z0-9_.-]*$`)
)

// RedactArgs renders a process's arguments as one line for a report: the value
// after a flag or NAME= whose name matches token, secret, password, passwd, key,
// auth or bearer becomes <redacted>, control bytes become '?', and the line is
// cut at MaxArgsBytes with "..." after it. The redaction runs before the cut, so
// a secret that straddles the cut leaves no prefix.
func RedactArgs(args []string) string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case headerRE.MatchString(a):
			a = headerRE.ReplaceAllString(a, "${1}"+redacted)
		case strings.Contains(a, "="):
			a = assignRE.ReplaceAllString(a, "${1}"+redacted)
		case flagRE.MatchString(a) && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			out = append(out, a, redacted)
			i++
			continue
		}
		out = append(out, a)
	}
	return truncateText(printableText(strings.Join(out, " ")), MaxArgsBytes)
}

// printableText replaces every control byte with '?', so a hostile argument or
// path cannot carry terminal control sequences into a table.
func printableText(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c < 0x20 || c == 0x7f {
			b[i] = '?'
		}
	}
	return string(b)
}

// truncateText cuts s at max bytes on a character boundary and adds "...".
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

const (
	textUnreadable = "unreadable"
	textUnknown    = "unknown"
)

// detailText turns what a source read into the two report strings. A process of
// another user, which the kernel will not let this one read, is "unreadable"; a
// working directory that cannot be read for any other reason is "unknown".
func detailText(d ProcDetail) (args, cwd string) {
	perm := errors.Is(d.ArgsErr, fs.ErrPermission) || errors.Is(d.CwdErr, fs.ErrPermission)
	if d.ArgsErr != nil {
		args = textUnreadable
	} else {
		args = RedactArgs(d.Args)
	}
	switch {
	case d.CwdErr != nil && perm:
		cwd = textUnreadable
	case d.CwdErr != nil, d.Cwd == "":
		cwd = textUnknown
	default:
		cwd = truncateText(printableText(d.Cwd), 4*MaxArgsBytes)
	}
	return args, cwd
}
