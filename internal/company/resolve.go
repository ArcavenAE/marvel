package company

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readLink is how Resolve reads current, so a test can count the reads.
var readLink = os.Readlink

// Env is what the resolver and Check read from outside the package: the
// configured company roots in configuration order, the tools and vars set with
// marvel config, and the running marvel's version. A test fakes it; the daemon
// supplies the real one.
//
// Owner reading, proposed for review before merge: the interface and its four
// methods, including MarvelVersion.
type Env interface {
	Roots() ([]string, error)
	Tool(name string) (path string, ok bool)
	Var(name string) (value string, ok bool)
	MarvelVersion() string
}

// Pin is one pin of a team: the key that carries it (company or recipes), the
// library name, and the exact version, or empty when the pin follows current.
type Pin struct {
	Team, Key, Name, Version string
}

// Library is one resolved library: the version directory it was read from and
// the bytes of its company.toml, read through that directory.
type Library struct {
	Ident
	Root string
	Dir  string
	Data []byte
}

// holder is one root that holds the pinned name, with the versions it lists.
type holder struct {
	root     string
	versions map[string]bool
}

// Resolve finds the pinned library in the roots and resolves its version
// (steps 1 and 2 of the checks at launch). It reads real directories only,
// through os.Lstat, so a symlinked name or version directory is skipped. It
// skips any entry whose name starts with a dot, the name current, and any
// version directory without a company.toml, and it reads current once, with
// os.Readlink, only for a pin that names no version. Every read goes through
// the resolved version directory. A name held in two roots is refused, and a
// pin that cannot be found is refused with no fallback to current.
func Resolve(env Env, pin Pin) (Library, error) {
	roots, err := env.Roots()
	if err != nil {
		return Library{}, fmt.Errorf("read company roots: %w", err)
	}
	var holders []holder
	if validName(pin.Name) {
		for _, root := range roots {
			if vs := listVersions(filepath.Join(root, pin.Name)); len(vs) > 0 {
				holders = append(holders, holder{root: root, versions: vs})
			}
		}
	}
	switch {
	case len(holders) > 1:
		return Library{}, refuse("13", pin.Name, holders[0].root, holders[1].root)
	case len(holders) == 0:
		return Library{}, absent(pin, roots)
	}
	h := holders[0]
	version := pin.Version
	if version == "" {
		target, err := readLink(filepath.Join(h.root, pin.Name, "current"))
		if err != nil {
			return Library{}, refuse("N9b", pin.Team, pin.Key, pin.Name, h.root)
		}
		if !singleElement(target) {
			return Library{}, refuse("N10", pin.Name, h.root, pin.Name)
		}
		if !h.versions[target] {
			return Library{}, refuse("N9a", pin.Team, pin.Key, pin.Name, target)
		}
		version = target
	} else if !h.versions[version] {
		return Library{}, absent(pin, roots)
	}
	dir := filepath.Join(h.root, pin.Name, version)
	data, err := os.ReadFile(filepath.Join(dir, "company.toml"))
	if err != nil {
		return Library{}, fmt.Errorf("read company.toml of %s@%s: %w", pin.Name, version, err)
	}
	return Library{Ident: Ident{Name: pin.Name, Version: version}, Root: h.root, Dir: dir, Data: data}, nil
}

// absent is the refusal for a pin no root holds: row 2 for an exact pin and N8
// for a name-only pin, each naming the roots that were read.
func absent(pin Pin, roots []string) error {
	read := "none"
	if len(roots) > 0 {
		q := make([]string, len(roots))
		for i, r := range roots {
			q[i] = quoteField(r, false)
		}
		read = strings.Join(q, ", ")
	}
	if pin.Version == "" {
		return refuse("N8", pin.Team, pin.Key, pin.Name, read)
	}
	return refuse("2", pin.Team, pin.Key, pin.Name, pin.Version, read)
}

// validName reports whether name can be one directory under a root.
func validName(name string) bool {
	return singleElement(name) && !strings.HasPrefix(name, ".")
}

// singleElement reports whether s is one path element: not empty, not . or ..,
// and holding no separator.
func singleElement(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsRune(s, '/')
}

// listVersions lists the version directories of one name under a root. The name
// directory must itself be a real directory. A version is a real directory, not
// a dot name and not current, that holds a regular file company.toml.
func listVersions(nameDir string) map[string]bool {
	if fi, err := os.Lstat(nameDir); err != nil || !fi.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(nameDir)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") || n == "current" || !e.IsDir() {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(nameDir, n, "company.toml")); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out[n] = true
	}
	return out
}
