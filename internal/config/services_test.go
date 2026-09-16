package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/arcavenae/marvel/internal/service"
)

func parseCluster(t *testing.T, body string) *Cluster {
	t.Helper()
	var cfg Config
	if err := yaml.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Clusters) != 1 {
		t.Fatalf("want one cluster, got %d", len(cfg.Clusters))
	}
	return &cfg.Clusters[0]
}

const busBlockSpelling = `clusters:
  - name: kinu
    bus:
      managed: true
      listen: 127.0.0.1:4222
      store_dir: /var/nats
      hub:
        url: nats-leaf://192.168.100.110:7442
`

const servicesSpelling = `clusters:
  - name: kinu
    services:
      - name: bus
        class: message-bus
        provider: nats-server
        mode: managed
        listen: 127.0.0.1:4222
        store_dir: /var/nats
        hub:
          url: nats-leaf://192.168.100.110:7442
`

// The golden test for services-list.md section 1.3: the two spellings
// produce one record, field for field, and the nats-server body resolves
// the same.
func TestBusBlockAndServicesEntryParseToOneRecord(t *testing.T) {
	t.Parallel()
	fromBus := parseCluster(t, busBlockSpelling)
	fromList := parseCluster(t, servicesSpelling)
	for _, cl := range []*Cluster{fromBus, fromList} {
		if err := ValidateServices(cl); err != nil {
			t.Fatalf("valid spelling refused: %v", err)
		}
	}
	a, err := fromBus.AllServices()
	if err != nil {
		t.Fatal(err)
	}
	b, err := fromList.AllServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("want one entry each, got %d and %d", len(a), len(b))
	}
	// Common fields, with the parse-private carriers cleared.
	strip := func(s Service) Service { s.body, s.bus = nil, nil; return s }
	if got, want := strip(a[0]), strip(b[0]); !reflect.DeepEqual(got, want) {
		t.Errorf("records differ:\n bus:      %+v\n services: %+v", got, want)
	}
	if a[0].Name != BusServiceName || a[0].Class != service.ClassMessageBus || a[0].Provider != service.ProviderNATSServer || a[0].Mode != "managed" {
		t.Errorf("lifted record = %+v", strip(a[0]))
	}
	// Provider bodies resolve identically.
	ra, _ := a[0].BusSpec()
	rb, _ := b[0].BusSpec()
	if got, want := ra.Resolve("/state"), rb.Resolve("/state"); !reflect.DeepEqual(got, want) {
		t.Errorf("resolved bodies differ:\n bus:      %+v\n services: %+v", got, want)
	}
	if !ra.Resolve("/state").Managed || ra.Resolve("/state").Mode != service.ModeManaged {
		t.Errorf("resolved body not managed: %+v", ra.Resolve("/state"))
	}
	svc, err := fromList.BusService()
	if err != nil || svc == nil || svc.Name != BusServiceName {
		t.Errorf("BusService() = %+v, %v", svc, err)
	}
}

func TestBothSpellingsTogetherAreRefused(t *testing.T) {
	t.Parallel()
	cl := parseCluster(t, `clusters:
  - name: kinu
    bus:
      managed: false
      url: nats://127.0.0.1:4222
    services:
      - name: broker
        class: message-bus
        provider: nats-server
        mode: adopted
        url: nats://127.0.0.1:4222
`)
	err := ValidateServices(cl)
	if !errors.Is(err, ErrInvalidService) || !strings.Contains(err.Error(), "both") {
		t.Errorf("both spellings accepted: %v", err)
	}
	if _, err := cl.AllServices(); !errors.Is(err, ErrInvalidService) {
		t.Errorf("AllServices did not refuse: %v", err)
	}
}

func TestServicesRefuseUnregisteredAndMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, entry, want string
		alsoBus           bool
	}{
		{"unregistered class", "name: llm\n        class: inference-gateway\n        provider: litellm\n        mode: adopted\n        url: http://x:1", "class", false},
		{"unregistered provider", "name: bus\n        class: message-bus\n        provider: redis\n        mode: managed\n        listen: 127.0.0.1:1", "provider", false},
		{"reserved mode", "name: bus\n        class: message-bus\n        provider: nats-server\n        mode: internal", "reserved", false},
		{"unknown mode", "name: bus\n        class: message-bus\n        provider: nats-server\n        mode: hosted", "mode", false},
		{"missing mode", "name: bus\n        class: message-bus\n        provider: nats-server\n        url: nats://x:1", "mode", false},
		{"no name", "class: message-bus\n        provider: nats-server\n        mode: adopted\n        url: nats://x:1", "no name", false},
		{"bad nats body", "name: bus\n        class: message-bus\n        provider: nats-server\n        mode: managed", "listen", true},
	}
	for _, tc := range cases {
		cl := parseCluster(t, "clusters:\n  - name: kinu\n    services:\n      - "+tc.entry+"\n")
		err := ValidateServices(cl)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		if !errors.Is(err, ErrInvalidService) && !tc.alsoBus {
			t.Errorf("%s: %v does not carry ErrInvalidService", tc.name, err)
		}
		if tc.alsoBus && !errors.Is(err, ErrInvalidBus) {
			t.Errorf("%s: %v does not carry ErrInvalidBus", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v lacks %q", tc.name, err, tc.want)
		}
	}
	// Two entries with one name.
	cl := parseCluster(t, "clusters:\n  - name: kinu\n    services:\n      - name: bus\n        class: message-bus\n        provider: nats-server\n        mode: adopted\n        url: nats://x:1\n      - name: bus\n        class: message-bus\n        provider: nats-server\n        mode: adopted\n        url: nats://y:1\n")
	if err := ValidateServices(cl); !errors.Is(err, ErrInvalidService) || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate name accepted: %v", err)
	}
}

