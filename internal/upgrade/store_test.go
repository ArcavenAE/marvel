package upgrade

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arcavenae/marvel/internal/paths"
)

func TestParseChecksumsReadsSha256sumOutput(t *testing.T) {
	list := "AAAA  marvel-linux-amd64.sig\n" +
		"bbbb  marvel-linux-amd64\n" +
		"cccc *marvel-darwin-arm64\n" +
		"not a line at all\n"
	for _, c := range []struct{ name, want string }{
		{"marvel-linux-amd64", "bbbb"},
		{"marvel-darwin-arm64", "cccc"},
		{"marvel-linux-amd64.sig", "aaaa"},
	} {
		got, ok := parseChecksums([]byte(list), c.name)
		if !ok || got != c.want {
			t.Errorf("parseChecksums(%q) = %q, %v; want %q, true", c.name, got, ok, c.want)
		}
	}
	if got, ok := parseChecksums([]byte(list), "marvel-linux-arm64"); ok {
		t.Errorf("a name with no line was found: %q", got)
	}
}

// serveRelease serves files by name and returns a release whose assets point
// at them. A name mapped to "" answers 404.
func serveRelease(t *testing.T, tag string, files map[string]string) *githubRelease {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok || body == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	rel := &githubRelease{TagName: tag}
	for name := range files {
		rel.Assets = append(rel.Assets, githubAsset{Name: name, BrowserDownloadURL: srv.URL + "/" + name})
	}
	return rel
}

func TestVerifyDownloadHoldsAReleaseToItsChecksums(t *testing.T) {
	body := []byte("marvel build two")
	good := sumBytes(body) + "  marvel-linux-amd64\n"
	client := http.DefaultClient

	rel := serveRelease(t, releaseTag, map[string]string{"marvel-linux-amd64": string(body), checksumsAsset: good})
	if ok, err := verifyDownload(client, rel, "marvel-linux-amd64", body); err != nil || !ok {
		t.Errorf("a matching download: verified=%v err=%v, want verified", ok, err)
	}

	if ok, err := verifyDownload(client, rel, "marvel-linux-amd64", []byte("something else")); err == nil || ok {
		t.Errorf("a download that does not match was accepted (verified=%v)", ok)
	} else if !strings.Contains(err.Error(), "does not match") || !strings.Contains(err.Error(), "nothing was installed") {
		t.Errorf("error = %q, want it to say the download does not match and nothing was installed", err)
	}

	missing := serveRelease(t, releaseTag, map[string]string{checksumsAsset: sumBytes(body) + "  marvel-darwin-arm64\n"})
	if ok, err := verifyDownload(client, missing, "marvel-linux-amd64", body); err == nil || ok {
		t.Errorf("a list without the asset was accepted (verified=%v)", ok)
	}

	unreadable := serveRelease(t, releaseTag, map[string]string{checksumsAsset: ""})
	if ok, err := verifyDownload(client, unreadable, "marvel-linux-amd64", body); err == nil || ok {
		t.Errorf("a list that cannot be read was accepted (verified=%v)", ok)
	}
}

// A release cut before checksums.txt was published cannot be checked. That is
// not an error; the build is recorded unverified.
func TestVerifyDownloadWithoutChecksumsIsUnverified(t *testing.T) {
	rel := serveRelease(t, olderTag, map[string]string{"marvel-linux-amd64": "x"})
	ok, err := verifyDownload(http.DefaultClient, rel, "marvel-linux-amd64", []byte("x"))
	if err != nil || ok {
		t.Errorf("verified=%v err=%v, want unverified and no error", ok, err)
	}
}

