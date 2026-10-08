// Package i18n holds the UI message catalogs (English and Swedish) and
// picks a language per request.
package i18n

import (
	"fmt"
	"net/http"
	"strings"
)

// Lang is a supported UI language code.
type Lang string

const (
	English Lang = "en"
	Swedish Lang = "sv"

	// Cookie remembers the user's explicit choice.
	Cookie = "edugit_lang"
)

var catalogs = map[Lang]map[string]string{English: en, Swedish: sv}

// Supported lists the languages in display order.
var Supported = []Lang{English, Swedish}

// Parse returns the language for a code, and false if unsupported.
func Parse(code string) (Lang, bool) {
	l := Lang(strings.ToLower(strings.TrimSpace(code)))
	_, ok := catalogs[l]
	return l, ok
}

// FromRequest picks the language: the cookie wins, then the first supported
// Accept-Language entry, then English.
func FromRequest(r *http.Request) Lang {
	if c, err := r.Cookie(Cookie); err == nil {
		if l, ok := Parse(c.Value); ok {
			return l
		}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		base, _, _ := strings.Cut(tag, "-")
		if l, ok := Parse(base); ok {
			return l
		}
	}
	return English
}

// T translates a message key for the language, falling back to English and
// then to the key itself. Extra args are applied with fmt.Sprintf.
func (l Lang) T(key string, args ...any) string {
	msg, ok := catalogs[l][key]
	if !ok {
		if msg, ok = en[key]; !ok {
			msg = key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// Code is the language code, for the html lang attribute.
func (l Lang) Code() string { return string(l) }

// Name is the language's name in itself, for the switcher.
func (l Lang) Name() string { return catalogs[l]["lang.name"] }
