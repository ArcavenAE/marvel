// Package fixture: Marry calls a function held in a field of a package
// variable, which an init in another file sets.
package fixture

var zzCfg struct{ run func() }

func Marry() {
	if zzCfg.run != nil {
		zzCfg.run()
	}
}
