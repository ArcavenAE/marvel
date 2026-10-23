package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
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
	clusterConfig(t,
		config.Cluster{Name: "a", Socket: ans.sock},
		config.Cluster{Name: "s", Socket: start.sock},
		config.Cluster{Name: "d", Socket: derr.sock},
		config.Cluster{Name: "b", Socket: bad.sock},
		config.Cluster{Name: "h", Socket: hung.sock},
		config.Cluster{Name: "x", Socket: filepath.Join(t.TempDir(), "absent.sock")},
		config.Cluster{Name: "u", Server: "mrvl://rig@" + addr, Identity: identityFile(t)},
	)
	names := "a,s,d,b,h,x,u"
	code, out, _ := runFanGet(t, "get", "sessions", "--cluster", names, "-o", "json")
	if code != 10 {
		t.Errorf("exit = %d, want 10", code)
	}
	doc := decodeSet(t, out)
	var got []string
	for _, r := range doc.Clusters {
		got = append(got, r["cluster"].(string)+"="+r["outcome"].(string))
	}
	want := []string{
		"a=answered", "s=starting", "d=daemon_error", "b=bad_reply",
		"h=no_answer_in_time", "x=unreachable", "u=host_key_unknown",
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
	if fp, _ := doc.Clusters[6]["fingerprint"].(string); fp == "" {
		t.Errorf("the host_key_unknown record has no fingerprint: %v", doc.Clusters[6])
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
	const raw = "a\x1bb‮c"
	bad := row("w", raw)
	a := startFake(t, fakeAnswers, []api.Session{bad})
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})

	_, table, _ := runFanGet(t, "get", "sessions", "--cluster", "a,b")
	if strings.ContainsAny(table, "\x1b‮") || !strings.Contains(table, fffd) {
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

// --watch is still refused across clusters, with or without -o.
func TestGetSessionsWatchStillRefusedAcrossClusters(t *testing.T) {
	a := startFake(t, fakeAnswers, nil)
	b := startFake(t, fakeAnswers, nil)
	clusterConfig(t, config.Cluster{Name: "a", Socket: a.sock}, config.Cluster{Name: "b", Socket: b.sock})
	code, _, stderr := runFanGet(t, "get", "sessions", "--cluster", "a,b", "-o", "json", "--watch=2")
	if code == 0 || !strings.Contains(stderr, "--watch across clusters is not available yet") {
		t.Errorf("exit = %d, stderr %q, want the --watch refusal", code, stderr)
	}
}
