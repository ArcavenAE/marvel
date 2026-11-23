package api

import (
	"strings"
	"testing"
)

func envPlaceholderManifest(env map[string]string) *Manifest {
	return &Manifest{
		Workspace: ManifestWorkspace{Name: "ph"},
		Teams: []ManifestTeam{{
			Name: "crew",
			Roles: []ManifestRole{{
				Name: "a", Replicas: 1, Runtime: ManifestRuntime{Command: "sh", Env: env},
			}},
		}},
	}
}

func TestValidateEnvPlaceholdersRefusesTheLiteral(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"<value>", " <value>", "<value>\n"} {
		err := envPlaceholderManifest(map[string]string{"TOKEN": v}).ValidateEnvPlaceholders()
		if err == nil {
			t.Fatalf("value %q was accepted, want a refusal", v)
		}
		for _, want := range []string{"role[0=a]", "env TOKEN", "1 key(s)"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("value %q: error %q, want it to contain %q", v, err, want)
			}
		}
	}
}

func TestValidateEnvPlaceholdersLeavesRealValuesAlone(t *testing.T) {
	t.Parallel()
	for _, env := range []map[string]string{
		nil,
		{},
		{"A": "real", "B": ""},
		{"A": "a<value>b", "B": "<value>x", "C": "<VALUE>", "D": "<>"},
	} {
		if err := envPlaceholderManifest(env).ValidateEnvPlaceholders(); err != nil {
			t.Errorf("env %v: %v, want no refusal", env, err)
		}
	}
}

func TestValidateEnvPlaceholdersListsKeysInOrder(t *testing.T) {
	t.Parallel()
	err := envPlaceholderManifest(map[string]string{"Z": "<value>", "A": "<value>", "M": "ok"}).ValidateEnvPlaceholders()
	if err == nil {
		t.Fatal("want a refusal")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 key(s)") || strings.Index(msg, "env A") > strings.Index(msg, "env Z") || strings.Contains(msg, "env M") {
		t.Errorf("error = %q, want keys A then Z, not M", msg)
	}
}
