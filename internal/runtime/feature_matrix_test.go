package runtime

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// docs/feature-matrix.md carries two things that can rot: a table of which
// optional adapter interfaces each adapter implements, and file:line citations.
// The table is derived from the registry here and compared with the document,
// so a new adapter, or a changed capability, fails this test until the document
// is regenerated. The citations are checked only for existence (the file is
// there and is at least that long); whether a line still says what the matrix
// claims is a review question, and the document says so.
//
// Regenerate the table with: MARVEL_UPDATE_MATRIX=1 go test ./internal/runtime -run TestFeatureMatrix

const (
	matrixDoc   = "../../docs/feature-matrix.md"
	matrixBegin = "<!-- adapter-capabilities:begin -->"
	matrixEnd   = "<!-- adapter-capabilities:end -->"
)

// matrixAdapters is the column order of the document. A registered adapter that
// is not listed here fails TestFeatureMatrixNamesEveryRegisteredAdapter.
var matrixAdapters = []string{"claude", "codex", "opencode", "forestage", "simulator", "generic"}

type matrixCapability struct {
	name string
	has  func(Adapter) bool
}

func matrixCapabilities() []matrixCapability {
	return []matrixCapability{
		{"StreamCapable", func(a Adapter) bool { _, ok := a.(StreamCapable); return ok }},
		{"SessionIDAssigner", func(a Adapter) bool { _, ok := a.(SessionIDAssigner); return ok }},
		{"SessionHomeAssigner", func(a Adapter) bool { _, ok := a.(SessionHomeAssigner); return ok }},
		{"StatuslineFeeder", func(a Adapter) bool { _, ok := a.(StatuslineFeeder); return ok }},
		{"ComposerCapable", func(a Adapter) bool { _, ok := a.(ComposerCapable); return ok }},
		{"ForegroundRule", func(a Adapter) bool { _, ok := a.(ForegroundRule); return ok }},
	}
}

func capabilityTable(t *testing.T) string {
	t.Helper()
	reg := NewRegistry()
	var b strings.Builder
	b.WriteString("| Capability | " + strings.Join(matrixAdapters, " | ") + " |\n")
	b.WriteString("|---|" + strings.Repeat("---|", len(matrixAdapters)) + "\n")
	for _, c := range matrixCapabilities() {
		cells := make([]string, 0, len(matrixAdapters))
		for _, name := range matrixAdapters {
			a := reg.Resolve(name)
			if a.Name() != name {
				t.Fatalf("adapter %q resolves to %q", name, a.Name())
			}
			if c.has(a) {
				cells = append(cells, "yes")
			} else {
				cells = append(cells, "no")
			}
		}
		fmt.Fprintf(&b, "| %s | %s |\n", c.name, strings.Join(cells, " | "))
	}
	return b.String()
}

func readMatrixDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(matrixDoc)
	if err != nil {
		t.Fatalf("read %s: %v", matrixDoc, err)
	}
	return string(raw)
}

func TestFeatureMatrixNamesEveryRegisteredAdapter(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	listed := map[string]bool{}
	for _, n := range matrixAdapters {
		listed[n] = true
	}
	for name := range reg.adapters {
		if !listed[name] {
			t.Errorf("adapter %q is registered but has no column in docs/feature-matrix.md (add it to matrixAdapters and the document)", name)
		}
	}
	for _, n := range matrixAdapters {
		if _, ok := reg.adapters[n]; !ok {
			t.Errorf("matrixAdapters names %q, which is not registered", n)
		}
	}
}

func TestFeatureMatrixAdapterTableMatchesTheCode(t *testing.T) {
	want := capabilityTable(t)
	doc := readMatrixDoc(t)
	i, j := strings.Index(doc, matrixBegin), strings.Index(doc, matrixEnd)
	if i < 0 || j < i {
		t.Fatalf("%s lacks the %s ... %s block", matrixDoc, matrixBegin, matrixEnd)
	}
	got := doc[i+len(matrixBegin)+1 : j]
	if os.Getenv("MARVEL_UPDATE_MATRIX") != "" && got != want {
		updated := doc[:i+len(matrixBegin)+1] + want + doc[j:]
		if err := os.WriteFile(matrixDoc, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if got != want {
		t.Errorf("the adapter capability table in docs/feature-matrix.md is out of date.\nwant:\n%s\ngot:\n%s\nregenerate: MARVEL_UPDATE_MATRIX=1 go test ./internal/runtime -run TestFeatureMatrix", want, got)
	}
}

var matrixCite = regexp.MustCompile("`((?:internal|cmd)/[A-Za-z0-9_./-]+\\.(?:go|md|yaml)):([0-9]+)(?:-([0-9]+))?`")

// Every file:line citation points at a file that exists and is long enough.
func TestFeatureMatrixCitationsExist(t *testing.T) {
	t.Parallel()
	doc := readMatrixDoc(t)
	cites := matrixCite.FindAllStringSubmatch(doc, -1)
	if len(cites) == 0 {
		t.Fatal("the document carries no citations")
	}
	root := filepath.Join("..", "..")
	for _, m := range cites {
		path, first, last := m[1], m[2], m[3]
		lines := countLines(t, filepath.Join(root, path))
		if lines < 0 {
			t.Errorf("citation %s:%s names a file that does not exist", path, first)
			continue
		}
		hi, _ := strconv.Atoi(first)
		if last != "" {
			hi, _ = strconv.Atoi(last)
		}
		if hi > lines {
			t.Errorf("citation %s is past the end of the file (%d lines)", m[0], lines)
		}
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	return bytes.Count(raw, []byte("\n"))
}
