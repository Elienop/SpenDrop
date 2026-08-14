package database

import (
	"math"
	"testing"
)

// Unit coverage for the two helpers behind the sign-insensitive freeze. Both
// have arms the HTTP-level tests cannot reach: absCents at int64 minimum (every
// money column is bounded far below it at the api edge) and withSignOf's
// zero-reference arm (migration 019 constrains original_amount_cents to != 0).
// Unreachable is not untestable, and a totality guard nothing exercises is a
// guard nobody notices deleting.

func TestAbsCents(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int64
		want int64
	}{
		{"a refund's magnitude", -150_000_000, 150_000_000},
		{"a purchase is already its own magnitude", 150_000_000, 150_000_000},
		{"one cent either way", -1, 1},
		{"zero", 0, 0},
		// Negating int64 minimum returns int64 minimum. Without the clamp the
		// predicate would call a minimum-valued original "the same magnitude"
		// as any other negative one it happened to be compared against.
		{"int64 minimum clamps instead of wrapping", math.MinInt64, math.MaxInt64},
		{"one above the minimum negates normally", math.MinInt64 + 1, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := absCents(tc.in); got != tc.want {
				t.Errorf("absCents(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}

	if absCents(math.MinInt64) < 0 {
		t.Errorf("absCents(math.MinInt64) = %d, which is negative — a magnitude never is",
			absCents(math.MinInt64))
	}
}

func TestWithSignOf(t *testing.T) {
	for _, tc := range []struct {
		name       string
		value, ref int64
		want       int64
	}{
		// The two cases the freeze actually runs: a booked purchase being
		// flipped into a refund, and the reverse.
		{"purchase flipped to refund", 1685, -150_000_000, -1685},
		{"refund flipped to purchase", -1685, 150_000_000, 1685},
		// ...and the two where nothing needs doing, which is the common path:
		// every edit that is not a flip.
		{"already agreeing, positive", 1685, 150_000_000, 1685},
		{"already agreeing, negative", -1685, -150_000_000, -1685},
		// A zero reference carries no direction, so the value keeps its own
		// rather than being forced positive. Unreachable through the store (the
		// predicate requires Valid on both sides and the column forbids 0), but
		// forcing a booked refund positive is the wrong way to fail.
		{"zero reference leaves a refund alone", -1685, 0, -1685},
		{"zero reference leaves a purchase alone", 1685, 0, 1685},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := withSignOf(tc.value, tc.ref); got != tc.want {
				t.Errorf("withSignOf(%d, %d) = %d, want %d", tc.value, tc.ref, got, tc.want)
			}
		})
	}

	// The property the store depends on, stated directly: whatever comes back
	// agrees with the reference, and its magnitude is untouched.
	for _, value := range []int64{1685, -1685} {
		for _, ref := range []int64{150_000_000, -150_000_000} {
			got := withSignOf(value, ref)
			if (got < 0) != (ref < 0) {
				t.Errorf("withSignOf(%d, %d) = %d — the result must agree with the reference",
					value, ref, got)
			}
			if absCents(got) != absCents(value) {
				t.Errorf("withSignOf(%d, %d) = %d — the magnitude must survive", value, ref, got)
			}
		}
	}
}
