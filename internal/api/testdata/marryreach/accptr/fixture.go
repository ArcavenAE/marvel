// Package fixture: Marry calls a package function value through an accessor
// that returns a pointer to it, and an init in another file writes through
// the same accessor.
package fixture

var zzHook = func() {}

func zzHookPtr() *func() { return &zzHook }

func Marry() { (*zzHookPtr())() }
