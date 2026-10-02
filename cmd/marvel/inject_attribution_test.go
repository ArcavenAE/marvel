package main

import "testing"

// The CLI declares the seat it runs in and the user, from its own environment,
// so a seat's inject is traceable to that seat.
func TestInjectRequestParamsDeclareTheCaller(t *testing.T) {
	t.Setenv("MARVEL_SESSION", "seat-x-g1-0")
	t.Setenv("USER", "example-user")
	p := injectRequestParams("ws/sess", injectStep{Text: "hi", Literal: true})
	in, ok := p["injector"].(map[string]string)
	if !ok {
		t.Fatalf("params carry no injector: %v", p)
	}
	if in["session"] != "seat-x-g1-0" || in["user"] != "example-user" {
		t.Errorf("injector = %v, want the session and user from the environment", in)
	}
}

// Outside a seat there is no session to declare; the user alone is declared.
func TestInjectRequestParamsOutsideASeat(t *testing.T) {
	t.Setenv("MARVEL_SESSION", "")
	t.Setenv("USER", "example-user")
	in, _ := injectRequestParams("ws/sess", injectStep{Text: "hi"})["injector"].(map[string]string)
	if _, has := in["session"]; has {
		t.Errorf("injector = %v, want no session outside a seat", in)
	}
	if in["user"] != "example-user" {
		t.Errorf("injector = %v, want the user", in)
	}
}
