package hostvitals

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/arcavenae/marvel/internal/events"
	"github.com/arcavenae/marvel/internal/procargs"
)

// detailRig adds a fake source for the arguments and working directory of a
// process, and counts the reads, to the orphan rig.
type detailRig struct {
	*orphanRig
	args    map[int][]string
	cwd     map[int]string
	argsErr map[int]error
	cwdErr  map[int]error
	reads   map[int]int
	cpuAt   map[int]float64
	elAt    map[int]float64
}

func newDetailRig(t *testing.T) *detailRig {
	t.Helper()
	d := &detailRig{
		orphanRig: newRig(t),
		args:      map[int][]string{}, cwd: map[int]string{},
		argsErr: map[int]error{}, cwdErr: map[int]error{}, reads: map[int]int{},
		cpuAt: map[int]float64{}, elAt: map[int]float64{},
	}
	src := OrphanSources{
		Roots:       func() []int { return d.roots },
		RootSession: func(pid int) string { return d.seat[pid] },
		Marker: func(pid int) (procargs.Marker, error) {
			return d.markers[pid], nil
		},
		Detail: func(pid int) ProcDetail {
			d.reads[pid]++
			return ProcDetail{Args: d.args[pid], ArgsErr: d.argsErr[pid], Cwd: d.cwd[pid], CwdErr: d.cwdErr[pid]}
		},
	}
	d.s.SetOrphanSources(src)
	return d
}

// busy feeds the parentless process 300 burning 58 CPU seconds a minute for n
// passes, one pass a minute. The first call starts it with a first reading, and
// later calls carry on from where it was.
func (d *detailRig) busy(n int) {
	d.t.Helper()
	const pid, perMinute = 300, 58
	if _, ok := d.elAt[pid]; !ok {
		d.cpuAt[pid], d.elAt[pid] = 5000, 130000
		d.pass(op(pid, 1, "zsh", d.cpuAt[pid], d.elAt[pid]))
	}
	for i := 0; i < n; i++ {
		d.cpuAt[pid] += perMinute
		d.elAt[pid] += 60
		d.pass(op(pid, 1, "zsh", d.cpuAt[pid], d.elAt[pid]))
	}
}

func findOrphan(os []Orphan, pid int) *Orphan {
	for i := range os {
		if os[i].PID == pid {
			return &os[i]
		}
	}
	return nil
}

// A parentless process with no seat-tree history and no marker, over 90% of a
// core for the 30 minute window, is reported as unattributed with its pid, its
// arguments with the secret redacted, its working directory and its age.
func TestAnUnattributedPID1ProcessOverTheLineIsReported(t *testing.T) {
	d := newDetailRig(t)
	d.args[300] = []string{"/bin/zsh", "-c", "while :; do :; done", "--token", "abc123"}
	d.cwd[300] = "/srv/job"
	d.busy(31)
	o := findOrphan(d.orphans(), 300)
	if o == nil {
		t.Fatalf("orphans = %+v, want pid 300 reported as unattributed", d.orphans())
	}
	if o.Reason != ReasonUnattributed || ReasonUnattributed != "unattributed" {
		t.Errorf("reason = %q (constant %q), want %q", o.Reason, ReasonUnattributed, "unattributed")
	}
	if o.Name != "zsh" {
		t.Errorf("name = %q, want zsh", o.Name)
	}
	if !strings.Contains(o.Args, "while :; do :; done") || !strings.Contains(o.Args, "--token <redacted>") || strings.Contains(o.Args, "abc123") {
		t.Errorf("args = %q, want the loop text and the token value redacted", o.Args)
	}
	if o.Cwd != "/srv/job" {
		t.Errorf("cwd = %q, want /srv/job", o.Cwd)
	}
	if o.AgeSeconds != 130000+31*60 {
		t.Errorf("age = %v, want the table's elapsed seconds %d", o.AgeSeconds, 130000+31*60)
	}
	if o.Share == nil || *o.Share <= OrphanShare {
		t.Errorf("share = %v, want a share over %v", o.Share, OrphanShare)
	}
}

