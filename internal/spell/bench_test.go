//go:build !nospell

package spell

import "testing"

func BenchmarkNewFR(b *testing.B) {
	for range b.N {
		if _, err := New("fr", ""); err != nil {
			b.Skip(err)
		}
	}
}
