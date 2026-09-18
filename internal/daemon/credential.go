package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/events"
)

// Credential RPC surface (brief 9 S2, aae-orc-gdum6). put, list, and delete are
// in the credential-push scope set (scope.go); get is not, so only an admin key
// or the local socket reaches it. Revealing a value is a later, local-socket
// only seam (S3); every method here keeps the value out of results, events, and
// logs.

type credentialPutParams struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Audience string `json:"audience,omitempty"`
	Binding  string `json:"binding,omitempty"`
	// Value is the secret material. It rides the local unix socket or an
	// SSH-encrypted mrvl:// channel; it is stored in memory, then zeroed here
	// once the store holds its own copy, and never logged.
	Value []byte `json:"value"`
}

type credentialNameParams struct {
	Name string `json:"name"`
}

// handleCredentialPut stores a pushed credential and records who pushed it. The
// caller's key fingerprint becomes the credential's IssuedBy and rides the
// event; the value never leaves this method except into the store.
func (d *Daemon) handleCredentialPut(params json.RawMessage, c caller) Response {
	var p credentialPutParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	if p.Name == "" {
		return Response{Error: "credential.put: name is required"}
	}
	if len(p.Value) == 0 {
		return Response{Error: "credential.put: value is required"}
	}
	cred := &api.Credential{
		Name:     p.Name,
		Kind:     api.CredentialKind(p.Kind),
		Audience: p.Audience,
		Binding:  p.Binding,
		IssuedAt: time.Now().UTC(),
		IssuedBy: c.fingerprint,
		Value:    p.Value,
	}
	if err := d.store.CreateCredential(cred); err != nil {
		return Response{Error: err.Error()}
	}
	// The store cloned the value; clear this request's copy so the secret does
	// not linger in the decoded params.
	for i := range p.Value {
		p.Value[i] = 0
	}
	d.emitCredentialEvent(events.KindCredentialPut, cred.Name, cred.Kind, c)
	if cred.Name == busLeafCredential {
		// Enrollment: the seed enters the store. Bring the leaf up the cheap way
		// when the running broker's environment already carries the seed (a
		// reload, no bounce); a broker that booted unenrolled needs a fresh
		// process to read the seed from its environment (nats-server reads it
		// once at start). This is the one accepted restart, and every later
		// connect/disconnect is reload-only (aae-orc-ct0l4).
		d.enrollLeafSeed()
	}

	meta, err := d.store.GetCredential(cred.Name) // metadata only (value stripped)
	if err != nil {
		return Response{Error: err.Error()}
	}
	data, _ := json.Marshal(meta)
	return Response{Result: data}
}

// handleCredentialGet returns a credential's metadata. It never returns the
// value: reveal is a separate local-socket-only seam (S3). Admin only, since
// get is outside the credential-push scope set.
func (d *Daemon) handleCredentialGet(params json.RawMessage) Response {
	var p credentialNameParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	cred, err := d.store.GetCredential(p.Name)
	if err != nil {
		return Response{Error: err.Error()}
	}
	data, _ := json.Marshal(cred)
	return Response{Result: data}
}

// credentialRevealResult carries a revealed secret value back to the local
// caller. Value marshals as base64 (encoding/json's []byte behavior), so it is
// binary-safe; the CLI decodes it and writes the raw bytes to stdout.
type credentialRevealResult struct {
	Value []byte `json:"value"`
}

// handleCredentialReveal returns a credential's secret value. It is the one
// path that returns a value, and the daemon reaches it only for a local-socket
// caller (dispatchAs gates credential.reveal to the local socket; brief 9 S3).
// The reveal is recorded on the event ring and the log, naming the credential
// and never the value.
func (d *Daemon) handleCredentialReveal(params json.RawMessage, c caller) Response {
	var p credentialNameParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	value, err := d.store.RevealCredentialValue(p.Name)
	if err != nil {
		return Response{Error: err.Error()}
	}
	d.emitCredentialEvent(events.KindCredentialRevealed, p.Name, "", c)
	data, _ := json.Marshal(credentialRevealResult{Value: value})
	return Response{Result: data}
}

// handleCredentialList returns metadata for every credential.
func (d *Daemon) handleCredentialList() Response {
	data, err := json.Marshal(d.store.ListCredentials())
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal credentials: %v", err)}
	}
	return Response{Result: data}
}

// handleCredentialDelete removes a credential (its value is zeroed by the
// store) and records who removed it.
func (d *Daemon) handleCredentialDelete(params json.RawMessage, c caller) Response {
	var p credentialNameParams
	if err := json.Unmarshal(params, &p); err != nil {
		return Response{Error: fmt.Sprintf("bad params: %v", err)}
	}
	if err := d.store.DeleteCredential(p.Name); err != nil {
		return Response{Error: err.Error()}
	}
	d.emitCredentialEvent(events.KindCredentialDeleted, p.Name, "", c)
	if p.Name == busLeafCredential {
		d.regenerateBus("credential.delete " + busLeafCredential)
	}
	data, _ := json.Marshal(map[string]string{"deleted": p.Name})
	return Response{Result: data}
}

// emitCredentialEvent records a credential action on the event ring and the
// log, carrying the credential name and the caller key fingerprint and never
// the value.
func (d *Daemon) emitCredentialEvent(kind events.Kind, name string, ck api.CredentialKind, c caller) {
	who := c.fingerprint
	if who == "" {
		who = "local"
	}
	msg := fmt.Sprintf("credential %q by %s", name, who)
	if ck != "" {
		msg = fmt.Sprintf("credential %q (%s) by %s", name, ck, who)
	}
	events.Emit(d.events, events.Event{
		Kind:     kind,
		Severity: events.SeverityInfo,
		Message:  msg,
	})
	log.Printf("%s: %s", kind, msg)
}
