// Package service holds the two registries the cluster Services list is
// validated against: class contracts (what every provider of a class must
// deliver to a consumer) and provider drivers (what renders, spawns,
// health-checks, and mints for a class). Both are code, not config: an
// operator declares an entry in the client config, and the entry is
// refused unless its class and provider are registered here and the
// provider belongs to that class. See docs/design/services-list.md
// sections 1 and 2.
//
// One class and one provider are registered today, the message bus and
// nats-server. Names the design puts on record and reserves without
// registering: classes inference-gateway (liteLLM) and model-endpoint
// (PAIR, Switchyard); providers litellm, pair, switchyard. Declaring any
// of them is a structural refusal until the ticket that adds the driver
// lands.
package service

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Mode is how much of a service marvel runs.
type Mode string

const (
	// ModeManaged: marvel renders, starts, supervises, provisions, and
	// mints the credentials a consumer presents. Marvel-held secrets for a
	// managed service are issuance under ADR-009: the daemon mints them,
	// can rewrite and reload them, and no third party's console is
	// involved. Moving a managed service out of marvel's supervision is
	// the ADR-009 tripwire.
	ModeManaged Mode = "managed"
	// ModeAdopted: something on this host, started by someone else, with
	// authorization marvel did not write. Marvel hands out the URL and
	// mints nothing. The record has no field for a foreign credential on
	// purpose; holding one would be custody.
	ModeAdopted Mode = "adopted"
	// ModeExternal: a service elsewhere, reached over a link marvel can
	// observe. Health is whatever the link reports.
	ModeExternal Mode = "external"
)

// The word "internal" is reserved for an in-process provider (a broker or
// library inside the daemon; the marvel-builder Q1 thread in
// docs/design/service-provider/03-probes-and-roadmap.md). It is not a
// Mode value: declaring it is refused with a message that says it is
// reserved, so the day it is registered no config written earlier changes
// meaning.
const reservedModeInternal = "internal"

// CallerIdentity is what a consumer presents to be recognized by a
// service. The set a consumer may meet is a clause of the class contract;
// which values a driver can enforce is a property of the driver.
type CallerIdentity string

const (
	// CallerNone: the service does not identify callers; network position
	// is the only control. A Binding to such a service informs (the URL is
	// delivered) and cannot gate.
	CallerNone CallerIdentity = "none"
	// CallerAPIKey: the caller presents a marvel-minted secret (a
	// user/password line, a virtual key) that the service checks. The
	// Binding gates: marvel mints per team or per session and revokes by
	// rewrite and reload.
	CallerAPIKey CallerIdentity = "api-key"
	// CallerSessionCert: the caller presents the fleet-CA per-generation
	// leaf and the service maps its SAN to an identity. The credential is
	// the identity; there is no separate secret.
	CallerSessionCert CallerIdentity = "session-cert"
)

// ClassContract states what every provider of a class must deliver.
type ClassContract struct {
	Name string
	// CallerIdentities is the set a consumer written to this class may
	// meet. A provider declares which of them it delivers; an entry
	// declares which is in force; neither invents one.
	CallerIdentities []CallerIdentity
	// ChildEnvAllow is the daemon environment a managed child of this
	// class may inherit, by key. It is code, never config: an operator
	// cannot widen a child's environment from YAML. Consumed by the
	// workload kind that spawns managed children (aae-orc-oo62t).
	ChildEnvAllow []string
}

// ProviderDriver is one driver of a class.
type ProviderDriver struct {
	Name  string
	Class string
	Modes []Mode
	// Delivers is the subset of the class's CallerIdentities this driver
	// can enforce today. An entry declaring a value outside it is refused
	// at parse, naming the ticket that adds the capability where one is
	// known.
	Delivers []CallerIdentity
}

// Registered names.
const (
	ClassMessageBus    = "message-bus"
	ProviderNATSServer = "nats-server"
)

var classes = map[string]ClassContract{
	ClassMessageBus: {
		Name:             ClassMessageBus,
		CallerIdentities: []CallerIdentity{CallerAPIKey, CallerSessionCert},
		ChildEnvAllow:    []string{"PATH", "HOME", "TMPDIR"},
	},
}

var providers = map[string]ProviderDriver{
	ProviderNATSServer: {
		Name:     ProviderNATSServer,
		Class:    ClassMessageBus,
		Modes:    []Mode{ModeManaged, ModeAdopted, ModeExternal},
		Delivers: []CallerIdentity{CallerAPIKey},
	},
}

// ErrUnregistered marks a class, provider, or mode the registries do not
// carry. Callers match it with errors.Is; config wraps it in
// ErrInvalidService.
var ErrUnregistered = errors.New("unregistered")

// Class returns the contract for a registered class.
func Class(name string) (ClassContract, error) {
	c, ok := classes[name]
	if !ok {
		return ClassContract{}, fmt.Errorf("%w: class %q is not registered (registered: %s)", ErrUnregistered, name, joinKeys(classes))
	}
	return c, nil
}

// Provider returns the driver for a registered provider.
func Provider(name string) (ProviderDriver, error) {
	p, ok := providers[name]
	if !ok {
		return ProviderDriver{}, fmt.Errorf("%w: provider %q is not registered (registered: %s)", ErrUnregistered, name, joinKeys(providers))
	}
	return p, nil
}

// ParseMode checks a mode string against the three values. The reserved
// word is refused with its own message.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeManaged, ModeAdopted, ModeExternal:
		return Mode(s), nil
	}
	if s == reservedModeInternal {
		return "", fmt.Errorf("%w: mode %q is reserved for an in-process provider and is not implemented", ErrUnregistered, s)
	}
	return "", fmt.Errorf("%w: mode %q is not one of managed, adopted, external", ErrUnregistered, s)
}

// Check validates that provider belongs to class and supports mode. It
// returns the resolved contract and driver so a caller validates once and
// reads twice.
func Check(class, provider string, mode Mode) (ClassContract, ProviderDriver, error) {
	c, err := Class(class)
	if err != nil {
		return ClassContract{}, ProviderDriver{}, err
	}
	p, err := Provider(provider)
	if err != nil {
		return ClassContract{}, ProviderDriver{}, err
	}
	if p.Class != class {
		return ClassContract{}, ProviderDriver{}, fmt.Errorf("%w: provider %q drives class %q, not %q", ErrUnregistered, provider, p.Class, class)
	}
	if !slices.Contains(p.Modes, mode) {
		return ClassContract{}, ProviderDriver{}, fmt.Errorf("%w: provider %q does not support mode %q", ErrUnregistered, provider, mode)
	}
	return c, p, nil
}

func joinKeys[V any](m map[string]V) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
