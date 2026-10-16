package daemonrpc

import "testing"

func TestListenNetwork(t *testing.T) {
	t.Parallel()
	tests := []struct {
		addr     string
		expected string
	}{
		{"/tmp/marvel.sock", "unix"},
		{"0.0.0.0:9090", "tcp"},
		{":9090", "tcp"},
		{"localhost:9090", "tcp"},
		{"192.168.1.5:9090", "tcp"},
		{"/var/run/marvel.sock", "unix"},
	}
	for _, tt := range tests {
		got := ListenNetwork(tt.addr)
		if got != tt.expected {
			t.Errorf("ListenNetwork(%q) = %q, want %q", tt.addr, got, tt.expected)
		}
	}
}

func TestIsSSH(t *testing.T) {
	t.Parallel()
	tests := []struct {
		addr     string
		expected bool
	}{
		{"ssh://user@host/tmp/marvel.sock", true},
		{"ssh://host:9090", true},
		{"/tmp/marvel.sock", false},
		{"localhost:9090", false},
		{"tcp://host:9090", false},
	}
	for _, tt := range tests {
		got := IsSSH(tt.addr)
		if got != tt.expected {
			t.Errorf("IsSSH(%q) = %v, want %v", tt.addr, got, tt.expected)
		}
	}
}
