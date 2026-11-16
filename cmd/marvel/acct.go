package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/arcavenae/marvel/internal/api"
)

// accountCell is the ACCOUNT cell: the display name the client config maps the
// account to, else the label the role or team declared, else a short key for the
// account. A row the daemon did not stamp (a held role's synthetic row) prints a
// bare dash. On a cluster-set read the key folds in the cluster; the map is
// keyed on the cluster-free key, so one account carries one name on a plain read
// and a set read, and "cluster/key" names it on one cluster.
func accountCell(s api.Session) string {
	if s.LimitReading == "" {
		return "-"
	}
	id := acctKey(clientAccountKey(s))
	if s.Cluster != "" {
		if name, ok := accountNames[s.Cluster+"/"+id]; ok {
			return name
		}
	}
	if name, ok := accountNames[id]; ok {
		return name
	}
	if s.Runtime.AccountLabel != "" {
		return s.Runtime.AccountLabel
	}
	return acctKeyIn(clientAccountKey(s), s.Cluster)
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
	} else if dir := api.ClaudeConfigDirEnv(s); dir != "" {
		k.ConfigHome = filepath.Clean(dir)
	}
	return k
}

// acctKey is a short stable name for an account, so rows on one account can be
// seen to share it. It is a fingerprint of the key's fields joined by NUL, not
// of AccountKey.String: String spells an empty home "default-home", so a home
// literally named that would hash like the default row. It is for people to
// compare, not an identity to act on.
func acctKey(k api.AccountKey) string { return acctKeyIn(k, "") }

// acctKeyIn is acctKey for a row read from a cluster. cluster is set only on a
// cluster-set read (several clusters, or --all-clusters with one configured). It is the fifth field only when non-empty, so a
// plain read hashes exactly as before; on a set read the same home path on two
// clusters is two accounts, and the key says so.
func acctKeyIn(k api.AccountKey, cluster string) string {
	fields := []string{k.Harness, string(k.Backend), string(k.CredentialSource), k.ConfigHome}
	if cluster != "" {
		fields = append(fields, cluster)
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x00")))
	return hex.EncodeToString(sum[:3])
}
