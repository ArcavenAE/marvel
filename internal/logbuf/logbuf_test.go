package logbuf

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestWrite_SplitsOnNewline(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\nb\nc\n"))
	got := b.Tail(10)
	want := []string{"a", "b", "c"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWrite_RingDrops(t *testing.T) {
	b := New(3)
	for i := 0; i < 5; i++ {
		_, _ = fmt.Fprintf(b, "line%d\n", i)
	}
	got := b.Tail(10)
	want := []string{"line2", "line3", "line4"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWrite_TrailingNoNewline(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("incomplete"))
	got := b.Tail(10)
	if len(got) != 1 || got[0] != "incomplete" {
		t.Errorf("got %v", got)
	}
}

func TestWrite_StripsCR(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\r\nb\r\n"))
	got := b.Tail(10)
	want := []string{"a", "b"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTail_ZeroOrNegative(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\nb\n"))
	if got := b.Tail(0); len(got) != 0 {
		t.Errorf("Tail(0): got %v, want empty", got)
	}
	if got := b.Tail(-1); len(got) != 0 {
		t.Errorf("Tail(-1): got %v, want empty", got)
	}
}

func TestTail_ReturnsCopy(t *testing.T) {
	b := New(10)
	_, _ = b.Write([]byte("a\nb\n"))
	got := b.Tail(10)
	got[0] = "mutated"
	got2 := b.Tail(10)
	if got2[0] != "a" {
		t.Errorf("expected tail copy to be independent, buffer corrupted: %v", got2)
	}
}

func TestConcurrentWrites(t *testing.T) {
	b := New(1000)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = fmt.Fprintf(b, "w%d-j%d\n", i, j)
			}
		}(i)
	}
	wg.Wait()
	if got := b.Len(); got != 1000 {
		t.Errorf("Len after 1000 writes across 10 goroutines: got %d, want 1000", got)
	}
}

func TestWrite_LargeSinglePayload(t *testing.T) {
	b := New(3)
	big := strings.Repeat("x\n", 100)
	_, _ = b.Write([]byte(big))
	if got := b.Len(); got != 3 {
		t.Errorf("ring cap violated: got %d, want 3", got)
	}
}
