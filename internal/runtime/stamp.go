package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
)

// The seat stamp (aae-orc-mdr0n, operator ruling I-35): a harness's own
// telemetry says which harness session it came from and nothing about which
// seat, so marvel writes the seat's identity into OTEL_RESOURCE_ATTRIBUTES at
// launch, where a harness that reads the variable picks it up. marvel sets no
// exporter and does not switch telemetry on (SOUL section 6): with telemetry
// off the attribute is inert.

// resourceAttributesEnv is the standard OpenTelemetry variable.
const resourceAttributesEnv = "OTEL_RESOURCE_ATTRIBUTES"

// stampNamespace prefixes every key marvel writes. A key under it that a role
// or the daemon's environment carries is dropped, so the namespace is marvel's.
const stampNamespace = "marvel."

// hashHex is the lowercase hex sha256 of the exact bytes of s.
func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// newFirstPrompt records a launch's first message by source, size and hash.
// The hash is left empty when no message was passed, and the text is never
// kept.
func newFirstPrompt(source, text string) FirstPrompt {
	fp := FirstPrompt{Source: source, Bytes: len(text)}
	if text != "" {
		fp.Sha256 = hashHex(text)
	}
	return fp
}

// stampSeat sets OTEL_RESOURCE_ATTRIBUTES in env: the value the role declared,
// else the daemon's own, then marvel's pairs. Pairs under the marvel. namespace
// in the existing value are dropped and logged by key only, so marvel's values
// for its own namespace always win. Pairs under any other key stay as written,
// in order. A pair with no value to carry is left out, never sent empty.
//
// The two hashes are of the exact bytes passed; pass "" for one marvel did not
// pass or cannot read.
func stampSeat(ctx *LaunchContext, env map[string]string, systemPromptSha, firstPromptSha string) {
	existing, declared := env[resourceAttributesEnv]
	if !declared {
		existing = os.Getenv(resourceAttributesEnv)
	}
	var out []string
	for _, pair := range strings.Split(existing, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, _, _ := strings.Cut(pair, "=")
		if strings.HasPrefix(strings.TrimSpace(key), stampNamespace) {
			log.Printf("warning: session %s: %s key %q dropped, marvel owns the %s namespace", ctx.Session.Name, resourceAttributesEnv, strings.TrimSpace(key), stampNamespace)
			continue
		}
		out = append(out, pair)
	}

	seat, _ := seatURI(ctx, "", true)
	for _, kv := range [][2]string{
		{"workspace", ctx.Workspace.Name},
		{"team", ctx.Team.Name},
		{"role", ctx.Role.Name},
		{"session", ctx.Session.Name},
		{"seat", seat},
		{"cluster", ctx.Cluster},
		{"system_prompt.sha256", systemPromptSha},
		{"first_prompt.sha256", firstPromptSha},
	} {
		if kv[1] == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s%s=%s", stampNamespace, kv[0], encodeResourceValue(kv[1])))
	}
	env[resourceAttributesEnv] = strings.Join(out, ",")
}

// encodeResourceValue percent-encodes a value for OTEL_RESOURCE_ATTRIBUTES: the
// separators (comma, equals sign), the escape itself (percent sign), a
// semicolon, a double quote, a backslash, whitespace and control characters, and
// every byte outside printable ASCII. A plain value stays readable.
func encodeResourceValue(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte(`",;=%\`, c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// flagValue reads the value of the last occurrence of flag in words, in the
// separate form (--flag value) or the joined form (--flag=value). The last one
// wins because claude keeps only the last it is given.
func flagValue(words []string, flag string) (string, bool) {
	value, found := "", false
	for i, w := range words {
		switch {
		case w == flag && i+1 < len(words):
			value, found = words[i+1], true
		case strings.HasPrefix(w, flag+"="):
			value, found = strings.TrimPrefix(w, flag+"="), true
		}
	}
	return value, found
}