// managed: true is mode managed; managed: false with url is adopted; mode
// wins when both are written; a disagreement is refused; the reserved word
// is refused inside a bus: block too.
func TestBusModeDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, block string
		want        service.Mode
		refuse      string
	}{
		{"managed bool", "managed: true\n      listen: 127.0.0.1:4222", service.ModeManaged, ""},
		{"adopted bool", "managed: false\n      url: nats://x:1", service.ModeAdopted, ""},
		{"url only", "url: nats://x:1", service.ModeAdopted, ""},
		{"mode managed", "mode: managed\n      listen: 127.0.0.1:4222", service.ModeManaged, ""},
		{"mode external", "mode: external\n      url: nats://elsewhere:4222", service.ModeExternal, ""},
		{"mode wins beside agreeing bool", "managed: true\n      mode: managed\n      listen: 127.0.0.1:4222", service.ModeManaged, ""},
		{"disagree true", "managed: true\n      mode: adopted\n      url: nats://x:1", "", "disagrees"},
		{"disagree false", "managed: false\n      mode: managed\n      listen: 127.0.0.1:4222", "", "disagrees"},
		{"reserved", "mode: internal\n      listen: 127.0.0.1:4222", "", "reserved"},
		{"wrong class in block", "class: inference-gateway\n      managed: true\n      listen: 127.0.0.1:4222", "", "class"},
	}
	for _, tc := range cases {
		cl := parseCluster(t, "clusters:\n  - name: kinu\n    bus:\n      "+tc.block+"\n")
		err := ValidateServices(cl)
		if tc.refuse != "" {
			if err == nil || !strings.Contains(err.Error(), tc.refuse) {
				t.Errorf("%s: err %v, want refusal carrying %q", tc.name, err, tc.refuse)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: refused: %v", tc.name, err)
			continue
		}
		got, merr := cl.Bus.EffectiveMode()
		if merr != nil || got != tc.want {
			t.Errorf("%s: mode %q, %v; want %q", tc.name, got, merr, tc.want)
		}
		if r := cl.Bus.Resolve("/s"); r.Mode != tc.want || r.Managed != (tc.want == service.ModeManaged) {
			t.Errorf("%s: resolved %+v", tc.name, r)
		}
	}
	// A Bus built in code (no yaml) with Managed: true still derives managed.
	if m, err := (&Bus{Managed: true, Listen: "127.0.0.1:1"}).EffectiveMode(); err != nil || m != service.ModeManaged {
		t.Errorf("literal Bus: %q, %v", m, err)
	}
}

// Save must write a services: entry back with its provider body intact, and
// a bus: block must stay a bus: block: the lift is a read-time view.
func TestServicesRoundTripThroughMarshal(t *testing.T) {
	t.Parallel()
	for _, body := range []string{busBlockSpelling, servicesSpelling} {
		var cfg Config
		if err := yaml.Unmarshal([]byte(body), &cfg); err != nil {
			t.Fatal(err)
		}
		out, err := yaml.Marshal(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		var again Config
		if err := yaml.Unmarshal(out, &again); err != nil {
			t.Fatalf("re-parse: %v\n%s", err, out)
		}
		a, _ := cfg.Clusters[0].AllServices()
		b, _ := again.Clusters[0].AllServices()
		ra, _ := a[0].BusSpec()
		rb, _ := b[0].BusSpec()
		if !reflect.DeepEqual(ra.Resolve("/s"), rb.Resolve("/s")) {
			t.Errorf("provider body lost in round trip:\n%s", out)
		}
		hasBus := strings.Contains(string(out), "bus:")
		hasList := strings.Contains(string(out), "services:")
		if strings.Contains(body, "services:") != hasList || strings.Contains(body, "bus:") != hasBus {
			t.Errorf("spelling changed in round trip:\n%s", out)
		}
	}
}