// One at or under the line is not reported, and its arguments are never read,
// while a busy one watched in the same table is reported (the control).
func TestAnUnattributedPID1ProcessUnderTheLineIsNotReported(t *testing.T) {
	d := newDetailRig(t)
	d.args[300] = []string{"/bin/zsh", "-c", "loop"}
	d.args[301] = []string{"/usr/bin/backupd"}
	cpu300, cpu301, el := 5000.0, 5000.0, 130000.0
	for i := 0; i <= 40; i++ {
		d.pass(op(300, 1, "zsh", cpu300, el), op(301, 1, "backupd", cpu301, el))
		if i == 10 && len(d.orphans()) != 0 {
			t.Fatalf("10 minutes in, orphans = %+v, want none: the window is not watched", d.orphans())
		}
		cpu300, cpu301, el = cpu300+58, cpu301+10, el+60
	}
	got := d.orphans()
	if findOrphan(got, 300) == nil {
		t.Fatalf("orphans = %+v, want the busy pid 300 reported as the control", got)
	}
	if findOrphan(got, 301) != nil {
		t.Errorf("orphans = %+v, want the quiet pid 301 not reported", got)
	}
	if d.reads[301] != 0 {
		t.Errorf("arguments of an unreported process were read %d times, want 0", d.reads[301])
	}
}

// The arguments and the working directory are read once, when the process first
// crosses the line, and not on every pass after it.
func TestUnattributedDetailIsReadOnceAtTheCrossing(t *testing.T) {
	d := newDetailRig(t)
	d.args[300] = []string{"zsh"}
	d.cwd[300] = "/srv/job"
	d.busy(60)
	if got := d.reads[300]; got != 1 {
		t.Errorf("detail read %d times over 60 passes, want exactly 1", got)
	}
	if o := findOrphan(d.orphans(), 300); o == nil || o.Cwd != "/srv/job" {
		t.Errorf("orphan = %+v, want it still listed with its cwd", o)
	}
}

// The existing reasons keep precedence and their behavior: a process that was
// under a seat's tree is "seat tree", one with a marker is "marker", and neither
// becomes unattributed or waits for the CPU line to be listed.
func TestSeatTreeAndMarkerReasonsKeepPrecedence(t *testing.T) {
	d := newDetailRig(t)
	d.roots = []int{100}
	d.markers[200] = procargs.Marker{Session: "aae/seat-2"}
	d.markers[101] = procargs.Marker{Session: "aae/seat-1"}
	d.pass(op(100, 50, "sh", 1, 600), op(101, 100, "tmux", 10, 600))
	d.pass(op(100, 50, "sh", 1, 660), op(101, 1, "tmux", 12, 660), op(200, 1, "node", 3, 9000), op(300, 1, "zsh", 3, 9000))
	got := d.orphans()
	seat, marker := findOrphan(got, 101), findOrphan(got, 200)
	if seat == nil || seat.Reason != ReasonSeatTree {
		t.Errorf("pid 101 = %+v, want seat tree even though it also carries a marker", seat)
	}
	if marker == nil || marker.Reason != ReasonMarker {
		t.Errorf("pid 200 = %+v, want marker", marker)
	}
	if findOrphan(got, 300) != nil {
		t.Errorf("pid 300 = %+v, want it unlisted: unattributed and under the line", findOrphan(got, 300))
	}
	for _, o := range []*Orphan{seat, marker} {
		if o != nil && (o.Args != "" || o.Cwd != "") {
			t.Errorf("pid %d carries args %q cwd %q, want none for a seat tree or marker row", o.PID, o.Args, o.Cwd)
		}
	}
	if d.reads[101] != 0 || d.reads[200] != 0 {
		t.Errorf("detail reads for seat tree and marker rows = %d, %d, want 0, 0", d.reads[101], d.reads[200])
	}
}

// A process of another user, whose arguments the kernel will not return, is
// still reported, with the arguments and the directory marked unreadable.
func TestAnUnreadableUnattributedProcessIsReportedNotDropped(t *testing.T) {
	d := newDetailRig(t)
	d.argsErr[300] = fs.ErrPermission
	d.cwdErr[300] = fs.ErrPermission
	d.busy(31)
	o := findOrphan(d.orphans(), 300)
	if o == nil {
		t.Fatalf("orphans = %+v, want pid 300 kept", d.orphans())
	}
	if o.Args != "unreadable" || o.Cwd != "unreadable" {
		t.Errorf("args %q cwd %q, want both \"unreadable\"", o.Args, o.Cwd)
	}
	if o.Name != "zsh" || o.AgeSeconds == 0 {
		t.Errorf("row = %+v, want its name and age kept", o)
	}
}

