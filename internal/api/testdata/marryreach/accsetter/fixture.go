// Package fixture: Marry runs a function held in a package variable it reads
// through an accessor, and an init in another file sets it with a
// pointer-receiver setter called on the accessor's result.
package fixture

type zzBox struct{ run func() }

func (b *zzBox) set(f func()) { b.run = f }

var zzBoxV zzBox

func zzBoxRef() *zzBox { return &zzBoxV }

func Marry() {
	if r := zzBoxRef().run; r != nil {
		r()
	}
}
