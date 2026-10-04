package limitmenu

import "testing"

func TestSelectionValidateAndSelects(t *testing.T) {
	after := Screen{Version: "v", Rows: []string{"Waiting", "Will continue at Oct 6 at 9pm"}, SpanRow: 1}
	ok := Selection{Version: "v", Key: "2", FromOption: 1, Took: 2, After: after}
	if !ok.SelectsOptionTwo() {
		t.Fatal("a valid option-2 measurement does not select")
	}
	bad := map[string]Selection{
		"no version":   {Key: "2", FromOption: 1, Took: 2, After: after},
		"no key":       {Version: "v", FromOption: 1, Took: 2, After: after},
		"row 0":        {Version: "v", Key: "2", Took: 2, After: after},
		"row 4":        {Version: "v", Key: "2", FromOption: 4, Took: 2, After: after},
		"version skew": {Version: "w", Key: "2", FromOption: 1, Took: 2, After: after},
		"bad screen":   {Version: "v", Key: "2", FromOption: 1, Took: 2, After: Screen{Version: "v"}},
	}
	for name, s := range bad {
		if s.Validate() == nil {
			t.Errorf("%s: validated", name)
		}
		if s.SelectsOptionTwo() {
			t.Errorf("%s: selects", name)
		}
	}
	for _, took := range []int{0, 1, 3} {
		s := ok
		s.Took = took
		if s.SelectsOptionTwo() {
			t.Errorf("took %d selects option 2", took)
		}
	}
	other := ok
	other.Key = "1"
	if other.SelectsOptionTwo() {
		t.Error("key 1 selects option 2")
	}
}
