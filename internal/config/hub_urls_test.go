package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHubURLsAcceptsAList: bus.hub takes a list of hub addresses beside the
// single url, so a leaf can rotate between a hub's names or addresses
// (marvel#575). The single string stays valid.
func TestHubURLsAcceptsAList(t *testing.T) {
	t.Parallel()
	const a, b = "nats-leaf://hub-a.example:7442", "nats-leaf://hub-b.example:7442"

	var parsed Bus
	if err := yaml.Unmarshal([]byte("managed: true\nlisten: 127.0.0.1:4222\nhub:\n  urls:\n    - "+a+"\n    - "+b+"\n"), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := []string{a, b}; !reflect.DeepEqual(parsed.Hub.URLs, want) {
		t.Fatalf("parsed urls = %v, want %v", parsed.Hub.URLs, want)
	}

	list := &Bus{Managed: true, Listen: "127.0.0.1:4222", Hub: &Hub{URLs: []string{a, b}}}
	if err := ValidateBus("kinu", list); err != nil {
		t.Fatalf("a two-entry list is refused: %v", err)
	}
	r := list.Resolve("/state")
	if want := []string{a, b}; !reflect.DeepEqual(r.HubURLs, want) {
		t.Errorf("HubURLs = %v, want %v", r.HubURLs, want)
	}
	if r.HubURL != a {
		t.Errorf("HubURL = %q, want the first entry %q", r.HubURL, a)
	}

	single := &Bus{Managed: true, Listen: "127.0.0.1:4222", Hub: &Hub{URL: a}}
	if err := ValidateBus("kinu", single); err != nil {
		t.Fatalf("the single url is refused: %v", err)
	}
	if rs := single.Resolve("/state"); rs.HubURL != a || !reflect.DeepEqual(rs.HubURLs, []string{a}) {
		t.Errorf("single url resolved as HubURL=%q HubURLs=%v, want one entry %q", rs.HubURL, rs.HubURLs, a)
	}
}

// TestHubURLsRefusals: both forms at once is ambiguous, and every list
// entry gets the scheme and host check the single url gets.
func TestHubURLsRefusals(t *testing.T) {
	t.Parallel()
	const a = "nats-leaf://hub-a.example:7442"
	for _, tc := range []struct {
		name string
		hub  *Hub
		want string
	}{
		{"both url and urls", &Hub{URL: a, URLs: []string{a}}, "not both"},
		{"bad scheme in the list", &Hub{URLs: []string{a, "http://h:1"}}, "hub.urls[1]"},
		{"no host in the list", &Hub{URLs: []string{"nats-leaf://"}}, "no host"},
		{"empty list entry", &Hub{URLs: []string{a, ""}}, "hub.urls[1]"},
	} {
		err := ValidateBus("kinu", &Bus{Managed: true, Listen: "127.0.0.1:4222", Hub: tc.hub})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one carrying %q", tc.name, err, tc.want)
		}
	}
}
