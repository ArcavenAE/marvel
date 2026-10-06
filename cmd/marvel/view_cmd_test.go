package main

import (
	"encoding/json"
	"testing"
)

func TestViewRefreshCommandShape(t *testing.T) {
	cmd := viewCmd()
	refresh, _, err := cmd.Find([]string{"refresh"})
	if err != nil || refresh.Name() != "refresh" {
		t.Fatalf("marvel view has no refresh subcommand: %v", err)
	}
	for _, args := range [][]string{{}, {"a", "b", "c"}} {
		if err := refresh.Args(refresh, args); err == nil {
			t.Errorf("refresh accepted %d arguments, want 1 or 2", len(args))
		}
	}
	for _, args := range [][]string{{"ws/seat"}, {"ws/seat", "repo"}} {
		if err := refresh.Args(refresh, args); err != nil {
			t.Errorf("refresh refused %v: %v", args, err)
		}
	}

	var p struct{ Session, Name string }
	if err := json.Unmarshal(viewRefreshParams([]string{"ws/seat", "repo"}), &p); err != nil || p.Session != "ws/seat" || p.Name != "repo" {
		t.Errorf("params = %+v, %v, want session ws/seat and name repo", p, err)
	}
	p = struct{ Session, Name string }{}
	if err := json.Unmarshal(viewRefreshParams([]string{"ws/seat"}), &p); err != nil || p.Name != "" {
		t.Errorf("one-argument params = %+v, %v, want an empty name", p, err)
	}
}
