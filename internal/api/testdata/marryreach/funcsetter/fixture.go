// Package fixture: Marry runs a function held in a package variable, and an
// init in another file sets it through a plain setter that returns nothing.
package fixture

var zzHook = func() {}

func zzSetHook(f func()) { zzHook = f }

func Marry() { zzHook() }
