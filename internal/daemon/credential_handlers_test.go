package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

const (
	testFP     = "SHA256:operatorkey"
	testSecret = "NKEYSEEDSECRET"
)

func putParams(t *testing.T, name, value string) json.RawMessage {
	t.Helper()
	p, err := json.Marshal(map[string]any{
		"name":     name,
		"kind":     string(api.CredentialNATSNKeySeed),
		"audience": "hub",
		"binding":  "write agent.director.>",
		"value":    []byte(value),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func nameParams(t *testing.T, name string) json.RawMessage {
	t.Helper()
	p, _ := json.Marshal(map[string]string{"name": name})
	return p
}

func TestCredentialPutStoresAndEmitsFingerprintNotValue(t *testing.T) {
	d := newHandlerDaemon(t)
	pushKey := caller{scope: ScopeCredentialPush, fingerprint: testFP}

	resp := d.dispatchAs(Request{Method: "credential.put", Params: putParams(t, "bus", testSecret)}, pushKey)
	if resp.Error != "" {
		t.Fatalf("credential.put by a credential-push key: %s", resp.Error)
	}

	// The result is metadata only, and carries the caller fingerprint.
	var meta api.Credential
	if err := json.Unmarshal(resp.Result, &meta); err != nil {
		t.Fatalf("unmarshal put result: %v", err)
	}
	if meta.IssuedBy != testFP {
		t.Errorf("IssuedBy = %q, want the caller fingerprint %q", meta.IssuedBy, testFP)
	}
	if len(meta.Value) != 0 {
		t.Errorf("put result carried a value; results are metadata only")
	}

	// The credential is in the store. Its value stays there for the S3 reveal;
	// the api package tests the value round-trip and zeroing, which this
	// package cannot see (the value is unexported and never returned).
	if _, err := d.store.GetCredential("bus"); err != nil {
		t.Errorf("GetCredential after put: %v", err)
	}

	// An event fired carrying the fingerprint and never the value.
	evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialPut}, 0)
	if len(evs) != 1 {
		t.Fatalf("credential.put events = %d, want 1", len(evs))
	}
	if !strings.Contains(evs[0].Message, testFP) {
		t.Errorf("event message %q does not name the caller fingerprint", evs[0].Message)
	}
	if strings.Contains(evs[0].Message, testSecret) {
		t.Errorf("event message leaked the credential value: %q", evs[0].Message)
	}
}

func TestCredentialGetIsAdminOnly(t *testing.T) {
	d := newHandlerDaemon(t)
	// Seed one credential (as the local admin).
	if resp := d.dispatchAs(Request{Method: "credential.put", Params: putParams(t, "bus", testSecret)}, localCaller()); resp.Error != "" {
		t.Fatalf("seed put: %s", resp.Error)
	}

	// A credential-push key may not read.
	resp := d.dispatchAs(Request{Method: "credential.get", Params: nameParams(t, "bus")}, caller{scope: ScopeCredentialPush, fingerprint: testFP})
	if !strings.Contains(resp.Error, "is not permitted for a") {
		t.Errorf("credential.get by a credential-push key: error = %q, want a scope refusal", resp.Error)
	}

	// The local admin may, and gets metadata only.
	resp = d.dispatchAs(Request{Method: "credential.get", Params: nameParams(t, "bus")}, localCaller())
	if resp.Error != "" {
		t.Fatalf("credential.get by admin: %s", resp.Error)
	}
	var meta api.Credential
	if err := json.Unmarshal(resp.Result, &meta); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if meta.Name != "bus" || len(meta.Value) != 0 {
		t.Errorf("get returned %+v, want metadata for bus with no value", meta)
	}
}

func TestCredentialListAndDelete(t *testing.T) {
	d := newHandlerDaemon(t)
	pushKey := caller{scope: ScopeCredentialPush, fingerprint: testFP}
	if resp := d.dispatchAs(Request{Method: "credential.put", Params: putParams(t, "bus", testSecret)}, pushKey); resp.Error != "" {
		t.Fatalf("put: %s", resp.Error)
	}

	// list is reachable by a credential-push key and returns metadata only.
	resp := d.dispatchAs(Request{Method: "credential.list"}, pushKey)
	if resp.Error != "" {
		t.Fatalf("list: %s", resp.Error)
	}
	var list []api.Credential
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "bus" || len(list[0].Value) != 0 {
		t.Fatalf("list = %+v, want one metadata-only bus credential", list)
	}

	// delete is reachable by a credential-push key, removes the credential, and
	// emits an event naming the fingerprint.
	resp = d.dispatchAs(Request{Method: "credential.delete", Params: nameParams(t, "bus")}, pushKey)
	if resp.Error != "" {
		t.Fatalf("delete: %s", resp.Error)
	}
	if _, err := d.store.GetCredential("bus"); err == nil {
		t.Error("credential still present after delete")
	}
	evs := d.events.Snapshot(events.Filter{Kind: events.KindCredentialDeleted}, 0)
	if len(evs) != 1 || !strings.Contains(evs[0].Message, testFP) {
		t.Errorf("credential.deleted events = %+v, want one naming the fingerprint", evs)
	}
	if len(evs) == 1 && strings.Contains(evs[0].Message, testSecret) {
		t.Errorf("delete event leaked the value: %q", evs[0].Message)
	}
}

func TestCredentialPutRejectsEmptyValue(t *testing.T) {
	d := newHandlerDaemon(t)
	p, _ := json.Marshal(map[string]any{"name": "bus", "kind": string(api.CredentialNATSNKeySeed)})
	resp := d.dispatchAs(Request{Method: "credential.put", Params: p}, localCaller())
	if resp.Error == "" {
		t.Fatal("credential.put with no value: want an error, got success")
	}
}
