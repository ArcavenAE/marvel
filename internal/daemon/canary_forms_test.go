package daemon

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
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

// seedLeaks names each surface that carries the seed in a form the canary
// recognises. aae-orc-4h9xp review: the first canary looked for the raw string
// only, so a hex or byte-list copy in a log, or a raw copy in a file nobody
// read, passed it.
func seedLeaks(surfaces map[string]string, seed string) []string {
	var hit []string
	for name, text := range surfaces {
		if strings.Contains(text, seed) {
			hit = append(hit, name)
		}
	}
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
