package ingest

import "strings"

type kvPair struct {
	Key, Value string
	quoted     bool
}

// parseKV reads the key=value body that firewalls and routers put after the
// syslog header. Values may contain spaces, as in `msg=DNS blocked policy=x`:
// a token that does not itself start a pair continues the previous value.
// Double-quoted values are taken literally, with \" and \\ unescaped. Text
// before the first pair is skipped; the whole line is kept in raw anyway.
func parseKV(body string) []kvPair {
	var pairs []kvPair
	i := 0
	for i < len(body) {
		for i < len(body) && isBlank(body[i]) {
			i++
		}
		if i == len(body) {
			break
		}

		key, n := keyAt(body[i:])
		if n == 0 {
			end := tokenEnd(body, i)
			if last := len(pairs) - 1; last >= 0 && !pairs[last].quoted {
				if pairs[last].Value == "" {
					pairs[last].Value = body[i:end]
				} else {
					pairs[last].Value += " " + body[i:end]
				}
			}
			i = end
			continue
		}

		i += n
		if i < len(body) && body[i] == '"' {
			value, m := unquote(body[i:])
			pairs = append(pairs, kvPair{Key: key, Value: value, quoted: true})
			i += m
			continue
		}
		end := tokenEnd(body, i)
		pairs = append(pairs, kvPair{Key: key, Value: body[i:end]})
		i = end
	}
	return pairs
}

// keyAt reports the key at the start of s and the length of `key=`, or 0 when
// s does not start with one. Keys are letters, digits, '_', '.' and '-', and
// start with a letter or '_'.
func keyAt(s string) (string, int) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '=' && i > 0:
			return s[:i], i + 1
		case isLetter(c) || c == '_':
		case i > 0 && (isDigit(c) || c == '.' || c == '-'):
		default:
			return "", 0
		}
	}
	return "", 0
}

// unquote reads a double-quoted value at the start of s and returns it with
// the number of bytes consumed. An unterminated quote runs to the end.
func unquote(s string) (string, int) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
			b.WriteByte(s[i+1])
			i++
		case c == '"':
			return b.String(), i + 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), len(s)
}

func tokenEnd(s string, i int) int {
	for i < len(s) && !isBlank(s[i]) {
		i++
	}
	return i
}

func isBlank(c byte) bool  { return c == ' ' || c == '\t' }
func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }
func isDigit(c byte) bool  { return '0' <= c && c <= '9' }
