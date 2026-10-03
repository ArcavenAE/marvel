package upgrade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// formulaVersion is what the tap's formula reported on 2026-10-03
// (`brew info --json=v2 arcavenae/tap/marvel`), and releaseTag is the same
// build as GitHub names it. The two spell one build differently: dots against
// dashes, and a leading 0.1.0 on the formula (marvel#485).
const (
	formulaVersion = "0.1.0-alpha.20261002.232554.bc327df"
	releaseTag     = "alpha-20261002-232554-bc327df"
	olderTag       = "alpha-20261002-230409-de9e409"
)

// fakeBrew puts a brew on PATH. `info` prints the tap's formula JSON and every
// call is logged, so a test can tell whether `upgrade` ran. It stands in for
// Homebrew, which these tests must not run.
func fakeBrew(t *testing.T, versioned ...string) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	info, err := json.Marshal(map[string]any{"formulae": []map[string]any{{
		"full_name":          "arcavenae/tap/marvel",
		"versions":           map[string]any{"stable": formulaVersion},
		"versioned_formulae": versioned,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + log + "'\n" +
		"case \"$1\" in\n" +
		"  info) echo '" + string(info) + "' ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func brewCalls(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSameBuildAcrossTagAndFormulaSpelling(t *testing.T) {
	cases := []struct {
		tag, formula string
		want         bool
	}{
		{releaseTag, formulaVersion, true},
		{olderTag, formulaVersion, false},
		{"alpha-20261002-232554-bc327df", "0.1.0-alpha.20261002.232554.deadbee", false},
		{"v1.2.3", "1.2.3", true},
		{"v1.2.3", "1.2.4", false},
		{"bc327df", formulaVersion, false}, // a partial tag names no build
		{"", formulaVersion, false},
	}
	for _, tc := range cases {
		if got := sameBuild(tc.tag, tc.formula); got != tc.want {
			t.Errorf("sameBuild(%q, %q) = %v, want %v", tc.tag, tc.formula, got, tc.want)
		}
	}
}

// The kinu case: --version named an older build, brew installed the tap's
// latest, and marvel exited 0. Now it refuses before brew upgrade runs.
func TestBrewRefusesAVersionTheFormulaCannotInstall(t *testing.T) {
	log := fakeBrew(t)

	err := upgradeViaHomebrew("alpha", olderTag)
	if err == nil {
		t.Fatal("an older --version on brew returned nil: it would install the tap's latest and report success")
	}
	for _, want := range []string{"Homebrew", "alpha", olderTag, formulaVersion} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if !strings.Contains(err.Error(), "without --version") {
		t.Errorf("error %q does not say what to do instead", err)
	}
	if calls := brewCalls(t, log); strings.Contains(calls, "upgrade") {
		t.Errorf("brew upgrade ran after the refusal:\n%s", calls)
	}
}

func TestBrewHonorsTheFormulasOwnVersion(t *testing.T) {
	log := fakeBrew(t)

	if err := upgradeViaHomebrew("alpha", releaseTag); err != nil {
		t.Fatalf("the formula's own version was refused: %v", err)
	}
	if calls := brewCalls(t, log); !strings.Contains(calls, "upgrade arcavenae/tap/marvel") {
		t.Errorf("brew was not asked to upgrade:\n%s", calls)
	}
}

func TestBrewWithoutAVersionIsUnchanged(t *testing.T) {
	log := fakeBrew(t)

	if err := upgradeViaHomebrew("alpha", ""); err != nil {
		t.Fatalf("an upgrade with no --version failed: %v", err)
	}
	calls := brewCalls(t, log)
	if !strings.Contains(calls, "upgrade arcavenae/tap/marvel") || strings.Contains(calls, "info") {
		t.Errorf("an unpinned upgrade should only update and upgrade:\n%s", calls)
	}
}

// A versioned formula would let brew install the pinned build, but marvel
// cannot do that without a tap that has one. It refuses, naming the formula.
func TestBrewRefusesAndNamesAVersionedFormulaWhenTheTapHasOne(t *testing.T) {
	fakeBrew(t, "arcavenae/tap/marvel@"+olderTag)

	err := upgradeViaHomebrew("alpha", olderTag)
	if err == nil || !strings.Contains(err.Error(), "brew install arcavenae/tap/marvel@"+olderTag) {
		t.Fatalf("error = %v, want a refusal that names the versioned formula to install", err)
	}
}

// If the formula version cannot be read, nothing says --version is honored.
func TestBrewRefusesWhenTheFormulaVersionCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in info) echo 'not json' ;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := upgradeViaHomebrew("alpha", releaseTag); err == nil {
		t.Fatal("--version was honored without knowing the formula version")
	}
}

// Exact tag only: a partial tag used to resolve to the first release that
// contained it, which may be a different build.
func TestFindReleaseIsExactOnly(t *testing.T) {
	releases := []githubRelease{{TagName: releaseTag}, {TagName: olderTag}}

	got, err := findRelease(releases, "alpha", olderTag)
	if err != nil || got.TagName != olderTag {
		t.Fatalf("exact tag: %v, %v", got, err)
	}
	if got, err := findRelease(releases, "alpha", "de9e409"); err == nil {
		t.Fatalf("a partial tag resolved to %s", got.TagName)
	}
	if got, err := findRelease(releases, "alpha", "alpha-20261002"); err == nil {
		t.Fatalf("a tag prefix resolved to %s", got.TagName)
	}
}

func releasePage(n, from int) []githubRelease {
	page := make([]githubRelease, n)
	for i := range page {
		page[i] = githubRelease{TagName: fmt.Sprintf("alpha-%08d", from+i)}
	}
	return page
}

// serveReleases serves total releases, releasesPerPage at a time, and counts
// the requests so a test can see how far the lookup paged.
func serveReleases(t *testing.T, total int) (base string, requests *int) {
	t.Helper()
	requests = new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		from := (page - 1) * releasesPerPage
		n := total - from
		if n > releasesPerPage {
			n = releasesPerPage
		}
		if n < 0 {
			n = 0
		}
		if err := json.NewEncoder(w).Encode(releasePage(n, from)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, requests
}

// An exact tag beyond the first page is found by paging the listing.
func TestFetchReleasesPagesToFindAnExactTag(t *testing.T) {
	base, requests := serveReleases(t, 250)

	releases, err := fetchReleases(base, "alpha-00000230")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range releases {
		found = found || r.TagName == "alpha-00000230"
	}
	if !found {
		t.Fatalf("the tag on page 3 was not found among %d releases", len(releases))
	}
	if *requests != 3 {
		t.Errorf("requests = %d, want 3 (stop at the page holding the tag)", *requests)
	}
}

func TestFetchReleasesStopsAtTheLastPageForAMissingTag(t *testing.T) {
	base, requests := serveReleases(t, 250)

	releases, err := fetchReleases(base, "alpha-99999999")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 250 || *requests != 3 {
		t.Errorf("releases = %d, requests = %d, want 250 and 3", len(releases), *requests)
	}
}

func TestFetchReleasesWithoutATargetReadsOnePage(t *testing.T) {
	base, requests := serveReleases(t, 250)

	releases, err := fetchReleases(base, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != releasesPerPage || *requests != 1 {
		t.Errorf("releases = %d, requests = %d, want %d and 1", len(releases), *requests, releasesPerPage)
	}
}

func TestRefuseDev(t *testing.T) {
	err := RefuseDev("dev")
	if err == nil {
		t.Fatal("a dev build was allowed to self-upgrade")
	}
	for _, want := range []string{"dev build", "channel dev", "overwrite", "go build"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if err := RefuseDev("0.1.0-alpha.20261002.232554.bc327df"); err != nil {
		t.Errorf("a release build was refused: %v", err)
	}
}
