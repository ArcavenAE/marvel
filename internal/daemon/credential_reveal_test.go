package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

// revealName is the credential the reveal tests seed and read back. It carries
// a slash, the shape the operator ruling uses (bus/leaf), so the tests also
// prove a slashed credential name round-trips through reveal. revealSecret is
// its value, distinct from the S2 tests' testSecret.
const (
	revealName   = "bus/leaf"
	revealSecret = "LEAFNKEYSEED"
)

// seedLeaf stores one credential as the local admin and returns the daemon.
func seedLeaf(t *testing.T) *Daemon {
	t.Helper()
	d := newHandlerDaemon(t)
	if resp := d.dispatchAs(Request{Method: "credential.put", Params: putParams(t, revealName, revealSecret)}, localCaller()); resp.Error != "" {
		t.Fatalf("seed put: %s", resp.Error)
	}
	return d
}

func TestCredentialRevealOnLocalSocketReturnsValue(t *testing.T) {
	d := seedLeaf(t)

	resp := d.dispatchAs(Request{Method: "credential.reveal", Params: nameParams(t, revealName)}, localCaller())
	if resp.Error != "" {
		t.Fatalf("reveal on local socket: %s", resp.Error)
	}
	var rv struct {
		Value []byte `json:"value"`
	}
	if err := json.Unmarshal(resp.Result, &rv); err != nil {
		t.Fatalf("unmarshal reveal: %v", err)
	}
	if string(rv.Value) != revealSecret {
		t.Errorf("revealed value = %q, want the stored secret", rv.Value)
	}

	// The reveal is audited: an event names the credential, never the value.
	evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialRevealed}, 0)
	if len(evs) != 1 {
		t.Fatalf("credential.revealed events = %d, want 1", len(evs))
	}
	if !strings.Contains(evs[0].Message, revealName) {
		t.Errorf("reveal event %q does not name the credential", evs[0].Message)
	}
	if strings.Contains(evs[0].Message, revealSecret) {
		t.Errorf("reveal event leaked the value: %q", evs[0].Message)
	}
}

func TestCredentialRevealRefusedOverTunnel(t *testing.T) {
	d := seedLeaf(t)

	// An admin key arriving over mrvl:// (local=false) is refused: reveal is
	// local-socket only, whatever authority the key holds.
	tunnelAdmin := caller{scope: ScopeAdmin, fingerprint: "SHA256:adminkey"}
	resp := d.dispatchAs(Request{Method: "credential.reveal", Params: nameParams(t, revealName)}, tunnelAdmin)
	if !strings.Contains(resp.Error, "only available on the local unix socket") {
		t.Errorf("tunnelled admin reveal: error = %q, want a local-socket refusal", resp.Error)
	}
	if len(resp.Result) != 0 {
		t.Errorf("refused reveal still returned a result: %q", resp.Result)
	}
	// A refused reveal is not audited as a reveal.
	if evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialRevealed}, 0); len(evs) != 0 {
		t.Errorf("refused reveal emitted %d reveal events, want 0", len(evs))
	}
}

func TestCredentialRevealRefusedForPushKey(t *testing.T) {
	d := seedLeaf(t)

	// A credential-push key is stopped at the scope gate, before the local
	// gate: reveal is not in its method set.
	pushKey := caller{scope: ScopeCredentialPush, fingerprint: testFP}
	resp := d.dispatchAs(Request{Method: "credential.reveal", Params: nameParams(t, revealName)}, pushKey)
	if !strings.Contains(resp.Error, "is not permitted for a") {
		t.Errorf("credential-push reveal: error = %q, want a scope refusal", resp.Error)
	}
}
