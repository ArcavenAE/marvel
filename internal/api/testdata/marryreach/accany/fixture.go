// Package fixture: Marry runs a function held in a package variable, and an
// init in another file writes it through an accessor whose result is an
// interface.
package fixture

var zzHook = func() {}

func zzBox() any { return &zzHook }

func Marry() { zzHook() }
