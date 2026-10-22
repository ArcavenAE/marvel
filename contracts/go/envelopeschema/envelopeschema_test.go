package envelopeschema

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// The package is a leaf: it imports nothing outside the standard library, so
// a reader of the schema bytes pulls in no validator.
func TestLeafImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	got := strings.Fields(string(out))
	if len(got) != 1 || got[0] != "github.com/arcavenae/marvel/contracts/go/envelopeschema" {
		t.Errorf("non-standard dependencies = %v, want only the package itself", got)
	}
}

// The bytes are the schema document, not an empty or partial embed.
func TestJSONIsTheEnvelopeSchema(t *testing.T) {
	t.Parallel()
	var doc struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(JSON, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ID != "https://schema.arcaven.com/director/envelope/v1" {
		t.Errorf("$id = %q", doc.ID)
	}
}
