package runtime

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// gooseStderrTailBytes is how much of a per-launch stderr file the reap reads:
// the last this many bytes, never the whole file. v1 section 3 says the file is
// size-capped; a shell redirect cannot cap a file as it is written, so the cap is
// met at read. The write cap belongs to the watchdog wrapper, which execs goose
// and can hold its stderr.
const gooseStderrTailBytes = 4096

// gooseCredentialShapes are the shapes the tail is scanned for before it is kept:
// goose#12413, about keys leaking into logs, is open (goose-adapter-v1.md section
// 3). A match is replaced, never kept, and the count is returned.
var gooseCredentialShapes = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|token|password|passwd)\b["']?\s*[:=]\s*["']?[^\s"']{8,}`),
}

const gooseRedacted = "[redacted]"

// readGooseStderrTail returns the last gooseStderrTailBytes of the stderr file at
// path, cut to start at a line boundary when the file was longer, with every
// credential shape replaced. It seeks to the tail and reads no further, so a
// file of any size costs one bounded read. redacted counts the replacements.
func readGooseStderrTail(path string) (tail string, redacted int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open goose stderr file: %w", err)
	}
	// Read-only: a close error loses nothing.
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat goose stderr file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return "", 0, fmt.Errorf("goose stderr file is not a regular file")
	}
	size := st.Size()
	cut := size > gooseStderrTailBytes
	if cut {
		if _, err := f.Seek(size-gooseStderrTailBytes, io.SeekStart); err != nil {
			return "", 0, fmt.Errorf("seek goose stderr file: %w", err)
		}
	}
	buf, err := io.ReadAll(io.LimitReader(f, gooseStderrTailBytes))
	if err != nil {
		return "", 0, fmt.Errorf("read goose stderr file: %w", err)
	}
	text := string(buf)
	if cut {
		// The first line is a fragment of a longer one; a credential split
		// across the seek point would not match any shape, so drop the fragment.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		} else {
			text = ""
		}
	}
	for _, re := range gooseCredentialShapes {
		text = re.ReplaceAllStringFunc(text, func(string) string {
			redacted++
			return gooseRedacted
		})
	}
	return strings.TrimRight(text, "\n"), redacted, nil
}
