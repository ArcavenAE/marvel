package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Store headroom (aae-orc-kjix3, marvel#358 section 5). The write probe finds
// a store that already refuses writes; this warns before it does. It reads
// what the broker says about its own stores (/jsz) and what the filesystem
// under the store directory says about free space, and reports a store that
// is near its cap or a filesystem that is nearly full. A warning only: it
// never restarts the broker and never holds spawns. The thresholds are
// vital signs, not gates (ADR-007 clause 3, diagnostic-not-gate).

const (
	// headroomUsedPct is the share of a store's cap at which it reads low.
	headroomUsedPct = 80
	// headroomFreePct and headroomFreeMin are the free-space floors for the
	// store's filesystem: under a tenth of it, or under a gibibyte, whichever
	// trips first.
	headroomFreePct = 10
	headroomFreeMin = uint64(1) << 30
)

// Headroom is one reading of the stores and the filesystem under them. A
// zero limit or a zero filesystem size means the figure is not known and
// raises nothing.
type Headroom struct {
	FileUsed, FileLimit uint64
	MemUsed, MemLimit   uint64
	FSFree, FSTotal     uint64
}

// lowStore is one store that reads low, for the warning text.
type lowStore struct {
	name string
	text string
}

func (h Headroom) low() []lowStore {
	var out []lowStore
	if h.FileLimit > 0 && h.FileUsed*100 >= h.FileLimit*headroomUsedPct {
		out = append(out, lowStore{"file store", fmt.Sprintf("the file store is at %d of %d bytes (%d%%)", h.FileUsed, h.FileLimit, h.FileUsed*100/h.FileLimit)})
	}
	if h.MemLimit > 0 && h.MemUsed*100 >= h.MemLimit*headroomUsedPct {
		out = append(out, lowStore{"memory store", fmt.Sprintf("the memory store is at %d of %d bytes (%d%%)", h.MemUsed, h.MemLimit, h.MemUsed*100/h.MemLimit)})
	}
	if h.FSTotal > 0 && (h.FSFree < headroomFreeMin || h.FSFree*100 < h.FSTotal*headroomFreePct) {
		out = append(out, lowStore{"store filesystem", fmt.Sprintf("the store filesystem has %d of %d bytes free (%d%%)", h.FSFree, h.FSTotal, h.FSFree*100/h.FSTotal)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// Low returns one sentence per store that reads low, sorted by store.
func (h Headroom) Low() []string {
	var out []string
	for _, l := range h.low() {
		out = append(out, l.text)
	}
	return out
}

// Problems names the stores that read low, sorted and comma separated. It
// doubles as the change key: the same string means the same set.
func (h Headroom) Problems() string {
	var names []string
	for _, l := range h.low() {
		names = append(names, l.name)
	}
	return strings.Join(names, ", ")
}

// ReadHeadroom reads the broker's store caps and use from the loopback
// monitor's /jsz and the free space under storeDir. An error is no
// information: the caller keeps its last reading.
func ReadHeadroom(ctx context.Context, monitorAddr, storeDir string) (Headroom, error) {
	var h Headroom
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+monitorAddr+"/jsz?config=true", nil)
	if err != nil {
		return h, err
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return h, fmt.Errorf("read /jsz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return h, fmt.Errorf("read /jsz: %s", resp.Status)
	}
	var jsz struct {
		Config struct {
			MaxMemory  int64 `json:"max_memory"`
			MaxStorage int64 `json:"max_storage"`
		} `json:"config"`
		Memory  uint64 `json:"memory"`
		Storage uint64 `json:"storage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jsz); err != nil {
		return h, fmt.Errorf("decode /jsz: %w", err)
	}
	if jsz.Config.MaxStorage > 0 {
		h.FileLimit = uint64(jsz.Config.MaxStorage)
	}
	if jsz.Config.MaxMemory > 0 {
		h.MemLimit = uint64(jsz.Config.MaxMemory)
	}
	h.FileUsed, h.MemUsed = jsz.Storage, jsz.Memory
	var fs syscall.Statfs_t
	if err := syscall.Statfs(storeDir, &fs); err != nil {
		return h, fmt.Errorf("free space under %s: %w", storeDir, err)
	}
	h.FSFree = uint64(fs.Bavail) * uint64(fs.Bsize)  //nolint:unconvert // the field widths differ by platform
	h.FSTotal = uint64(fs.Blocks) * uint64(fs.Bsize) //nolint:unconvert // the field widths differ by platform
	return h, nil
}
