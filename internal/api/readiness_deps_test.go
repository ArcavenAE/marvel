package api

import (
	"os/exec"
	"strings"
	"testing"
)

// internal/api reads the envelope schema's agent_id class for the marry
// address rule. It must read only the schema bytes: the envelope package
// compiles the whole schema with a JSON Schema library at init, which every
// binary importing internal/api would then carry and run at start.
func TestAPIDoesNotCompileTheEnvelopeSchema(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) == 0 {
		t.Fatal("go list named no packages, so the check read nothing")
	}
	for _, d := range deps {
		if d == "github.com/arcavenae/marvel/contracts/go/envelope" || strings.HasPrefix(d, "github.com/santhosh-tekuri/jsonschema") {
			t.Errorf("internal/api depends on %s", d)
		}
	}
}
