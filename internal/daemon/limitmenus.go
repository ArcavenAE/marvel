package daemon

import "github.com/arcavenae/marvel/internal/limitmenu"

// shippedLimitMenus are the usage-limit menus marvel checks an inject against.
// It is empty: limitmenu ships no sample until the operator-gated capture
// (P-UL7) supplies one, so nothing is refused live today.
func shippedLimitMenus() []limitmenu.Sample { return nil }
