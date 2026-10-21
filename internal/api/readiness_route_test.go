package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// routingVectorsPath is the shared golden file. director reads the same file.
var routingVectorsPath = filepath.Join("..", "..", "contracts", "readiness", "routing-vectors.json")

type routingVectors struct {
	Version int       `json:"version"`
	Now     time.Time `json:"now"`
	Vectors []struct {
		Name  string          `json:"name"`
		Rule  string          `json:"rule"`
		Input RouteInput      `json:"input"`
		Want  json.RawMessage `json:"want"`
	} `json:"vectors"`
}

func loadRoutingVectors(t *testing.T) routingVectors {
	t.Helper()
	b, err := os.ReadFile(routingVectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", routingVectorsPath, err)
	}
	var v routingVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("parse %s: %v", routingVectorsPath, err)
	}
	if v.Version != 1 || v.Now.IsZero() || len(v.Vectors) == 0 {
		t.Fatalf("%s: version %d, now %v, %d vectors", routingVectorsPath, v.Version, v.Now, len(v.Vectors))
	}
	return v
}

// TestRouteSeatGoldenVectors runs every shared vector: marvel and director
// must both return want for input at now.
func TestRouteSeatGoldenVectors(t *testing.T) {
	t.Parallel()
	gv := loadRoutingVectors(t)
	for _, vec := range gv.Vectors {
		t.Run(vec.Name, func(t *testing.T) {
			t.Parallel()
			var want RouteDecision
			if err := json.Unmarshal(vec.Want, &want); err != nil {
				t.Fatalf("parse want: %v", err)
			}
			got := RouteSeat(vec.Input, gv.Now)
			if !reflect.DeepEqual(got, want) {
				g, _ := json.Marshal(got)
				t.Fatalf("rule %s\n got %s\nwant %s", vec.Rule, g, vec.Want)
			}
		})
	}
}

// TestRoutingVectorsCoverEveryOutcome: each verdict and each reason appears
// in some vector, so a new reason cannot ship without a shared vector.
func TestRoutingVectorsCoverEveryOutcome(t *testing.T) {
	t.Parallel()
	gv := loadRoutingVectors(t)
	seen := map[string]bool{}
	for _, vec := range gv.Vectors {
		var want RouteDecision
		if err := json.Unmarshal(vec.Want, &want); err != nil {
			t.Fatalf("%s: parse want: %v", vec.Name, err)
		}
		seen[string(want.Verdict)] = true
		seen[string(want.Reason)] = true
		for _, d := range want.Duties {
			seen[string(d.Verdict)] = true
			seen[string(d.Reason)] = true
		}
	}
	for _, w := range []string{
		string(RouteExcluded), string(RouteEligible), string(RouteAskFirst),
		string(ReasonWithdrawn), string(ReasonStructuralFail), string(ReasonProven),
		string(ReasonHostVantage), string(ReasonTransientFail), string(ReasonUnproven),
		string(ReasonNeverObserved), string(ReasonExpired), string(ReasonOverridden),
		string(ReasonNoDuties),
	} {
		if !seen[w] {
			t.Errorf("no vector produces %q", w)
		}
	}
}

// TestRoutingVectorsUseTheReadinessVocabulary: every cell in the vectors is
// one the store could hold, so the vectors test the rule on real shapes.
func TestRoutingVectorsUseTheReadinessVocabulary(t *testing.T) {
	t.Parallel()
	gv := loadRoutingVectors(t)
	for _, vec := range gv.Vectors {
		for name, c := range vec.Input.Cells {
			duty, withdrawn := strings.CutPrefix(name, "withdrawn.")
			if !withdrawn {
				var ok bool
				if duty, ok = strings.CutPrefix(name, "duty."); !ok {
					t.Errorf("%s: cell %q is not a readiness cell", vec.Name, name)
					continue
				}
			}
			r := DutyRecord{
				Seat: vec.Input.Seat, Duty: duty, Withdrawn: withdrawn,
				State: DutyState(c.Value), Source: c.Source,
				ObservedAt: c.ObservedAt, ValidUntil: c.ValidUntil, SetBy: SetByDaemon,
			}
			if err := ValidateDutyRecord(r); err != nil {
				t.Errorf("%s: cell %s is not a valid record: %v", vec.Name, name, err)
			}
		}
	}
}

// TestRouteInputCarriesNoCount: counts, rates and time thresholds never
// exclude (design section 6, ADR-007). The input has no numeric or duration
// field for one to arrive through.
func TestRouteInputCarriesNoCount(t *testing.T) {
	t.Parallel()
	var walk func(reflect.Type, string)
	walk = func(rt reflect.Type, path string) {
		switch rt.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			t.Errorf("%s is numeric (%s); the router reads no count or threshold", path, rt)
		case reflect.Pointer, reflect.Slice, reflect.Map:
			walk(rt.Elem(), path+"[]")
		case reflect.Struct:
			if rt == reflect.TypeFor[time.Time]() {
				return
			}
			for i := range rt.NumField() {
				f := rt.Field(i)
				walk(f.Type, path+"."+f.Name)
			}
		}
	}
	walk(reflect.TypeFor[RouteInput](), "RouteInput")
}
