package bus

import (
	"fmt"
	"sort"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/arcavenae/marvel/internal/config"
)

// The declared provisioning set (docs/design/bus-as-service.md section 4):
// one Go value naming every object the managed broker must carry and every
// principal its authorization file must hold. Three consumers read it and
// nothing else declares a stream, bucket, or user: the renderer writes
// Principals into authorization.conf, the provisioner creates the Objects
// that are absent and alters nothing present, and the structural health
// check (aae-orc-vy6k7) confirms the service-scope Objects exist. A stream
// cannot be added without its grant or the other way around, because both
// come from here.
//
// Origin on each entry names the brief or design that declared it, so a
// reader a year from now knows why an object has the parameters it has.

// Scope says what an entry belongs to. A service-scope entry exists once
// per broker; a binding-scope entry exists once per applied team.
type Scope string

const (
	ScopeService Scope = "service"
	ScopeBinding Scope = "binding"
)

// ObjectKind is what a declared object is.
type ObjectKind string

const (
	ObjectStream ObjectKind = "stream"
	ObjectKV     ObjectKind = "kv"
)

// Object is one JetStream stream or KV bucket the broker must carry.
// Exactly one of Stream and KV is set, by Kind.
type Object struct {
	Name   string
	Kind   ObjectKind
	Scope  Scope
	Origin string
	Stream *jetstream.StreamConfig
	KV     *jetstream.KeyValueConfig
}

// Principal is one broker user line. Team is set for binding scope. The
// password is minted by the Manager and filled in at render time; a
// declared principal carries none.
type Principal struct {
	Name      string
	Scope     Scope
	Team      string // binding scope: the team the line belongs to
	Publish   []string
	Subscribe []string
	Origin    string
	Password  string
}

// Declared is the whole set for one rendered broker.
type Declared struct {
	Objects    []Object
	Principals []Principal
}

// SeatUser is the director seat's broker identity: the user named
// director, confined to the seat's own subtree for reads and given the
// cross-workspace publish grants of the R-95 seat row.
type SeatUser struct {
	Workspace string
	Team      string
	Password  string
}

// SeatUserName is the broker user the seat presents. Reserved: a team by
// this name is refused (config.ReservedBusUsers).
const SeatUserName = "director"

// Plumbing every non-admin principal needs to use JetStream, the presence
// bucket, and request-reply.
var (
	plumbingPublish   = []string{"$JS.API.>", "$JS.ACK.>", "$KV." + StateBucket + ".>", "_INBOX.>"}
	plumbingSubscribe = []string{"$KV." + StateBucket + ".>", "_INBOX.>"}
)

// DeclaredObjects is the object set. The three carry the exact parameters
// of director sim/twin/README.md precondition 6 (brief 10 section 5,
// aae-orc-apeoc). EVENTS_GH and EVENTS_MARVEL join under aae-orc-aubd6.
func DeclaredObjects() []Object {
	return []Object{
		{
			Name: InboxStream, Kind: ObjectStream, Scope: ScopeService,
			Origin: "brief 10 section 5 (aae-orc-apeoc): the shim's inbox, two subject shapes",
			Stream: &jetstream.StreamConfig{
				Name:       InboxStream,
				Subjects:   inboxSubjects,
				Storage:    jetstream.FileStorage,
				Retention:  jetstream.LimitsPolicy,
				MaxAge:     24 * time.Hour,
				MaxMsgSize: 65536,
				Duplicates: 2 * time.Minute,
			},
		},
		{
			Name: AuditStream, Kind: ObjectStream, Scope: ScopeService,
			Origin: "brief 10 section 5 (aae-orc-apeoc): the audit trail, 30 days",
			Stream: &jetstream.StreamConfig{
				Name:      AuditStream,
				Subjects:  []string{"agent.audit"},
				Storage:   jetstream.FileStorage,
				Retention: jetstream.LimitsPolicy,
				MaxAge:    720 * time.Hour,
			},
		},
		{
			Name: StateBucket, Kind: ObjectKV, Scope: ScopeService,
			Origin: "brief 10 section 5 (aae-orc-apeoc): presence, 90s TTL",
			KV: &jetstream.KeyValueConfig{
				Bucket:  StateBucket,
				TTL:     90 * time.Second,
				Storage: jetstream.FileStorage,
			},
		},
	}
}

