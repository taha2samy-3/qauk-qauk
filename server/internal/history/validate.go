package history

import (
	"encoding/json"
	"errors"
)

// ErrUnstorable marks a message that some backends can't store.
var ErrUnstorable = errors.New("message contains a NUL character or an unpaired surrogate")

// ValidateMessage rejects messages that are valid JSON for Go but not
// storable everywhere: "\u0000" and unpaired UTF-16 surrogates ("\ud800") in
// strings or keys. Postgres jsonb refuses both, and one such row would fail
// a whole ingest batch. The gateway calls this before accepting a frame.
func ValidateMessage(msg json.RawMessage) error {
	if !json.Valid(msg) {
		return errors.New("invalid JSON")
	}
	inStr := false
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		if !inStr {
			if c == '"' {
				inStr = true
			}
			continue
		}
		switch c {
		case '"':
			inStr = false
		case '\\':
			if i+1 >= len(msg) {
				return nil // json.Valid already rejected this
			}
			if msg[i+1] != 'u' {
				i++ // skip the escaped char
				continue
			}
			r, ok := hex4(msg, i+2)
			if !ok {
				return nil
			}
			switch {
			case r == 0:
				return ErrUnstorable
			case r >= 0xD800 && r <= 0xDBFF: // high surrogate: must be followed by a low one
				if i+11 < len(msg) && msg[i+6] == '\\' && msg[i+7] == 'u' {
					if lo, ok := hex4(msg, i+8); ok && lo >= 0xDC00 && lo <= 0xDFFF {
						i += 11
						continue
					}
				}
				return ErrUnstorable
			case r >= 0xDC00 && r <= 0xDFFF: // a low surrogate on its own
				return ErrUnstorable
			}
			i += 5
		}
	}
	return nil
}

func hex4(b []byte, at int) (rune, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	var r rune
	for _, c := range b[at : at+4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return 0, false
		}
	}
	return r, true
}
