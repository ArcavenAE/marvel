// Package fixture: Marry runs a function held in a package variable, and an
// init in another file sets it through a setter that calls a pointer-receiver
// method on an accessor's result.
package fixture

type zzBox struct{ run func() }

func (b *zzBox) zzSet(f func()) { b.run = f }

var zzBoxV zzBox

func zzPtr() *zzBox { return &zzBoxV }

func zzSetter(f func()) { zzPtr().zzSet(f) }

func Marry() {
	if r := zzBoxV.run; r != nil {
		r()
	}
}
