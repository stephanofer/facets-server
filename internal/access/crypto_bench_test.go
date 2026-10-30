package access

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func BenchmarkPasswordHash(b *testing.B) {
	c, err := newCryptography([]byte(strings.Repeat("b", 32)), DefaultPolicy(nil).Password, map[[32]byte]struct{}{sha256.Sum256([]byte("blocked")): {}})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.hash(b.Context(), "A synthetic calibration phrase only"); err != nil {
			b.Fatal(err)
		}
	}
}
