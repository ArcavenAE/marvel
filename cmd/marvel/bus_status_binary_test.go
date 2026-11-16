package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/bus"
)

// busStatusFromJSON builds the Status the way the daemon sends it, so the test
// compiles against the Status of today and fails on a field it does not yet
// carry.
func busStatusFromJSON(t *testing.T, body string) bus.Status {
	t.Helper()
	var st bus.Status
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// `marvel bus status` prints the binary rows (aae-orc-ktts0): the path, its
// realpath, whether it exists, and the serving version, with a skew between
// the serving version and the file on disk named as a diagnostic.
func TestPrintBusStatusNamesTheBinaryItsRealpathAndTheServingVersion(t *testing.T) {
	st := busStatusFromJSON(t, `{"managed":true,"leaf":"n/a","url":"u","listen":"l","domain":"d",
		"version":"v2.14.6","binary":"/opt/pin/bin/nats-server","binary_configured":true,
		"binary_realpath":"/opt/kegs/nats-server/2.14.6/bin/nats-server","binary_exists":true,
		"serving_version":"v2.14.6"}`)
	var b strings.Builder
	printBusStatus(&b, st)
	got := b.String()
	for _, want := range []string{
		"binary:   /opt/pin/bin/nats-server (bus.binary)",
		"realpath: /opt/kegs/nats-server/2.14.6/bin/nats-server",
		"exists:   yes",
		"serving:  v2.14.6",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "differs") {
		t.Errorf("equal versions must not print a skew line:\n%s", got)
	}

	// A removed file and a broker still serving an older version.
	st = busStatusFromJSON(t, `{"managed":true,"leaf":"n/a","url":"u","listen":"l","domain":"d",
		"version":"v2.14.7","binary":"/opt/pin/bin/nats-server","binary_configured":true,
		"binary_exists":false,"serving_version":"v2.14.5"}`)
	b.Reset()
	printBusStatus(&b, st)
	got = b.String()
	for _, want := range []string{
		"exists:   no",
		"serving:  v2.14.5 (differs from the binary on disk, v2.14.7; diagnostic only)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}
}
