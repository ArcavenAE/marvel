package bus

import (
	"fmt"
	"slices"
	"sort"
	"strings"
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

// InboxMaxAge is how long AGENT_INBOX keeps a message: 7 days (operator ruling
// I-65, 2026-10-30). It was 24h, which let an idle seat's mail expire unread.
// Provision leaves an existing stream as found, so a live broker keeps its
// old value until the stream is edited in place.
const InboxMaxAge = 168 * time.Hour

// DeclaredObjects is the object set. The three carry the exact parameters
// of director sim/twin/README.md precondition 6 (brief 10 section 5,
// aae-orc-apeoc). EVENTS_GH and EVENTS_MARVEL join under aae-orc-aubd6.
func DeclaredObjects() []Object {
	return []Object{
		{
			Name: InboxStream, Kind: ObjectStream, Scope: ScopeService,
			Origin: "brief 10 section 5 (aae-orc-apeoc): the shim's inbox, two subject shapes; 7 days since operator ruling I-65 (2026-10-30), up from 24h, so an idle seat's mail does not expire unread",
			Stream: &jetstream.StreamConfig{
				Name:       InboxStream,
				Subjects:   inboxSubjects,
				Storage:    jetstream.FileStorage,
				Retention:  jetstream.LimitsPolicy,
				MaxAge:     InboxMaxAge,
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
			// the audit trail. The one publisher that reaches every inbox in
			// every workspace (global-bus-tier.md section 4.1, R-95). The
			// per-role supervisor users below also publish across
			// workspaces since the operator ruling of 2026-10-28, but only to
			// the exact role inboxes of other teams' global roles, never a
			// wildcard.
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
	peers := peerInboxes(teams, s.HubURL != "")
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
		basePub := append([]string{own, "agent.audit"}, plumbingPublish...)
		baseSub := append([]string{own, fmt.Sprintf("agent.%s.broadcast", t.Workspace)}, plumbingSubscribe...)
		pub := slices.Clone(basePub)
		sub := slices.Clone(baseSub)
		globalPub := []string{"global.director.inbox", "global.*.supervisor.inbox", "$JS.global.API.>"}
		hasGlobal := len(t.GlobalRoles) > 0 && s.HubURL != ""
		if hasGlobal {
			// global.*.supervisor.inbox: any supervisor may publish to every
			// cluster's supervisor inbox (director#155, operator-granted).
			// Stage 2 of per-role users is additive: the team user keeps these
			// grants so a running supervisor connected as the team loses
			// nothing at reload. Stage 3 removes them from this user.
			pub = append(pub, globalPub...)
			sub = append(sub, fmt.Sprintf("global.%s.>", s.Domain))
		}
		out = append(out, Principal{
			Name: t.Team, Scope: ScopeBinding, Team: t.Team,
			Publish: pub, Subscribe: sub,
			Origin:   "brief 10 section 3 (aae-orc-e9g8i): one confined user per applied team (director#4 shape); supervisor global grants per global-bus-tier.md 4.1, plus publish on every cluster's supervisor inbox per director#155",
			Password: t.Password,
		})
		for _, ru := range t.GlobalRoles {
			if !hasGlobal {
				break
			}
			if err := validToken("role", ru.Role); err != nil {
				return nil, err
			}
			if ru.Password == "" {
				return nil, fmt.Errorf("team %s/%s declares global role %s but its user has no password", t.Workspace, t.Team, ru.Role)
			}
			// The per-role user (per-role-broker-users.md section 3, named by
			// role in global-role-declaration.md section 4): the team user's
			// own-subtree grants, the global publishes, and a subscribe
			// narrowed to the cluster's supervisor inbox, not global.<domain>.>.
			//
			// Peer publish (operator ruling 2026-10-28): the role inbox of every
			// other applied team's global role on this cluster, exact subjects,
			// publish only, on this user and never the team user. A local
			// agent. publish does not cross the hub (global-bus-tier.md
			// section 4), so the reach stops at the cluster.
			rolePub := append(slices.Clone(basePub), globalPub...)
			for _, p := range peers {
				if p.team != t.Team {
					rolePub = append(rolePub, p.subject)
				}
			}
			out = append(out, Principal{
				Name: t.Team + "." + ru.Role, Scope: ScopeBinding, Team: t.Team,
				Publish:   rolePub,
				Subscribe: append(slices.Clone(baseSub), fmt.Sprintf("global.%s.supervisor.inbox", s.Domain)),
				Origin:    "global-role-declaration.md section 4 (marvel#518): the role's own user, so its global reach does not ride the team user; publish on the other teams' global role inboxes on this cluster per the operator ruling of 2026-10-28",
				Password:  ru.Password,
			})
		}
	}
	return out, nil
}

// peerInbox is one global role's inbox subject and the team it belongs to.
type peerInbox struct {
	team    string
	subject string
}

// peerInboxes lists the role inbox of every global role on the cluster, in
// subject order so the rendered file is the same from one render to the next.
// With no hub no per-role user renders, so the list is empty and no user
// carries a peer grant. The subjects are exact: no wildcard token, so a team
// applied later, or a role the cluster does not admit, is not reachable until
// it is rendered into GlobalRoles.
func peerInboxes(teams []TeamUser, hasHub bool) []peerInbox {
	if !hasHub {
		return nil
	}
	var out []peerInbox
	for _, t := range teams {
		for _, ru := range t.GlobalRoles {
			out = append(out, peerInbox{team: t.Team, subject: fmt.Sprintf("agent.%s.%s.role.%s.inbox", t.Workspace, t.Team, ru.Role)})
		}
	}
	slices.SortFunc(out, func(a, b peerInbox) int { return strings.Compare(a.subject, b.subject) })
	return out
}
