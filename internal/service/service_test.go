package service

import (
	"errors"
	"strings"
	"testing"
)

func TestRegistriesCarryTheBusAndNothingElse(t *testing.T) {
	t.Parallel()
	c, err := Class(ClassMessageBus)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.CallerIdentities) != 2 || c.CallerIdentities[0] != CallerAPIKey || c.CallerIdentities[1] != CallerSessionCert {
		t.Errorf("message-bus caller identities = %v, want [api-key session-cert]", c.CallerIdentities)
	}
	p, err := Provider(ProviderNATSServer)
	if err != nil {
		t.Fatal(err)
	}
	if p.Class != ClassMessageBus || len(p.Delivers) != 1 || p.Delivers[0] != CallerAPIKey {
		t.Errorf("nats-server driver = %+v, want class message-bus delivering [api-key]", p)
	}
	// Names on record but unregistered stay refused, naming the registered set.
	for _, name := range []string{"inference-gateway", "model-endpoint", ""} {
		if _, err := Class(name); !errors.Is(err, ErrUnregistered) || !strings.Contains(err.Error(), "message-bus") {
			t.Errorf("Class(%q) = %v, want ErrUnregistered naming the registered set", name, err)
		}
	}
	for _, name := range []string{"litellm", "switchyard", ""} {
		if _, err := Provider(name); !errors.Is(err, ErrUnregistered) {
			t.Errorf("Provider(%q) = %v, want ErrUnregistered", name, err)
		}
	}
}

func TestParseModeRefusesReservedAndUnknown(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"managed", "adopted", "external"} {
		if m, err := ParseMode(s); err != nil || string(m) != s {
			t.Errorf("ParseMode(%q) = %q, %v", s, m, err)
		}
	}
	_, err := ParseMode("internal")
	if !errors.Is(err, ErrUnregistered) || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("internal: %v, want a reserved-word refusal", err)
	}
	if _, err := ParseMode("hosted"); !errors.Is(err, ErrUnregistered) {
		t.Errorf("hosted: %v, want ErrUnregistered", err)
	}
}

func TestCheckBindsProviderToClassAndMode(t *testing.T) {
	// Not parallel: it edits the class registry for the cross-class case,
	// and parallel tests only start once the sequential ones are done.
	if _, _, err := Check(ClassMessageBus, ProviderNATSServer, ModeManaged); err != nil {
		t.Errorf("the registered pair was refused: %v", err)
	}
	if _, _, err := Check("model-endpoint", ProviderNATSServer, ModeManaged); !errors.Is(err, ErrUnregistered) {
		t.Errorf("unregistered class accepted: %v", err)
	}
	// A provider registered under a different class is refused even when
	// both names are registered: the check is the pairing, not the names.
	classes["other"] = ClassContract{Name: "other"}
	t.Cleanup(func() { delete(classes, "other") })
	if _, _, err := Check("other", ProviderNATSServer, ModeManaged); !errors.Is(err, ErrUnregistered) || !strings.Contains(err.Error(), "drives class") {
		t.Errorf("cross-class pairing accepted: %v", err)
	}
	if _, _, err := Check(ClassMessageBus, ProviderNATSServer, Mode("internal")); !errors.Is(err, ErrUnregistered) {
		t.Errorf("unsupported mode accepted: %v", err)
	}
}
