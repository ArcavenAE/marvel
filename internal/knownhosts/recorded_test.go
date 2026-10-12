package knownhosts

import (
	"os"
	"testing"

	"golang.org/x/crypto/ssh"
)

func sameKey(a, b ssh.PublicKey) bool {
	return string(a.Marshal()) == string(b.Marshal())
}

func TestRecordedReturnsTheKeysHeldForOneAddress(t *testing.T) {
	l := testLayout(t)
	a, b := genKey(t), genKey(t)
	if err := Trust(l, "10.0.0.5:6785", a); err != nil {
		t.Fatal(err)
	}
	if err := Trust(l, "10.0.0.6:6785", b); err != nil {
		t.Fatal(err)
	}

	got, err := Recorded(l, "10.0.0.5:6785")
	if err != nil {
		t.Fatalf("Recorded: %v", err)
	}
	if len(got) != 1 || !sameKey(got[0], a) {
		t.Fatalf("Recorded(10.0.0.5) = %d keys, want exactly the one recorded for it", len(got))
	}
	got, err = Recorded(l, "10.0.0.6:6785")
	if err != nil || len(got) != 1 || !sameKey(got[0], b) {
		t.Fatalf("Recorded(10.0.0.6) = %v, %v; want only its own key", got, err)
	}
}

func TestRecordedIsEmptyForAnAddressWithNoEntry(t *testing.T) {
	l := testLayout(t)
	if err := Trust(l, "10.0.0.5:6785", genKey(t)); err != nil {
		t.Fatal(err)
	}
	got, err := Recorded(l, "10.0.0.9:6785")
	if err != nil {
		t.Fatalf("an unknown address is not an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Recorded(unknown) = %d keys, want none", len(got))
	}
}

func TestRecordedIsEmptyWhenKnownHostsDoesNotExist(t *testing.T) {
	l := testLayout(t)
	if err := os.Remove(l.KnownHosts()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	got, err := Recorded(l, "10.0.0.5:6785")
	if err != nil || len(got) != 0 {
		t.Fatalf("Recorded with no file = %v, %v; want nothing and no error", got, err)
	}
}

func TestRecordedDoesNotModifyKnownHosts(t *testing.T) {
	l := testLayout(t)
	if err := Trust(l, "10.0.0.5:6785", genKey(t)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(l.KnownHosts())
	if _, err := Recorded(l, "10.0.0.5:6785"); err != nil {
		t.Fatal(err)
	}
	if _, err := Recorded(l, "10.0.0.9:6785"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(l.KnownHosts())
	if string(before) != string(after) {
		t.Fatalf("known_hosts changed by a read:\n%s\n---\n%s", before, after)
	}
}

func TestRecordedKeepsTwoKeysHeldForOneAddress(t *testing.T) {
	l := testLayout(t)
	a, b := genKey(t), genKey(t)
	for _, k := range []ssh.PublicKey{a, b} {
		if err := Trust(l, "10.0.0.5:6785", k); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Recorded(l, "10.0.0.5:6785")
	if err != nil || len(got) != 2 {
		t.Fatalf("Recorded = %d keys, %v; want both", len(got), err)
	}
}
