package api

import (
	"reflect"
	"slices"
	"strings"
	"sync"
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

// secretKeyWords are whole words of a key, split on _ - . and :, that mark it
// as secret-looking. They are words and not substrings so KEYBOARD, MONKEY,
// PATH, AUTHOR_NAME, BYPASS_CACHE and TOKENIZERS_PARALLELISM stay visible.
var secretKeyWords = []string{
	"key", "pat", "pass", "pw", "token", "tokens", "auth", "authorization",
}

// secretKeyMultiWordOnly are words that mark a key as secret-looking only when
// the key has another word beside them: MYSQL_PWD is a password, and PWD alone
// is the shell's working directory.
var secretKeyMultiWordOnly = []string{"pwd"}

// SecretKey reports whether an Env key looks like it names a secret.
func SecretKey(name string) bool {
	lower := strings.ToLower(name)
	for _, s := range secretKeySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	// A run-together camel-case name such as GitHubToken ends in the word.
	if strings.HasSuffix(lower, "token") {
		return true
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ':'
	})
	for _, w := range words {
		if slices.Contains(secretKeyWords, w) {
			return true
		}
		if len(words) > 1 && slices.Contains(secretKeyMultiWordOnly, w) {
			return true
		}
	}
	return false
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
