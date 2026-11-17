package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// A cluster that shares a hub's host reads the hub's loopback monitor and
// shows the hub's leaf list by name in bus status (aae-orc-stqn9). The lines
// are readings: they never raise an event, because the cluster-gone alert is
// director's. A reading too old to trust, or one that failed, is "not
// observed", never a finding.

// hubValidPolls is how many tick intervals a hub reading stays observed.
const hubValidPolls = 3

// hubReadTimeout bounds one read of the hub monitor, and hubBodyLimit the body
// read from it. They are variables so a test can shorten them.
var (
	hubReadTimeout = 3 * time.Second
	hubBodyLimit   = int64(1 << 20)
)

// HubReading is one read of a hub's loopback /leafz.
type HubReading struct {
	// ObservedAt is when the read succeeded; zero when none has.
	ObservedAt time.Time
	// Leafs is the leaf names the hub listed.
	Leafs []string
	// Err is why the last read failed or timed out; empty on success.
	Err string
}

// ParseHubLeafz returns the leaf names a hub's /leafz body lists.
func ParseHubLeafz(body []byte) ([]string, error) {
	var v struct {
		Leafs []struct {
			Name string `json:"name"`
		} `json:"leafs"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("decode /leafz: %w", err)
	}
	var names []string
	for _, l := range v.Leafs {
		if l.Name != "" {
			names = append(names, l.Name)
		}
	}
	return names, nil
}

// HubStatusLines renders a hub reading as diagnostic lines. Names match
// exactly, as written. A reading older than validFor, one that failed, or one
// never made reads "not observed". With no expected list no comparison is
// made, and the line says so.
func HubStatusLines(r HubReading, expected []string, own string, now time.Time, validFor time.Duration) []string {
	if r.ObservedAt.IsZero() || r.Err != "" || now.Sub(r.ObservedAt) > validFor {
		last := "never read"
		if !r.ObservedAt.IsZero() {
			last = "last good reading " + r.ObservedAt.UTC().Format(time.RFC3339)
		}
		why := ""
		if r.Err != "" {
			why = "; " + r.Err
		} else if !r.ObservedAt.IsZero() {
			why = "; the reading is stale"
		}
		return []string{fmt.Sprintf("hub leafs: not observed (%s%s; not an alert)", last, why)}
	}
	stamp := r.ObservedAt.UTC().Format(time.RFC3339)
	line := func(format string, a ...any) string {
		return fmt.Sprintf("hub leafs at %s (not an alert): %s", stamp, fmt.Sprintf(format, a...))
	}
	listed := slices.Clone(r.Leafs)
	slices.Sort(listed)
	if len(expected) == 0 {
		return []string{line("no comparison made, no hub_expected_leafs declared; the hub lists %s", nameList(listed))}
	}
	var present, missing, unexpected []string
	for _, n := range expected {
		if slices.Contains(listed, n) {
			present = append(present, n)
		} else {
			missing = append(missing, n)
		}
	}
	for _, n := range listed {
		if !slices.Contains(expected, n) {
			unexpected = append(unexpected, n)
		}
	}
	slices.Sort(present)
	slices.Sort(missing)
	var out []string
	if len(present) > 0 {
		out = append(out, line("present %s", strings.Join(present, ", ")))
	}
	if len(missing) > 0 {
		out = append(out, line("missing %s", strings.Join(missing, ", ")))
	}
	if len(unexpected) > 0 {
		out = append(out, line("unexpected %s", strings.Join(unexpected, ", ")))
	}
	var remote []string
	for _, n := range expected {
		if n != own {
			remote = append(remote, n)
		}
	}
	if own != "" && slices.Contains(listed, own) && len(remote) > 0 && !slices.ContainsFunc(remote, func(n string) bool { return slices.Contains(listed, n) }) {
		out = append(out, line("partition: every expected remote leaf (%s) is missing while %s is present", strings.Join(remote, ", "), own))
	}
	if len(out) == 0 {
		out = append(out, line("no leafs listed"))
	}
	return out
}

func nameList(names []string) string {
	if len(names) == 0 {
		return "no leafs"
	}
	return strings.Join(names, ", ")
}

// pollHub reads the configured hub monitor and stores the reading. It reads
// loopback only, sends no credential, follows no redirect, and raises no event.
func (s *Supervisor) pollHub() {
	mon := s.mgr.bus.HubMonitor
	if mon == "" {
		return
	}
	r := HubReading{}
	read := s.hubRead
	if read == nil {
		read = readHubLeafz
	}
	names, err := read(mon)
	if err != nil {
		r.Err = err.Error()
	} else {
		r.ObservedAt, r.Leafs = s.now(), names
	}
	s.mu.Lock()
	if r.Err != "" {
		// Keep when the last good reading was taken, so the line can say.
		r.ObservedAt = s.hubReading.ObservedAt
		r.Leafs = s.hubReading.Leafs
	}
	s.hubReading = r
	s.mu.Unlock()
}

// readHubLeafz reads names from the hub monitor. Its errors carry no address.
func readHubLeafz(mon string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hubReadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+mon+"/leafz", nil)
	if err != nil {
		return nil, errors.New("bad request")
	}
	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cli.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("timed out")
		}
		return nil, errors.New("monitor unreachable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("monitor answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, hubBodyLimit))
	if err != nil {
		return nil, errors.New("monitor read failed")
	}
	names, err := ParseHubLeafz(body)
	if err != nil {
		return nil, errors.New("monitor reply is not /leafz")
	}
	return names, nil
}

// hubLinesLocked is what Status shows: nothing without a hub_monitor. The
// caller holds s.mu.
func (s *Supervisor) hubLinesLocked() []string {
	if s.mgr.bus.HubMonitor == "" {
		return nil
	}
	r := s.hubReading
	return HubStatusLines(r, s.mgr.bus.HubExpectedLeafs, s.mgr.Domain(), s.now(), hubValidPolls*defaultLeafPoll)
}