// directLayout is a direct install at <tmp>/bin/marvel holding "build one",
// and a marvel home of its own.
func directLayout(t *testing.T) (l paths.Layout, bin string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(root, "bin", "marvel")
	writeBinary(t, bin, "build one")
	return paths.WithHome(filepath.Join(root, "home")), bin
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustRecord(t *testing.T, l paths.Layout) Record {
	t.Helper()
	rec, found, err := ReadRecord(l)
	if err != nil || !found {
		t.Fatalf("version record: found=%v err=%v", found, err)
	}
	return rec
}

// The direct channel used to delete the build it replaced (marvel#910). It now
// keeps it under the marvel home by version and records both builds.
func TestInstallDirectKeepsThePriorAndRecordsBoth(t *testing.T) {
	l, bin := directLayout(t)
	captureErr(t)

	if err := installDirect(l, bin, []byte("build two"), releaseTag, olderTag, true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, bin); got != "build two" {
		t.Errorf("installed binary = %q, want build two", got)
	}
	kept := filepath.Join(l.VersionsDir(), olderTag, "marvel")
	if got := readFile(t, kept); got != "build one" {
		t.Errorf("kept prior = %q, want build one", got)
	}
	if _, err := os.Stat(bin + ".old"); !os.IsNotExist(err) {
		t.Errorf("%s.old is left beside the binary (err=%v)", bin, err)
	}

	rec := mustRecord(t, l)
	want := Build{Version: releaseTag, Channel: channelDirect, Path: bin, SHA256: sumBytes([]byte("build two")), Verified: true}
	if rec.Active != want {
		t.Errorf("active = %+v, want %+v", rec.Active, want)
	}
	// No record named the prior when it was installed, so it is unverified.
	wantPrior := Build{Version: olderTag, Channel: channelDirect, Path: kept, SHA256: sumBytes([]byte("build one"))}
	if rec.Prior == nil || *rec.Prior != wantPrior {
		t.Errorf("prior = %+v, want %+v", rec.Prior, wantPrior)
	}
}

// A prior keeps the verification it was installed with, and only while it is
// the same build the record named.
func TestInstallDirectCarriesThePriorsVerification(t *testing.T) {
	for _, c := range []struct {
		name      string
		recordSum string
		recordVer bool
		wantPrior bool
	}{
		{"verified and the same build", sumBytes([]byte("build one")), true, true},
		{"recorded unverified", sumBytes([]byte("build one")), false, false},
		{"replaced out of band", sumBytes([]byte("another build")), true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, bin := directLayout(t)
			captureErr(t)
			active := Build{Version: olderTag, Channel: channelDirect, Path: bin, SHA256: c.recordSum, Verified: c.recordVer}
			if err := writeRecord(l, Record{Active: active}); err != nil {
				t.Fatal(err)
			}
			if err := installDirect(l, bin, []byte("build two"), releaseTag, olderTag, true); err != nil {
				t.Fatal(err)
			}
			if got := mustRecord(t, l).Prior; got == nil || got.Verified != c.wantPrior {
				t.Errorf("prior = %+v, want verified=%v", got, c.wantPrior)
			}
		})
	}
}

// The store keeps one prior: a second upgrade drops the build two back.
func TestInstallDirectKeepsOnePrior(t *testing.T) {
	l, bin := directLayout(t)
	captureErr(t)
	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", false); err != nil {
		t.Fatal(err)
	}
	if err := installDirect(l, bin, []byte("build three"), "v3", "v2", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.VersionsDir(), "v1")); !os.IsNotExist(err) {
		t.Errorf("the build two back is still kept (err=%v)", err)
	}
	if got := readFile(t, filepath.Join(l.VersionsDir(), "v2", "marvel")); got != "build two" {
		t.Errorf("kept prior = %q, want build two", got)
	}
	if rec := mustRecord(t, l); rec.Active.Version != "v3" || rec.Prior == nil || rec.Prior.Version != "v2" {
		t.Errorf("record = %+v, want active v3 and prior v2", rec)
	}
}

// Dropping the older prior removes only from inside the store, whatever path
// the record names.
func TestInstallDirectRemovesNothingOutsideTheStore(t *testing.T) {
	l, bin := directLayout(t)
	captureErr(t)
	outside := filepath.Join(t.TempDir(), "keep", "marvel")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	writeBinary(t, outside, "not the store's")
	if err := writeRecord(l, Record{Prior: &Build{Version: "v0", Path: outside}}); err != nil {
		t.Fatal(err)
	}
	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", false); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, outside); got != "not the store's" {
		t.Errorf("a file outside the store was touched: %q", got)
	}
}

