package daemon

import "testing"

type sendCall struct {
	pane, text     string
	literal, enter bool
}

type fakeKeys struct{ calls []sendCall }

func (f *fakeKeys) SendKeys(pane, text string, literal, enter bool) error {
	f.calls = append(f.calls, sendCall{pane, text, literal, enter})
	return nil
}

// The real sender, through a fake driver: exactly one SendKeys call, the digit 2,
// not literal, no Enter. Rewriting the key or turning Enter on fails this
// (marvel#556 review mutants c and d).
func TestLimitSenderSendsExactlyTheDigitTwo(t *testing.T) {
	t.Parallel()
	fake := &fakeKeys{}
	if err := limitSender(fake)("%9"); err != nil {
		t.Fatal(err)
	}
	want := sendCall{pane: "%9", text: "2", literal: false, enter: false}
	if len(fake.calls) != 1 || fake.calls[0] != want {
		t.Fatalf("calls = %+v, want exactly [%+v]", fake.calls, want)
	}
}
