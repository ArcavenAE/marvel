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
	"secret", "password", "passwd", "passphrase", "credential",
	"apikey", "api_key", "api-key", "accesskey", "access_key",
	"privatekey", "private_key", "sshkey", "signingkey", "encryptionkey",
	"bearer", "cookie", "webhook", "dsn",
	"database_url", "db_url", "redis_url", "mongodb_uri", "mongodb_url", "mongo_uri", "mongo_url",
}

// secretKeyWords are whole words of a key that mark it as secret-looking. A key
// is split into words on _ - . and :, on a lower-to-upper case change, on the
// end of an upper-case run before a capitalised word (HTTPAuth is HTTP, Auth),
// and between letters and digits (TOKEN2 is TOKEN, 2). They are words and not
// substrings so KEYBOARD, MONKEY and PATH stay visible.
var secretKeyWords = []string{"key", "pat", "authorization"}

// secretKeyLastWords mark a key only as its last (or only) word: DB_PASS and
// SMTP_PW are passwords, and PASS_THROUGH and PW_DEBUG are not. pwd needs a
// second word as well, because PWD alone is the shell's working directory.
var secretKeyLastWords = []string{"pass", "pw"}

// SecretKey reports whether an Env key looks like it names a secret.
func SecretKey(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range secretKeySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	words := keyWords(name)
	for i, w := range words {
		last := i == len(words)-1
		switch {
		case slices.Contains(secretKeyWords, w),
			last && slices.Contains(secretKeyLastWords, w),
			last && len(words) > 1 && w == "pwd",
			tokenWord(w), authWord(w):
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
// AUTHHEADER), but not oauth, author or authority.
func authWord(w string) bool {
	if w == "oauth" || strings.HasPrefix(w, "author") {
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
		}
		out[k] = val
	}
	return out
}
