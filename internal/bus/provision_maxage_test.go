package bus

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestProvisionRaisesShorterMaxAge runs Provision against a scratch broker
// that already holds a stream under an older declaration. The live max_age is
// raised to the declared one only when it is finite and shorter; a longer
// value and an unlimited (zero) value stay as found, and a raise changes no
// other field of the live config.
func TestProvisionRaisesShorterMaxAge(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		live   time.Duration
		want   time.Duration
	}{
		{"inbox 24h raised to 168h", InboxStream, 24 * time.Hour, InboxMaxAge},
		{"inbox 200h left alone", InboxStream, 200 * time.Hour, 200 * time.Hour},
		{"inbox unlimited left alone", InboxStream, 0, 0},
		{"inbox already at the declared value", InboxStream, InboxMaxAge, InboxMaxAge},
		{"audit 24h raised to 720h", AuditStream, 24 * time.Hour, 720 * time.Hour},
		{"audit 800h left alone", AuditStream, 800 * time.Hour, 800 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, m, _ := newTestSupervisor(t, "")
			// The test seeds the old stream itself, so the broker must come up bare.
			s.AfterReady = nil
			if err := s.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			admin := m.Admin()
			nc, err := nats.Connect(m.URL(), nats.UserInfo(admin.Name, admin.Password))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(nc.Close)
			js, err := jetstream.New(nc)
			if err != nil {
				t.Fatal(err)
			}

			seeded, err := js.CreateStream(ctx, seedStreamConfig(t, tc.stream, tc.live))
			if err != nil {
				t.Fatal(err)
			}
			// The broker fills defaults on create, so the baseline is the live config.
			before := seeded.CachedInfo().Config
			if _, err := Provision(ctx, m.URL(), admin.Name, admin.Password); err != nil {
				t.Fatal(err)
			}

			st, err := js.Stream(ctx, tc.stream)
			if err != nil {
				t.Fatal(err)
			}
			got := st.CachedInfo().Config
			if got.MaxAge != tc.want {
				t.Errorf("%s max_age = %s, want %s", tc.stream, got.MaxAge, tc.want)
			}
			// Everything but max_age is the config the broker already held.
			want := before
			want.MaxAge = tc.want
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s config changed beyond max_age:\n got %+v\nwant %+v", tc.stream, got, want)
			}
		})
	}
}

// seedStreamConfig is a live stream under an older declaration: the declared
// subjects, but a max_age the test picks and values for the other fields that
// differ from the declared ones, so a raise that rewrote the config from the
// declared set would be seen.
func seedStreamConfig(t *testing.T, name string, maxAge time.Duration) jetstream.StreamConfig {
	t.Helper()
	for _, obj := range DeclaredObjects() {
		if obj.Name != name || obj.Stream == nil {
			continue
		}
		cfg := *obj.Stream
		cfg.MaxAge = maxAge
		cfg.MaxMsgSize = 4096
		cfg.MaxMsgs = 1000
		cfg.Description = "seeded by the test"
		return cfg
	}
	t.Fatalf("stream %s is not declared", name)
	return jetstream.StreamConfig{}
}

// refusingUpdates is a JetStream handle whose UpdateStream always fails, as a
// broker that refuses the edit (or an admin that may not make it) would.
type refusingUpdates struct {
	jetstream.JetStream
}

func (refusingUpdates) UpdateStream(context.Context, jetstream.StreamConfig) (jetstream.Stream, error) {
	return nil, errors.New("update refused for the test")
}

// TestProvisionKeepsOldMaxAgeWhenTheUpdateIsRefused provisions a broker whose
// 24h inbox stream cannot be updated. The refusal is logged with the stream,
// the old and the declared value and the error; provisioning still succeeds,
// and the stream keeps its old retention.
func TestProvisionKeepsOldMaxAgeWhenTheUpdateIsRefused(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	s.AfterReady = nil
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin := m.Admin()
	nc, err := nats.Connect(m.URL(), nats.UserInfo(admin.Name, admin.Password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateStream(ctx, seedStreamConfig(t, InboxStream, 24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	if _, err := provisionObjects(ctx, refusingUpdates{js}); err != nil {
		t.Fatalf("provisioning returned %v for a refused max_age update, want success", err)
	}
	line := logged.String()
	for _, want := range []string{InboxStream, "24h0m0s", "168h0m0s", "not raised", "update refused for the test"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q lacks %q", line, want)
		}
	}
	st, err := js.Stream(ctx, InboxStream)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.CachedInfo().Config.MaxAge; got != 24*time.Hour {
		t.Errorf("%s max_age = %s, want the old 24h", InboxStream, got)
	}
}
