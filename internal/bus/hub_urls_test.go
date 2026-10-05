package bus

import (
	"strings"
	"testing"
)

// TestRenderWritesEveryHubURL: a hub list renders every entry into the one
// leaf remote's urls, in order, so NATS rotates between them (marvel#575).
func TestRenderWritesEveryHubURL(t *testing.T) {
	s := Spec{
		Domain: "kinu", Listen: "127.0.0.1:4222", StoreDir: "/state/nats",
		HubURL:   "nats-leaf://hub-a.example:7442",
		HubURLs:  []string{"nats-leaf://hub-a.example:7442", "nats-leaf://hub-b.example:7442"},
		LeafSeed: true, LeafAttached: true,
	}
	conf, err := RenderConf(s)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := `urls: ["nats-leaf://hub-a.example:7442", "nats-leaf://hub-b.example:7442"], nkey:`
	if !strings.Contains(conf, want) {
		t.Errorf("leaf remote does not list both hubs, want %q in:\n%s", want, conf)
	}
	if n := strings.Count(conf, "remotes"); n != 1 {
		t.Errorf("remotes appears %d times, want one remote carrying the list", n)
	}
}
