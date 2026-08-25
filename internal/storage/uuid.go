package storage

import (
	"crypto/sha1"
	"fmt"
)

// Standard URL namespace UUID (RFC 4122): 6ba7b811-9dad-11d1-80b4-00c04fd430c8
var urlNamespace = []byte{
	0x6b, 0xa7, 0xb8, 0x11,
	0x9d, 0xad,
	0x11, 0xd1,
	0x80, 0xb4,
	0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8,
}

// WingSummarySessionID returns a deterministic RFC 4122 UUIDv5 for a canonical wing path.
func WingSummarySessionID(wingPath string) string {
	cleanPath := normalizePath(wingPath)
	h := sha1.New()
	h.Write(urlNamespace)
	h.Write([]byte(cleanPath))
	sum := h.Sum(nil)

	// Set version 5 (bits 4-7 of byte 6 = 0101)
	sum[6] = (sum[6] & 0x0f) | 0x50
	// Set variant RFC 4122 (bits 6-7 of byte 8 = 10)
	sum[8] = (sum[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4],
		sum[4:6],
		sum[6:8],
		sum[8:10],
		sum[10:16],
	)
}
