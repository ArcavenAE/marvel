package api

import (
	"strings"
	"testing"
)

// Operator ruling redaction-url-userinfo (a), relayed by director on 2026-10-09:
// redact only the userinfo part of URL-shaped values, whatever the key. Host,
// port, path and query stay readable. A URL with no userinfo is untouched.
func TestRedactURLUserinfo(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"user and password", "redis://user:password@host:6379/0", "redis://(redacted)@host:6379/0"},
		{"password with at and colon", "postgres://user:p@ss:w0rd@db.example.com:5432/app?sslmode=require", "postgres://(redacted)@db.example.com:5432/app?sslmode=require"},
		{"ipv6 host", "redis://u:p@[::1]:6379/0", "redis://(redacted)@[::1]:6379/0"},
		{"ipv6 host without userinfo", "redis://[::1]:6379/0", "redis://[::1]:6379/0"},
		{"token as username", "https://ghp_abc123@github.com/org/repo.git", "https://(redacted)@github.com/org/repo.git"},
		{"no userinfo", "https://example.com/path?x=1", "https://example.com/path?x=1"},
		{"at sign in the path", "https://example.com/@user/repo", "https://example.com/@user/repo"},
		{"at sign in the query", "https://example.com/p?email=a@b.com", "https://example.com/p?email=a@b.com"},
		{"at sign in the fragment", "https://example.com/p#a@b", "https://example.com/p#a@b"},
		{"empty userinfo", "http://@host/", "http://@host/"},
		{"scheme with plus and dot", "redis+sentinel://u:p@h1:26379/mymaster", "redis+sentinel://(redacted)@h1:26379/mymaster"},
		{"nested scheme", "jdbc:postgresql://u:p@h:5432/db", "jdbc:postgresql://(redacted)@h:5432/db"},
		{"two urls", "redis://a:b@h1:1,redis://c:d@h2:2", "redis://(redacted)@h1:1,redis://(redacted)@h2:2"},
		{"url inside text", "see postgres://u:p@h/db for details", "see postgres://(redacted)@h/db for details"},
		{"not a url", "us-east-1", "us-east-1"},
		{"address without a scheme", "a@b.com", "a@b.com"},
		{"host and port", "host:6379", "host:6379"},
		{"userinfo without scheme", "user:pass@host", "user:pass@host"},
		{"empty", "", ""},
		{"scheme only", "https://", "https://"},
		{"already redacted", "redis://(redacted)@host/0", "redis://(redacted)@host/0"},
	} {
		if got := redactURLUserinfo(tc.in); got != tc.want {
			t.Errorf("%s: redactURLUserinfo(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// Redact applies it to every Env value, whatever its key, and a key that
// already redacts still redacts as a whole.
func TestRedactAppliesURLUserinfoToAnyEnvKey(t *testing.T) {
	rt := Runtime{Env: map[string]string{
		"SERVICE_URL":       "https://u:hunter2@svc.example.com:8443/v1",
		"SLACK_WEBHOOK_URL": "https://hooks.slack.com/services/T000/B000/XXXX",
		"BASE_URL":          "https://example.com/app",
		"REGION":            "us-east-1",
		"SERVICE_TOKEN":     "https://u:p@h/",
	}}
	got := Redact(rt).Env
	if got["SERVICE_URL"] != "https://(redacted)@svc.example.com:8443/v1" {
		t.Errorf("SERVICE_URL = %q", got["SERVICE_URL"])
	}
	if got["SLACK_WEBHOOK_URL"] != Redacted {
		t.Errorf("SLACK_WEBHOOK_URL = %q, want it redacted as a whole", got["SLACK_WEBHOOK_URL"])
	}
	if got["SERVICE_TOKEN"] != Redacted {
		t.Errorf("SERVICE_TOKEN = %q, want it redacted as a whole", got["SERVICE_TOKEN"])
	}
	if got["BASE_URL"] != "https://example.com/app" || got["REGION"] != "us-east-1" {
		t.Errorf("an ordinary value changed: %q %q", got["BASE_URL"], got["REGION"])
	}
	if rt.Env["SERVICE_URL"] != "https://u:hunter2@svc.example.com:8443/v1" {
		t.Error("Redact wrote through to its input")
	}
}

// The URL-class rows of the reviewer's table (redact_table_test.go) get value
// assertions: whichever way the key goes, the password never prints, and where
// the key does not redact the value, the host and path stay readable.
func TestReviewerTableURLRowsNeverPrintTheirPassword(t *testing.T) {
	const url = "scheme://dbuser:hunter2@db.example.com:1234/app?x=1"
	for _, key := range []string{
		"DSN", "SENTRY_DSN", "DATABASE_URL", "REDIS_URL", "MONGODB_URI", "CONN_STR", "CONNECTION_STRING",
		"AMQP_URL", "POSTGRES_URL", "PG_URL", "DB_URI", "MYSQL_URL", "AUTH_URL", "TOKEN_URL",
	} {
		got := Redact(Runtime{Env: map[string]string{key: url}}).Env[key]
		if strings.Contains(got, "hunter2") || strings.Contains(got, "dbuser") {
			t.Errorf("%s prints its credentials: %q", key, got)
		}
		if SecretKey(key) {
			if got != Redacted {
				t.Errorf("%s redacts by key, so want %q, got %q", key, Redacted, got)
			}
			continue
		}
		if got != "scheme://(redacted)@db.example.com:1234/app?x=1" {
			t.Errorf("%s = %q, want the userinfo redacted and the host and path readable", key, got)
		}
	}
}
