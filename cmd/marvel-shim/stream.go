package main

import (
	"net"
	"sync"
	"time"
)

// lagLimit bounds the bytes queued for one slow subscriber. Past it the
// subscriber is dropped rather than allowed to stall the pane render, and the
// drop is counted so loss is observable instead of silent.
const lagLimit = 64 << 20

// drainGrace bounds how long child exit waits for subscribers to finish
// reading. Without a wait the shim's own exit kills the pump goroutines
// mid-queue and the supervisor silently loses the child's last output, which
// is exactly what a lagging consumer hits first.
const drainGrace = 10 * time.Second

// broadcaster tees child output to zero or more stream subscribers. A
// subscriber sees bytes from its connect time forward; there is no replay
// buffer in this spike.
type broadcaster struct {
	mu      sync.Mutex
	clients map[*subscriber]struct{}
	closed  bool
}

type subscriber struct {
	conn    net.Conn
	mu      sync.Mutex
	cond    *sync.Cond
	queue   [][]byte
	queued  int
	dropped int
	done    bool
	drained chan struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{clients: make(map[*subscriber]struct{})}
}

func (b *broadcaster) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s := &subscriber{conn: conn, drained: make(chan struct{})}
		s.cond = sync.NewCond(&s.mu)

		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			_ = conn.Close()
			continue
		}
		b.clients[s] = struct{}{}
		b.mu.Unlock()

		go s.pump(func() { b.remove(s) })
	}
}

func (b *broadcaster) remove(s *subscriber) {
	b.mu.Lock()
	delete(b.clients, s)
	b.mu.Unlock()
}

func (b *broadcaster) clientCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}

// publish must not block on subscriber I/O: the same goroutine writes the
// pane, so a slow marvel-side reader would otherwise freeze the human's view.
func (b *broadcaster) publish(chunk []byte) {
	b.mu.Lock()
	subs := make([]*subscriber, 0, len(b.clients))
	for s := range b.clients {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.enqueue(chunk)
	}
}

// close tells every subscriber no more bytes are coming and waits for their
// queues to reach the socket, so the shim's exit does not truncate the
// supervisor's view of the child's final output.
func (b *broadcaster) close() {
	b.mu.Lock()
	b.closed = true
	subs := make([]*subscriber, 0, len(b.clients))
	for s := range b.clients {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.finish()
	}
	deadline := time.After(drainGrace)
	for _, s := range subs {
		select {
		case <-s.drained:
		case <-deadline:
			return
		}
	}
}

func (s *subscriber) enqueue(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	if s.queued+len(chunk) > lagLimit {
		s.dropped += len(chunk)
		return
	}
	s.queue = append(s.queue, chunk)
	s.queued += len(chunk)
	s.cond.Signal()
}

func (s *subscriber) finish() {
	s.mu.Lock()
	s.done = true
	s.cond.Signal()
	s.mu.Unlock()
}

// pump drains the queue in order, then closes the connection so the consumer
// sees EOF only after every byte it was owed.
func (s *subscriber) pump(cleanup func()) {
	defer close(s.drained)
	defer cleanup()
	defer func() { _ = s.conn.Close() }()
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.done {
			s.cond.Wait()
		}
		if len(s.queue) == 0 && s.done {
			s.mu.Unlock()
			return
		}
		batch := s.queue
		s.queue = nil
		s.queued = 0
		s.mu.Unlock()

		for _, chunk := range batch {
			if _, err := s.conn.Write(chunk); err != nil {
				s.finish()
				return
			}
		}
	}
}