// DeclaredPrincipals is the principal set for a Spec: the admin, the seat
// user when a seat is declared, and one binding-scope user per applied
// team, sorted by workspace then team. It validates what the renderer
// used to: every name a subject token, every team with a password, no
// team name applied twice across workspaces (users are named by team, so
// the second would share the first's grants), and no team by a reserved
// user name.
func DeclaredPrincipals(s Spec) ([]Principal, error) {
	if s.Admin.Name == "" || s.Admin.Password == "" {
		return nil, fmt.Errorf("admin user and password are required")
	}
	out := []Principal{{
		Name: s.Admin.Name, Scope: ScopeService,
		Publish: []string{">"}, Subscribe: []string{">"},
		Origin:   "brief 10 section 3 (aae-orc-e9g8i): marvel's own identity, provisioning and break-glass, never handed to a session",
		Password: s.Admin.Password,
	}}
	if s.Seat != nil {
		if err := validToken("seat workspace", s.Seat.Workspace); err != nil {
			return nil, err
		}
		if err := validToken("seat team", s.Seat.Team); err != nil {
			return nil, err
		}
		if s.Seat.Password == "" {
			return nil, fmt.Errorf("seat user has no password")
		}
		own := fmt.Sprintf("agent.%s.%s.>", s.Seat.Workspace, s.Seat.Team)
		out = append(out, Principal{
			Name: SeatUserName, Scope: ScopeService,
			// Publish-only across workspaces: every inbox, every broadcast,
			// the audit trail. The one cross-workspace publisher the
			// asymmetry permits (global-bus-tier.md section 4.1, R-95).
			Publish: append([]string{
				"agent.*.*.*.inbox", "agent.*.*.role.*.inbox",
				"agent.*.*.broadcast", "agent.*.broadcast", "agent.audit",
			}, plumbingPublish...),
			// Reads its own subtree and its own workspace's broadcast; no
			// other workspace.
			Subscribe: append([]string{own, fmt.Sprintf("agent.%s.broadcast", s.Seat.Workspace)}, plumbingSubscribe...),
			Origin:    "global-bus-tier.md section 4.1, the R-95 seat row; bus-as-service.md section 8.2",
			Password:  s.Seat.Password,
		})
	}

	teams := append([]TeamUser(nil), s.Teams...)
	sort.Slice(teams, func(i, j int) bool {
		if teams[i].Workspace != teams[j].Workspace {
			return teams[i].Workspace < teams[j].Workspace
		}
		return teams[i].Team < teams[j].Team
	})
	seen := map[string]string{}
	for _, t := range teams {
		if err := validToken("workspace", t.Workspace); err != nil {
			return nil, err
		}
		if err := validToken("team", t.Team); err != nil {
			return nil, err
		}
		if t.Password == "" {
			return nil, fmt.Errorf("team %s/%s has no password", t.Workspace, t.Team)
		}
		for _, r := range config.ReservedBusUsers {
			if t.Team == r {
				return nil, fmt.Errorf("team name %q is a reserved broker user name; it is refused, not merged", t.Team)
			}
		}
		if other, dup := seen[t.Team]; dup {
			return nil, fmt.Errorf("team name %q is applied in workspaces %q and %q; broker users are named by team, so the second is refused rather than sharing the first's grants", t.Team, other, t.Workspace)
		}
		seen[t.Team] = t.Workspace
		own := fmt.Sprintf("agent.%s.%s.>", t.Workspace, t.Team)
		pub := append([]string{own, "agent.audit"}, plumbingPublish...)
		sub := append([]string{own, fmt.Sprintf("agent.%s.broadcast", t.Workspace)}, plumbingSubscribe...)
		if t.Supervisor && s.HubURL != "" {
			pub = append(pub, "global.director.inbox", "$JS.global.API.>")
			sub = append(sub, fmt.Sprintf("global.%s.>", s.Domain))
		}
		out = append(out, Principal{
			Name: t.Team, Scope: ScopeBinding, Team: t.Team,
			Publish: pub, Subscribe: sub,
			Origin:   "brief 10 section 3 (aae-orc-e9g8i): one confined user per applied team (director#4 shape); supervisor global grants per global-bus-tier.md 4.1",
			Password: t.Password,
		})
	}
	return out, nil
}
