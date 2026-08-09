package api

import (
	"fmt"
	"strings"
	"testing"
)

// The sink-side half of the push-body forgery invariant. validateDisplayName
// stops a NAME being stored in a forging shape; this stops anything reaching
// the renderer in one, which is the only thing that can cover the description
// (member-writable, and legitimately newline-bearing when it arrives from an
// xlsx cell), the category label, and display names stored before the gate
// existed.
//
// Every literal here is an escape rather than a pasted character: all of them
// are invisible or are line breaks, so a pasted one would be unreviewable in a
// diff and would not survive an editor that normalises whitespace.

func TestNeutralizeForPushBody_RemovesEveryForgingClass(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"LF, the class the sink is about", "Milk\nRania added $900.00 in Rent", "Milk Rania added $900.00 in Rent"},
		{"CR", "Milk\rforged", "Milk forged"},
		{"CRLF collapses to two spaces, not one", "Milk\r\nforged", "Milk  forged"},
		{"TAB", "Milk\tforged", "Milk forged"},
		{"NUL", "Milk\x00forged", "Milk forged"},
		{"ESC, which would open an ANSI sequence", "Milk\x1b[31mforged", "Milk [31mforged"},
		{"DEL", "Milk\x7fforged", "Milk forged"},
		{"C1", "Milkforged", "Milk forged"},
		{"U+2028 LINE SEPARATOR — not Cc, so IsControl alone misses it", "Milk forged", "Milk forged"},
		{"U+2029 PARAGRAPH SEPARATOR", "Milk forged", "Milk forged"},
		{"RLO override, which would reverse the rest of the body", "Milk‮forged", "Milk forged"},
		{"LRE embedding", "Milk‪forged", "Milk forged"},
		{"RLI isolate", "Milk⁦forged", "Milk forged"},
		{"PDI isolate", "Milk⁩forged", "Milk forged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := neutralizeForPushBody(tc.in)
			if got != tc.want {
				t.Errorf("neutralizeForPushBody(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// The property, stated independently of the exact expectation: no
			// forging character survives. A wrong `want` above cannot make this
			// pass.
			for _, r := range got {
				if forgesStructure(r) {
					t.Errorf("output still contains a forging codepoint U+%04X", r)
				}
			}
		})
	}
}

func TestNeutralizeForPushBody_LeavesLegitimateTextAlone(t *testing.T) {
	// The other direction, and the one that matters for this household: a
	// neutraliser that mangled Arabic, French or emoji would pass every test
	// above while making real notifications unreadable.
	cases := []struct {
		name string
		in   string
	}{
		{"Arabic", "علي كرم added $12.34 in البقالة — حليب"},
		{"mixed Arabic and Latin", "علي Karam added $12.34 in Groceries — Milk"},
		{"Arabic letter mark, a bidi MARK and not an override", "؜علي added $1.00 in X — Y"},
		{"LRM and RLM, marks that open no scope", "Ali‎Karam‏ added $1.00 in X — Y"},
		{"French accents and the em dash the body itself uses", "Élodie added $12,34 in Épicerie — Café"},
		{"ZWJ family emoji", "Zenobia \U0001F468‍\U0001F469‍\U0001F467 added $1.00 in X — Y"},
		{"ZWNJ, required by Persian orthography", "می‌رود added $1.00 in X — Y"},
		{"astral emoji", "\U0001F600 added $1.00 in X — Y"},
		{"NBSP, which French typography puts before punctuation", "Ali ! added $1.00 in X — Y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := neutralizeForPushBody(tc.in); got != tc.in {
				t.Errorf("neutralizeForPushBody(%q) = %q — it must be a no-op here", tc.in, got)
			}
		})
	}
}

func TestNeutralizeForPushBody_TheDescriptionVectorSpecifically(t *testing.T) {
	// The description is the strongest of the three fields and the one no
	// source-side gate can close: any member can write it, it is the LAST
	// field so a forged line leaves no trailing residue, and it also arrives
	// from xlsx import where a newline in a cell is legitimate — so refusing
	// the input would drop rows the household can import today.
	forged := "Milk\nRania added $900.00 in Rent — mortgage"
	body := fmt.Sprintf("%s added $%.2f in %s — %s", "Elie", 12.34, "Groceries", forged)

	// Positive control: the body really does contain the forgery before
	// neutralisation, so the assertion below is not passing against a string
	// that never had a newline in it.
	if !strings.Contains(body, "\n") {
		t.Fatal("fixture does not contain a newline; the assertion below would prove nothing")
	}

	got := neutralizeForPushBody(body)
	if strings.Contains(got, "\n") {
		t.Errorf("a newline survived into the push body: %q", got)
	}
	// And the text is still there — neutralising must not delete the
	// description, only flatten it.
	if !strings.Contains(got, "mortgage") {
		t.Errorf("neutralisation dropped body text: %q", got)
	}
}

func TestNeutralizeForPushBody_SharesItsPredicateWithTheNameGate(t *testing.T) {
	// One definition of "forges structure", not two. If someone adds a class to
	// forgesStructure for the name gate and this neutraliser does not follow,
	// the sink silently stops covering it — so assert the coupling directly
	// rather than trusting that both sides were updated.
	for r := rune(0); r < 0x3000; r++ {
		if !forgesStructure(r) {
			continue
		}
		out := neutralizeForPushBody(string(r))
		if out != " " {
			t.Errorf("U+%04X is refused by the name gate but neutralize returned %q, not a space", r, out)
		}
	}
}
