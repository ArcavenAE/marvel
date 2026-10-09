package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/arcavenae/marvel/internal/api"
)

// marshalRedacted marshals a redacted copy of v, so no Runtime Env value that
// looks like a secret leaves the daemon whichever method built the value
// (docs/design/describe-redaction.md section 2). The store is never touched.
func marshalRedacted(v any) (json.RawMessage, error) {
	return json.Marshal(api.Redact(v))
}

// respond is the one place a successful response body is built: every handler
// returns through it, and events.watch encodes what it returns, so the
// redaction in marshalRedacted cannot be skipped by a handler that marshals its
// own bytes. A source test (response_source_test.go) fails if anything else
// sets Response.Result.
func respond(v any) Response {
	data, err := marshalRedacted(v)
	if err != nil {
		return Response{Error: fmt.Sprintf("marshal response: %v", err)}
	}
	return Response{Result: data}
}
