// Package fixture: Marry calls a function from a package map, whose element
// an init in another file sets.
package fixture

var zzHooks = map[string]func(){}

func Marry() {
	if h := zzHooks["x"]; h != nil {
		h()
	}
}
