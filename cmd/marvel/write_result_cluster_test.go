package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
)

// The writes an operator with several clusters configured can mistake for
// one another: each result line has to say which cluster it changed (#824,
// ship order item 5 of the multi-cluster read view, #816).
var writeCases = []struct {
	name   string
	method string
	args   []string
}{
	{"kill", "delete", []string{"kill", "w/s1"}},
	{"delete", "delete", []string{"delete", "session", "w/s1"}},
	{"inject", "inject", []string{"inject", "w/s1", "hello"}},
	{"scale", "scale", []string{"scale", "w/t", "--role", "r", "--replicas", "2"}},
	{"shift", "shift", []string{"shift", "w/t"}},
}

func TestWriteResultNamesCluster(t *testing.T) {
	for _, tc := range writeCases {
		t.Run(tc.name, func(t *testing.T) {
			a := startFake(t, fakeAnswers, []api.Session{})
			b := startFake(t, fakeAnswers, []api.Session{})
			clusterConfig(t,
				config.Cluster{Name: "alpha", Socket: a.sock},
				config.Cluster{Name: "beta", Socket: b.sock},
			)
			args := append(append([]string{}, tc.args...), "--cluster", "beta")
			code, out, errOut := runFanGet(t, args...)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if got := a.methods(); len(got) != 0 {
				t.Fatalf("alpha received %v; the write was meant for beta", got)
			}
			if got := b.methods(); len(got) != 1 || got[0] != tc.method {
				t.Fatalf("beta received %v, want one %s", got, tc.method)
			}
			if !strings.Contains(out, "cluster beta") {
				t.Errorf("result %q does not name cluster beta", out)
			}
			if strings.Contains(out, "alpha") {
				t.Errorf("result %q names the wrong cluster", out)
			}
		})
	}
}

// With no --cluster the selected cluster is the one acted on, and the line
// says so: the operator ruled on 2026-10-10 that a bare key keeps going
// there.
func TestWriteResultNamesSelectedCluster(t *testing.T) {
	for _, tc := range writeCases {
		t.Run(tc.name, func(t *testing.T) {
			a := startFake(t, fakeAnswers, []api.Session{})
			clusterConfig(t, config.Cluster{Name: "alpha", Socket: a.sock})
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.CurrentCluster = "alpha"
			if err := config.Save(cfg); err != nil {
				t.Fatal(err)
			}
			code, out, errOut := runFanGet(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if !strings.Contains(out, "cluster alpha") {
				t.Errorf("result %q does not name the selected cluster alpha", out)
			}
		})
	}
}

// A --socket names no cluster, so the line names the socket it wrote to.
func TestWriteResultNamesSocketOnSocketRung(t *testing.T) {
	for _, tc := range writeCases {
		t.Run(tc.name, func(t *testing.T) {
			a := startFake(t, fakeAnswers, []api.Session{})
			resolveFixture(t, false)
			t.Setenv("MARVEL_SESSION", "")
			args := append(append([]string{}, tc.args...), "--socket", a.sock)
			code, out, errOut := runFanGet(t, args...)
			if code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			if !strings.Contains(out, "socket "+filepath.ToSlash(a.sock)) {
				t.Errorf("result %q does not name socket %s", out, a.sock)
			}
			if strings.Contains(out, "cluster ") {
				t.Errorf("result %q names a cluster on the socket rung", out)
			}
		})
	}
}
