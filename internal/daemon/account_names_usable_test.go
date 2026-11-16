package daemon

import (
	"testing"

	"github.com/arcavenae/marvel/internal/config"
)

// A refused display name is a display mistake: the config still reads, so the
// daemon keeps attaching services and keeps the schedule cap and the account
// warn level (review 5914176997).
func TestConfigLoadUsableAcceptsABadAccountName(t *testing.T) {
	for name, names := range map[string]map[string]string{
		"padded value":     {"ab12cd": " work"},
		"malformed key":    {"nope": "work"},
		"credential shape": {"ab12cd": "sk-ant-api03-AbCdEf0123456789xyzAbCdEf"},
	} {
		cfg := &config.Config{Display: config.Display{AccountNames: names}}
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("%s: Validate accepted the map", name)
		}
		if !configLoadUsable(err) {
			t.Errorf("%s: configLoadUsable = false for %v, so the daemon would treat the client config as unreadable", name, err)
		}
	}
}