// A working directory that cannot be read for any other reason is "unknown",
// and the row stays with its arguments.
func TestAnUnreadableWorkingDirectoryIsUnknownNotAnError(t *testing.T) {
	d := newDetailRig(t)
	d.args[300] = []string{"sleep", "9999"}
	d.cwdErr[300] = errors.New("no such process")
	d.busy(31)
	o := findOrphan(d.orphans(), 300)
	if o == nil {
		t.Fatalf("orphans = %+v, want pid 300 kept", d.orphans())
	}
	if o.Cwd != "unknown" || o.Args != "sleep 9999" {
		t.Errorf("cwd %q args %q, want \"unknown\" and \"sleep 9999\"", o.Cwd, o.Args)
	}
}

// An unattributed process over the line turns the orphan CPU vital over and
// emits one host_vitals_crossed event at warning level, naming it, with no
// exception for any command name. It clears once, when the load falls back.
// Director's ruling, relayed by the supervisor: staying silent let 46 spin for
// 36 hours.
func TestAnUnattributedProcessOverTheLineCrossesTheVitalOnce(t *testing.T) {
	d := newDetailRig(t)
	d.args[300] = []string{"zsh"}
	d.cwd[300] = "/srv/job"
	d.busy(29)
	if th := d.vital(); th.State != "under" || len(d.rec.got) != 0 {
		t.Fatalf("29 minutes in, state %q events %v, want under and none", th.State, d.rec.kinds())
	}
	d.busy(31)
	th := d.vital()
	if th.State != "over" {
		t.Fatalf("state = %q, want over for an unattributed process over the line (%+v)", th.State, th)
	}
	if !strings.Contains(th.Detail, "zsh") || !strings.Contains(th.Detail, "300") || !strings.Contains(th.Detail, "unattributed") {
		t.Errorf("detail = %q, want the command, the pid and the reason", th.Detail)
	}
	got := d.rec.got
	if len(got) != 1 || got[0].Kind != events.KindHostVitalsCrossed || got[0].Severity != events.SeverityWarning {
		t.Fatalf("events = %+v, want one host_vitals_crossed at warning level", got)
	}
	if !strings.Contains(got[0].Message, "orphan CPU") || !strings.Contains(got[0].Message, "300") {
		t.Errorf("message = %q, want it to name the vital and the pid", got[0].Message)
	}
	d.busy(5)
	if n := len(d.rec.got); n != 1 {
		t.Errorf("%d events after five more busy passes, want still 1: one per crossing", n)
	}
}

// Many unattributed processes over the line are one crossing, and the detail
// says how many there are beyond the worst.
func TestManyUnattributedProcessesAreOneCrossingWithACount(t *testing.T) {
	d := newDetailRig(t)
	cpu, el := 5000.0, 130000.0
	feed := func() {
		var procs []Proc
		for i := 0; i < 46; i++ {
			procs = append(procs, op(4000+i, 1, "zsh", cpu, el))
		}
		d.pass(procs...)
		cpu, el = cpu+58, el+60
	}
	for i := 0; i < 32; i++ {
		feed()
	}
	if n := len(d.rec.got); n != 1 {
		t.Fatalf("%d events for 46 busy processes, want 1 crossing", n)
	}
	if th := d.vital(); th.State != "over" || !strings.Contains(th.Detail, "46 unattributed") {
		t.Errorf("threshold = %+v, want over with \"46 unattributed\" in the detail", th)
	}
	for i := 0; i < 5; i++ {
		d.pass(op(9999, 1, "zsh", 1, 9999))
	}
	if th := d.vital(); th.State != "under" {
		t.Errorf("after they exit, state = %q, want under", th.State)
	}
	if got := d.rec.kinds(); len(got) != 2 || got[1] != events.KindHostVitalsCleared {
		t.Errorf("events = %v, want crossed then cleared", got)
	}
}

