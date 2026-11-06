package jetstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	njs "github.com/nats-io/nats.go/jetstream"
)

// Presence says where a tier's sessions announce themselves, which is how a live
// durable is told from a departed one. A durable's liveness is not a property of
// the consumer: the broker keeps a durable after its session ends.
//
// The shapes are the contract read of director's reader (cmd/director-mcp/unread.go,
// quietKeys and readUnread, and global.go, writePresenceWith, at director
// fedbb54). A change on director's side changes this read.
type Presence struct {
	// Bucket is the key-value bucket holding the keys.
	Bucket string
	// Global is the hub tier: its durables are mcp_global_<id>_<instance>, and a
	// session is named by the key's JSON value (agent_id and instance), never by
	// parsing the key. The local tier names it by the key
	// presence.<team>.<id>.<instance>.
	Global bool
}

var (
	// LocalPresence is the local tier's: AGENT_STATE.
	LocalPresence = Presence{Bucket: "AGENT_STATE"}
	// GlobalPresence is the hub's: GLOBAL_PRESENCE, read over the leaf in the
	// hub's domain by the handle the caller gives that tier.
	GlobalPresence = Presence{Bucket: "GLOBAL_PRESENCE", Global: true}
)

// sessionOf returns "<id>.<instance>" for a durable name, or "" for a name that
// is not this tier's. The instance follows the last underscore.
func (p Presence) sessionOf(durable string) string {
	rest := ""
	switch {
	case p.Global && strings.HasPrefix(durable, "mcp_global_"):
		rest = strings.TrimPrefix(durable, "mcp_global_")
	case !p.Global && strings.HasPrefix(durable, "mcp_") && !strings.HasPrefix(durable, "mcp_global_"):
		rest = strings.TrimPrefix(durable, "mcp_")
	default:
		return ""
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 {
		return ""
	}
	return rest[:i] + "." + rest[i+1:]
}

// live returns the set of "<id>.<instance>" that have a presence key now. A read
// that fails is an error, so a tier whose presence is unreadable reads unknown
// and never "every durable departed". A stale key left by a crashed shim reads
// live; nothing here can tell.
func (p Presence) live(ctx context.Context, js njs.JetStream) (map[string]bool, error) {
	kv, err := js.KeyValue(ctx, p.Bucket)
	if err != nil {
		return nil, fmt.Errorf("presence bucket %s: %w", p.Bucket, err)
	}
	// kv.Keys opens a watcher, an ephemeral consumer on the bucket's stream, so
	// the keys come from stream info with a subject filter instead.
	stream, err := js.Stream(ctx, "KV_"+p.Bucket)
	if err != nil {
		return nil, fmt.Errorf("presence stream of %s: %w", p.Bucket, err)
	}
	prefix := "$KV." + p.Bucket + "."
	info, err := stream.Info(ctx, njs.WithSubjectFilter(prefix+">"))
	if err != nil {
		return nil, fmt.Errorf("presence keys of %s: %w", p.Bucket, err)
	}
	out := map[string]bool{}
	for subj := range info.State.Subjects {
		key := strings.TrimPrefix(subj, prefix)
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, njs.ErrKeyNotFound) {
			continue // the latest entry is a delete or purge marker
		}
		if err != nil {
			return nil, fmt.Errorf("presence key %s of %s: %w", key, p.Bucket, err)
		}
		if p.Global {
			var rec struct {
				AgentID  string `json:"agent_id"`
				Instance string `json:"instance"`
			}
			if json.Unmarshal(entry.Value(), &rec) != nil || rec.AgentID == "" || rec.Instance == "" {
				continue
			}
			out[rec.AgentID+"."+rec.Instance] = true
			continue
		}
		// presence.<team>.<id>.<instance>
		parts := strings.Split(key, ".")
		if len(parts) != 4 || parts[0] != "presence" {
			continue
		}
		out[parts[2]+"."+parts[3]] = true
	}
	return out, nil
}
