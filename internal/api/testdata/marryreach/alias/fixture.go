// Package fixture: Marry calls a function from a package map that an init in
// another file fills through a copy of the map value.
package fixture

var zzHooks = map[string]func(){}

func Marry() {
	if h := zzHooks["x"]; h != nil {
		h()
	}
}
