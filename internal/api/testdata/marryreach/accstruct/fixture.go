// Package fixture: Marry runs a function held in a package map, and an init
// in another file writes an element through an accessor whose result is a
// struct holding the map.
package fixture

type zzConf struct{ m map[string]func() }

var zzConfV = zzConf{m: map[string]func(){}}

func zzCfg() zzConf { return zzConfV }

func Marry() {
	if h := zzConfV.m["x"]; h != nil {
		h()
	}
}
