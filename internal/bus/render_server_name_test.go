package bus

import (
	"strings"
	"testing"
)

// The managed broker's conf names the server after the cluster (aae-orc-a7h5h).
// Without it a broker's name is its server id, new on every start, and a hub
// listing its leaves shows ids and not clusters.

// serverNameLines returns every line of conf that sets server_name, with its
// surrounding whitespace trimmed, and the 0-based index of the first one.
func serverNameLines(conf string) (lines []string, first int) {
	first = -1
	for i, l := range strings.Split(conf, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "server_name") {
			if first < 0 {
				first = i
			}
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines, first
}

func TestRenderConfEmitsServerNameFromDomain(t *testing.T) {
	t.Parallel()
	s := baseSpec()
	s.Domain = "c1"
	conf, err := RenderConf(s)
	if err != nil {
		t.Fatal(err)
	}
	lines, first := serverNameLines(conf)
	if len(lines) != 1 || lines[0] != "server_name: c1" {
		t.Fatalf("server_name lines = %q, want exactly [\"server_name: c1\"]:\n%s", lines, conf)
	}
	// It is a top-level setting: before any block opens, so the broker reads
	// it as its own name and not as a key inside jetstream or a leaf remote.
	head := strings.Join(strings.Split(conf, "\n")[:first], "\n")
	if strings.Contains(head, "{") {
		t.Errorf("server_name follows an opened block, so it is not top level:\n%s", conf)
	}
}

// The name follows the cluster, whatever the rest of the spec carries, so two
// clusters that lead to one hub arrive under two names.
func TestRenderConfServerNameFollowsTheDomainAcrossSpecs(t *testing.T) {
	t.Parallel()
	for _, domain := range []string{"c1", "c2", "east-1", "a"} {
		for name, mod := range map[string]func(*Spec){
			"local only":    func(s *Spec) {},
			"with a hub":    func(s *Spec) { s.HubURL = "nats://hub.example:7422"; s.LeafSeed = true },
			"with a ca":     func(s *Spec) { s.HubURL = "nats://hub.example:7422"; s.LeafSeed = true; s.HubCAFile = "/etc/ca.pem" },
			"leaf detached": func(s *Spec) { s.HubURL = "nats://hub.example:7422"; s.LeafSeed = true; s.LeafAttached = false },
			"with caps":     func(s *Spec) { s.MaxFileStore = 1 << 30; s.MaxMemoryStore = 1 << 28 },
		} {
			s := baseSpec()
			s.Domain = domain
			mod(&s)
			conf, err := RenderConf(s)
			if err != nil {
				t.Fatalf("%s %s: %v", domain, name, err)
			}
			lines, _ := serverNameLines(conf)
			if len(lines) != 1 || lines[0] != "server_name: "+domain {
				t.Errorf("%s %s: server_name lines = %q, want [%q]", domain, name, lines, "server_name: "+domain)
			}
		}
	}
}