// Unattributed processes under the line neither fill the list, which is capped,
// nor hide a real orphan.
func TestUnreportedUnattributedProcessesDoNotCrowdTheList(t *testing.T) {
	d := newDetailRig(t)
	d.markers[9000] = procargs.Marker{Session: "aae/seat-1"}
	var first, second []Proc
	first = append(first, op(9000, 1, "tmux", 10, 9000))
	second = append(second, op(9000, 1, "tmux", 11, 9060))
	for i := 0; i < 3*OrphansShown; i++ {
		first = append(first, op(1000+i, 1, "launchd-child", 10, 9000))
		second = append(second, op(1000+i, 1, "launchd-child", 11, 9060))
	}
	d.pass(first...)
	d.pass(second...)
	got := d.orphans()
	if len(got) != 1 || got[0].PID != 9000 {
		t.Errorf("%d orphans listed (%+v), want only the marker orphan 9000", len(got), got)
	}
}

// Tracking every parentless process stays bounded: the readings kept for each
// are cut to the window however long it runs, and an exited process or a
// reused pid is dropped.
func TestTrackingEveryPID1ProcessIsBoundedByTheWindow(t *testing.T) {
	d := newDetailRig(t)
	const n = 300
	cpu := make([]float64, n)
	for pass := 0; pass < 180; pass++ {
		procs := make([]Proc, 0, n)
		for i := 0; i < n; i++ {
			cpu[i] += 1
			procs = append(procs, op(2000+i, 1, "launchd-child", cpu[i], 90000+float64(pass)*60))
		}
		d.pass(procs...)
	}
	most := 0
	for _, e := range d.s.orph.marked {
		if len(e.hist) > most {
			most = len(e.hist)
		}
	}
	if len(d.s.orph.marked) != n {
		t.Fatalf("%d processes tracked, want %d", len(d.s.orph.marked), n)
	}
	if want := int(OrphanWindow.Minutes()) + 2; most > want {
		t.Errorf("a process keeps %d readings after 3 hours, want at most %d (the window plus slack)", most, want)
	}
	d.pass(op(2000, 1, "launchd-child", 999, 90000+180*60))
	if len(d.s.orph.marked) != 1 {
		t.Errorf("%d processes tracked after the rest exited, want 1", len(d.s.orph.marked))
	}
	d.pass(op(2000, 1, "launchd-child", 1, 5))
	if len(d.s.orph.marked) != 1 {
		t.Errorf("tracked = %d after pid reuse, want the reused pid tracked fresh as 1", len(d.s.orph.marked))
	}
	if e := d.s.orph.marked[2000]; e == nil || len(e.hist) != 1 {
		t.Errorf("a reused pid keeps old readings: %+v", e)
	}
}

