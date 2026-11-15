package generate

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func changeFirst(s string, f func(rune) rune) string {
	c, n := utf8.DecodeRuneInString(s)
	if c == utf8.RuneError { // empty, invalid UTF-8, or leading replacement character
		return s
	}
	return string(f(c)) + s[n:]
}

func upperFirst(s string) string {
	return changeFirst(strings.TrimLeft(s, "_"), unicode.ToUpper)
}

func goConstName(s string) string {
	if strings.TrimLeft(s, "_") == "" {
		return s
	}
	var prev rune
	return strings.Map(func(r rune) rune {
		ret := unicode.ToLower(r)
		switch {
		case r == '_':
			ret = -1
		case prev == '_' || prev == 0:
			ret = unicode.ToUpper(r)
		}
		prev = r
		return ret
	}, s)
}

// https://go.dev/ref/spec#Keywords
var goKeywords = map[string]bool{
	"break":       true,
	"default":     true,
	"func":        true,
	"interface":   true,
	"select":      true,
	"case":        true,
	"defer":       true,
	"go":          true,
	"map":         true,
	"struct":      true,
	"chan":        true,
	"else":        true,
	"goto":        true,
	"package":     true,
	"switch":      true,
	"const":       true,
	"fallthrough": true,
	"if":          true,
	"range":       true,
	"type":        true,
	"continue":    true,
	"for":         true,
	"import":      true,
	"return":      true,
	"var":         true,
}
