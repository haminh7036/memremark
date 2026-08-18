package antigravity

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func appendTag(b []byte, num int, wireType int) []byte {
	return binary.AppendUvarint(b, uint64(num<<3|wireType))
}

func appendVarint(b []byte, v uint64) []byte {
	return binary.AppendUvarint(b, v)
}

func appendBytes(b []byte, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

func appendString(b []byte, s string) []byte {
	return appendBytes(b, []byte(s))
}

func TestExtractStringsRecoversTopLevelStringField(t *testing.T) {
	var buf []byte
	buf = appendTag(buf, 1, wireBytes)
	buf = appendString(buf, "Append a line 'foo' to sample.txt")

	got := ExtractStrings(buf)
	want := []string{"Append a line 'foo' to sample.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestExtractStringsPreservesMultiLineText proves the fix for the critical
// data-loss bug: unicode.IsPrint('\n')/('\t') are false, so before the fix
// isMeaningfulText rejected any real text containing a newline or tab --
// which is virtually all real tool-use content (multi-line prompts, file
// contents, diffs) -- and extractStringsDepth then silently tried (and
// failed) to parse the bytes as a nested sub-message, dropping the text
// with no error. This must fail against the unfixed isMeaningfulText and
// pass once common whitespace controls are accepted as meaningful text.
func TestExtractStringsPreservesMultiLineText(t *testing.T) {
	text := "line one\nline two\twith a tab\nline three"

	var buf []byte
	buf = appendTag(buf, 1, wireBytes)
	buf = appendString(buf, text)

	got := ExtractStrings(buf)
	want := []string{text}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v (multi-line text must survive intact, not be dropped or truncated)", got, want)
	}
}

func TestExtractStringsRecursesIntoNestedMessages(t *testing.T) {
	var inner []byte
	inner = appendTag(inner, 1, wireBytes)
	inner = appendString(inner, "nested tool name")
	inner = appendTag(inner, 2, wireVarint)
	inner = appendVarint(inner, 42)

	var outer []byte
	outer = appendTag(outer, 3, wireBytes)
	outer = appendBytes(outer, inner)

	got := ExtractStrings(outer)
	want := []string{"nested tool name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractStringsIgnoresShortAndBinaryNoise(t *testing.T) {
	var buf []byte
	buf = appendTag(buf, 1, wireBytes)
	buf = appendBytes(buf, []byte{0x00, 0x01, 0xff, 0xfe})
	buf = appendTag(buf, 2, wireBytes)
	buf = appendString(buf, "ok") // shorter than minStringLen

	got := ExtractStrings(buf)
	if len(got) != 0 {
		t.Fatalf("expected no strings extracted from noise, got %v", got)
	}
}

func TestExtractStringsOnEmptyInputReturnsNil(t *testing.T) {
	got := ExtractStrings(nil)
	if len(got) != 0 {
		t.Fatalf("expected no strings for empty input, got %v", got)
	}
}

func TestExtractStringsOnTruncatedInputStopsGracefully(t *testing.T) {
	var buf []byte
	buf = appendTag(buf, 1, wireBytes)
	buf = append(buf, 0x05) // claims a 5-byte string but provides none -- truncated
	got := ExtractStrings(buf) // must not panic
	if len(got) != 0 {
		t.Fatalf("expected no strings from truncated input, got %v", got)
	}
}

func TestExtractStringsDeeplyNestedDoesNotStackOverflow(t *testing.T) {
	// Build a genuinely nested chain with a canary string at depth 150+.
	// With maxRecursionDepth=100, the canary should NOT be extracted (cap prevents recursion).
	// If the cap is removed/broken, the canary WILL be extracted — this test will fail.
	const canary = "CANARY_HIDDEN_AT_DEPTH_150"
	const wrappingDepth = 150 // exceed maxRecursionDepth (100) to prove the cap works

	// Innermost: a BytesType field containing the canary string.
	var innermost []byte
	innermost = appendTag(innermost, 1, wireBytes)
	innermost = appendString(innermost, canary)

	// Wrap the canary in wrappingDepth layers of empty BytesType fields.
	nested := innermost
	for i := 0; i < wrappingDepth; i++ {
		var wrapper []byte
		wrapper = appendTag(wrapper, 1, wireBytes)
		wrapper = appendBytes(wrapper, nested)
		nested = wrapper
	}

	got := ExtractStrings(nested)
	// The canary is at depth 150. With maxRecursionDepth=100, we stop before reaching it.
	// So the canary should NOT be in the output.
	for _, s := range got {
		if s == canary {
			t.Fatalf("depth cap broken: canary at depth %d should not be extracted with maxRecursionDepth=%d, but got %v",
				wrappingDepth, maxRecursionDepth, got)
		}
	}
}
