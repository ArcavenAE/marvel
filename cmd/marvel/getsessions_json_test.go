package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/knownhosts"
	"github.com/arcavenae/marvel/internal/paths"
)

// setDoc is the document `get sessions -o json` prints for a cluster set.
type setDoc struct {
	Clusters []map[string]any `json:"clusters"`
	Sessions []map[string]any `json:"sessions"`
}

func decodeSet(t *testing.T, out string) setDoc {
	t.Helper()
	var doc setDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not the set document: %v\n%s", err, out)
	}
	return doc
}

// With one cluster, -o json prints the sessions as a list, as the daemon sent
// them, with no cluster field.
func TestGetSessionsOutputJSONSingleClusterIsAList(t *testing.T) {
	cannedDaemon(t, map[string]any{"session": []api.Session{row("w", "s1"), row("w", "s2")}})
	out, err := runGet(t, "sessions", "-o", "json")
	if err != nil {
		t.Fatalf("get sessions -o json: %v", err)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("stdout is not a JSON list: %v\n%s", err, out)
	}
	if len(list) != 2 || list[0]["Name"] != "s1" || list[1]["Name"] != "s2" {
		t.Errorf("list = %v, want the two sessions in the order the daemon sent them", list)
	}
	for _, r := range list {
		if _, ok := r["cluster"]; ok {
			t.Errorf("a single-cluster row carries a cluster field: %v", r)
		}
	}
}

