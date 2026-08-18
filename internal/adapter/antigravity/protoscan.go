package antigravity

import (
	"encoding/binary"
	"unicode"
	"unicode/utf8"
)

// minStringLen is the shortest byte run treated as a meaningful embedded
// string rather than incidental binary noise.
//
// ponytail: this is a heuristic wire-format scan, not a real decode
// against Antigravity CLI's (undocumented, proprietary) .proto schema --
// it recovers human-readable text embedded in length-delimited fields
// without knowing which field number means what. Upgrade to a real
// field-mapped decoder if structured tool-name/argument separation is
// ever needed; see spec §10.
const minStringLen = 3

// maxRecursionDepth prevents stack overflow from deeply nested or malformed
// protobuf structures. Legitimate real-world protobuf nesting is shallow;
// deeply nested structures are either corrupted/version-drifted data or
// a DoS attack vector.
//
// ponytail: hard recursion cap, per-message limits if per-field nesting
// ever matters in real data.
const maxRecursionDepth = 100

// Protobuf wire types.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

// ExtractStrings recovers every meaningful UTF-8 string embedded in a
// protobuf-encoded blob, by walking the wire format generically (without
// a schema) and recursing into length-delimited fields that aren't
// themselves valid text, since those are likely nested sub-messages.
// "Meaningful text" means every rune is either unicode.IsPrint or one of
// the common whitespace controls (\n, \t, \r, \v, \f) -- real tool-use
// content (multi-line prompts, file contents, diffs) is overwhelmingly
// multi-line, and a field that fails this check is instead treated as a
// nested sub-message, so under-accepting here silently drops content
// rather than erroring. Malformed or truncated input causes the scan to
// stop and return whatever was recovered so far, never a panic. Deep
// nesting is stopped at a fixed depth limit to prevent stack overflow.
func ExtractStrings(data []byte) []string {
	return extractStringsDepth(data, 0)
}

func extractStringsDepth(data []byte, depth int) []string {
	var out []string

	// Stop recursing if we've exceeded the depth limit.
	if depth >= maxRecursionDepth {
		return out
	}

	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 {
			return out
		}
		data = data[n:]

		wireType := int(tag & 7)
		switch wireType {
		case wireVarint:
			_, n := binary.Uvarint(data)
			if n <= 0 {
				return out
			}
			data = data[n:]
		case wireFixed32:
			if len(data) < 4 {
				return out
			}
			data = data[4:]
		case wireFixed64:
			if len(data) < 8 {
				return out
			}
			data = data[8:]
		case wireBytes:
			length, n := binary.Uvarint(data)
			if n <= 0 || uint64(len(data)-n) < length {
				return out
			}
			v := data[n : n+int(length)]
			data = data[n+int(length):]
			if isMeaningfulText(v) {
				out = append(out, string(v))
			} else {
				out = append(out, extractStringsDepth(v, depth+1)...)
			}
		default:
			return out
		}
	}
	return out
}

// isMeaningfulText reports whether v looks like real embedded text rather
// than incidental binary noise. unicode.IsPrint alone rejects \n, \t, and
// \r, which would wrongly disqualify almost all real multi-line tool-use
// content (prompts, file contents, diffs) -- so common whitespace controls
// are accepted alongside printable runes.
func isMeaningfulText(v []byte) bool {
	if len(v) < minStringLen || !utf8.Valid(v) {
		return false
	}
	s := string(v)
	acceptable := 0
	for _, r := range s {
		if unicode.IsPrint(r) || isCommonWhitespaceControl(r) {
			acceptable++
		}
	}
	return acceptable == utf8.RuneCountInString(s)
}

// isCommonWhitespaceControl reports whether r is a whitespace control rune
// that routinely appears in real text but fails unicode.IsPrint: newline,
// tab, carriage return, vertical tab, and form feed.
func isCommonWhitespaceControl(r rune) bool {
	switch r {
	case '\n', '\t', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}
