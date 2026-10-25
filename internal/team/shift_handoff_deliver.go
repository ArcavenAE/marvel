package team

import (
	"context"

	"github.com/arcavenae/marvel/contracts/go/envelope"
)

// HandoffPublisher is the controller's only bus capability: it publishes one
// envelope to a role's inbox. The implementation builds the subject itself
// from validated tokens and can publish nowhere else, because the principal
// behind it can publish to ">" (docs/design/handoff-missing-delivery.md,
// section 4). Nil means no managed bus, which is event-only escalation.
type HandoffPublisher interface {
	PublishRoleInbox(ctx context.Context, ws, team, role string, env envelope.Envelope, msgID string) error
}
