package api

import (
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Redacted is what the wire shows in place of a secret-looking Env value.
const Redacted = "(redacted)"

// secretKeySubstrings are the fragments that mark an Env key as secret-looking,
// matched case-insensitively anywhere in the key. The list is short on purpose
// and is the whole definition: a credential stored under a key that matches
// nothing here prints (docs/design/describe-redaction.md section 10, ruling 1).
var secretKeySubstrings = []string{
	"secret", "password", "passwd", "passphrase", "passcode", "credential",
	"apikey", "api_key", "api-key", "accesskey", "access_key",
	"privatekey", "private_key", "sshkey", "signingkey", "encryptionkey",
	"bearer", "cookie", "webhook", "dsn",
	"database_url", "db_url", "redis_url", "mongodb_uri", "mongodb_url", "mongo_uri", "mongo_url",
}

// secretKeyWords are whole words that mark a key as secret-looking (authorization
// is covered by authWord, which matches any word that starts with auth). They are
// taken from the key split on _ - . and : only, so a camel-case hotKey or
// patCount is not a key or a pat, and KEYBOARD, MONKEY and PATH stay visible.
var secretKeyWords = []string{"key", "pat", "sk", "jwt"}

// secretKeyLastWords mark a key when they are the last word of the finer split
// (see keyWords), so a camel-case clientKey, githubPat and stripeKey redact
// while patCount does not.
var secretKeyLastWords = []string{"key", "pat"}

// notAuthWords are whole words that begin with auth and are not credentials.
// They are exact words so a run-together name such as AUTHENTICATIONKEY still
// redacts.
var notAuthWords = []string{
	"oauth", "author", "authors", "authored", "authoring", "authority", "authorities", "authorship",
	"authentic", "authenticate", "authenticated", "authentication",
}

// secretPasswordWords mark a key as secret-looking when they are any word but
// the first of a longer name: DB_PASS, DB_PASS_PROD, SMTP_PW_2 and MYSQL_PWD
// are passwords, and PASS_THROUGH and PW_DEBUG are not. pass and pw also match
// as a whole one-word name; pwd does not, since PWD alone is the shell's working
// directory.
var secretPasswordWords = []string{"pass", "pw", "pwd"}

// SecretKey reports whether an Env key looks like it names a secret.
func SecretKey(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range secretKeySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	for _, w := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ':'
	}) {
		if slices.Contains(secretKeyWords, w) {
			return true
		}
	}
	// The finer split below also cuts on case and digit changes (see keyWords),
	// so TOKEN2, basicAuth and HTTPAuth are found.
	words := keyWords(name)
	for i, w := range words {
		if tokenWord(w) || authWord(w) {
			return true
		}
		if i == len(words)-1 && slices.Contains(secretKeyLastWords, w) {
			return true
		}
		if slices.Contains(secretPasswordWords, w) && (i > 0 || (len(words) == 1 && w != "pwd")) {
			return true
		}
		// PW_HASH, PASS_HASH: a first password word that names a password hash.
		if slices.Contains(secretPasswordWords, w) && i+1 < len(words) && words[i+1] == "hash" {
			return true
		}
	}
	return false
}

// tokenWord matches a word that is, ends in, or starts with token (APITOKEN,
// TOKENVALUE), but not tokenizer or tokenize, which name a text tool.
func tokenWord(w string) bool {
	if strings.HasPrefix(w, "tokeniz") {
		return false
	}
	return strings.HasSuffix(w, "token") || strings.HasSuffix(w, "tokens") || strings.HasPrefix(w, "token")
}

// authWord matches a word that is, ends in, or starts with auth (BASICAUTH,
// AUTHHEADER), but not the words in notAuthWords.
func authWord(w string) bool {
	if slices.Contains(notAuthWords, w) {
		return false
	}
	return strings.HasSuffix(w, "auth") || strings.HasPrefix(w, "auth")
}

// keyWords splits a key into lower-case words (see secretKeyWords).
func keyWords(name string) []string {
	runes := []rune(name)
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ':':
			flush()
			continue
		case len(cur) > 0:
			prev := cur[len(cur)-1]
			switch {
			case unicode.IsLower(prev) && unicode.IsUpper(r):
				flush()
			case unicode.IsUpper(prev) && unicode.IsUpper(r) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
				flush()
			case unicode.IsDigit(prev) != unicode.IsDigit(r):
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	// OAuth splits by case into o and auth; it is one word, and not a secret.
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "o" && words[i+1] == "auth" {
			words[i] = "oauth"
			words = slices.Delete(words, i+1, i+2)
		}
	}
	return words
}

