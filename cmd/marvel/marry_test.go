package main

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"maps"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/shorttmp"
)

// marryVectorsFile is the shared golden file for the marrying step, the same
// one internal/api's tests and director (M2) read.
var marryVectorsFile = filepath.Join("..", "..", "contracts", "readiness", "marry-vectors.json")

// marryCLIStep is one marry step of the shared vectors. Input is the step's
// bytes exactly as the file holds them, so the CLI reads what a caller in
// another repo reads, not a copy re-encoded by this test.
type marryCLIStep struct {
	Input json.RawMessage
	Error string
	Want  json.RawMessage
}

// marryVectorSteps returns every marry step of every shared vector. The
// file's other ops (take, recheck, address, grep) have no CLI and are read by
// internal/api's tests.
func marryVectorSteps(t *testing.T) map[string]marryCLIStep {
	t.Helper()
	b, err := os.ReadFile(marryVectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []struct {
			Name  string `json:"name"`
			Steps []struct {
				Op    string          `json:"op"`
				Input json.RawMessage `json:"input"`
				Error string          `json:"error"`
				Want  json.RawMessage `json:"want"`
			} `json:"steps"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	out := map[string]marryCLIStep{}
	for _, v := range file.Vectors {
		for i, st := range v.Steps {
			if st.Op != "marry" {
				continue
			}
			if len(st.Input) == 0 || string(st.Input) == "null" {
				t.Fatalf("%s step %d: no input", v.Name, i)
			}
			out[v.Name+"/"+strconv.Itoa(i)] = marryCLIStep{st.Input, st.Error, st.Want}
		}
	}
	if len(out) == 0 {
		t.Fatal("no marry steps in the vectors, so the test checks nothing")
	}
	return out
}

// pairingVectorInput returns, as JSON, the input of a vector step that
// pairs at least one item, so a run with it goes through the whole step.
func pairingVectorInput(t *testing.T) string {
	t.Helper()
	steps := marryVectorSteps(t)
	for _, name := range slices.Sorted(maps.Keys(steps)) {
		st := steps[name]
		var want api.MarryOutput
		if st.Error == "" && json.Unmarshal(st.Want, &want) == nil && len(want.Pairs) > 0 {
			return string(st.Input)
		}
	}
	t.Fatal("no vector pairs anything")
	return ""
}

// runMarryCLI runs `marvel marry` with args through the real command tree,
// with stdin as given, and returns stdout, stderr and the error main would
// turn into a nonzero exit.
func runMarryCLI(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"marry"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

// Every marry vector, run through the CLI, gives the vector's output
// (compared decoded, as the Go tests compare it) or fails with its error on
// stderr and a nonzero exit. Each vector's input goes in as the file's own
// bytes, once as a file and once on stdin.
func TestMarryVerbRunsTheVectors(t *testing.T) {
	for name, st := range marryVectorSteps(t) {
		in := []byte(st.Input)
		path := filepath.Join(t.TempDir(), "input.json")
		if err := os.WriteFile(path, in, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, how := range []struct {
			via, stdin, input string
		}{{"file", "", path}, {"stdin", string(in), "-"}} {
			stdout, stderr, err := runMarryCLI(t, how.stdin, "--input", how.input)
			if st.Error != "" {
				if err == nil || !strings.Contains(stderr, st.Error) || stdout != "" {
					t.Errorf("%s via %s: err %v, stderr %q, stdout %q; want a failure naming %q and no output", name, how.via, err, stderr, stdout, st.Error)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s via %s: %v: %s", name, how.via, err, stderr)
				continue
			}
			var got, want api.MarryOutput
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Errorf("%s via %s: stdout is not a MarryOutput: %v: %q", name, how.via, err, stdout)
				continue
			}
			if err := json.Unmarshal(st.Want, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s via %s\n got %s\nwant %s", name, how.via, stdout, st.Want)
			}
		}
	}
}

// Input the contract does not describe is refused, not half read: an unknown
// field, a file that is not JSON, and a missing file each fail with nothing
// on stdout.
func TestMarryVerbRefusesInputOutsideTheContract(t *testing.T) {
	// Each bad input is a good one changed in one place, so it is refused for
	// that change and not for a missing field.
	good := pairingVectorInput(t)
	if _, stderr, err := runMarryCLI(t, good, "--input", "-"); err != nil {
		t.Fatalf("the good input is refused, so the cases below prove nothing: %v: %s", err, stderr)
	}
	dir := t.TempDir()
	for name, c := range map[string]struct{ stdin, input string }{
		"an unknown field":    {`{"surprise":1,` + good[1:], "-"},
		"not JSON":            {`items: []`, "-"},
		"a second document":   {good + ` {}`, "-"},
		"a file that is gone": {"", filepath.Join(dir, "missing.json")},
	} {
		stdout, stderr, err := runMarryCLI(t, c.stdin, "--input", c.input)
		if err == nil || stdout != "" || stderr == "" {
			t.Errorf("%s: err %v, stdout %q, stderr %q; want a failure on stderr only", name, err, stdout, stderr)
		}
	}
	if _, _, err := runMarryCLI(t, ""); err == nil {
		t.Error("marry with no --input succeeded")
	}
}

// The verb never reaches a daemon, at either address a client would dial
// with no flag: the one MARVEL_SOCKET names, and the layout default under
// HOME when it names none. A listener stands at that address for the run; the
// verb must succeed and the listener must see no connection.
func TestMarryVerbDialsNoDaemon(t *testing.T) {
	t.Run("MARVEL_SOCKET", func(t *testing.T) {
		sock := filepath.Join(shorttmp.Dir(t), "m.sock")
		t.Setenv("MARVEL_SOCKET", sock)
		marryDialsNothingAt(t, sock)
	})
	t.Run("the layout default", func(t *testing.T) {
		home := shorttmp.Dir(t)
		t.Setenv("HOME", home)
		t.Setenv("MARVEL_SOCKET", "")
		sock := config.DefaultSocket()
		if !strings.HasPrefix(sock, home+string(filepath.Separator)) {
			t.Fatalf("the default socket %s is not under the test's HOME %s", sock, home)
		}
		if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
			t.Fatal(err)
		}
		marryDialsNothingAt(t, sock)
	})
}

// marryDialsNothingAt listens at sock, proves the listener answers a dial,
// then runs the verb on a pairing input and fails if it connects.
func marryDialsNothingAt(t *testing.T, sock string) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan error, 1)
	accept := func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
		accepted <- err
	}
	go accept()

	// Positive control: the listener answers a dial, so a verb that dialed
	// would be seen.
	probe, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("the listener does not answer, so the check proves nothing: %v", err)
	}
	_ = probe.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("control accept: %v", err)
	}
	go accept()

	// A vector input that pairs, so the run goes through the whole step.
	stdout, stderr, err := runMarryCLI(t, pairingVectorInput(t), "--input", "-")
	if err != nil || !strings.Contains(stdout, `"pairs":[{`) {
		t.Fatalf("marry: %v: stdout %q, stderr %q", err, stdout, stderr)
	}
	select {
	case err := <-accepted:
		if err == nil {
			t.Fatalf("marry connected to %s", sock)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// marry.go imports nothing that reaches a daemon, a socket, the network or
// another process: it decodes, calls api.Marry and encodes.
func TestMarryVerbImportsNoTransport(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "marry.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"encoding/json", "errors", "fmt", "io", "os", "github.com/arcavenae/marvel/internal/api", "github.com/spf13/cobra"}
	if len(f.Imports) == 0 {
		t.Fatal("marry.go has no imports, so the check read nothing")
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if !slices.Contains(allowed, p) {
			t.Errorf("marry.go imports %s", p)
		}
	}
}