// No sessions is an empty list, not null.
func TestGetSessionsOutputJSONSingleClusterEmptyIsAnEmptyList(t *testing.T) {
	cannedDaemon(t, map[string]any{"session": []api.Session{}})
	out, err := runGet(t, "sessions", "-o", "json")
	if err != nil {
		t.Fatalf("get sessions -o json: %v", err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("stdout = %q, want []", out)
	}
}

// With a set, -o json prints one object holding exactly clusters and sessions.
func TestGetSessionsOutputJSONSetIsOneObject(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	b := startFake(t, fakeAnswers, []api.Session{row("w", "s2")})
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	code, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	var got []string
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)
	if want := []string{"clusters", "sessions"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	doc := decodeSet(t, out)
	if len(doc.Clusters) != 2 || len(doc.Sessions) != 2 {
		t.Errorf("%d cluster records and %d rows, want 2 and 2:\n%s", len(doc.Clusters), len(doc.Sessions), out)
	}
}

// --all-clusters prints the same object.
func TestGetSessionsOutputJSONAllClustersIsOneObject(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	code, out, _ := runFanGet(t, "get", "sessions", "--all-clusters", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	doc := decodeSet(t, out)
	if len(doc.Clusters) != 2 || len(doc.Sessions) != 1 {
		t.Errorf("%d cluster records and %d rows, want 2 and 1:\n%s", len(doc.Clusters), len(doc.Sessions), out)
	}
}

// Every row names the cluster it came from, and the rows sort as the table
// does: cluster, then workspace and name.
func TestGetSessionsOutputJSONRowsCarryTheirCluster(t *testing.T) {
	a := startFake(t, fakeAnswers, []api.Session{row("z", "zz"), row("a", "aa")})
	b := startFake(t, fakeAnswers, []api.Session{row("a", "aa")})
	clusterConfig(t, config.Cluster{Name: "b", Socket: b.sock}, config.Cluster{Name: "a", Socket: a.sock})
	_, out, _ := runFanGet(t, "get", "sessions", "--cluster", "b,a", "-o", "json")
	var got []string
	for _, r := range decodeSet(t, out).Sessions {
		c, ok := r["cluster"].(string)
		if !ok || c == "" {
			t.Fatalf("a row has no cluster field: %v", r)
		}
		got = append(got, c+":"+r["Workspace"].(string)+"/"+r["Name"].(string))
	}
	if want := []string{"a:a/aa", "a:z/zz", "b:a/aa"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

// One record per cluster, in the order named, each with the outcome the table
// path gives it, and every outcome name is in the closed set. The exit code is
// the one the table gives.
func TestGetSessionsOutputJSONOneRecordPerClusterAcrossTheOutcomes(t *testing.T) {
	fastTimer(t, 400*time.Millisecond)
	ans := startFake(t, fakeAnswers, []api.Session{row("w", "s1")})
	start := startFake(t, fakeStarting, []api.Session{row("w", "s2")})
	derr := startFake(t, fakeDaemonError, nil)
	bad := startFake(t, fakeBadReply, nil)
	hung := startFake(t, fakeHung, nil)
	addr := hostKeyServer(t)
	changed := hostKeyServer(t)
	clusterConfig(t,
		config.Cluster{Name: "a", Socket: ans.sock},
		config.Cluster{Name: "s", Socket: start.sock},
		config.Cluster{Name: "d", Socket: derr.sock},
		config.Cluster{Name: "b", Socket: bad.sock},
		config.Cluster{Name: "h", Socket: hung.sock},
		config.Cluster{Name: "x", Socket: filepath.Join(t.TempDir(), "absent.sock")},
		config.Cluster{Name: "u", Server: "mrvl://rig@" + addr, Identity: identityFile(t)},
		config.Cluster{Name: "k", Server: "mrvl://rig@" + changed, Identity: identityFile(t)},
		config.Cluster{Name: "n", Server: "mrvl://rig@" + addr},
	)
	layout, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureHome(); err != nil {
		t.Fatal(err)
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	other, err := ssh.NewSignerFromKey(otherPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := knownhosts.Trust(layout, changed, other.PublicKey()); err != nil {
		t.Fatal(err)
	}
	names := "a,s,d,b,h,x,u,k,n"
	code, out, stderr := runFanGet(t, "get", "sessions", "--cluster", names, "-o", "json")
	if code != 10 {
		t.Errorf("exit = %d, want 10", code)
	}
	if stderr != "" {
		t.Errorf("stderr under -o = %q, want empty: the records carry the status", stderr)
	}
	doc := decodeSet(t, out)
	var got []string
	for _, r := range doc.Clusters {
		got = append(got, r["cluster"].(string)+"="+r["outcome"].(string))
	}
	want := []string{
		"a=answered", "s=starting", "d=daemon_error", "b=bad_reply",
		"h=no_answer_in_time", "x=unreachable", "u=host_key_unknown",
		"k=host_key_changed", "n=auth_refused",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("records = %v, want %v", got, want)
	}
	closed := map[string]bool{}
	for _, o := range allOutcomes {
		closed[string(o)] = true
	}
	for _, r := range doc.Clusters {
		if o := r["outcome"].(string); !closed[o] {
			t.Errorf("outcome %q is not in the closed set", o)
		}
		if r["as_of"] == nil || r["as_of"] == "" {
			t.Errorf("record %v has no as_of", r)
		}
	}
	for _, i := range []int{6, 7} {
		if fp, _ := doc.Clusters[i]["fingerprint"].(string); fp == "" {
			t.Errorf("the %v record has no fingerprint: %v", doc.Clusters[i]["outcome"], doc.Clusters[i])
		}
	}
	if n, _ := doc.Clusters[0]["sessions"].(float64); n != 1 {
		t.Errorf("the answered record counts %v sessions, want 1", doc.Clusters[0]["sessions"])
	}
	if len(doc.Sessions) != 2 {
		t.Errorf("%d rows, want the 2 from the clusters that gave rows", len(doc.Sessions))
	}
}

// A cluster that gave nothing is still a record, and an empty rows list is [],
// not null.
func TestGetSessionsOutputJSONNoneAnsweredStillPrintsTheRecords(t *testing.T) {
	clusterConfig(t,
		config.Cluster{Name: "x", Socket: filepath.Join(t.TempDir(), "absent.sock")},
		config.Cluster{Name: "y", Socket: filepath.Join(t.TempDir(), "absent2.sock")},
	)
	code, out, _ := runFanGet(t, "get", "sessions", "--cluster", "x,y", "-o", "json")
	if code != exitUnreachable {
		t.Errorf("exit = %d, want %d (both unreachable)", code, exitUnreachable)
	}
	doc := decodeSet(t, out)
	if len(doc.Clusters) != 2 {
		t.Errorf("%d records, want 2:\n%s", len(doc.Clusters), out)
	}
	if !strings.Contains(out, `"sessions": []`) {
		t.Errorf("an empty rows list is not []:\n%s", out)
	}
}

// The table replaces a name's control and bidi characters with U+FFFD and
// says -o json shows them; -o json shows them. The JSON decodes to the
// daemon's bytes, in a single-cluster list and in a set.
func TestGetSessionsOutputJSONShowsWhatTheTableReplaces(t *testing.T) {
	const raw = "a\x1bb\u202ec"
	bad := row("w", raw)
	a := startFake(t, fakeAnswers, []api.Session{bad})
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})

	_, table, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	if strings.ContainsAny(table, "\x1b\u202e") || !strings.Contains(table, fffd) {
		t.Fatalf("the table is not the control: want U+FFFD and no raw characters:\n%q", table)
	}

	_, out, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "-o", "json")
	if strings.Contains(out, fffd) {
		t.Errorf("-o json replaced a character with U+FFFD:\n%s", out)
	}
	rows := decodeSet(t, out).Sessions
	if len(rows) != 1 || rows[0]["Name"] != raw {
		t.Errorf("set row names = %v, want the raw %q", rows, raw)
	}

	cannedDaemon(t, map[string]any{"session": []api.Session{bad}})
	single, err := runGet(t, "sessions", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(single), &list); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(single, fffd) || len(list) != 1 || list[0]["Name"] != raw {
		t.Errorf("single-cluster names = %v (U+FFFD present: %v), want the raw %q", list, strings.Contains(single, fffd), raw)
	}
}

// -o is refused for a format that is not json or yaml, before any dial, for a
// set as for one cluster.
func TestGetSessionsOutputRefusesAnUnknownFormatBeforeAnyDial(t *testing.T) {
	a := startFake(t, fakeAnswers, nil)
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	code, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,b", "-o", "xml")
	if code == 0 || !strings.Contains(stderr, "json or yaml") {
		t.Errorf("exit = %d, stderr %q, want a refusal naming json or yaml", code, stderr)
	}
	if a.conns()+b.conns() != 0 {
		t.Errorf("%d connections before the refusal, want 0", a.conns()+b.conns())
	}
}

// --watch across clusters cannot be combined with -o, as on one cluster; and
// without -o it needs a terminal, which a test run does not have. Both are
// refused before any cluster is dialled.
func TestGetSessionsWatchAcrossClustersRefusals(t *testing.T) {
	a := startFake(t, fakeAnswers, nil)
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	cases := map[string]struct {
		args []string
		want string
	}{
		"with -o":     {[]string{"get", "sessions", "--cluster", "a,b", "-o", "json", "--watch=2"}, "-o and --watch cannot be combined"},
		"no tty":      {[]string{"get", "sessions", "--cluster", "a,b", "--watch=2"}, "watch mode requires a terminal"},
		"bare no tty": {[]string{"get", "sessions", "--all-clusters", "-w"}, "watch mode requires a terminal"},
	}
	for name, tc := range cases {
		code, _, stderr := runFanGet(t, tc.args...)
		if code == 0 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: exit = %d, stderr %q, want %q", name, code, stderr, tc.want)
		}
	}
	if a.conns()+b.conns() != 0 {
		t.Errorf("%d connections before the refusals, want 0", a.conns()+b.conns())
	}
}

// withSecret is a session whose Env holds a secret-looking value and an
// ordinary one, as a daemon that does not redact would send it.
func withSecret(ws, name string) api.Session {
	r := row(ws, name)
	r.Runtime = api.Runtime{Name: "claude", Command: "claude", Env: map[string]string{
		"API_KEY": "sk-test-9f8e7d6c", "REGION": "eu-west",
	}}
	return r
}

// A secret-looking Env value never reaches -o output, even when the daemon
// sent it unredacted (an older daemon), for one cluster and for a set, in
// both formats; an ordinary value stays.
func TestGetSessionsOutputRedactsSecretEnv(t *testing.T) {
	const secret = "sk-test-9f8e7d6c"
	check := func(t *testing.T, what, out string) {
		t.Helper()
		if strings.Contains(out, secret) {
			t.Errorf("%s output carries the secret value:\n%s", what, out)
		}
		if !strings.Contains(out, api.Redacted) || !strings.Contains(out, "eu-west") {
			t.Errorf("%s output lacks %q for the secret or the kept eu-west:\n%s", what, api.Redacted, out)
		}
	}
	for _, format := range []string{"json", "yaml"} {
		cannedDaemon(t, map[string]any{"session": []api.Session{withSecret("w", "s1")}})
		out, err := runGet(t, "sessions", "-o", format)
		if err != nil {
			t.Fatalf("single cluster -o %s: %v", format, err)
		}
		check(t, "single-cluster "+format, out)

		a := startFake(t, fakeAnswers, []api.Session{withSecret("w", "s1")})
		b := startFake(t, fakeAnswers, []api.Session{withSecret("w", "s2")})
		clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
		code, set, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b", "-o", format)
		if code != 0 {
			t.Fatalf("set -o %s: exit %d", format, code)
		}
		check(t, "set "+format, set)
	}
}