// A version string is a directory name in the store, so one that is a path is
// replaced by the build's sum.
func TestStoreNameIsNeverAPath(t *testing.T) {
	sum := sumBytes([]byte("x"))
	for _, v := range []string{"", ".", "..", "../escape", "a/b", `a\b`} {
		if got := storeName(v, sum); got != "sha256-"+sum[:12] {
			t.Errorf("storeName(%q) = %q, want the sum", v, got)
		}
	}
	if got := storeName(releaseTag, sum); got != releaseTag {
		t.Errorf("storeName(%q) = %q, want the tag", releaseTag, got)
	}
}

// mise keeps versions in directories of its own. Its install is swapped as it
// always was and recorded, and no prior is kept in the marvel home.
func TestMiseInstallIsRecordedAndNotKept(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mise", "installs", "marvel", "1.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "marvel")
	writeBinary(t, bin, "build one")
	l := paths.WithHome(filepath.Join(root, "home"))
	captureErr(t)

	if err := installDirect(l, bin, []byte("build two"), "v2", "v1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.VersionsDir(), "v1")); !os.IsNotExist(err) {
		t.Errorf("a mise prior was kept in the marvel home (err=%v)", err)
	}
	rec := mustRecord(t, l)
	if rec.Active.Channel != channelMise || rec.Active.Path != bin || rec.Prior != nil {
		t.Errorf("record = %+v, want the mise path active and no prior", rec)
	}
}

// A Homebrew upgrade is recorded where brew put it: the new keg active, the old
// keg prior while it exists. Nothing is moved.
func TestHomebrewUpgradeRecordsTheKegs(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		name := "without cleanup"
		if cleanup {
			name = "with cleanup"
		}
		t.Run(name, func(t *testing.T) {
			self := kegLayout(t, cleanup)
			home := t.TempDir()
			prev := storeLayout
			storeLayout = func() (paths.Layout, error) { return paths.WithHome(home), nil }
			t.Cleanup(func() { storeLayout = prev })
			captureOut(t)
			captureErr(t)

			if _, err := runInstall(methodHomebrew, installedBinary(methodHomebrew, self), "0.1", "alpha", ""); err != nil {
				t.Fatalf("a successful upgrade failed: %v", err)
			}
			rec := mustRecord(t, paths.WithHome(home))
			cellar := resolvedPath(filepath.Dir(filepath.Dir(filepath.Dir(self))))
			if want := filepath.Join(cellar, "0.2", "bin", "marvel"); rec.Active.Path != want || rec.Active.Version != "0.2" ||
				rec.Active.Channel != channelHomebrew {
				t.Errorf("active = %+v, want the 0.2 keg at %s", rec.Active, want)
			}
			if cleanup {
				if rec.Prior != nil {
					t.Errorf("prior = %+v, want none: brew's cleanup removed the old keg", rec.Prior)
				}
				return
			}
			if want := filepath.Join(cellar, "0.1", "bin", "marvel"); rec.Prior == nil || rec.Prior.Path != want || rec.Prior.Version != "0.1" {
				t.Errorf("prior = %+v, want the 0.1 keg at %s", rec.Prior, want)
			}
			if got := readFile(t, filepath.Join(cellar, "0.1", "bin", "marvel")); got != "marvel 0.1" {
				t.Errorf("the old keg was changed: %q", got)
			}
			if _, err := os.Stat(filepath.Join(home, "versions", "0.1")); !os.IsNotExist(err) {
				t.Errorf("a Homebrew keg was copied into the store (err=%v)", err)
			}
		})
	}
}

// captureErr keeps the version store's notes out of the test output.
func captureErr(t *testing.T) {
	t.Helper()
	prev := errOut
	errOut = io.Discard
	t.Cleanup(func() { errOut = prev })
}
