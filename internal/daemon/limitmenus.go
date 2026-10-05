package daemon

import "github.com/arcavenae/marvel/internal/panemenu"

// shippedLimitMenus are the usage-limit samples marvel checks an inject and a
// pane against. It is empty: nothing ships a sample until the operator-gated
// capture (P-UL7) supplies one, so nothing is refused or set live today.
func shippedLimitMenus() panemenu.Samples { return panemenu.Samples{} }
