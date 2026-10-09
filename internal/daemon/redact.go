package daemon

import (
	"encoding/json"

	"github.com/arcavenae/marvel/internal/api"
)

// marshalRedacted is the one place a response body becomes wire bytes: it
// marshals a redacted copy of v, so no Runtime Env value that looks like a
// secret leaves the daemon whichever method built the value
// (docs/design/describe-redaction.md section 2). The store is never touched.
func marshalRedacted(v any) (json.RawMessage, error) {
	return json.Marshal(api.Redact(v))
}
