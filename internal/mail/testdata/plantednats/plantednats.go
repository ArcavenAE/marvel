// Package plantednats is the control for the import-boundary test: a package
// that imports NATS, which the boundary check must flag. It lives under
// testdata so no build or test of the module picks it up.
package plantednats

import _ "github.com/nats-io/nats.go"
