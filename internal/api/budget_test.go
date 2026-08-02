package api

import (
	"strings"
	"testing"
)

// TestValidateManifestBudget covers the parse-time rules for a declared
// budget (aae-orc-qiay). The declaration clause is the load-bearing row:
// making sum(replicas) <= max_sessions an invariant of every parsed manifest
// is what lets the reconciler converge on declared replicas without a
// growth predicate, so repair can never be refused.
func TestValidateManifestBudget(t *testing.T) {
	t.Parallel()
	role := func(replicas int) ManifestRole {
		return ManifestRole{Name: "crew", Replicas: replicas, Runtime: ManifestRuntime{Command: "sleep"}}
	}
	tests := []struct {
		name    string
		team    ManifestTeam
		wantErr string
	}{
		{
			name: "no budget block is valid",
			team: ManifestTeam{Name: "crew", Roles: []ManifestRole{role(40)}},
		},
		{
			name: "an all-zero block declares no gate and is valid",
			team: ManifestTeam{Name: "crew", Budget: &ManifestBudget{}, Roles: []ManifestRole{role(40)}},
		},
		{
			name: "declared replicas exactly at the ceiling is valid",
			team: ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxSessions: 6}, Roles: []ManifestRole{role(6)}},
		},
		{
			name:    "declared replicas over the ceiling is refused",
			team:    ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxSessions: 6}, Roles: []ManifestRole{role(40)}},
			wantErr: "declares 40 replicas across 1 role(s) but budget.max_sessions is 6",
		},
		{
			name: "the replica sum spans every role",
			team: ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxSessions: 6}, Roles: []ManifestRole{
				role(4),
				{Name: "reviewer", Replicas: 4, Runtime: ManifestRuntime{Command: "sleep"}},
			}},
			wantErr: "declares 8 replicas across 2 role(s)",
		},
		{
			name:    "a negative session ceiling is refused",
			team:    ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxSessions: -1}, Roles: []ManifestRole{role(1)}},
			wantErr: "budget.max_sessions must be >= 0",
		},
		{
			name:    "a negative token ceiling is refused",
			team:    ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTokens: -1}, Roles: []ManifestRole{role(1)}},
			wantErr: "budget.max_tokens must be >= 0",
		},
		{
			name:    "an unenforced cost dimension names its owner",
			team:    ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxCostUSD: 5}, Roles: []ManifestRole{role(1)}},
			wantErr: "budget.max_cost_usd is a known dimension (matrix row 2) but is not enforced in this slice",
		},
		{
			name:    "an unenforced memory dimension names its owner",
			team:    ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTeamRSSBytes: 1 << 30}, Roles: []ManifestRole{role(1)}},
			wantErr: "budget.max_team_rss_bytes is a known dimension",
		},
		{
			name:    "an unenforced occupancy dimension names its owner",
			team:    ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxSessionCtxPct: 80}, Roles: []ManifestRole{role(1)}},
			wantErr: "budget.max_session_ctx_percent is a known dimension",
		},
		{
			name:    "a mistyped on_unmeasured lists the valid values",
			team:    ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTokens: 10, OnUnmeasured: "deny"}, Roles: []ManifestRole{role(1)}},
			wantErr: `budget.on_unmeasured "deny" is not valid (valid: admit, refuse)`,
		},
		{
			name: "both valid on_unmeasured values pass",
			team: ManifestTeam{Name: "crew", Budget: &ManifestBudget{MaxTokens: 10, OnUnmeasured: UnmeasuredRefuse}, Roles: []ManifestRole{role(1)}},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateManifestBudget(0, tt.team)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestBudgetDefaultOpen pins the governance property as code: an undeclared
// budget is not a gate, and a declared one resolves its unmeasured mode
// rather than leaving it empty.
func TestBudgetDefaultOpen(t *testing.T) {
	t.Parallel()
	var zero Budget
	if zero.Declared() {
		t.Error("the zero Budget declares a gate; every pre-existing manifest would change behavior")
	}
	if _, ok := zero.Limit(DimMaxSessions); ok {
		t.Error("an unset clause reported a limit")
	}
	if zero.Unmeasured() != UnmeasuredAdmit {
		t.Errorf("default unmeasured mode = %q, want %q", zero.Unmeasured(), UnmeasuredAdmit)
	}

	b := Budget{MaxSessions: 6, MaxTokens: 10, OnUnmeasured: UnmeasuredRefuse}
	if !b.Declared() {
		t.Error("a declared budget reported no gate")
	}
	if limit, ok := b.Limit(DimMaxSessions); !ok || limit != 6 {
		t.Errorf("Limit(max_sessions) = (%d, %v), want (6, true)", limit, ok)
	}
	if b.Unmeasured() != UnmeasuredRefuse {
		t.Errorf("unmeasured mode = %q, want %q", b.Unmeasured(), UnmeasuredRefuse)
	}
	// An unimplemented dimension must not report a limit even if someone
	// later adds a field for it, or a gate would fire with no evaluator.
	if _, ok := b.Limit(DimMaxCostUSD); ok {
		t.Error("an unimplemented dimension reported a limit")
	}
}

// TestDimensionRegistry covers the registry contract: stable order, working
// lookup, and a caller who cannot reorder it for everyone else.
func TestDimensionRegistry(t *testing.T) {
	t.Parallel()
	specs := Specs()
	if len(specs) != 5 {
		t.Fatalf("got %d specs, want 5", len(specs))
	}
	// Count-shaped rows evaluate before cumulative ones, so a session
	// ceiling decides before a spend ceiling.
	if specs[0].Dimension != DimMaxSessions || specs[1].Dimension != DimMaxTokens {
		t.Errorf("registry order = %q, %q; want max_sessions then max_tokens", specs[0].Dimension, specs[1].Dimension)
	}
	specs[0].Dimension = "clobbered"
	if Specs()[0].Dimension != DimMaxSessions {
		t.Error("Specs returned the live registry: a caller reordered it for everyone")
	}

	for _, want := range []Dimension{DimMaxSessions, DimMaxTokens, DimMaxCostUSD, DimMaxTeamRSSBytes, DimMaxSessionCtxPct} {
		spec, ok := LookupDimension(want)
		if !ok {
			t.Errorf("LookupDimension(%q) not found", want)
			continue
		}
		if spec.Dimension != want {
			t.Errorf("LookupDimension(%q) returned %q", want, spec.Dimension)
		}
		if spec.Owner == "" {
			t.Errorf("%q has no owner, so its validation error points nowhere", want)
		}
	}
	if _, ok := LookupDimension("max_vibes"); ok {
		t.Error("LookupDimension found an unregistered dimension")
	}

	if got := DimensionList(); got != "max_cost_usd, max_session_ctx_percent, max_sessions, max_team_rss_bytes, max_tokens" {
		t.Errorf("DimensionList() = %q", got)
	}
}

// TestCountAlive pins the shared definition of "live" so the reconciler and
// the daemon cannot drift into counting differently.
func TestCountAlive(t *testing.T) {
	t.Parallel()
	sessions := []Session{
		{State: SessionPending},
		{State: SessionRunning},
		{State: SessionCrashLoopBackOff},
		{State: SessionSucceeded},
		{State: SessionFailed},
		{State: SessionCrashed},
	}
	if got := CountAlive(sessions); got != 3 {
		t.Errorf("CountAlive = %d, want 3", got)
	}
	if got := CountAlive(nil); got != 0 {
		t.Errorf("CountAlive(nil) = %d, want 0", got)
	}
}
