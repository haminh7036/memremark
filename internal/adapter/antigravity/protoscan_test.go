package antigravity

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestExtractStringsRecoversTopLevelStringField(t *testing.T) {
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendString(buf, "Append a line 'foo' to sample.txt")

	got := ExtractStrings(buf)
	want := []string{"Append a line 'foo' to sample.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractStringsRecursesIntoNestedMessages(t *testing.T) {
	var inner []byte
	inner = protowire.AppendTag(inner, 1, protowire.BytesType)
	inner = protowire.AppendString(inner, "nested tool name")
	inner = protowire.AppendTag(inner, 2, protowire.VarintType)
	inner = protowire.AppendVarint(inner, 42)

	var outer []byte
	outer = protowire.AppendTag(outer, 3, protowire.BytesType)
	outer = protowire.AppendBytes(outer, inner)

	got := ExtractStrings(outer)
	want := []string{"nested tool name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractStringsIgnoresShortAndBinaryNoise(t *testing.T) {
	var buf []byte
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
	buf = protowire.AppendBytes(buf, []byte{0x00, 0x01, 0xff, 0xfe})
	buf = protowire.AppendTag(buf, 2, protowire.BytesType)
	buf = protowire.AppendString(buf, "ok") // shorter than minStringLen

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
	buf = protowire.AppendTag(buf, 1, protowire.BytesType)
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
	innermost = protowire.AppendTag(innermost, 1, protowire.BytesType)
	innermost = protowire.AppendString(innermost, canary)

	// Wrap the canary in wrappingDepth layers of empty BytesType fields.
	nested := innermost
	for i := 0; i < wrappingDepth; i++ {
		var wrapper []byte
		wrapper = protowire.AppendTag(wrapper, 1, protowire.BytesType)
		wrapper = protowire.AppendBytes(wrapper, nested)
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
