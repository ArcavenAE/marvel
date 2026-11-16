package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/service"
)

const partitionBusEntry = "      - name: bus\n        class: message-bus\n        provider: nats-server\n        mode: adopted\n        url: nats://127.0.0.1:4222\n"

// An entry whose class, provider or pairing is not registered is skipped on
// its own; the bus beside it stays in the attachable set. marvel#916.
func TestPartitionServicesSplitsOnlyUnregistered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, extra string
	}{
		{"unregistered class", "      - name: llm\n        class: inference-gateway\n        provider: litellm\n        mode: adopted\n        url: http://127.0.0.1:4000\n"},
		{"unregistered provider", "      - name: cache\n        class: message-bus\n        provider: redis\n        mode: adopted\n        url: redis://127.0.0.1:6379\n"},
	}
	for _, tc := range cases {
		cl := parseCluster(t, "clusters:\n  - name: kinu\n    services:\n"+partitionBusEntry+tc.extra)
		ok, skipped := PartitionServices(cl)
		if len(ok) != 1 || ok[0].Name != "bus" || ok[0].Provider != service.ProviderNATSServer {
			t.Errorf("%s: attachable = %+v, want only the bus", tc.name, ok)
		}
		if len(skipped) != 1 || !errors.Is(skipped[0], ErrInvalidService) || !errors.Is(skipped[0], service.ErrUnregistered) {
			t.Errorf("%s: skipped = %v, want one ErrInvalidService wrapping ErrUnregistered", tc.name, skipped)
		}
		// The CLI verdict does not change: the list still fails validation.
		if err := ValidateServices(cl); !errors.Is(err, ErrInvalidService) {
			t.Errorf("%s: ValidateServices = %v, want ErrInvalidService", tc.name, err)
		}
	}
}

// Any other refusal is not skipped: the whole list stays refused, as before.
func TestPartitionServicesKeepsOtherRefusalsWhole(t *testing.T) {
	t.Parallel()
	dup := "clusters:\n  - name: kinu\n    services:\n" + partitionBusEntry + partitionBusEntry
	cl := parseCluster(t, dup)
	ok, skipped := PartitionServices(cl)
	if len(ok) != 0 {
		t.Errorf("duplicate name: attachable = %+v, want none", ok)
	}
	if len(skipped) == 0 || !strings.Contains(errors.Join(skipped...).Error(), "twice") {
		t.Errorf("duplicate name: skipped = %v, want the duplicate-name refusal", skipped)
	}
}
