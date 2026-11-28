// Package middle imports no os/exec itself; it leads to the package that does.
package middle

import _ "github.com/arcavenae/marvel/internal/layering/testdata/execrule/second"
