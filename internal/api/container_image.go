package api

import "regexp"

// containerImageRE is a plain image reference: it starts with a letter or digit
// and holds only the characters a registry path, tag or digest uses. Anything
// else (a space, a quote, a shell character, a leading dash that would read as a
// flag) is not an image.
var containerImageRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// ValidContainerImage reports whether s is a plain image reference. It does not
// ask whether the image exists or can be pulled.
func ValidContainerImage(s string) bool { return containerImageRE.MatchString(s) }
