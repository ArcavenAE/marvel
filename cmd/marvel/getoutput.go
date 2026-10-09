package main

import (
	"errors"

	"github.com/arcavenae/marvel/internal/api"
)

// buildGetOutput is the document `get team|workspace -o` prints.
func buildGetOutput(resource string, teams []api.Team, workspaces []api.Workspace, sessions []api.Session) (map[string]any, error) {
	return nil, errors.New("not implemented")
}

// renderGetOutput encodes the document as json or yaml.
func renderGetOutput(doc map[string]any, format string) (string, error) {
	return "", errors.New("not implemented")
}
