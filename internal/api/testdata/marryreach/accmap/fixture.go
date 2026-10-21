// Package fixture: Marry reads a package map through an accessor, and an init
// in another file writes an element through the same accessor.
package fixture

var zzHooks = map[string]func(){}

func zzHookMap() map[string]func() { return zzHooks }

func Marry() {
	if h := zzHookMap()["x"]; h != nil {
		h()
	}
}
