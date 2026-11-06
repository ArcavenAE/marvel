package api

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// accountLabelMaxRunes bounds a declared login label. A full API key is longer,
// so the bound also stops one being pasted in by mistake.
const accountLabelMaxRunes = 32

// validateAccountLabel refuses a declared login label that cannot be shown
// safely in a table cell. where names the team or role, for the error. The
// error names the field and the reason and never repeats the label: a label
// that looks like a key must not reach a log through its own refusal. An empty
// label means none was declared.
func validateAccountLabel(where, label string) error {
	if label == "" {
		return nil
	}
	refuse := func(reason string) error {
		return fmt.Errorf("%s: account_label %s", where, reason)
	}
	if !utf8.ValidString(label) {
		return refuse("is not valid UTF-8")
	}
	if utf8.RuneCountInString(label) > accountLabelMaxRunes {
		return refuse(fmt.Sprintf("is longer than %d characters", accountLabelMaxRunes))
	}
	if strings.HasPrefix(label, "sk-ant-") {
		return refuse("looks like an API key; declare a word such as a tier name")
	}
	for _, r := range label {
		switch {
		case r == utf8.RuneError:
			return refuse("holds the replacement character")
		case unicode.IsControl(r):
			return refuse("holds a control character")
		case unicode.Is(unicode.Cf, r):
			return refuse("holds a formatting character")
		case unicode.IsSpace(r) || unicode.Is(unicode.Z, r):
			return refuse("holds whitespace; it is one word")
		case r == '@':
			return refuse("holds @; it is a word, not an address")
		}
	}
	return nil
}