// The redaction: the value after a flag or KEY= whose name matches token,
// secret, password, passwd, key, auth or bearer, in any case, becomes
// <redacted>. Other arguments pass through.
func TestRedactArgsHidesSecretValues(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"flag then value", []string{"tool", "--token", "abc123"}, "tool --token <redacted>"},
		{"flag equals value", []string{"tool", "--password=hunter2"}, "tool --password=<redacted>"},
		{"env style", []string{"env", "API_KEY=sk-live-1", "run"}, "env API_KEY=<redacted> run"},
		{"upper and mixed case", []string{"x", "--TOKEN", "a", "--Secret=b", "SeCrEt_Value=c"}, "x --TOKEN <redacted> --Secret=<redacted> SeCrEt_Value=<redacted>"},
		{"passwd and auth and bearer", []string{"x", "--passwd", "p", "--auth", "q", "--bearer", "r"}, "x --passwd <redacted> --auth <redacted> --bearer <redacted>"},
		{"key in a longer name", []string{"x", "--ssh-key", "k", "--keyfile=/p"}, "x --ssh-key <redacted> --keyfile=<redacted>"},
		{"query string", []string{"curl", "https://h.example/p?user=u&token=abc&n=1"}, "curl https://h.example/p?user=u&token=<redacted>&n=1"},
		{"header with a bearer", []string{"curl", "-H", "Authorization: Bearer abc.def"}, "curl -H Authorization: <redacted>"},
		{"header after an equals sign", []string{"curl", "--header=Authorization: Bearer abc123"}, "curl --header=Authorization: <redacted>"},
		{"key header after an equals sign", []string{"curl", "--header=X-Api-Key: v1"}, "curl --header=X-Api-Key: <redacted>"},
		{"unrelated", []string{"sleep", "100", "--verbose", "--name=job"}, "sleep 100 --verbose --name=job"},
		{"flag last with no value", []string{"tool", "--token"}, "tool --token"},
		{"the value that follows is a flag", []string{"tool", "--key", "--other"}, "tool --key --other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RedactArgs(c.args); got != c.want {
				t.Errorf("RedactArgs(%q) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// The arguments are truncated at MaxArgsBytes, and the redaction runs first, so
// a secret that straddles the cut cannot survive as a prefix.
func TestRedactArgsTruncatesAfterRedacting(t *testing.T) {
	if MaxArgsBytes != 120 {
		t.Fatalf("MaxArgsBytes = %d, want 120", MaxArgsBytes)
	}
	long := strings.Repeat("a", 110)
	got := RedactArgs([]string{long, "--token", "SECRETSECRETSECRET"})
	if strings.Contains(got, "SECRET") || strings.Contains(got, "SECR") {
		t.Errorf("output %q leaks a prefix of the secret", got)
	}
	if !strings.HasPrefix(got, long) {
		t.Errorf("output %q lost the head of the arguments", got)
	}
	if len(got) > MaxArgsBytes+len("...") || !strings.HasSuffix(got, "...") {
		t.Errorf("output is %d bytes %q, want at most %d with a \"...\" suffix", len(got), got, MaxArgsBytes+3)
	}
	multi := RedactArgs([]string{strings.Repeat("é", 100)})
	if !utf8.ValidString(multi) {
		t.Errorf("truncation split a multibyte character: %q", multi)
	}
	if short := RedactArgs([]string{"sleep", "9"}); short != "sleep 9" {
		t.Errorf("short args = %q, want \"sleep 9\" with no suffix", short)
	}
}

// The crossing event and the vital's detail carry the pid, the name, the reason
// and a count, never the arguments or the working directory: those appear only in
// the orphan rows. A positional secret that redaction cannot see must stay out of
// the event.
func TestTheCrossingEventAndDetailCarryNoArgsOrCwd(t *testing.T) {
	d := newDetailRig(t)
	d.args[300] = []string{"/usr/local/bin/uploader", "s3cr3t-positional-value"}
	d.cwd[300] = "/srv/private-job-dir"
	d.busy(40)
	o := findOrphan(d.orphans(), 300)
	if o == nil || !strings.Contains(o.Args, "uploader") {
		t.Fatalf("orphan = %+v, want pid 300 reported with its args in the row", o)
	}
	if len(d.rec.got) != 1 {
		t.Fatalf("events = %v, want one crossing", d.rec.kinds())
	}
	for _, text := range []string{d.rec.got[0].Message, d.vital().Detail} {
		for _, leak := range []string{"s3cr3t", "uploader", "private-job-dir", "/srv"} {
			if strings.Contains(text, leak) {
				t.Errorf("text %q carries %q, want pid, name, reason and count only", text, leak)
			}
		}
	}
}

// Many processes crossing the line on one pass are not all read on that pass:
// at most MaxDetailReadsPerPass reads a pass, the rest read "pending" and are
// finished on later passes, and a process that is over the line but below the
// cap of listed rows is never read at all.
func TestDetailReadsAreBoundedPerPassAndLimitedToShownRows(t *testing.T) {
	d := newDetailRig(t)
	n := OrphansShown + 10
	cpu, el := 5000.0, 130000.0
	feed := func() {
		var procs []Proc
		for i := 0; i < n; i++ {
			procs = append(procs, op(5000+i, 1, "zsh", cpu+float64(i), el))
		}
		d.pass(procs...)
		cpu, el = cpu+58, el+60
	}
	for i := 0; i < 31; i++ {
		feed()
	}
	total := func() int {
		sum := 0
		for _, c := range d.reads {
			sum += c
		}
		return sum
	}
	if got := total(); got > MaxDetailReadsPerPass {
		t.Fatalf("%d reads on the pass the %d processes crossed, want at most %d", got, n, MaxDetailReadsPerPass)
	}
	pending := 0
	for _, o := range d.orphans() {
		if o.Args == "pending" && o.Cwd == "pending" {
			pending++
		}
	}
	if pending != OrphansShown-MaxDetailReadsPerPass {
		t.Errorf("%d rows read pending, want %d", pending, OrphansShown-MaxDetailReadsPerPass)
	}
	for i := 0; i < 10; i++ {
		feed()
	}
	if got := total(); got != OrphansShown {
		t.Errorf("%d reads after the rows settled, want exactly %d (one per shown row, none for the %d below the cap)", got, OrphansShown, n-OrphansShown)
	}
	for pid, c := range d.reads {
		if c > 1 {
			t.Errorf("pid %d was read %d times, want once", pid, c)
		}
	}
	for _, o := range d.orphans() {
		if o.Args == "pending" {
			t.Errorf("pid %d still reads pending after 10 passes", o.PID)
		}
	}
}

// flaky replaces the rig's detail source with one that fails the first n reads
// of every pid with err, then succeeds, and counts the reads.
func (d *detailRig) flaky(n int, argsErr, cwdErr error) {
	d.s.SetOrphanSources(OrphanSources{
		Detail: func(pid int) ProcDetail {
			d.reads[pid]++
			if d.reads[pid] <= n {
				return ProcDetail{ArgsErr: argsErr, CwdErr: cwdErr}
			}
			return ProcDetail{Args: []string{"sleep", "9"}, Cwd: "/srv/job"}
		},
	})
}

// A read that fails for a passing reason, such as an lsof timeout, is tried
// again on a later pass, and the row shows what the last try read.
func TestADetailReadThatTimedOutIsRetriedOnALaterPass(t *testing.T) {
	d := newDetailRig(t)
	d.flaky(2, nil, errors.New("signal: killed"))
	d.busy(40)
	o := findOrphan(d.orphans(), 300)
	if o == nil || o.Cwd != "/srv/job" || o.Args != "sleep 9" {
		t.Fatalf("orphan = %+v, want the third read's cwd /srv/job and args", o)
	}
	if d.reads[300] != 3 {
		t.Errorf("%d reads, want 3 (two failures, then one success, then no more)", d.reads[300])
	}
}

// A read that keeps failing is tried MaxDetailTries times and no more, so a
// process whose directory can never be read does not cost a read on every pass.
func TestADetailReadThatKeepsFailingStopsAtTheLimit(t *testing.T) {
	d := newDetailRig(t)
	d.flaky(1000, nil, errors.New("signal: killed"))
	d.busy(60)
	o := findOrphan(d.orphans(), 300)
	if o == nil || o.Cwd != "unknown" {
		t.Fatalf("orphan = %+v, want cwd unknown", o)
	}
	if d.reads[300] != MaxDetailTries {
		t.Errorf("%d reads, want exactly %d", d.reads[300], MaxDetailTries)
	}
}

// A permission failure is final at once: another user's process is not retried.
func TestAPermissionFailureIsNotRetried(t *testing.T) {
	d := newDetailRig(t)
	d.flaky(1000, fs.ErrPermission, fs.ErrPermission)
	d.busy(60)
	o := findOrphan(d.orphans(), 300)
	if o == nil || o.Args != "unreadable" || o.Cwd != "unreadable" {
		t.Fatalf("orphan = %+v, want both unreadable", o)
	}
	if d.reads[300] != 1 {
		t.Errorf("%d reads, want 1", d.reads[300])
	}
}

// Arguments that fail for a reason other than permission, such as a process that
// exited between the listing and the read, read "unknown", not "unreadable".
func TestArgumentsThatFailForAnotherReasonReadUnknown(t *testing.T) {
	d := newDetailRig(t)
	d.flaky(1000, errors.New("no such process"), errors.New("no such process"))
	d.busy(40)
	o := findOrphan(d.orphans(), 300)
	if o == nil || o.Args != "unknown" || o.Cwd != "unknown" {
		t.Errorf("orphan = %+v, want args and cwd unknown", o)
	}
}
