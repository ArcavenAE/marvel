package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Structural health (docs/design/bus-as-service.md section 3,
// aae-orc-vy6k7). Ready used to be listener plus pid, so a broker that was
// bare, anonymous, or plaintext read as ready and provisioning was asserted
// once at start and never again. Structure is read from the running broker,
// never inferred from the rendered files: existence of every service-scope
// object in the declared set, and what /varz says about authorization and
// TLS. Structural only (ADR-007 clause 3): consumer counts, undelivered
// durables, and stream age are vital signs and never enter this reading.

// Structure is one reading of a managed broker.
type Structure struct {
	// Missing names the declared service-scope objects the broker does not
	// carry, sorted. Existence only; parameters are not compared.
	Missing []string `json:"missing,omitempty"`
	// Authorized is /varz auth_required. On a managed broker the rendered
	// conf always carries the include, so false means a reload did not
	// land.
	Authorized bool `json:"authorized"`
	// TLS is /varz tls_required. Reported from day one; it joins the ready
	// predicate only once the client listener is rendered with TLS
	// (aae-orc-5yqw3). Until then false is the interim posture, not a miss.
	TLS bool `json:"tls"`
}

// Provisioned is true when every declared service-scope object exists.
func (st Structure) Provisioned() bool { return len(st.Missing) == 0 }

// Healthy is the part of the reading that gates ready today.
func (st Structure) Healthy() bool { return st.Provisioned() && st.Authorized }

// Problems renders what is wrong, for the event and the hold reason, and
// doubles as the change key: the same string means the same missing set.
func (st Structure) Problems() string {
	var parts []string
	if len(st.Missing) > 0 {
		parts = append(parts, "missing "+strings.Join(st.Missing, ", "))
	}
	if !st.Authorized {
		parts = append(parts, "authorization not loaded (auth_required false)")
	}
	return strings.Join(parts, "; ")
}

const structureRPC = 10 * time.Second

// CheckStructure reads the broker at url as the admin identity for the
// declared objects and the loopback monitor at monitorAddr for /varz. An
// error is no information, not a miss: the caller keeps its last reading.
func CheckStructure(ctx context.Context, url, user, password, monitorAddr string) (Structure, error) {
	var st Structure
	nc, err := connectAdmin(ctx, url, user, password)
	if err != nil {
		return st, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return st, fmt.Errorf("jetstream context: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, structureRPC)
	defer cancel()
	for _, obj := range DeclaredObjects() {
		if obj.Scope != ScopeService {
			continue
		}
		var lookupErr error
		var notFound error
		switch obj.Kind {
		case ObjectStream:
			_, lookupErr = js.Stream(ctx, obj.Name)
			notFound = jetstream.ErrStreamNotFound
		case ObjectKV:
			_, lookupErr = js.KeyValue(ctx, obj.Name)
			notFound = jetstream.ErrBucketNotFound
		default:
			return st, fmt.Errorf("declared object %s has unknown kind %q", obj.Name, obj.Kind)
		}
		switch {
		case lookupErr == nil:
		case errors.Is(lookupErr, notFound):
			st.Missing = append(st.Missing, obj.Name)
		default:
			return st, fmt.Errorf("look up %s: %w", obj.Name, lookupErr)
		}
	}
	sort.Strings(st.Missing)

	cli := &http.Client{Timeout: 3 * time.Second}
	resp, err := cli.Get("http://" + monitorAddr + "/varz")
	if err != nil {
		return st, fmt.Errorf("read /varz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var varz struct {
		AuthRequired bool `json:"auth_required"`
		TLSRequired  bool `json:"tls_required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&varz); err != nil {
		return st, fmt.Errorf("decode /varz: %w", err)
	}
	st.Authorized, st.TLS = varz.AuthRequired, varz.TLSRequired
	return st, nil
}
