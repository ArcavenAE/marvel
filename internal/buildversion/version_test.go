package buildversion

import "testing"

func TestVersionOlder(t *testing.T) {
	cases := []struct {
		a, b         string
		older, known bool
		why          string
	}{
		{"0.1.0", "0.2.0", true, true, "lower minor"},
		{"0.2.0", "0.1.0", false, true, "higher minor"},
		{"0.2.0", "0.2.0", false, true, "equal"},
		{"0.2.0-alpha.1", "0.2.0", true, true, "a prerelease is below its release"},
		{"0.2.0", "0.2.0-alpha.1", false, true, "a release is above its prerelease"},
		{"0.1.0-alpha.20261002.231214.b533ca5", "0.1.0-alpha.20261008.044910.0d33f29", true, true, "earlier alpha"},
		{"0.1.0-alpha.20261008.044910.0d33f29", "0.1.0-alpha.20261002.231214.b533ca5", false, true, "later alpha"},
		{"0.1.0-alpha.9", "0.1.0-alpha.10", true, true, "numeric identifiers compare as numbers"},
		{"v0.3.0", "0.2.9", false, true, "a leading v is ignored"},
		{"0.2", "0.2.0", false, true, "a shorter core pads with zero"},
		{"dev", "0.2.0", false, false, "dev has no order"},
		{"0.2.0", "dev", false, false, "dev has no order"},
		{"nonsense", "0.2.0", false, false, "unparseable"},
	}
	for _, c := range cases {
		older, known := VersionOlder(c.a, c.b)
		if older != c.older || known != c.known {
			t.Errorf("%s: VersionOlder(%q, %q) = %v, %v; want %v, %v", c.why, c.a, c.b, older, known, c.older, c.known)
		}
	}
}

// A build that was not stamped reads dev, and dev has no order against any
// version. Set records the stamp, an empty stamp changes nothing, and the
// stamped alpha form orders by the date and time in it.
func TestVersionReadsTheStampOrDev(t *testing.T) {
	t.Cleanup(func() { stamped = "dev" })
	if got := Version(); got != "dev" {
		t.Fatalf("unstamped Version = %q, want dev", got)
	}
	if older, known := VersionOlder(Version(), "0.1.0"); older || known {
		t.Errorf("VersionOlder(dev, 0.1.0) = %v, %v; want false, false", older, known)
	}
	const stamp = "0.1.0-alpha.20261008.044910.0d33f29"
	Set(stamp)
	if got := Version(); got != stamp {
		t.Fatalf("stamped Version = %q, want %q", got, stamp)
	}
	Set("")
	if got := Version(); got != stamp {
		t.Errorf("an empty Set changed Version to %q", got)
	}
	if older, known := VersionOlder("0.1.0-alpha.20261002.231214.b533ca5", stamp); !older || !known {
		t.Errorf("an earlier alpha: %v, %v; want true, true", older, known)
	}
}
