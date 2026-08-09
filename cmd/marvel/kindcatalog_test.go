package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/events"
)

// TestKindCatalogPrintsEveryKind is the reason --list-kinds exists: the
// operator has to be able to trust that what it prints is the whole set.
// A catalog missing one name sends the reader back to the source, which
// is the state this command replaces.
func TestKindCatalogPrintsEveryKind(t *testing.T) {
	var buf bytes.Buffer
	printKindCatalog(&buf)
	out := buf.String()
	for _, k := range events.AllKinds() {
		if !strings.Contains(out, "  "+string(k)+"\n") {
			t.Errorf("catalog omits kind %q", k)
		}
	}
}

// TestKindCatalogSplitsTheTwoFamilies holds the grouping, which is the
// only structure in the output. Agent kinds depend on a runtime marvel
// can observe and control-plane kinds do not, so a reader who cannot tell
// them apart cannot tell a missing event from an unobservable one.
func TestKindCatalogSplitsTheTwoFamilies(t *testing.T) {
	var buf bytes.Buffer
	printKindCatalog(&buf)
	out := buf.String()

	controlHeader := strings.Index(out, "Control plane (")
	agentHeader := strings.Index(out, "Agent stream (")
	if controlHeader < 0 || agentHeader < 0 {
		t.Fatalf("catalog missing a group header:\n%s", out)
	}
	if controlHeader > agentHeader {
		t.Error("agent group printed before the control-plane group")
	}

	for _, k := range events.AllKinds() {
		at := strings.Index(out, "  "+string(k)+"\n")
		if at < 0 {
			continue // covered by TestKindCatalogPrintsEveryKind
		}
		isAgent := strings.HasPrefix(string(k), "agent.")
		if isAgent && at < agentHeader {
			t.Errorf("agent kind %q listed under the control-plane group", k)
		}
		if !isAgent && at > agentHeader {
			t.Errorf("control-plane kind %q listed under the agent group", k)
		}
	}
}

// TestKindCatalogCountsMatchTheGroups guards the two numbers in the
// headers. They are the only claim in the output a reader cannot check by
// counting the lines themselves without effort.
func TestKindCatalogCountsMatchTheGroups(t *testing.T) {
	var control, agent int
	for _, k := range events.AllKinds() {
		if strings.HasPrefix(string(k), "agent.") {
			agent++
		} else {
			control++
		}
	}
	var buf bytes.Buffer
	printKindCatalog(&buf)
	out := buf.String()

	for _, want := range []string{
		"Control plane (" + strconv.Itoa(control) + ")",
		"Agent stream (" + strconv.Itoa(agent) + ")",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog header missing %q:\n%s", want, out)
		}
	}
}
