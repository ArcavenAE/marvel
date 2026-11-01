package runtime

import (
	"strings"
	"testing"
)

func TestGooseContextLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		declared int
		model    string
		want     int
		wantNote bool
	}{
		{"both set, role lower", 2048, "qwen3:0.6b", 2048, false},
		{"both set, role equal", 4096, "qwen3:0.6b", 4096, false},
		{"both set, role higher draws a note", 32768, "qwen3:0.6b", 4096, true},
		{"table entry only", 0, "qwen3:0.6b", 4096, false},
		{"model with no entry, declared", 8192, "qwen3:8b", 8192, false},
		{"model with no entry, nothing declared", 0, "qwen3:8b", 0, false},
		{"no model, declared", 8192, "", 8192, false},
		{"neither", 0, "", 0, false},
		{"model name is trimmed", 0, " qwen3:0.6b ", 4096, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, note := gooseContextLimit(tc.declared, tc.model)
			if got != tc.want {
				t.Errorf("limit = %d, want %d", got, tc.want)
			}
			if (note != "") != tc.wantNote {
				t.Errorf("note = %q, wantNote %v", note, tc.wantNote)
			}
			if tc.wantNote && !strings.Contains(note, "goose arm 2") {
				t.Errorf("note %q does not cite the measuring run", note)
			}
		})
	}
}

func TestGooseCeilingEntriesCiteTheirRun(t *testing.T) {
	t.Parallel()
	for model, c := range gooseCeilings {
		if c.Tokens <= 0 || c.MeasuredBy == "" {
			t.Errorf("entry %q: tokens %d, measured_by %q; every entry needs a ceiling and the run that measured it", model, c.Tokens, c.MeasuredBy)
		}
	}
}
