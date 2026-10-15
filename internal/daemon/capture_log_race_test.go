package daemon

import (
	"log"
	"strings"
	"sync"
	"testing"
)

// captureLog hands a test the log output so far while a goroutine the test
// started (a broker supervisor's watcher, for one) may still be logging.
// log serializes the writes, but not a read of the buffer behind them, so the
// reader must be safe against the writer. Run under -race this fails against
// a plain bytes.Buffer on the first overlapping read and write.
func TestCaptureLogCanBeReadWhileAWriterIsLogging(t *testing.T) {
	logs := captureLog(t)
	stop := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				log.Printf("bus.leaf: still writing")
				once.Do(func() { close(started) })
			}
		}
	}()
	<-started
	for i := 0; i < 2000; i++ {
		_ = logs.String()
	}
	close(stop)
	wg.Wait()
	if !strings.Contains(logs.String(), "still writing") {
		t.Error("the captured log is missing what the writer logged")
	}
}
