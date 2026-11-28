// Package root is the fixture for rule 4: it reaches os/exec through the
// allowed package and, two imports down, through a second one.
package root

import (
	_ "github.com/arcavenae/marvel/internal/layering/testdata/execrule/allowed"
	_ "github.com/arcavenae/marvel/internal/layering/testdata/execrule/middle"
)
