package bus

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// A nats-server that exits at startup, here because its monitoring port is
// taken, is reported at once and by what it said, not after the whole
// listener wait as a server that "did not answer" (#962). Under a loaded
// -race run the +4000 monitoring port of a free client port can be any
// other socket's ephemeral port, and the server exits in milliseconds.
func TestStartReportsAServerThatExitsBeforeItListens(t *testing.T) {
	s, m, _ := newTestSupervisor(t, "")
	mon, err := MonitorAddr(m.bus.Listen)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := net.Listen("tcp", mon)
	if err != nil {
		t.Fatalf("hold the monitoring port %s: %v", mon, err)
	}
	t.Cleanup(func() { _ = hold.Close() })

	began := time.Now()
	err = s.Start(context.Background())
	took := time.Since(began)
	if err == nil {
		t.Fatal("Start succeeded with the monitoring port taken")
	}
	if took > 5*time.Second {
		t.Errorf("Start took %v to fail, want well under the %v listener wait", took, s.dialTimeout)
	}
	if !strings.Contains(err.Error(), "exited") || !strings.Contains(err.Error(), "monitor") {
		t.Errorf("err = %v, want it to say the server exited and quote its log line about the monitor port", err)
	}
}
