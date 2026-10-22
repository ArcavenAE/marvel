// Package fixture: the accessor Marry calls wraps another accessor, so it
// mentions no package variable itself, and an init in another file writes an
// element through the outer one.
package fixture

var zzHooks = map[string]func(){}

func zzInner() map[string]func() { return zzHooks }

func zzOuter() map[string]func() { return zzInner() }

func Marry() {
	if h := zzOuter()["x"]; h != nil {
		h()
	}
}
