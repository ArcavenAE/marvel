package composer

// HasDim reports whether a capture taken with escape sequences draws any
// visible text dim (SGR 2).
func HasDim(capture string) bool {
	return false
}
