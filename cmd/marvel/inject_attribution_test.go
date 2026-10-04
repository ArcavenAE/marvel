package main

import (
	"strings"
	"testing"
	"time"
)

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

func TestApplyInjectOptionsClearsFirstAndVerifiesLast(t *testing.T) {
	steps := []injectStep{{Text: "hello", Literal: true}, {Text: "Enter"}}
	got := applyInjectOptions(steps, true, true, 1500*time.Millisecond)
	if !got[0].Clear || got[0].Verify {
		t.Errorf("first step = %+v, want clear and no verify", got[0])
	}
	if got[1].Clear || !got[1].Verify {
		t.Errorf("last step = %+v, want verify and no clear", got[1])
	}
	for i, s := range got {
		if s.SettleMS != 1500 {
			t.Errorf("step %d settle = %d, want 1500", i, s.SettleMS)
		}
	}
	if one := applyInjectOptions([]injectStep{{Text: "x", Literal: true}}, true, true, 0); !one[0].Clear || !one[0].Verify {
		t.Errorf("a single step = %+v, want both", one[0])
	}
	plain := applyInjectOptions([]injectStep{{Text: "x", Literal: true}}, false, false, time.Second)
	if plain[0].Clear || plain[0].Verify || plain[0].SettleMS != 0 {
		t.Errorf("no options = %+v, want the step untouched", plain[0])
	}
}

func TestInjectRequestParamsCarryVerifyClearAndSettle(t *testing.T) {
	p := injectRequestParams("ws/sess", injectStep{Text: "hi", Literal: true, Clear: true, Verify: true, SettleMS: 900})
	if p["clear"] != true || p["verify"] != true || p["settle_ms"] != 900 {
		t.Errorf("params = %v, want clear, verify and settle_ms 900", p)
	}
	q := injectRequestParams("ws/sess", injectStep{Text: "hi", Literal: true})
	for _, k := range []string{"clear", "verify", "settle_ms"} {
		if _, ok := q[k]; ok {
			t.Errorf("params carry %q for a plain inject: %v", k, q)
		}
	}
}

// A verified inject whose effect was not seen exits non-zero, so a script
// driving a handoff does not take a typed-but-unsent message for a delivered one.
func TestUnconfirmedErrorFollowsTheDaemonsVerdict(t *testing.T) {
	err := unconfirmedError("ws/sess", []byte(`{"status":"injected","verify":"unconfirmed","composer":"unknown"}`))
	if err == nil || !strings.Contains(err.Error(), "ws/sess") || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error = %v, want one naming the session and the composer state", err)
	}
	for _, ok := range []string{
		`{"status":"injected"}`,
		`{"status":"injected","verify":"confirmed","composer":"empty"}`,
		`{"status":"injected","verify":"skipped"}`,
		`not json`,
	} {
		if err := unconfirmedError("ws/sess", []byte(ok)); err != nil {
			t.Errorf("%s: error = %v, want none", ok, err)
		}
	}
}

// --allow-bare-digit reaches the daemon as allow_bare_digit on every step, and a
// plain inject carries no such field.
func TestAllowBareDigitReachesTheRequest(t *testing.T) {
	steps := []injectStep{{Text: "3", Literal: true}, {Text: "Enter"}}
	for i, s := range withAllowBareDigit(steps, true) {
		if p := injectRequestParams("ws/sess", s); p["allow_bare_digit"] != true {
			t.Errorf("step %d params = %v, want allow_bare_digit true", i, p)
		}
	}
	for i, s := range withAllowBareDigit(steps, false) {
		if p := injectRequestParams("ws/sess", s); p["allow_bare_digit"] != nil {
			t.Errorf("step %d carries allow_bare_digit without the flag: %v", i, p)
		}
	}
	if steps[0].AllowBareDigit {
		t.Error("withAllowBareDigit changed its input")
	}
}
