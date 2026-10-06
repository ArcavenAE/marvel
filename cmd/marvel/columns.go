package main

import (
	"fmt"

	"github.com/arcavenae/marvel/internal/api"
)

// sessionColumn is one selectable `get sessions` column. name is the long
// name the --columns flag and display.session_columns use; header is what
// the table prints.
type sessionColumn struct {
	name   string
	header string
}

// defaultSessionColumns is today's table, in today's order.
func defaultSessionColumns() []sessionColumn {
	return []sessionColumn{
		{"workspace", "WORKSPACE"},
		{"team", "TEAM"},
		{"role", "ROLE"},
		{"generation", "GEN"},
		{"name", "AGENT NAME"},
		{"state", "STATE"},
		{"health", "HEALTH"},
		{"context", "CTX%"},
		{"cpu", "CPU%"},
		{"rss", "RSS"},
		{"desk", "DESK"},
		{"runtime", "RUNTIME"},
		{"llm", "LLM"},
	}
}

// selectSessionColumns resolves the columns to print: the flag over the
// preference over the default.
func selectSessionColumns(flag string, preference []string) ([]sessionColumn, error) {
	if flag == "" && len(preference) == 0 {
		return defaultSessionColumns(), nil
	}
	return nil, fmt.Errorf("column selection is not built yet: flag %q, preference %v", flag, preference)
}

// loadSessionColumns reads the preference from the client config and
// resolves it against the flag.
func loadSessionColumns(flag string) ([]sessionColumn, error) {
	return selectSessionColumns(flag, nil)
}

// renderSessionTableCols renders the table with the given columns.
func renderSessionTableCols(sessions []api.Session, cols []sessionColumn) string {
	if len(cols) != len(defaultSessionColumns()) {
		return fmt.Sprintf("column selection is not built yet: %d columns\n", len(cols))
	}
	return renderSessionTable(sessions)
}
