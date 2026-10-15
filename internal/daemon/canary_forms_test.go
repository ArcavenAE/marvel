package daemon

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func hexOf(s string) string    { return hex.EncodeToString([]byte(s)) }
func b64Of(s string) string    { return base64.StdEncoding.EncodeToString([]byte(s)) }
func b64URLOf(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func byteList(s, sep, open, closer string) string {
	parts := make([]string, 0, len(s))
	for _, b := range []byte(s) {
		parts = append(parts, strconv.Itoa(int(b)))
	}
	return open + strings.Join(parts, sep) + closer
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedForms are the encodings a copy of the seed can take in a log or a file:
// raw, hex in both cases, base64 in the standard and URL alphabets with and
// without padding, and the byte values as a list joined three ways.
func seedForms(seed string) []string {
	forms := []string{
		seed,
		hexOf(seed),
		strings.ToUpper(hexOf(seed)),
		b64Of(seed),
		base64.RawStdEncoding.EncodeToString([]byte(seed)),
		base64.URLEncoding.EncodeToString([]byte(seed)),
		b64URLOf(seed),
	}
	for _, sep := range []string{" ", ",", ", "} {
		forms = append(forms, byteList(seed, sep, "", ""))
	}
	return forms
}

// seedLeaks names each surface that carries the seed in any of those forms.
// aae-orc-4h9xp review: the first canary looked for the raw string only, so a
// hex or byte-list copy in a log, or a raw copy in a file nobody read, passed.
func seedLeaks(surfaces map[string]string, seed string) []string {
	var hit []string
	for name, text := range surfaces {
		for _, form := range seedForms(seed) {
			if strings.Contains(text, form) {
				hit = append(hit, name)
				break
			}
		}
	}
	sort.Strings(hit)
	return hit
}

func TestTheCanaryRecognisesTheSeedInEveryFormAPlantCanTake(t *testing.T) {
	const seed = "SUADUMMYSEEDFORTHECANARYTEST"
	plants := map[string]string{
		"raw":               "key=" + seed,
		"hex":               "key=" + hexOf(seed),
		"uppercase hex":     "key=" + strings.ToUpper(hexOf(seed)),
		"base64":            "key=" + b64Of(seed),
		"url base64":        "key=" + b64URLOf(seed),
		"byte list":         "key=" + byteList(seed, " ", "[", "]"),
		"comma byte list":   "key=" + byteList(seed, ",", "[", "]"),
		"comma space bytes": "key=" + byteList(seed, ", ", "{", "}"),
	}
	for name, text := range plants {
		if got := seedLeaks(map[string]string{"a surface": text}, seed); len(got) != 1 {
			t.Errorf("a %s copy of the seed passed the canary", name)
		}
	}
	if got := seedLeaks(map[string]string{"a surface": "nothing here, SUA and 83 85 65 apart"}, seed); len(got) != 0 {
		t.Errorf("the canary fired on a surface without the seed: %v", got)
	}
}

func TestTheCanaryWalksEveryFileUnderTheRootNotOnlyTheLogStream(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "log/broker.log", "quiet")
	writeFile(t, root, "run/nats.pid", "123")
	writeFile(t, root, "state/deep/er/leak.bin", "SUADUMMYSEEDFORTHECANARYTEST")
	if got := seedLeaks(map[string]string{"the files": readAllFiles(t, root)}, "SUADUMMYSEEDFORTHECANARYTEST"); len(got) != 1 {
		t.Error("a raw seed in a file under the root was not found")
	}
}
