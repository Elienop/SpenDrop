package push

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestPerAddrDialTimeout_SharesTheBudget is the regression test for the dial
// walk running out of time before it reaches a working address.
//
// Each dial used to get a fixed 10s. The sender's request budget is 30s, so
// three slow failures — a dropped SYN, the normal signature of a broken IPv6
// route — consumed all of it, the loop's own ctx.Err() check fired, and every
// remaining address was skipped. Apple publishes 16 addresses, so "try every
// validated address" reached at most three of them in the one scenario the walk
// was written for.
func TestPerAddrDialTimeout_SharesTheBudget(t *testing.T) {
	cases := []struct {
		name      string
		budget    time.Duration
		remaining int
		want      time.Duration
	}{
		// The reported failure: 30s budget, 16 addresses. A fixed 10s reached 3.
		{"apple-sized list inside the sender budget", 30 * time.Second, 16, minPerAddrDialTimeout},
		{"two addresses split a generous budget", 30 * time.Second, 2, maxPerAddrDialTimeout},
		{"four addresses split 20s evenly", 20 * time.Second, 4, 5 * time.Second},
		// Never so small that a healthy handshake cannot finish.
		{"nearly exhausted budget still gets the floor", 100 * time.Millisecond, 8, minPerAddrDialTimeout},
		// Never so large that one address owns a long budget.
		{"huge budget is capped", 10 * time.Minute, 1, maxPerAddrDialTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tc.budget)
			defer cancel()
			got := perAddrDialTimeout(ctx, tc.remaining)
			// time.Until loses a sliver between the two calls; compare with slack.
			if d := got - tc.want; d > 50*time.Millisecond || d < -50*time.Millisecond {
				t.Errorf("got %s, want ~%s", got, tc.want)
			}
		})
	}

	// The property that actually matters: HOW MANY addresses get a chance inside
	// the sender's budget. Asserting on the summed timeout would be vacuous — the
	// old fixed 10s summed to 160s across 16 addresses, comfortably over budget,
	// while reaching only three of them. So simulate the walk, spending each
	// timeout in turn, and count the attempts that fit.
	//
	// dialTimeoutFor is used rather than perAddrDialTimeout so the budget can be
	// drawn down deterministically instead of in real time.
	t.Run("the walk reaches most of a 16-address list", func(t *testing.T) {
		const (
			budget = 30 * time.Second // defaultHTTPTimeout in sender.go
			addrs  = 16               // measured: apple publishes this many
		)
		remaining := budget
		attempts := 0
		for i := 0; i < addrs && remaining > 0; i++ {
			remaining -= dialTimeoutFor(remaining, addrs-i)
			attempts++
		}
		// With the old fixed 10s this was 3.
		if attempts < 12 {
			t.Errorf("only %d of %d addresses can be tried within the %s budget; "+
				"a slow-failing address family still starves the working one", attempts, addrs, budget)
		}
	})
}

func TestPerAddrDialTimeout_NoDeadlineUsesTheCeiling(t *testing.T) {
	if got := perAddrDialTimeout(context.Background(), 8); got != maxPerAddrDialTimeout {
		t.Errorf("got %s, want the ceiling %s when ctx carries no deadline", got, maxPerAddrDialTimeout)
	}
}

// TestInterleaveByFamily is the regression test for one address family
// monopolising the walk.
//
// Taking the resolver's order verbatim meant a dual-stack host whose IPv6 route
// blackholes spent the budget on consecutive AAAA records and never reached an A
// record. Interleaving guarantees the second attempt is the other family.
func TestInterleaveByFamily(t *testing.T) {
	mk := func(ss ...string) []net.IPAddr {
		out := make([]net.IPAddr, 0, len(ss))
		for _, s := range ss {
			ip := net.ParseIP(s)
			if ip == nil {
				t.Fatalf("bad fixture %q", s)
			}
			out = append(out, net.IPAddr{IP: ip})
		}
		return out
	}
	names := func(as []net.IPAddr) []string {
		out := make([]string, 0, len(as))
		for _, a := range as {
			out = append(out, a.IP.String())
		}
		return out
	}
	eq := func(t *testing.T, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}

	t.Run("AAAA-first order is preserved but alternated", func(t *testing.T) {
		in := mk("2001:db8::1", "2001:db8::2", "2001:db8::3", "1.1.1.1", "2.2.2.2")
		eq(t, names(interleaveByFamily(in)),
			[]string{"2001:db8::1", "1.1.1.1", "2001:db8::2", "2.2.2.2", "2001:db8::3"})
	})

	t.Run("A-first order keeps IPv4 first", func(t *testing.T) {
		in := mk("1.1.1.1", "2.2.2.2", "2001:db8::1")
		eq(t, names(interleaveByFamily(in)),
			[]string{"1.1.1.1", "2001:db8::1", "2.2.2.2"})
	})

	t.Run("single family is returned untouched", func(t *testing.T) {
		in := mk("1.1.1.1", "2.2.2.2", "3.3.3.3")
		eq(t, names(interleaveByFamily(in)), []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"})
	})

	t.Run("every address survives the reordering", func(t *testing.T) {
		in := mk("2001:db8::1", "1.1.1.1", "2001:db8::2", "2.2.2.2", "3.3.3.3", "2001:db8::3")
		got := interleaveByFamily(in)
		if len(got) != len(in) {
			t.Fatalf("got %d addresses, want %d — the walk would skip some entirely", len(got), len(in))
		}
		seen := map[string]int{}
		for _, a := range got {
			seen[a.IP.String()]++
		}
		for _, a := range in {
			if seen[a.IP.String()] != 1 {
				t.Errorf("%s appears %d times after interleaving, want exactly 1", a.IP, seen[a.IP.String()])
			}
		}
		// And no two consecutive entries share a family while both remain.
		for i := 1; i < 4; i++ {
			if (got[i].IP.To4() != nil) == (got[i-1].IP.To4() != nil) {
				t.Errorf("entries %d and %d are the same family: %v", i-1, i, names(got))
			}
		}
	})
}
