package session

import (
	"strings"
	"testing"
)

// The run profile is composed, never obeyed: role text, then the request, then
// the deny block, one text with the block last (seat-tcc-deny.md, section 5).
func TestComposeRunProfileOrdersRoleRequestBlock(t *testing.T) {
	role := "(version 1)\n;ROLE\n(allow default)\n"
	req := ";REQUEST\n(deny network*)\n"
	block := ";BLOCK\n(deny file-read* (subpath \"/Volumes\"))\n"
	got := ComposeRunProfile(role, req, block)
	r, q, b := strings.Index(got, ";ROLE"), strings.Index(got, ";REQUEST"), strings.Index(got, ";BLOCK")
	if r < 0 || q < 0 || b < 0 {
		t.Fatalf("a part is missing from the composed profile: role=%d request=%d block=%d\n%s", r, q, b, got)
	}
	if r >= q || q >= b {
		t.Fatalf("order is role=%d request=%d block=%d, want role < request < block", r, q, b)
	}
	if !strings.HasSuffix(got, block) {
		t.Fatalf("the deny block is not last:\n%s", got)
	}
}

// A request that carries its own allow still comes before the block, so it
// cannot lift a deny the block names.
func TestComposeRunProfileKeepsBlockAfterARequestedAllow(t *testing.T) {
	block := "(deny file-read* (subpath \"/Volumes\"))\n"
	got := ComposeRunProfile("(version 1)\n", "(allow file-read* (subpath \"/Volumes\"))\n", block)
	allow, deny := strings.LastIndex(got, "(allow file-read*"), strings.Index(got, block)
	if allow < 0 || deny < 0 {
		t.Fatalf("the requested allow (%d) or the deny block (%d) is missing:\n%s", allow, deny, got)
	}
	if allow > deny {
		t.Fatalf("a requested allow follows the deny block:\n%s", got)
	}
}

// The composer adds the version form once, and only when no part supplied it,
// so sandbox-exec reads one well-formed profile.
func TestComposeRunProfileHasOneVersionForm(t *testing.T) {
	got := ComposeRunProfile("", "(deny network*)\n", "(deny file-read* (subpath \"/Volumes\"))\n")
	if n := strings.Count(got, "(version 1)"); n != 1 {
		t.Fatalf("(version 1) appears %d times, want 1:\n%s", n, got)
	}
	got = ComposeRunProfile("(version 1)\n", "(deny network*)\n", "(deny file-read* (subpath \"/Volumes\"))\n")
	if n := strings.Count(got, "(version 1)"); n != 1 {
		t.Fatalf("(version 1) appears %d times with a role that names it, want 1:\n%s", n, got)
	}
}
