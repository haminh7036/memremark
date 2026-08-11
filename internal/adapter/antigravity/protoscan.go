package antigravity

import (
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
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

// ExtractStrings recovers every printable UTF-8 string embedded in a
// protobuf-encoded blob, by walking the wire format generically (without
// a schema) and recursing into length-delimited fields that aren't
// themselves valid text, since those are likely nested sub-messages.
// Malformed or truncated input causes the scan to stop and return
// whatever was recovered so far, never a panic. Deep nesting is stopped
// at a fixed depth limit to prevent stack overflow.
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
		_, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return out
		}
		data = data[n:]

		switch typ {
		case protowire.VarintType:
			_, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return out
			}
			data = data[n:]
		case protowire.Fixed32Type:
			_, n := protowire.ConsumeFixed32(data)
			if n < 0 {
				return out
			}
			data = data[n:]
		case protowire.Fixed64Type:
			_, n := protowire.ConsumeFixed64(data)
			if n < 0 {
				return out
			}
			data = data[n:]
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return out
			}
			data = data[n:]
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

func isMeaningfulText(v []byte) bool {
	if len(v) < minStringLen || !utf8.Valid(v) {
		return false
	}
	s := string(v)
	printable := 0
	for _, r := range s {
		if unicode.IsPrint(r) {
			printable++
		}
	}
	return printable == utf8.RuneCountInString(s)
}
