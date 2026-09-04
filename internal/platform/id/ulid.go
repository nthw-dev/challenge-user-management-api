// Package id generates ULIDs with no external dependency.
//
// ULID over UUIDv4 because the first 48 bits are a timestamp,
// which makes the values sort by time on their own when reading through logs, while the remaining 80 bits keep them unguessable.
package id

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// Crockford base32 — I, L, O and U are dropped so they cannot be misread as 1 and 0.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// The first 6 bytes are the timestamp; the remaining 10 are random.
const timestampBytes = 6

// NewULID returns 26 characters: the first 10 are the time in milliseconds, the rest is random.
func NewULID() string {
	var b [16]byte

	// 48 bits of milliseconds, most significant byte first.
	ms := time.Now().UTC().UnixMilli()
	for i := range timestampBytes {
		b[i] = byte(ms >> (40 - 8*i)) //nolint:gosec // the shift selects one byte of a 48-bit value on purpose; nothing is lost
	}

	// crypto/rand does not return an error in practice on any supported platform,
	// but if it ever did, we must not hand out a guessable value — so we accept a panic.
	if _, err := rand.Read(b[timestampBytes:]); err != nil {
		panic("id: failed to read random bytes: " + err.Error())
	}

	return encode(b)
}

// encode turns 128 bits into 26 characters (26 × 5 = 130 bits, so there are 2 leading zero bits).
func encode(b [16]byte) string {
	hi := binary.BigEndian.Uint64(b[0:8])
	lo := binary.BigEndian.Uint64(b[8:16])

	out := make([]byte, 26)
	shift := 125 // the first character takes only the top 3 bits; the rest take 5 each
	for i := range out {
		out[i] = alphabet[shiftRight(hi, lo, shift)&0x1F]
		shift -= 5
	}
	return string(out)
}

// shiftRight returns the low 64 bits of a 128-bit value after shifting right n times (0 <= n < 128).
func shiftRight(hi, lo uint64, n int) uint64 {
	switch {
	case n <= 0:
		return lo
	case n < 64:
		return lo>>n | hi<<(64-n)
	default:
		return hi >> (n - 64)
	}
}
