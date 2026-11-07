package main

import (
	"os"
	"testing"
)

// The 2.1.226 live capture carries a top-level "version", which the
// forwarder used to discard along with every other key it did not declare.
func TestStatuslineVersionFromLiveCapture(t *testing.T) {
	raw, err := os.ReadFile("testdata/statusline-2.1.226-empty.json")
	if err != nil {
		t.Fatal(err)
	}
	got := statuslineVersion(raw)
	if got == nil || *got != "2.1.226" {
		t.Fatalf("statuslineVersion = %v, want 2.1.226", got)
	}
}

// Only a bare x.y.z is forwarded. A missing key, a non-string and anything
// else is nil, which the daemon reads as "leave the stored value".
func TestStatuslineVersion(t *testing.T) {
	for _, tc := range []struct {
		name, payload, want string
	}{
		{"plain", `{"version":"2.1.319"}`, "2.1.319"},
		{"missing key", `{"model":{"display_name":"m"}}`, ""},
		{"empty", `{"version":""}`, ""},
		{"null", `{"version":null}`, ""},
		{"number", `{"version":2.1}`, ""},
		{"two parts", `{"version":"2.1"}`, ""},
		{"prerelease", `{"version":"2.1.319-beta.1"}`, ""},
		{"v prefix", `{"version":"v2.1.319"}`, ""},
		{"four parts", `{"version":"1.2.3.4"}`, ""},
		{"trailing newline", "{\"version\":\"2.1.319\\n\"}", ""},
		{"padded", `{"version":" 2.1.319"}`, ""},
		{"non-ascii digits", `{"version":"２.１.３"}`, ""},
		{"nested only", `{"x":{"version":"2.1.319"}}`, ""},
		{"unreadable", `not json`, ""},
	} {
		got := statuslineVersion([]byte(tc.payload))
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: got %q, want nil", tc.name, *got)
		case tc.want != "" && (got == nil || *got != tc.want):
			t.Errorf("%s: got %v, want %q", tc.name, got, tc.want)
		}
	}
}
