package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// acctCell renders the opt-in ACCT cell: a short key for the account the
// session spends against, then that account's limit reading as the daemon
// stamped it on the row (docs/design/get-sessions-output.md, section 4.3).
//
// The cell is per row and nothing more. Sessions on one account repeat the
// same key and reading, and no code here or elsewhere totals, averages or
// thresholds them across rows. A fresh reading prints its fullest window, a
// stale one prints the word and not the old figure, and an account never read
// prints a dash. A row the daemon did not stamp (a held role's synthetic row)
// prints a bare dash.
func acctCell(s api.Session) string {
	if s.LimitReading == "" {
		return "-"
	}
	return acctKey(clientAccountKey(s)) + " " + acctReading(s)
}

// clientAccountKey is the account key with the home left as recorded. The
// daemon folds a login's default directory into one spelling using its own
// home; the client may run under another account (a --cluster client), so
// folding here with the client's home would merge a default row with a row
// that names the client's directory, a different login. Left unfolded, the
// worst case is a split: the default row and the same login spelled out print
// two keys. A false merge is the worse error for a column whose job is to
// show which rows share an account.
func clientAccountKey(s api.Session) api.AccountKey {
	k := api.AccountKeyOf(s)
	k.ConfigHome = ""
	if s.AccountHome != "" {
		k.ConfigHome = filepath.Clean(s.AccountHome)
	}
	return k
}

// acctKey is a short stable name for an account, so rows on one account can be
// seen to share it. It is a fingerprint of AccountKey.String, for people to
// compare, not an identity to act on.
func acctKey(k api.AccountKey) string {
	sum := sha256.Sum256([]byte(k.String()))
	return hex.EncodeToString(sum[:3])
}

// acctReading is the reading half of the cell.
func acctReading(s api.Session) string {
	switch s.LimitReading {
	case api.ReadingFresh:
		if text, ok := strings.CutPrefix(s.LimitReadingText, "fresh "); ok {
			return text
		}
		return "-"
	case api.ReadingStale:
		return "stale"
	default:
		return "-"
	}
}
