// Package fixture: Marry uses a package-level value only as a map-literal key.
package fixture

func Marry() int { return len(map[string]int{zzKey: 1}) }
