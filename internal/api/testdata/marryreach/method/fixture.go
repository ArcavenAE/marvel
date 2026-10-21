// Package fixture: Marry returns a type whose method, called by an encoder
// outside the package, reaches os/exec.
package fixture

import "os/exec"

type zzOut struct{}

func (zzOut) MarshalJSON() ([]byte, error) {
	p, err := exec.LookPath("git")
	return []byte(`"` + p + `"`), err
}

func Marry() zzOut { return zzOut{} }
