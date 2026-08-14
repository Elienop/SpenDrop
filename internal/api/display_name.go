package api

import (
	"fmt"
	"unicode"
)

// validateDisplayName is the ONE gate on the display_name column, shared by
// every path that writes it: handleRegister, handleCreateUser,
// handleUpdateUser and handleUpdateMe. It bounds the length and the CONTENT,
// in that order, and it is one function rather than four copies because the
// four copies of the length check are how the content check would have been
// missed on a fifth path.
//
// An EMPTY name returns nil, because "" means "not supplied" on three of the
// four callers — handleRegister and handleCreateUser fall back to the username,
// handleUpdateUser falls back to the stored name. handleUpdateMe is the one
// path where "" is illegal, and it says so itself with its own message before
// calling this. Making "" an error here would 400 an admin who is only changing
// somebody's role.
//
// WHY CONTENT IS BOUNDED AT ALL. The name is not just a label the owner sees:
// it is interpolated as the FIRST field of every activity push body —
// fmt.Sprintf("%s added $%.2f in %s — %s", actor, …) in notifications.go — and
// the service worker rolls several activities into one notification with
// lines.join('\n') (web/src/lib/sw-notifications.ts). A name carrying a newline
// therefore injects an extra visual line into ANOTHER household member's
// notification, and that line can be written to read as a genuine,
// separately-attributed activity. That is forgery of structure, not merely an
// odd-looking name, and it is why this gate exists.
//
// The other candidate sinks are closed by construction, not by this gate, and
// none of them motivates a wider rule: React escapes every render site, the
// spreadsheet export carries no name column at all, and display_name is never
// passed to a logger (sanitizeLogValue in helpers.go covers the values that
// are). The push body is the live sink.
//
// THE RULE IS PER CODEPOINT CLASS, and the split is between "can forge
// structure in something that renders this" (refused) and "merely unusual"
// (allowed). This household writes Arabic and French alongside English, so a
// class refused for being unfamiliar rather than dangerous locks a member out
// of their own name — a worse outcome than the hole. Each class below is
// decided on its own:
//
// REFUSED
//
//   - Cc, the control characters (U+0000–U+001F and U+007F–U+009F), via
//     unicode.IsControl. This is the class the sink is about: LF and CR forge a
//     line in the rolled-up push body, ESC opens an ANSI sequence in anything
//     that renders to a terminal, NUL truncates in any C-string consumer, and
//     TAB forges column structure. The C1 half (U+0080–U+009F) is included
//     because no letter is encoded at those codepoints in Unicode — text typed
//     in any Latin-1 script arrives as the real letters, so refusing C1 cannot
//     cost a legitimate name.
//
//   - U+2028 LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR. These are the gap
//     in the obvious spelling: their category is Zl/Zp, NOT Cc, so
//     unicode.IsControl returns FALSE for both and a guard written as
//     IsControl alone still admits a line break. Unicode defines them as
//     unconditional breaks (UAX #14 class BK), so any conforming renderer
//     breaks the line there — exactly the forgery LF performs.
//
//   - U+202A–U+202E, the bidi embeddings and overrides (LRE, RLE, PDF, LRO,
//     RLO), and U+2066–U+2069, the bidi isolates (LRI, RLI, FSI, PDI). Each of
//     these OPENS A SCOPE that runs to its terminator or to the end of the
//     paragraph, and the name is not the end of the paragraph — it is followed
//     by " added $12.34 in Groceries — Milk". An unterminated RLO in a name
//     therefore reverses the display order of the whole rest of the body, so
//     the amount, the category and the description can be made to read as
//     something they are not.
//
// ALLOWED, deliberately, and each one would look plausible to refuse:
//
//   - The bidi MARKS: U+200E LRM, U+200F RLM and U+061C ARABIC LETTER MARK.
//     They are Cf like the overrides above and they are commonly lumped in with
//     them, but they open no scope: each carries only its own strong direction
//     and can at most reorder a run of neutral characters directly beside it,
//     never commandeer the text that follows. They are also what a bidi-aware
//     input method or a copy-paste out of mixed Arabic/Latin text legitimately
//     emits. Refusing them would refuse ordinary Arabic names for a reordering
//     they cannot perform.
//
//   - ORDINARY RTL LETTERS are not touched by any clause here, and that is the
//     point of enumerating the bidi CONTROLS individually rather than reaching
//     for unicode.Bidi_Control or a "no right-to-left text" rule. Arabic
//     letters have strong RTL directionality by nature; they reorder nothing
//     beyond themselves.
//
//   - U+200D ZERO WIDTH JOINER and U+200C ZERO WIDTH NON-JOINER. ZWJ is what
//     fuses a family or profession emoji into one glyph — MAN + ZWJ + WOMAN +
//     ZWJ + GIRL is a single family emoji, WOMAN + ZWJ + LAPTOP is a single
//     woman-technologist — and this package's own boundary tests treat emoji
//     names as supported. ZWNJ is required by Persian orthography and by
//     several Indic scripts. A guard that swept U+200B–U+200D as "zero width"
//     would break real names and real emoji while looking careful in review.
//     (Spelled out rather than pasted: a ZWJ sequence in a comment reduces to
//     three separate glyphs the moment anything normalises it, and the example
//     would then argue the opposite of what it says.)
//
//   - U+200B ZERO WIDTH SPACE and U+FEFF BOM, which arrive from a bad paste.
//     They are invisible padding: they cannot open a line, reorder anything, or
//     start an escape sequence, so they are unusual rather than dangerous. They
//     are also not silently stripped — quietly rewriting somebody's name is its
//     own surprise.
//
//   - U+00A0 NO-BREAK SPACE and the other Zs spaces. French typography puts one
//     before ! ? : and ;, and a no-break space renders as a space everywhere.
//     (Leading and trailing whitespace never reaches here: every caller runs
//     strings.TrimSpace first, and TrimSpace already removes NBSP.)
//
//   - Angle brackets, quotes and the rest of the ASCII punctuation a naive
//     "no <script>" rule would reach for. There is no HTML sink — see above —
//     and refusing punctuation buys nothing while making "O'Brien" a judgement
//     call.
//
// LENGTH IS CHECKED FIRST so that an over-long name keeps the message it has
// always had, whatever else it contains.
func validateDisplayName(s string) error {
	// CHARACTERS via charLen, and the identical wording the four call sites
	// used before they shared this function — Settings.test.tsx asserts on this
	// exact string, and internal/api's own boundary tests read it back.
	if charLen(s) > MaxDisplayNameLength {
		return fmt.Errorf("display name must be %d characters or less", MaxDisplayNameLength)
	}
	for _, r := range s {
		if forgesStructure(r) {
			// The offending codepoint is named because every character this
			// refuses is INVISIBLE — without it the user is told to remove
			// something they cannot see. Echoing it is safe by construction: it
			// is formatted as a hexadecimal number, so the value cannot carry
			// itself back out through the message.
			return fmt.Errorf(
				"display name may not contain line breaks, control characters, or text-direction overrides (found U+%04X)", r)
		}
	}
	return nil
}

// forgesStructure reports whether r can fabricate structure in a context that
// renders a display name. See validateDisplayName for the reasoning behind each
// class, and for the classes that are deliberately absent.
func forgesStructure(r rune) bool {
	// Every literal below is spelled as an ESCAPE, never as the character
	// itself. All of them are invisible or are line breaks, so a pasted literal
	// would be unreviewable in a diff and would not survive an editor that
	// normalises whitespace.
	switch {
	case unicode.IsControl(r):
		// Cc only — U+0000–U+001F and U+007F–U+009F. Go's IsControl is
		// exactly that category, which is why the three clauses below are
		// needed as well.
		return true
	case r == '\u2028' || r == '\u2029':
		// LINE SEPARATOR, PARAGRAPH SEPARATOR. Category Zl/Zp, NOT Cc.
		return true
	case r >= '\u202A' && r <= '\u202E':
		// LRE, RLE, PDF, LRO, RLO — the bidi embeddings and overrides.
		return true
	case r >= '\u2066' && r <= '\u2069':
		// LRI, RLI, FSI, PDI — the bidi isolates.
		return true
	}
	return false
}
