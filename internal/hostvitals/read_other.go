//go:build !darwin && !linux

package hostvitals

// Read has no implementation outside darwin and linux. marvel builds for
// those two; anywhere else the host says nothing and the sampler reports a
// gap instead of an invented figure.
func Read() Reading { return Reading{} }