// Redact returns a copy of v in which every Runtime, at any depth, shows
// Redacted for the values of secret-looking Env keys. The input is never
// changed: the durable record keeps what marvel needs, and only the wire gets
// the view. A type that cannot hold a Runtime is returned as it is.
func Redact[T any](v T) T {
	rv := reflect.ValueOf(&v).Elem()
	out := redactValue(rv)
	return out.Interface().(T)
}

var (
	runtimeType = reflect.TypeFor[Runtime]()
	holdsMu     sync.Mutex
	holdsCache  = map[reflect.Type]bool{}
)

// holdsRuntime reports whether a value of type t can contain a Runtime. A
// struct is walked by its exported fields only, which is all encoding/json
// reads. An interface may hold anything, so it always counts.
func holdsRuntime(t reflect.Type) bool {
	holdsMu.Lock()
	defer holdsMu.Unlock()
	return holdsLocked(t, map[reflect.Type]bool{})
}

func holdsLocked(t reflect.Type, visiting map[reflect.Type]bool) bool {
	if t == runtimeType {
		return true
	}
	if got, ok := holdsCache[t]; ok {
		return got
	}
	if visiting[t] {
		return false
	}
	visiting[t] = true
	defer delete(visiting, t)
	var holds bool
	switch t.Kind() {
	case reflect.Interface:
		holds = true
	case reflect.Pointer, reflect.Slice, reflect.Array:
		holds = holdsLocked(t.Elem(), visiting)
	case reflect.Map:
		holds = holdsLocked(t.Elem(), visiting)
	case reflect.Struct:
		for i := 0; i < t.NumField() && !holds; i++ {
			if f := t.Field(i); f.IsExported() {
				holds = holdsLocked(f.Type, visiting)
			}
		}
	}
	// A result computed while an ancestor is still being visited may be
	// incomplete, so only the outermost call caches.
	if len(visiting) == 1 {
		holdsCache[t] = holds
	}
	return holds
}

func redactValue(v reflect.Value) reflect.Value {
	if !holdsRuntime(v.Type()) {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(redactValue(v.Elem()))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(redactValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(redactValue(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(redactValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for _, k := range v.MapKeys() {
			out.SetMapIndex(k, redactValue(v.MapIndex(k)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		if v.Type() == runtimeType {
			rt := out.Addr().Interface().(*Runtime)
			rt.Env = redactEnv(rt.Env)
			return out
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				out.Field(i).Set(redactValue(v.Field(i)))
			}
		}
		return out
	}
	return v
}

func redactEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, val := range env {
		if SecretKey(k) {
			val = Redacted
		} else {
			val = redactURLUserinfo(val)
		}
		out[k] = val
	}
	return out
}

// authorityEnd are the characters that end a URL's authority: / ? and # by
// RFC 3986, and white space.
const authorityEnd = "/?# \t\r\n"

// redactURLUserinfo replaces the userinfo of every URL in s with Redacted, so
// redis://user:password@host/0 reads redis://(redacted)@host/0. Host, port,
// path and query stay readable, and a URL with no userinfo is left as it is
// (operator ruling redaction-url-userinfo, option a, relayed by director on
// 2026-10-09). A URL is found by its "://"; its authority runs to the next
// authorityEnd character or to the start of a second URL, and its userinfo is
// everything before the last @ in that authority, so a password that holds @, :
// or a raw " < > \ ^ ` { | or } is covered. The syntax cannot tell u:p@h"x@y
// from u:p@x"y@h, so those raw characters do not end the authority: a secret
// never prints for the sake of a readable host, and a later @ in the same
// value hides the host too. A password with an unencoded /, ? or # is not
// covered, since those end the authority. The cost list and the deviation from
// the ruling are in docs/design/describe-redaction.md, item 5.
func redactURLUserinfo(s string) string {
	var out strings.Builder
	last, i := 0, 0
	for {
		j := strings.Index(s[i:], "://")
		if j < 0 {
			break
		}
		start := i + j + len("://")
		end := len(s)
		if k := strings.IndexAny(s[start:], authorityEnd); k >= 0 {
			end = start + k
		}
		if n := strings.Index(s[start:], "://"); n >= 0 {
			end = min(end, start+n)
		}
		if at := strings.LastIndex(s[start:end], "@"); at > 0 {
			out.WriteString(s[last:start])
			out.WriteString(Redacted)
			last = start + at
		}
		i = max(end, start)
	}
	out.WriteString(s[last:])
	return out.String()
}
