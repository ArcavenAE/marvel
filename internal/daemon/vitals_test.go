package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/hostvitals"
	"github.com/arcavenae/marvel/internal/procstat"
)

func TestHostVitalsEnabledFromCluster(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cl   *config.Cluster
		want bool
	}{
		{"no cluster entry", nil, false},
		{"unset", &config.Cluster{Name: "local"}, false},
		{"on", &config.Cluster{Name: "local", HostVitals: true}, true},
	} {
		if got := hostVitalsEnabled(tc.cl); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestLoadHostVitalsReadsTheConfig covers the wiring: the cluster whose
// socket is ours turns the sampler on, another cluster's switch does not, and
// a config that is absent or unreadable leaves it off.
func TestLoadHostVitalsReadsTheConfig(t *testing.T) {
	const sock = "/tmp/m-vitals.sock"
	for _, tc := range []struct {
		name, yaml string
		want       bool
	}{
		{"matching cluster on", "clusters:\n  - name: mine\n    socket: " + sock + "\n    host_vitals: true\n", true},
		{"matching cluster unset", "clusters:\n  - name: mine\n    socket: " + sock + "\n", false},
		{"only another cluster on", "clusters:\n  - name: other\n    socket: /tmp/m-other.sock\n    host_vitals: true\n  - name: mine\n    socket: " + sock + "\n", false},
		{"unparseable config", "clusters: [", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			d := &Daemon{events: events.NewRing(16)}
			d.loadHostVitals(sock)
			if got := d.vitals.Enabled(); got != tc.want {
				t.Fatalf("vitals enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadHostVitalsWithNoConfigFileIsOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := &Daemon{events: events.NewRing(16)}
	d.loadHostVitals("/tmp/m-vitals.sock")
	if d.vitals.Enabled() {
		t.Fatal("host vitals are on with no config file")
	}
}

// The sampler rides the metrics pass: a reading on the first pass, then one
// every VitalsEvery passes, and none at all while it is off.
func TestVitalsTickReadsEveryTwelfthPass(t *testing.T) {
	for _, tc := range []struct {
		name   string
		on     bool
		passes int
		want   int
	}{
		{"off", false, 30, 0},
		{"on, first pass only", true, 1, 1},
		{"on, one short of the second", true, VitalsEvery, 1},
		{"on, the second", true, VitalsEvery + 1, 2},
		{"on, 25 passes", true, 2*VitalsEvery + 1, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			d := &Daemon{vitals: hostvitals.New(tc.on, func() hostvitals.Reading { reads++; return hostvitals.Reading{} }, nil)}
			for range tc.passes {
				d.vitalsTick()
			}
			if reads != tc.want {
				t.Errorf("%d readings over %d passes, want %d", reads, tc.passes, tc.want)
			}
		})
	}
}

// A daemon built without the sampler (a test's bare Daemon) ticks safely.
func TestVitalsTickWithNoSampler(t *testing.T) {
	d := &Daemon{}
	d.vitalsTick()
}

// A crossing lands in the daemon's events ring and nowhere else: the ring is
// the only thing the sampler is handed.
func TestAVitalsCrossingLandsInTheEventsRing(t *testing.T) {
	ring := events.NewRing(16)
	d := &Daemon{events: ring, vitals: hostvitals.New(true, func() hostvitals.Reading {
		return hostvitals.Reading{SwapOK: true, SwapUsed: 90, SwapTotal: 100}
	}, ring)}
	d.vitalsTick()
	got := ring.Snapshot(events.Filter{}, 10)
	if len(got) != 1 || got[0].Kind != events.KindHostVitalsCrossed {
		t.Fatalf("ring = %+v, want one host.vitals.crossed", got)
	}
}

// metricsPass is where the sampler is driven. Removing the call from it must
// fail here, since the tests above call vitalsTick directly.
func TestMetricsPassDrivesTheVitalsSampler(t *testing.T) {
	skipIfNoTmux(t)
	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	reads := 0
	d.vitals = hostvitals.New(true, func() hostvitals.Reading { reads++; return hostvitals.Reading{} }, d.events)

	d.metricsPass(procstat.NewSampler(), &metricsHold{})

	if reads != 1 {
		t.Errorf("one metrics pass took %d vitals readings, want 1", reads)
	}
}

// Start is where the cluster's switch is read. Removing loadHostVitals from it
// must fail here, since the tests above call it directly. The socket sits under
// the scratch TMPDIR, not a fixed path, so the test needs no write outside it.
func TestStartTurnsTheSamplerOnFromTheClusterConfig(t *testing.T) {
	skipIfNoTmux(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := os.MkdirTemp("", "vt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "v.sock")
	if err := os.MkdirAll(filepath.Join(home, ".marvel"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "clusters:\n  - name: mine\n    socket: " + sock + "\n    host_vitals: true\n"
	if err := os.WriteFile(filepath.Join(home, ".marvel", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	d, err := New()
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	t.Cleanup(d.Stop)
	if err := d.Start(sock); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	if !d.vitals.Enabled() {
		t.Fatal("a daemon started under a cluster with host_vitals: true has the sampler off")
	}
}
