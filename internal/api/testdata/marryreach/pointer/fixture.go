// Package fixture: Marry calls a package function value that an init in
// another file replaces through a pointer.
package fixture

var zzHook = func() {}

func Marry() { zzHook() }
