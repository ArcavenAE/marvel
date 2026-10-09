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
		{"at sign in a query that follows the host", "https://example.com?email=a@b.com", "https://example.com?email=a@b.com"},
		{"at sign in a fragment that follows the host", "https://example.com#a@b", "https://example.com#a@b"},
		{"userinfo then a query with an at sign", "https://u:p@example.com?email=a@b.com", "https://(redacted)@example.com?email=a@b.com"},
		{"no userinfo then a second url with one", "https://h1,redis://c:d@h2", "https://h1,redis://(redacted)@h2"},
		{"empty userinfo", "http://@host/", "http://@host/"},
		{"scheme with plus and dot", "redis+sentinel://u:p@h1:26379/mymaster", "redis+sentinel://(redacted)@h1:26379/mymaster"},
		{"nested scheme", "jdbc:postgresql://u:p@h:5432/db", "jdbc:postgresql://(redacted)@h:5432/db"},
		{"two urls", "redis://a:b@h1:1,redis://c:d@h2:2", "redis://(redacted)@h1:1,redis://(redacted)@h2:2"},
		{"url inside text", "see postgres://u:p@h/db for details", "see postgres://(redacted)@h/db for details"},
		{"white space ends the authority", "connect to redis://u:p@h then mail a@b.com", "connect to redis://(redacted)@h then mail a@b.com"},
		{"json value keeps the host and the next field", `{"url":"redis://u:CNRYpw@h","mail":"a@b.com"}`, `{"url":"redis://(redacted)@h","mail":"a@b.com"}`},
		{"tab ends the authority", "redis://u:p@h\tx@b.com", "redis://(redacted)@h\tx@b.com"},
		{"newline ends the authority", "redis://u:p@h\nx@b.com", "redis://(redacted)@h\nx@b.com"},
		{"carriage return ends the authority", "redis://u:p@h\rx@b.com", "redis://(redacted)@h\rx@b.com"},
		{"semicolon in the password", "redis://u:ab;cd@h/0", "redis://(redacted)@h/0"},
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

// RFC 3986 never allows these characters in an authority, so each one ends it:
// a value that carries a URL inside JSON, markup or a template keeps its host
// and the text after it.
func TestRedactURLUserinfoEndsAtCharactersAnAuthorityCannotHold(t *testing.T) {
	for _, c := range []string{`"`, "<", ">", `\`, "^", "`", "{", "|", "}"} {
		in := "redis://u:p@h" + c + "x@b.com"
		want := "redis://(redacted)@h" + c + "x@b.com"
		if got := redactURLUserinfo(in); got != want {
			t.Errorf("after %q: redactURLUserinfo(%q) = %q, want %q", c, in, got, want)
		}
	}
	// A comma and a semicolon are legal in userinfo and stay inside it.
	if got := redactURLUserinfo("https://host,ops@example.com"); got != "https://(redacted)@example.com" {
		t.Errorf("a comma in userinfo: got %q", got)
	}
}

// RFC 3986 wants these characters percent-encoded, but generated passwords carry
// them raw. They end the authority only after an @ has been seen, so a password
// holding one is redacted whole (review 5466478390, item 1).
func TestRedactURLUserinfoCoversAPasswordHoldingACharacterAnAuthorityCannotHold(t *testing.T) {
	for _, c := range []string{`"`, "<", ">", `\`, "^", "`", "{", "|", "}"} {
		in := "redis://u:CNRY" + c + "pw@h/0"
		want := "redis://(redacted)@h/0"
		if got := redactURLUserinfo(in); got != want {
			t.Errorf("password holding %q: redactURLUserinfo(%q) = %q, want %q", c, in, got, want)
		}
	}
}

// Shapes the ruling does not reach print as they were stored. Pinning them keeps
// the cost list in docs/design/describe-redaction.md honest: a change that makes
// one of these redact is a change to the ruling's reach and should be a choice.
func TestRedactURLUserinfoKnownLimits(t *testing.T) {
	for name, in := range map[string]string{
		"white space inside the password":       "redis://u:CNRY pw@h/0",
		"a tab inside the password":             "redis://u:CNRY\tpw@h/0",
		"a slash in the password (base64)":      "redis://u:ab+CNRY/pw==@h/0",
		"a question mark in the password":       "redis://u:ab?pw@h/0",
		"a hash in the password":                "redis://u:ab#pw@h/0",
		"a password in the query string":        "postgres://db/app?user=u&password=CNRYpw",
		"userinfo with no scheme":               "u:CNRYpw@host:5432/db",
		"userinfo after only two slashes":       "//u:CNRYpw@h/x",
		"keys and values joined by semicolons":  "https://host;user=u;password=CNRYpw",
		"a connection string that is not a URL": "Server=h;User Id=u;Password=CNRYpw",
	} {
		if got := redactURLUserinfo(in); got != in {
			t.Errorf("%s: %q changed to %q; if this is now redacted, update the cost list in describe-redaction.md item 5", name, in, got)
		}
	}
}
