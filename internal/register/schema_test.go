package register

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	rowSchemaPath = "../../contracts/register/seat-row.schema.json"
	rowSchemaID   = "https://schema.arcaven.com/marvel/register-seat-row/v1"
)

func compileRowSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(rowSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(rowSchemaID, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(rowSchemaID)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

func validateRowJSON(t *testing.T, sch *jsonschema.Schema, raw []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode instance: %v", err)
	}
	return sch.Validate(inst)
}

// rowAsMap marshals the fixture row to a generic map the mutants edit.
func rowAsMap(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(fixtureRow())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRegisterRowMatchesSchema(t *testing.T) {
	sch := compileRowSchema(t)
	raw, err := json.Marshal(fixtureRow())
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRowJSON(t, sch, raw); err != nil {
		t.Fatalf("the fixture row does not match the schema: %v", err)
	}
	empty, err := json.Marshal(Row{Seat: "ws/seat"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRowJSON(t, sch, empty); err != nil {
		t.Fatalf("a row of empty cells does not match the schema: %v", err)
	}
}

func TestRegisterRowSchemaRejectsMutants(t *testing.T) {
	sch := compileRowSchema(t)
	longDuty := "duty.a" + strings.Repeat("b", 48)
	cases := []struct {
		name string
		edit func(m map[string]any)
	}{
		{"a free field at the row", func(m map[string]any) { m["working"] = "yes" }},
		{"an extra field in a cell", func(m map[string]any) {
			m["role"].(map[string]any)["lease"] = "x"
		}},
		{"a bare duty key", func(m map[string]any) {
			m["duties"].(map[string]any)["first-turn"] = map[string]any{}
		}},
		{"an uppercase duty key", func(m map[string]any) {
			m["duties"].(map[string]any)["duty.First"] = map[string]any{}
		}},
		{"a duty key over the length bound", func(m map[string]any) {
			m["duties"].(map[string]any)[longDuty] = map[string]any{}
		}},
		{"a missing fixed cell", func(m map[string]any) { delete(m, "bus.publish_scope") }},
		{"a missing seat", func(m map[string]any) { delete(m, "seat") }},
		{"a seat over the length bound", func(m map[string]any) { m["seat"] = strings.Repeat("a", 129) }},
		{"a non-string cell value", func(m map[string]any) {
			m["role"].(map[string]any)["value"] = 3
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := rowAsMap(t)
			tc.edit(m)
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateRowJSON(t, sch, raw); err == nil {
				t.Fatalf("the schema accepted a row with %s", tc.name)
			}
		})
	}
}

// The schema's property names and Row's JSON tags are one list. A cell added to
// one and not the other fails here.
func TestRowSchemaNamesMatchRowJSONTags(t *testing.T) {
	raw, err := os.ReadFile(rowSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Required   []string          `json:"required"`
		Properties map[string]any    `json:"properties"`
		Defs       map[string]any    `json:"$defs"`
		Extra      map[string]string `json:"-"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var tags []string
	rt := reflect.TypeOf(Row{})
	for i := range rt.NumField() {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	var props []string
	for p := range doc.Properties {
		props = append(props, p)
	}
	sort.Strings(tags)
	sort.Strings(props)
	if !reflect.DeepEqual(tags, props) {
		t.Fatalf("schema properties %v differ from Row json tags %v", props, tags)
	}
	wantRequired := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag != "duties" {
			wantRequired = append(wantRequired, tag)
		}
	}
	got := append([]string(nil), doc.Required...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, wantRequired) {
		t.Fatalf("schema required %v differ from the row's fixed fields %v", got, wantRequired)
	}
}
