package procargs

import (
	"bytes"
	"errors"
)

// ParseLsofCwd reads the working directory out of `lsof -a -d cwd -p <pid> -Fn`
// output: a "p<pid>" line, an "fcwd" line and an "n<path>" line. A directory
// name that ends in a newline is not recognised; the report shows what it can.
func ParseLsofCwd(out []byte) (string, error) {
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(line) > 1 && line[0] == 'n' {
			return string(line[1:]), nil
		}
	}
	return "", errors.New("lsof printed no working directory")
}
