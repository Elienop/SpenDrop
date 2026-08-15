# SpenDrop — Backlog

Running list of what needs attention. **Update this file as things are fixed or found** — the
point is that nobody has to re-audit the whole project to know where things stand.

**Maintaining it:** when an item ships, move it to *Closed* with the commit hash rather than
deleting it — a closed item with its evidence is what stops the same thing being "discovered"
again in three months. When something new is found, add it with the same fields, and be honest
in the **Verified** field: an item nobody has actually reproduced is a lead, not a finding.

**Verified** means one of:
- `reproduced` — someone ran it and watched it happen. Highest confidence.
- `read` — the code was read and the mechanism confirmed, but not executed.
- `reported` — an agent or review found it and it has not been independently checked.

Last full audit: **2026-08-02**, against `v0.36.0`. 16 agents, plus direct verification of the
top items. Production figures in this file come from the owner's live database on that date:
1,839 live transactions, 88 of them in LBP, 189 recorded edits.

---

## Next up

*Numbers are discovery order, not priority. B11, B2 and B1 all shipped 2026-08-02 on
`fix/reprice-guard-and-restore` — see Closed. B5 also shipped 2026-08-02, on
`feat/delete-undo-member-trash` — see Closed. B3 shipped 2026-08-07, on
`fix/migration-snapshot-crash-loop-prune` — see Closed. B4 shipped 2026-08-07, on `fix/b4-replace-all-honors-filters` — see Closed.*

## Then

*B6 (the cheap batch) shipped 2026-08-07 on `fix/b6-cheap-batch` — see Closed. B7 shipped
2026-08-08 on `fix/b7-healthcheck-data`, merged via PR #124 and released as v0.39.0 — see
Closed. B8 shipped 2026-08-08 on `fix/b8-backup-verified-marker`, merged via PR #125 and
released as v0.39.1 — see Closed. The B9 batch shipped across #126, #128, #130 and #131
(v0.40.0–v0.42.1) and the 2026-08-14 phone-polish batch as #137 (v0.43.0) — B24, B29, B31,
B32, B35, B37 and B38 moved to Closed with them; B22 closed separately on the branch that
recorded all of this.*

### B12 — Bulk counts promise a member more than their write can touch
**Verified: read** (found 2026-08-07 while designing the B4 fix; owner asked for the line).
Every bulk surface shows the household-wide number while a member's write is scoped to rows
they own: the selection bar, both confirm dialogs, and the Replace All button all display
`total` from the list query (household-wide by design), but `update-by-filter` and
`delete-by-filter` append `t.user_id = ?` for non-admins, and page-mode batch endpoints skip
foreign rows. A member confirming "Apply changes to 10" may update 7; only the after-the-fact
toast reports the real number. Accepted knowingly in the bulk-edit spec and again in the B4
design — every path is consistent and honest after the fact, so this is a polish item, not a
correctness bug. An honest fix needs a per-actor count (scoped count in the list response, or
a count endpoint) applied to all bulk surfaces at once, not bolted onto one.
**Effort:** small-medium — one scoped count, several consumers.
**Owner ranking (2026-08-07): below B6.**
**2026-08-07, during the B6 review battery:** independently rediscovered by the security audit
(its M1) with a measured repro — member list `total=2`, write `updated=1`. Confirms the
direction is fail-safe (the write is always a subset of what was displayed, never a superset).
Still polish, still below B6.

### B14 — Budget mutations write no audit trail
**Verified: read** (B6 security audit M2, 2026-08-07). `handleSetBudget`, `handleDeleteBudget`,
and the category-budget set/delete all mutate budget state with no audit row; `transaction_audit`
has never covered budgets, and no FK references the budgets table. Not a regression — the B6a
DELETE mirrored the existing handlers. Low priority in a two-person household (every budget verb
is admin-only), but it is a second way to destroy state with no forensic trail. If done, cover
all four verbs in one pass, not one.
**Effort:** small-medium (a new audit surface, not a one-liner).

### B16 — Savings.test.tsx empty-state test passes without any API response
**Verified: read** (found 2026-08-07 by the debugger during the Dependabot-#120 flake bisect,
while sweeping all 32 test files for gate-then-assert races). `Savings.tsx` branches its empty
state on `goals.length === 0` with no `loading` flag, so "No savings goals yet" renders on the
FIRST paint; all three assertions in `Savings.test.tsx:80` pass before the mocked fetch
resolves — the test would pass even if the API never responded. Not a flake risk (the shape it
asserts is the pre-fetch shape), but vacuous as a guard of the loaded state. Fix is the same
gate-on-data discipline the B6 member-Budgets tests got in `dd49a5c`, plus deciding whether
the page itself should distinguish loading from empty (a UX question — currently a user with
goals sees the empty state flash).
**Effort:** small.

### B17 — An unauthenticated flood on /healthz/data starves real user writes
**Verified: reproduced** (measured 2026-08-08 by the B7 piece 2 security audit, on a built
branch binary against a seeded 10k-row DB). All traffic serializes on the single-connection
pool: under 16 concurrent unauthenticated `GET /healthz/data` clients (310 req/s sustained),
authenticated `POST /api/transactions` latency went from 0.6–0.9 ms to median 74.8 ms (~100x).
**Pre-existing, not introduced by the write probe** — timed individually, `quick_check` is
~90% of the endpoint's DB cost (4.49 ms), the unindexed `updated_at` scan ~0.45 ms, the probe
upsert 0.035 ms (~0.7%). Accepted under the household-trust doctrine (`router.go`: LAN
deployments "trusted by construction", no rate limit); the audit also noted a doctrine
tension — the README recommends scraping this endpoint from an EXTERNAL monitor, and an
operator following that past the LAN boundary exposes an unauthenticated write trigger. If
the doctrine is ever revisited, the lever is a short-TTL cache or rate limit on the whole
endpoint aimed at the pragma, not at the probe. Related WAL fact recorded at the router note:
the -wal recycles at ~4 MiB except while a reader pins a snapshot (VACUUM INTO), where it
grows ~4 KiB/request to a durable high-water mark.
**Effort:** small (if ever wanted) — but it is a doctrine decision first, not a code task.

### B15 — Nine test files use an Enter idiom that may prove nothing
**Verified: read** (found 2026-08-07 during the B6 debounce work). Under happy-dom,
`user.type(input, '{Enter}')` / `user.keyboard('{Enter}')` dispatch nothing React's
`onKeyDown` sees — a committed guard test on the B6 branch passed with its guard DELETED
until it was rebuilt on `fireEvent.keyDown` plus a positive control ("Enter opens the flow
when the gate is open" — only the positive can catch the key going dead again). Nine other
files use the same idiom and are unaudited: ImportPreviewTable, AmountCurrencyInput,
TagInput, TransactionRow, password-input, AutocompleteInput, TransactionEntryRow,
Transactions.BulkEditDialog, Transactions. Some uses may be legitimately handled by
user-event (form-submit paths); each needs the delete-the-handler check before being
trusted. Any that assert a NEGATIVE ("Enter does nothing") are the prime suspects.
**Effort:** small-medium — nine audits, each a delete-run-restore cycle.

### B18 — Account editing: password flow and the two-surfaces question (owner request)
**Verified: read** (owner request 2026-08-08, from the live v0.39.0 Settings → Users tab;
endpoint and UI gap confirmed in code the same day). *State AS FOUND, kept for the reasoning —
ask (1) has since shipped, see below.* No UI edited a user's details anywhere.
The motivating case: B6's attribution now shows the creator's display name on every
transaction row, and the owner wants a shorter one ("maybe I want to use something shorter
so it looks short in the transaction"). The asks, in the owner's framing: (1) edit display
name; (2) password change belongs in the same edit flow, not somewhere else; (3) consider
ONE common accounts page instead of the current two surfaces (Account tab for self, Users
tab for admin-managing-others). The backend half of (1) already exists: `PUT /api/users/{id}`
(admin-only, `handleUpdateUser`, `internal/api/user_handlers.go:142`) updates display_name
and role with merge semantics — an empty/missing field keeps the existing value (verified:
the role dropdown already PUTs `{ role }` alone, `Settings.tsx` `handleRoleChange`, and
display_name survives), so a display-name editor can send its field alone with no round-trip
concern. One consequence of the merge semantics: the endpoint cannot CLEAR a display name
(empty string means keep) — fine for this feature, worth knowing. Password paths today:
self-service `POST /api/auth/password` (Account tab) and admin
`POST /api/users/{id}/reset-password` (member rows only in the UI).
**Effort:** small for (1) as admin-only UI; the merged accounts page is its own design stage.

**Ask (1) SHIPPED 2026-08-08** — built on `feat/b9-mobile-shell-slice1`, merged via PR #126,
squash `661ad46`, released as **v0.40.0**. What landed: an admin display-name editor (exact
single-field PUT), a confirmation dialog on member Delete whose copy is test-pinned to NOT
claim a ledger cascade (the 409 pre-count guard makes that outcome impossible — a schema
`ON DELETE CASCADE` is not by itself evidence of the behaviour a handler produces), and a
self-rename refresh path (`useAuth.refreshUser`, epoch-guarded on every arm including the
failure arms, which were the reachable hole).

**Still open: asks (2) and (3)** — password change belonging in the same edit flow, and the
one-common-accounts-page question. Ask (3) is a real design decision (the admin-vs-member
capability split on a single surface) and needs a brainstorm with the owner before any build.
Also still open from the original framing: whether a MEMBER may edit their own display name.

### B23 — offline-capture hold filing on identity change is untested
**Verified: read** (found 2026-08-08 while guarding the stale-verify race). `markNeedsSignIn`
files the queued-capture hold when the session identity changes. The race tests assert it is
NOT called with the new user's id under a stale failure, but nothing pins the positive
semantics — "the hold is filed against the DEPARTED id, if at all." A queue-semantics
question, distinct from the auth race that exposed it.
**Effort:** small.

### B25 — the Reports page has one heading, and its Card labels are no-ops
**Verified: read** (B9 slice-3 accessibility recon, 2026-08-09; PRE-EXISTING and page-wide).
Three separate defects in one surface, none of them mobile-specific:
1. **`CardTitle` renders a `<div>`**, so the entire Reports page has exactly one heading
   (`<h1>Reports</h1>`). "Spending Heatmap", "Recurring Expenses" and "Tag Analysis" cannot be
   reached by heading navigation.
2. **`<Card aria-labelledby="…">` is silently discarded.** `Card` renders a bare `<div>`, whose
   implicit `generic` role is on WAI-ARIA's name-prohibited list — so the association does
   nothing while reading as done. Three call sites in `PatternsTab.tsx` alone.
3. **Loading and refetch states are silent to a screen reader.** `Skeleton` is a plain `<div>`
   with no `role`, no `aria-busy` and no live region; the refetch cue is `opacity-60`, purely
   visual. (The error path is fine — `alert.tsx` has `role="alert"`.)
Fixing (1) touches every card on all four tabs, which is why it was NOT folded into slice 3.
**Effort:** medium for (1); small for (2) and (3).

### B39 — the 20-slot category palette fails its own pair-separation check, in both themes

**Verified: measured** (2026-08-14, `web/scripts/validate_palette.js`). Adjacent-slot separation
is under the validator's normal-vision floor of ΔE 15 in BOTH themes — light worst pair **8.1**,
dark worst **5.7** with a CVD worst of **0.9** — so neighbouring categories in a chart legend can
be genuinely hard to tell apart, and were BEFORE the light retune (the dark block is the shipped
palette, untouched; the light block's ≥5.0:1 text-contrast floor is a separate, passing check
pinned by `src/lib/chart-colors.test.ts`). At 20 slots adjacent hues sit ~18° apart, so the floor
is unreachable by tuning alone — the fix is structural: reorder slots so confusable hues are not
adjacent, cut the palette to ~12 with a fold-into-"Other", or lean on the direct labels the
charts already carry (`ChartLegendContent`, category-named axes), which is the mitigation the
validator itself names. **Effort:** medium; a design decision, not a bug fix — do not retune
individual slots piecemeal, the 2026-08 light retune proved best-achievable pair gains are <1 ΔE.

### B40 — the Categories editor sheet closes on Escape mid-save

**Verified: read** (found during the #137 fix batch, 2026-08-14; scoped out of it deliberately).
The sheet's Escape/overlay close is not gated on `saving`, so closing abandons an in-flight save:
the request itself continues and may commit, but the surface that reports its outcome is gone and
the form state is lost. The scoping-out was itself a decision, not an oversight — hard-blocking
close on `saving` would trap the user behind a hung request. The clean fix is an AbortController
(closing aborts the request) plus a test for both halves: close aborts, and a save that completed
before the close still toasts.
**Effort:** small.

### B48 — 11 test files strip a hook's contract with `as unknown as ReturnType<…>`
**Verified: read** (2026-08-15, found during the B30 fix wave). The double cast opts a mock out
of the mocked hook's type contract, so a required field added to the hook later feeds
`undefined` into those tests silently — and any "adding member X produced N compile errors"
measurement is a floor, not a total, because these files never enter the count. One instance
(the `useAuth` mock in Settings.urlstate.test.tsx) was fixed on the phone-residue branch; the
same file and 10 others still carry the pattern for other hooks (`grep -rln "as unknown as
ReturnType" web/src`). Caveat before sweeping: some casts wrap large React-Query result shapes
where a fully typed object is genuinely impractical — the audit should separate "lazy auth/ctx
mock" (fix) from "pragmatic partial of a huge generic" (document). **Effort:** small-medium.

### B51 — nothing in the UI says a negative Amount bound is how you find refunds
**Verified: read** (2026-08-15, UX review of the B49 sweep). Since B10 made refunds negative,
the Filters → Amount min/max pair and the import preview's amount cell (`ImportPreviewTable`,
sign-preserving parser) are the two places a typed minus is meant, and that fact lives in code
comments (`FilterPanel.tsx`'s Amount-tab block) — no label, placeholder or chip says it. The durable fix is a sign affordance ("refunds only" switch or a sign chip) rather than any
keypad-hint change: on Android/Chromium a bare `type="number"` and `inputMode="decimal"` are
REPORTED to map to the same unsigned IME class (unverified — device only), and iOS is the one
platform where `decimal` itself removes the minus. If the S24 keypad hides `-`, compare against a
bare `type="number"` field first; then build the affordance. **Effort:** small-medium.
**Device result 2026-08-15 (owner screenshot, S24 / Samsung Keyboard):** the decimal keypad on
Filters → Amount shows `1–9 0 . ,` and NO `−`, so a phone user cannot type a negative bound at
all — confirmed real. The bare-`type="number"` comparison is still open; the fix is the sign
affordance, as its own small UI stage (not a rider on a data-correctness branch).

### B52 — an emptied export Year still exports year 0
**Verified: read** (2026-08-15, found while fixing the export field's clear-and-retype snap).
`DataSection` derives `year = Number(yearInput)`, so an empty field is `0` and Export builds
`/api/export/monthly/0/{m}` — pre-existing behaviour, deliberately preserved when the state moved
from number to string (a fallback or clamp would change export semantics, which that fix ruled
out). The string state also lets a lone `-` survive mid-typing, so `-5` now reaches the URL where
the numeric state used to collapse it to 5; the handler 400s anything outside
`[MinDataYear, MaxDataYear]`, so both shapes open a JSON error tab, never a wrong export. Wants a
decision: disable Export while the field is empty/out of range, or fall back to the current year.
**Effort:** small.


### B53 — an open SSE stream is the one authority not re-derived per request
**Verified: read** (2026-08-15, security review of the B20 session-wipe change).
`internal/api/events_handlers.go:~67-87` authorises once at connect (route-level
`RequireAuthOrAPIToken`) and then loops on the broker channel until the client disconnects; the
role-change session wipe deletes session rows but does not close the stream. Today this leaks
nothing material — frames are literal topic strings (`transactions`, `dashboard`, `reports`,
`budgets`) plus `retry`/pings, which a member is entitled to, and every follow-up fetch 401s.
Recorded because it is the single exception to "authority is re-read per request" (sessions,
API tokens and the localStorage cache all are): if an SSE payload ever carries row data or an
admin-scoped topic this becomes a live leak with no test in the way. Fix candidate: signal the
broker after the commit (`sse.Client` already carries `userID`) so a wipe drops that user's
streams. **Effort:** small.
---

### B54 — the import preview's READY state is `text-emerald-500` on white (2.54:1)
**Verified: computed** (2026-08-15, found while fixing the amber status line on the import-rate
branch). `ATTENTION_TEXT_CLASS` (`text-amber-700 dark:text-amber-500`, `web/src/lib/attention.ts`)
now carries the computed ratios; the other branch of the same ternary in `ImportPreviewTable.tsx`
is `text-emerald-500` — 2.54:1 on the light card, 7.12:1 on the dark one — and `text-emerald-500`
sits at 7 sites, so a success token is a palette decision, not a one-line edit. Symmetric fix: a
`SUCCESS_TEXT_CLASS` pair `text-emerald-700 dark:text-emerald-500` (5.48:1 / 7.12:1) beside the
attention pair, then sweep the sites. No single colour can meet 4.5:1 on both surfaces (light needs
relative luminance ≤ 0.1833, dark ≥ 0.2115 — disjoint). **Effort:** small.

### B55 — the Patterns tag chart's tooltip has an invisible swatch and an unformatted amount
**Verified: read** (2026-08-15, UX review of the Sonar baseline branch). `ChartTooltipContent`
derives its indicator from `color || item.payload.fill || item.color`; the Patterns `<Bar>` has no
`fill` prop and (deliberately, see the Sonar entry in Closed) keeps its per-bar colour on the
`shape` prop, so the swatch is a transparent 10px indent, and the value reads `22.49` rather than
`$22.49` (no formatter; the X axis is unformatted too). Spending's data-`fill` mechanism would
colour the swatch for free, but that is a visible change and belongs on its own branch with the
formatter. **Effort:** small.

### B56 — the tag chip's `×` remove button is far below the touch floor
**Verified: read** (2026-08-15, same review). `TagInput.tsx` `p-0 text-sm leading-none` on the
remove button gives roughly a 10×14px target on phone; Backspace-removes-last is the only
fallback. Same 44px `coarse:` floor discipline as the B9 batch. **Effort:** small.

## Queued stages

*B10 (with B1 step 2 folded in) shipped 2026-08-14 on `feat/b10-signed-amounts`, merged via
PR #139 and released as v0.44.0 — see Closed.*

- ~~Phone-residue batch (B44 + B45 + B28 + B30)~~ — **MERGED 2026-08-15** as PR #141
  (`bf500ca`, v0.44.2) from `fix/phone-residue-sheets-url-budgets-switches` — see Closed.
  Carried the `2bea69f` stamp and the B43 / native-Android / import-rate / ⌘Z decision
  records as riders.
- ~~Import per-row rate~~ — **MERGED 2026-08-15** as PR #144 (`ef0ef4a`, v0.46.0) from
  `feat/import-per-row-rate` — see Closed.
- **Re-price older rows at a new rate** (owner asked 2026-08-15: "if I want to apply a new rate
  for older data can I do that?" — today: no; the freeze-on-edit is one-way and the only escape
  is the undiscoverable two-save trick). Now honest to build because every manual row records
  `booked_rate` (#139) and imported rows will (stage above). Two sizes, own branch after the
  import-rate stage: (1) an editable **Rate** on the transaction edit form (USD recomputes,
  `booked_rate` updates — also the discoverable escape from the freeze); (2) a filter-scoped
  bulk "re-price all rows in currency C between A and B at rate R" that previews the touched
  rows and before/after totals, writes an audit row per row, clears their `content_hash` (the
  money moved), and fires the checkpoint + budget hooks like every bulk path must.

---

## Needs a decision or a fact from the owner

- ~~Is push enabled in production?~~ **Answered 2026-08-02: yes, and both members receive
  notifications.** Promoted to B11 — it is a live defect, not a question.
- ~~**Does anything already monitor the box from outside?** If yes, B7 shrinks considerably.~~
  **Answered 2026-08-02: yes, Dockhand watches every stack's container health.** That is what
  made B7 a two-line change to what the `HEALTHCHECK` scrapes rather than a new alerting
  channel; both pieces shipped in v0.39.0 — see B7 in Closed.
- ~~**Native Android app — reassess.**~~ **Answered 2026-08-14: CLOSED — the PWA is the
  answer**, and it keeps improving ("the pwa is good enough now we can keep on improving for
  the future as well"). The residue that last framed the question is gone: B28 and B30
  shipped in the phone-residue batch (PR #141, `bf500ca`), and B43 closed as intended
  behavior — see Closed.
- ~~**Import + foreign currency:** what rate should a back-dated import use?~~ **Answered
  2026-08-14, premise CORRECTED 2026-08-15:** the 08-14 answer ("rows without a rate fall back
  to the import-day rate — today's behaviour") assumed the importer converts; it does not (see
  the queued stage above for the measured facts). Owner's 08-15 decision: the sheet's `Rate`
  column is the source of the USD; no silent fallback; unknown currencies block with an
  "add it first" note. Promoted to the queued import-rate stage above.
- **Entry-row ⌘Z leaves member-visible tombstones** — **Answered 2026-08-14: fine as-is**
  (asked 2026-08-02, reaction was pending since). Trash purge clears them, and tombstoned rows
  are excluded from every aggregate by the soft-delete invariant, so the only cost is Trash
  clutter.
- **SonarQube: how hard should the Go cognitive-complexity rule bite?** (asked 2026-08-15, on
  the Sonar baseline branch.) The default "Sonar way" Go profile keeps `go:S3776` at threshold
  15; the baseline scan counts **131** existing violations, almost all handlers, and the gate
  is "0 new issues", so every new handler above 15 (a bulk endpoint, the re-price stage's
  filter path) will turn the gate red until it is split. Options: (a) keep the default and
  treat a red gate as pressure to split (the default; nothing to do), or (b) copy the Go
  profile to a "SpenDrop way" like the TypeScript one and raise the threshold to 25 for this
  project. Not decided; the TS profile decision (three rules off) is recorded in Closed.
- **SonarQube: two exclusions in `sonar-project.properties` hide real code** (deep review of the
  Sonar baseline branch, 2026-08-15). `web/src/components/ui/**` (5,211 lines / 42 files) is
  treated as vendored shadcn primitives, but it also holds local forks (`chart.tsx`,
  `select.tsx`, `dropdown-menu.tsx`) and `password-input.tsx`, bespoke code for a credential
  field — 10 of those files have their own test suites; a future edit to a fork gets no
  new-code gate. `.github/**` hides the four workflows from the GitHub-Actions rules. Sonar
  globs have no negation, so the honest options are: keep as-is; drop the `ui/**` exclusion
  and eat the primitives' noise (S6759 is already off); or list the pristine primitives one by
  one. Un-excluding either will surface pre-existing issues as *new* on the first scan after
  (issue dates = analysis date), so do it in a quiet moment, not on a feature PR. Owner's call.

---

## Verified clean — do not re-raise

Each was checked on 2026-08-02 and is either fixed or not worth acting on. Listed so the same
leads are not re-investigated.

| Item | Finding |
|---|---|
| PWA logs out on network change | **Fixed** in `69eb95f`, shipped v0.33.0+. The note claiming otherwise was stale for weeks. |
| Phone quick-add suggestions silently empty | **Fixed.** Phone is still fed by `useDescriptionHistory`, but the silent-empty behaviour is gone. |
| Suggestions handler hangs the app | **Latent only — proven unreachable.** Every path drains to exhaustion. A trap for a future edit, not a live bug. Correct symbol is `handleTransactionSuggestions`. It was previously over-ranked as "best next candidate"; it is not. |
| Export memory | Measured at ~10% of the ceiling. Every write path now caps text length so it cannot grow. The proposed streaming rewrite would make one member's export block the other's requests and inverts four existing tests. Not worth it. |
| Route text-limit guard blind spot | Real and documented in the file header. The one route it cannot see is hand-listed and does enforce its limit. A manual hunt for other dynamic-shaped routes found none. |
| No LICENSE / GHCR storage | No practical consequence for a two-person private deployment. If the package is ever flipped to private, actual storage is ~2.5 GB, so ~80% of versions need pruning first. |
| Export drops rows past 50,000 | About seven years away at the current rate. |
| Balance checkpoint tag matching and year bounds | Real bugs, but **there is no checkpoint UI at all** — unreachable except by hand-crafted API calls. Not worth fixing until it has a screen. |
| Go toolchain pinning | The running binary is already patched; this is CI hygiene. *But* the runtime base image went end-of-life in April and Dependabot security alerts are off — both worth a click, and PRs are already open. |
| Import misreading text-formatted columns | Narrow: genuine date and number cells from a Google Sheets export are unaffected. Only columns explicitly formatted as *text* are exposed. |

---

## Corrections to project instructions (B6g detail)

**CORRECTED 2026-08-07 with B6** — both rules below were rewritten in the local `.claude/CLAUDE.md`
(gitignored; the correction cannot appear in any diff). Kept for the record of what was wrong:

1. **The soft-delete `JOIN`-placement rule.** It warns that moving `t.deleted_at IS NULL` from
   `ON` to `WHERE` drops empty categories from reports. Tested directly against a fixture
   database: both placements return byte-identical results, because a separate condition already
   removes zero-total categories. The code's own comment admits this, and the "dashboard
   categories" example named in the rule is not that kind of join.
2. **"Mutations go through `TransactionStore` only — never raw SQL."** *(re-measured 2026-08-14
   during B10:)* `batch-update` and `batch-delete` now route every row through the store; raw SQL
   remains at exactly three `ExecContext` sites in two endpoints (`delete-by-filter`, and
   `update-by-filter`'s no-tags fast path plus its per-row tags variant), each deliberate and
   documented in place. The rule as stated is wrong, not the code — the invariant that actually
   holds on every path is audit-in-same-tx + tombstone exclusion + the checkpoint hook.

Neither is enforced by a test. A reviewer trusting the first could relax the real filtering
condition *and* move the predicate, believing one was safe because the other was checked.

---

## Closed

*(Move items here with their commit hash rather than deleting them.)*

- **SonarQube baseline — the first scan, read, triaged, and cleared** (2026-08-15, on
  `chore/sonar-baseline`; squash hash joins this entry at merge). The owner stood up a local
  SonarQube (Community Build 26.8, "Sonar way" profiles, default gate) and scanned `main` at
  `ef0ef4a`: gate OK only because a first analysis has zero new lines; underneath, 4
  "vulnerabilities" (Security C), 5 bugs (Reliability D), 44 reliability + 450 maintainability
  issues, coverage 0.0% (no report fed — the Zero Coverage Sensor), duplication 1.3%, 0
  hotspots. Read off the server via its API, not the log. **Three log-level facts:** the version
  was stamped `v0.44.2` because the wrapper reads `git describe` and the local clone had never
  fetched the CI-cut tags (fixed: the wrapper now fetches tags first); coverage was not being
  produced (fixed: `make coverage` → `coverage.out` + `web/coverage/lcov.info` with repo-root
  paths, `sonar-project.properties` points at both and is now versioned — it holds no secrets —
  and the wrapper runs the target before every upload; without it the default gate's
  `new_coverage < 80%` fails every PR — and, on the owner's friend's tip the next day, the
  SEPARATE test-execution channel: `go test -json` → `sonar.go.tests.reportPaths` and
  `vitest-sonar-reporter` XML → `sonar.testExecutionReportPaths`, so the dashboard's Unit Tests
  count / pass density / duration exist at all — verified on the throwaway project: tests 4,655,
  success density 100%); a `WARN Invalid character` at
  `export_truncation_test.go:359` was a literal U+FFFD (valid UTF-8; the Go lexer cannot tell it
  from a decode failure) — spelled `'\uFFFD'`, and the neutralizer test's raw bidi/C1/zero-width
  fixtures became `\u` escapes (`4ccae75`), so no source file carries an invisible character.
  **Triage:** *fixed* — `reduce` used as a side-effecting loop in Overview/Savings, IndexedDB
  rejections that could be `null` (and a blocked-open arm that read `req.error` while pending —
  the getter throws, so the arm could only hang; found by the review of the fix), the trailing-slash
  regex triplicated across `client.ts`/`import.ts`/`useLiveUpdates.ts` (one linear
  `trimTrailingSlashes`), the prefs rollback made legible, `parseInt`/`parseFloat` →
  `Number.*`, `replaceAll`/`codePointAt` in vapid, `FormEvent` → `SubmitEvent`, recharts
  `<Cell>` (deprecated, gone in 4.0) → `shape` in Patterns and per-entry data `fill` in Spending
  — two mechanisms on purpose, each measured against a byte-copy of the old file: `shape` flips
  recharts' `hasCustomShape` and paints a `$0.00` LabelList label on a zero-total category in
  Spending; data-`fill` colours shadcn's tooltip swatch in Patterns; either alone is a visible
  change (residue: one empty non-painting `<g>` per zero-total tag in Patterns), JSX "ambiguous
  spacing" disambiguated WITHOUT `{' '}` (which would have added a space — JSX collapses the
  newline, so no space renders today; rendered text pinned byte-identical), TagInput's
  click-to-focus box marked `role="presentation"` (chips, remove buttons and the input's label
  untouched; no tab stop — under the DEFAULT profile that role trips `S6819`, which answers "use
  `<img alt>`" for a div: the third exhibit for switching that rule off). *Deliberate, accepted server-side with rationale* — the Math.random
  fallback in `client-key.ts` (Web Crypto first; per-user idempotency scope), `.sort()` on ISO
  `YYYY-MM` keys, `e.returnValue = ''` beside `preventDefault()` in beforeunload, keyboard
  shortcuts on `<form onKeyDown>` / the row wrapper. *Profile* — TypeScript "SpenDrop way" =
  Sonar way minus `S6822` (redundant role: `role="list"` restores list semantics Tailwind's
  preflight strips under VoiceOver, documented at `TransactionCardList.tsx:89`), `S6819`
  (prefer tag over role: `role="status"`/`role="group"` are standard ARIA; `<output>`/
  `<fieldset>` carry the wrong semantics), `S6759` (read-only props: 67 sites, a wrapper the
  project does not use); assigned to `spendrop` only, never the server default. *Not chased* —
  131 Go cognitive-complexity, 37 nested ternaries, 30 duplicated Go literals, 19
  assertion-style: debt the new-code gate holds the line on; the Go threshold is an open owner
  decision above. **Verification:** every change behaviour-preserving by construction and by
  test — 22 mutants watched to die across the two builders (plus 11 re-verified independently by the reviewers);
  the new chart and spacing pins PASS against the OLD sources (true invariant pins); reviewers
  fuzzed `trimTrailingSlashes` vs the regex (200k inputs, 0 mismatches) and vapid old-vs-new
  (50k); charts pixel-diffed before/after on the rebuilt `:3535` in both themes (0 px on
  spending-light, ≤15 px at delta 1/255 elsewhere, all inside the sidebar theme toggle);
  `tsc -b --force`, eslint 0 errors, vitest 2236/2236. Commits: `4ccae75`, `f042263`, `4a526da`,
  `fde8182`, `88fbdd8`, `160f197`, `cc82d36`, `f853929`, `0cdeeff`, `ee3941b`, `885ba17` (deep-review minors: 4 closed, the exclusion question filed above), `80fa965` (test-execution reports). Filed: B55, B56, and the Go
  threshold decision. **Branch scanned into a throwaway project** (`spendrop-branchcheck`, so `main`'s analysis
  stayed untouched — Community Build has no branch analysis): 0 analysis warnings, coverage
  **86.0%** (both reports resolved), every targeted rule at 0, and the residue is exactly the
  deliberate set (S2245, S2871, S6822×12, S6847×2, S6848×1, S1874×2 `returnValue`, plus the
  S6819 above). Not verified: the server-side profile/marking POSTs were blocked from the
  session by the permission classifier, so they ship as `sonar-server-setup.sh` for the owner
  to run; the throwaway project is to be deleted after.

- **Import per-row rate — a `Rate` column is the source of a foreign row's USD** (2026-08-15, on
  `feat/import-per-row-rate`; merged the same day as PR #144, squash `ef0ef4a`, released
  **v0.46.0**). **Corrected premise first:**
  at `804dbc2` the import performed NO currency conversion — it required the USD `Amount` column,
  stored `Original Amount`/`Original Currency` verbatim as labels, never read the currencies
  table, and left `booked_rate` NULL; the 2026-08-14 "import-day rate fallback" wording assumed a
  conversion that did not exist. Owner chose **option A**. What shipped: a `Rate` column
  (aliases `rate` / `exchange rate` / `fx rate`; foreign units per USD) makes
  `amount = original ÷ rate` through the ONE divisor manual entry uses (`convertForeignMoney`,
  extracted from `resolveCurrency`; the original is rounded to cents BEFORE dividing on both
  doors, so the stored triple is self-consistent), records `booked_rate`, stores the canonical
  currency code (`lbp` → `LBP`, which is what lets the freeze-on-edit predicate match an imported
  row later), collapses a base-named or bare-currency label to no original, and a sheet may state
  its money in the foreign column only (header discovery accepts `original amount` as the money
  column). Seven money conditions BLOCK the preview on every surface and 409 `MONEY_ERRORS`
  (sibling of the byte-identical `FIELD_TOO_LONG`; gate order length → money → categories →
  collisions): rate missing / invalid / on base / without anything to convert, unknown currency
  ("add it under Settings → Currencies" — the session refetches when currencies change, so no
  re-upload), USD disagreeing with the quoted rate to the cent, and a derived value outside the
  bounds. **No silent fallback rate anywhere** — a rate-less foreign row is offered "apply today's
  N" as an explicit click that records the rate the user saw. Money flags are withheld from rows
  that will not import anyway (unparseable date, empty description, sign mismatch — matching the
  category family) and dropped where a length error already sits on the same cell. Collisions
  hash the derived cents, so a hand-typed LBP row at 89,000 and a sheet row `1,500,000 LBP @
  89000` with an empty USD are one identity; the hash formula and the schema are unchanged (only
  migration 019's comment gained a dated addendum; SCHEMA.md regenerated byte-identical). Export
  gains a `Rate` column after `Original Currency` (Tags/Notes shift H→I / I→J for positional
  readers) so a SpenDrop export re-imports losslessly — proved by round-trip tests incl. a
  sub-cent original. Numbers accept a comma ONLY as a thousands separator (`0,92` is refused,
  never read as 92 — a text-formatted decimal-comma rate would otherwise have booked a permanent
  100× rate; the same rule now guards amounts, which had the identical class). Preview UI:
  "Rate to base" editable cell (`inputMode="decimal"`; shows the sheet's raw text via `rate_raw`
  when the cell is unusable, so "clear the cell" is a real affordance), a second money line
  `1,500,000.00 LBP @ 89,000`, `—` instead of `0.00` for a rate-less row, blockers on the
  existing rail with server-authored sentences on both routes (the client-wide `apiErrorFrom`
  now reads `error` → `message` → `HTTP n`, closing a pre-existing bug where every rejected
  import PATCH rendered "HTTP 400" over the good flag), bulk "Apply today's 89,000 LBP to N rows"
  / "Use the computed 16.85 for this row" / "Clear the rate on N rows" / "Skip these N rows"
  (never an actionless bar; per-row containment on every burst; deliberate focus placement
  after a burst — bar heading, else another bar, else the enabled Import button, else the
  scroll container), category choices survive the Settings → Currencies round trip
  (`lib/import-decisions.ts`, keyed by `import_id`, cleared on all four exits), the aggregate
  bars hoisted above the scroll container (they were in a `colSpan` cell of the horizontally
  scrolling table — the second button sat off-screen at 360px and both bars scrolled away),
  the sticky column headers now actually stick (`ui/table.tsx` gained `containerProps` so the
  height cap and the scroller are one element — the header had NEVER stuck: the sticky's nearest
  scrolling ancestor was shadcn's own wrapper), and a shared `ATTENTION_TEXT_CLASS`
  (`text-amber-700 dark:text-amber-500`, computed 5.02:1 / 8.41:1 on the two card surfaces;
  amber-600 is 3.19:1, so the pair is forced — no single colour meets 4.5:1 on both themes).
  **Decisions the owner can veto:** no fallback rate; unknown currency blocks even with a USD
  amount (parity with manual entry); a base-named or bare-currency label collapses; a comma is
  never a decimal separator; a legacy sheet whose `Rate` column means something else now blocks
  (escape: bulk clear; README names the column); the Rate column renders on every preview
  (stable layout over data-dependent shape); rows already rejected before their money carry no
  money flag. **Verification:** SDD ledger with every ruling; four builders with disjoint
  ownership; per-task reviews (code, security APPROVED, data-correctness, UX, design) → 5 fix
  rounds on the frontend, 3 on the import backend, 1 each on the helper and export; controller
  gates (gofmt/vet/full Go suite/`-race`/SCHEMA regen byte-identical/`tsc -b`/2211 vitest/
  eslint); isolated deep review at `66befa6` (44 mutants, 39 killed; the two survivors that were
  real gaps became the decimal-comma and sub-cent fixes; the core money invariant survived every
  adversarial construction); browser passes on the rebuilt `:3535` at 1288 and 360 in both
  themes with a 10-row matrix workbook — every shape, both bulk fixes, the Currencies round trip
  with the manual mapping retained. **Filed, not fixed:** the READY-state `text-emerald-500`
  (2.54:1 on white; a `SUCCESS_TEXT_CLASS` pair `emerald-700 dark:emerald-500` is the symmetric
  fix); the import preview table has no phone fork (six columns scroll inside the card at 360;
  editable cells are dblclick-only — the bulk actions are the phone's remedies); the two-snapshot
  window at confirm is documented, structurally forced by `SetMaxOpenConns(1)`; case-folded
  duplicate currency codes are unreachable through the API; `parseImportAmount` still accepts
  Go's hex-float/underscore literals (pre-existing; the rate parser refuses them); the ~3×
  resolver cost per preview at household scale.
- **B20 + B36 — role integrity at the DB layer, and `@username` attribution** (2026-08-15, on
  `fix/role-guard-and-username-attribution`; merged via PR #143, squash `804dbc2`, released
  **v0.45.0**). Three
  parallel builders with disjoint ownership (backend B20, backend B36 wire, frontend B36 render).
  **B20** — migration `020_users_role_integrity.sql`: `BEFORE INSERT` + `BEFORE UPDATE OF role`
  triggers `RAISE(ABORT)` on any `users.role` outside {'admin','member'}, plus a repair `UPDATE
  … SET role='member' WHERE role NOT IN (…)` for hand-edited/restored DBs (semantics-preserving:
  every gate already treats such a row as non-admin; `updated_at` untouched). **The filed
  mechanism — a CHECK via a 019-style rebuild — was measured to DESTROY THE LEDGER**: `users` is
  the parent of seven cascading FKs, and dropping the old table inside the runner's transaction
  under `foreign_keys=ON` cascade-deleted every child row (transactions 2→0, sessions,
  checkpoints, push subscriptions, saved filters, audit actors) with COMMIT succeeding; no escape
  hatch works (`PRAGMA foreign_keys=OFF` is a no-op inside a tx, `defer_foreign_keys` defers the
  check not the action, rename-first rewrites the children's REFERENCES). Triggers enforce the
  same invariant with no rebuild — recorded in the migration header. `handleUpdateUser`: the role
  UPDATE and the session wipe now share one transaction, so a failed wipe rolls the demotion back
  and 500s — chosen over "keep the demotion, report failure" because the handler only wipes when
  the role CHANGES, so a persisted demotion without its wipe is invisible to the retry (mutant C
  measured exactly that). `handleDeleteUser`: wipe error-checked and kept (cascade is live but
  `ForeignKeys` is a config field). Premise correction: a surviving cookie was never
  "admin-capable" — role is re-read on every request — the real gap was the dishonest 200.
  Fault-injection seam for the tests is a `BEFORE DELETE ON sessions` trigger, no production
  seam. Security review then found the merge source (`existing`) was read OUTSIDE the
  transaction — a concurrent rename could merge a stale `admin` back over a just-committed
  demotion with no wipe; the read moved onto `qtx`, and the race was REPRODUCED, not reasoned:
  the regression test fires a demotion and a rename concurrently 100× and asserts the
  order-independent invariant (final role `member`); the pre-fix handler died 30/30 at two
  GOMAXPROCS regimes, the fixed one is clean under `-race`. `docs/SCHEMA.md` regenerated
  (append-only). Filed B53 (an open SSE stream is the one authority not re-derived per request).
  **B36** — owner decided 2026-08-15: render `@username` (option a), NOT a display-name
  uniqueness check (leaks the set of names). Wire: `created_by_username` on every
  transaction-shaped response — list (raw-SQL scan, same LEFT JOIN), create, batch, idempotent
  replay, and both trash queries (`u.username` on the existing LEFT JOIN; hand-maintained
  `queries.sql.go` row structs/Scan lists updated in lockstep) — always emitted, `""` when the
  creator's row is gone; `toTransactionResponse` takes it as a third positional so the compiler
  forces every future emit site. Disclosure trade written at the field: usernames are the
  non-secret half of a credential pair, login is rate-limited per IP with a dummy bcrypt on
  user-miss, admins already see every username in Settings; usernames are immutable ASCII
  (`usernameRegexp`, no UPDATE path). Render: one `<CreatorLabel createdBy createdByUsername>`
  replaces six byte-identical "Entered by" blocks — handle after a REAL space (a margin reads as
  one email-like token to a screen reader), suppressed when either half is empty; recipe in
  `docs/DESIGN_GUIDE.md`. UX review caught the first shape pinning the bug: name and handle in
  ONE truncating span clips the handle FIRST — the spoofable half survives and the identifier
  goes; now the display name is the `min-w-0 truncate` half and the handle a `shrink-0` sibling
  capped at `max-w-[50%]` (with `flex-1` on the row so the percentage has a definite basis —
  without it the cap clips even when there is room), `whitespace-pre` keeping the space a
  rendered character; `title` carries the full pair for the desktop cell. Browser pass on the
  rebuilt `:3535` (SW cleared; wire fingerprinted: `created_by_username` on every list row;
  migration 020 applied at boot with its pre-migration snapshot): the admin renamed to
  "Marie-Thérèse Abdelahad" for the probe — at 360×780 the phone card shows "Marie-Théré…
  @Elienop" on the LBP row (name clipped 62px, handle clipped 0), Trash card and desktop row
  clip nothing, page has zero horizontal overflow; the extreme (selection mode + LBP secondary
  line, 81px row) clips the name to 40px and the handle to " @Elien…" — the 50% cap doing its
  job of not starving the name entirely; `PUT /api/users/1 {role:'owner'}` → 400 whitelist,
  self-demotion → 400; display name restored.
  **Rider — the desktop tables overflowed their card at ≤ ~1380px** (owner-reported during
  this pass at 1288: amounts read `-$9` and the Actions column was scrolled off). Pre-existing
  and data-dependent, not B36 (measured identical with the handle removed): the Description
  cell's `max-w-md` is a cap not a floor, so one long description grew the column to 448px and
  the table past its `overflow-auto` scroller (1063 vs 951). Fix: `w-full max-w-0` on the
  Description cell of both the Transactions and Trash tables — `max-w-0` is what lets the column
  shrink (Chrome sizes an auto-layout column FROM the cap), `w-full` routes the slack to it
  instead of fattening the other six; every other column byte-identical (DOM-diffed). Measured
  on the rebuilt container: 0 overflow at 1400/1288/1130/1024 on both pages (was 78–153px at
  1130), cents and Actions in view, phone card lists untouched. Still overflows in the
  768–~900px band (seven fixed columns alone exceed 623px; no household device sits there —
  the tablet is ~720 portrait/card list, ~1130 landscape/fits); closing that band means
  dropping a column below `lg`, a separate call. Tests decode
  into maps with fixtures whose display_name ≠ username so a column swap is caught. Filed
  premise corrections: heatmap and RecentlyAdded are readers of the list endpoint, not producers;
  the username gate is `isValidUsername`, not `validateUsername`.
  Original findings, kept for the record:
  *B20 — users.role integrity rests on one handler.* **Verified: read** (found 2026-08-08 during B18 merge-semantics mutation testing; second half
  during the session-survival test work). Two hardening gaps in the same surface: (1)
  `users.role` has NO CHECK constraint — under a mutated handler, `role = ''` writes cleanly
  into SQLite; `handleUpdateUser`'s whitelist plus its merge fallback are the only guards of
  that column's well-formedness, and any future path writing role bypasses both. (2)
  `handleUpdateUser` discards `DeleteSessionsByUserID`'s error (`_ =`) — a failed DELETE after
  a demotion leaves admin-capable cookies alive and still returns 200; untestable today without
  a fault-injection seam.
  **Effort:** small — a migration adding the CHECK, plus an error check.

  *B36 — a member can take another member's display name, and attribution follows.* **Verified: reproduced by the security audit of `PATCH /api/auth/me`** (2026-08-09), and the
  mechanism confirmed independently: `created_by` **is** the display name
  (`internal/api/transaction_handlers.go:150`) and is the only attribution the ledger renders —
  `TransactionRow.tsx:297`, `TransactionCard.tsx:245`, `Trash.tsx:266`/`:1068`,
  `HeatmapDaySheet.tsx:262` all render `{transaction.created_by || 'Unknown'}` and nothing else.
  
  **Introduced by the self-service rename.** Before it, `display_name` was admin-written only, so a
  member could not self-select an impersonating label. Now a member can read the admin's exact string
  off the household-wide ledger and PATCH to it; because `created_by` comes from a live JOIN, the
  relabel applies **retroactively** to every row they have ever entered, and reverts instantly on
  rename-back. Observed: two ledger rows reading `Elie Abdelahad`, `user_id` 1 and 2.
  
  Low severity — needs an insider already trusted with the ledger, grants no privilege or data access,
  fully reversible, and the admin can disambiguate in Settings, where a Username column sits beside
  Display Name. It becomes Medium if the household grows past two, or if attribution is ever used for
  reimbursement.
  
  **Fix is frontend-only and NOT a server-side uniqueness check** — a uniqueness error leaks the set
  of existing display names to a member. `user_id` is already on the wire in `transactionResponse`, so
  render `@username` (or mark the current user's own rows) wherever `created_by` appears.
  **Effort:** small. **Owner has not yet decided whether to take it** (still undecided as of
  2026-08-14; no attribution change has shipped — grepped).

- **B21 — display_name charset gate** — **already shipped in PR #137 (`a8c484f`, 2026-08-14)**
  and closed here on 2026-08-15 when the stage-3 scout found the entry stale. The fix is exactly
  the "one pass" the entry asked for: `internal/api/display_name.go`'s `validateDisplayName` is
  the single gate every writer of the column calls (`handleRegister`, `handleCreateUser`,
  `handleUpdateUser`, `handleUpdateMe`), refusing Cc controls, U+2028/2029, the bidi
  embeddings/overrides/isolates, and allowing the bidi MARKS the household's Arabic/French names
  legitimately carry (per-codepoint reasoning at the site); `notifications.go` carries the
  sink-side half of the same invariant. Tests: `display_name_test.go`. Original finding, kept
  for the record — *B21 — display_name has no charset gate, and it reaches push-notification
  bodies.* **Verified: read** (B9 security audit 2026-08-08; PRE-EXISTING — B18 only made renames
  routine). Unlike `username` (which has a charset gate), `display_name` is length-bounded
  only, at both write sites (register and the admin PUT): control characters, newlines, and
  bidi overrides are accepted. Every render site is safe (JSX text nodes; no export path
  carries it). The one non-text sink: Web Push — `internal/api/notifications.go:79` interpolates
  the actor's display name into the body via `fmt.Sprintf`, so a `\n` in a name forges extra
  notification lines. Fix in ONE pass: sanitize at both write sites; consider the notification
  builder too.
  **Effort:** small.

- **B49 + B50 + B46 + B47 — keypad hints, Toaster order, stale counts, ModeToggle containing
  block** (2026-08-15, on `fix/phone-inputmode-toaster-order`; **merged as PR #142, squash
  `048e8e4`**). Two parallel builders with disjoint file ownership; 0 Critical / 0 Important from the
  code / UX / design battery, every Minor fixed. Per item:
  **B49** — every real `type="number"` `<Input>` now pairs with an `inputMode` (12 added: 10
  `decimal` for money/rates incl. AmountCurrencyInput — the wrapper all four amount surfaces
  render — FilterPanel min/max, Savings target, the three Budgets desktop inputs, the three
  Settings rate fields; 2 `numeric` for the Savings and export Year fields), plus the shared
  `ImportPreviewTable` cell editor deriving `decimal` per field for the amount column (found by
  the UX review — invisible to a `type="number"` grep). The filed premise was wrong for two of
  five files: `SpendingTab` and `PatternsTab` carry only recharts `<XAxis type="number">`, not
  inputs — deliberately untouched, not skipped. Tests pin the ATTRIBUTE at every site (happy-dom
  and headless Chrome raise no keyboard); the MECHANISM is the owner's S24 confirmation on the
  Budgets cards (v0.44.2), now recorded in the canonical `<MonthlyBudgetCard>` comment that
  every site points at, and the convention is in `docs/DESIGN_GUIDE.md`. Rider: the export Year
  field keeps its raw string in state so clearing it no longer snaps to "0" (`Number(s)` is the
  old coercion per value, but a lone `-`/`1e` no longer collapses to 0 mid-typing, so `-5` now
  reaches the URL and is 400'd instead of exporting year 5 — measured by the deep review; that
  and the empty-→-year-0 residue are B52). Signed-bound caveat recorded at
  FilterPanel and filed as B51. Device check DONE 2026-08-15 (owner screenshot): the S24's
  decimal keypad on FilterPanel's Amount fields shows `.` and `,` and NO `−` — B51 is a real
  phone gap, see its entry.
  **B50** — `<Toaster />` is the FIRST child of AppShell's root (was after `</main>`) and of
  QuickAdd's root, so a `toast.*` fired from a route's mount effect renders on a cold load.
  Mechanism confirmed in sonner 2.0.7 source: `Observer.addToast` publishes to current
  subscribers only, the Toaster's `useState([])` never replays, and React flushes passive
  effects in tree order. Audit found one live loser (Settings' `?tab=savings|budgets|general`
  forwarding toast) and three async sites that were never at risk; QuickAdd was NOT latent —
  its pin went red against the unfixed file (a mount-toasting CHILD is lost there today, none
  exists yet). Placement is visually free (the wrapper `<section>` is in-flow, unstyled,
  zero-height; only the `<ol>` is `position:fixed; z-index:999999999`, and it exists only with a
  toast) and carries an accepted a11y consequence written at the site: a VISIBLE toast is now
  the first Tab stop and "Notifications" first in the landmark list — and cold-load toasts are
  now announced by the live region, which they never were. Both pins render the REAL Toaster
  and were watched to die (Toaster moved back → `cold-load probe` never renders).
  **B46** — the "six sections" sweep separated eight stale counts (fixed to five, including a
  `Reports.tsx` comment the entry missed that also asserted retired scrolling behaviour) from
  three dated browser measurements (568/313/461px kept verbatim, now labelled as taken
  2026-08-09 at `cbaa4d0` when six sections existed; `users` merged into `account` at
  `a8c484f`). No measurement was renumbered.
  **B47** — `relative` on ModeToggle's trigger via `cn('relative', className)`, scoped rather
  than on the Button base (the only other Button with a positioned descendant,
  `password-input.tsx`, is itself `absolute`); structural pin walks from the `absolute` Moon to
  its first positioned ancestor and asserts it is the trigger, with an anti-vacuity guard on
  the glyph. `size-11` from MobileNav survives the merge (pinned).
  Suites 2090 → 2108. Browser pass on the rebuilt `:3535` (SW unregistered, caches cleared;
  deployment fingerprinted by the Notifications region being the shell's first child): cold
  loads of `/settings?tab=savings` (1280) and `?tab=budgets` (360×780) both render the
  forwarding toast with its Open action; `/quick`'s wrapper section is first and 0px tall,
  Tap-mode amount is `decimal`; the Transactions entry row, Filters min/max, all 29 Budgets
  inputs, the three Settings rate fields, Savings target are `decimal`, Savings Year and
  export Year `numeric`; the export Year clears to "" and retypes; and with the 360×640
  drawer scrolled its full 15px in dark mode the Moon glyph moved the same 15px as its button
  (inside it before and after). Not exercised live: the ImportPreviewTable amount cell
  (needs an import; test-pinned both directions) and the S24 minus key.

- **B28 + B30 + B44 + B45 — the phone-residue batch** (2026-08-15, on
  `fix/phone-residue-sheets-url-budgets-switches`, commits `6a16776..d324b12`; **merged as
  PR #141, squash `bf500ca`, released v0.44.2**; owner confirmed the same day that the S24
  raises the decimal keypad on the Budgets card fields). Three parallel builders with disjoint file ownership. Per item:
  **B28** — all four remaining sheet consumers (MobileNav, TransactionEditSheet, Categories
  editor, Transactions Filters) moved their `SheetHeader` into the primitive's `header` slot,
  so titles stay visible under body scroll; spacing preserved exactly (removed body margins
  became the sheet's flex gap — arithmetic verified by the deep review); MobileNav's flush
  header additionally got `relative bg-card` — deliberately WITHOUT z-index, which would bury
  the drawer's absolutely-positioned Close button (pinned by a dedicated mutant).
  TransactionEditSheet's migration is defensive (its form never overflows, `maxScroll: 0`).
  Browser-verified: divider unbroken under real scroll, Close tappable, and the two
  landscape-only sheets (66/68px overflow at 780x360) plus the unbounded 21-chip category
  filter (367px scroll) all keep their titles.
  **B30** — the chosen section is replace-written to `?tab=` through the one `handleTabChange`
  both surfaces share, clamped by `resolveSettingsTab` (the same function the inbound path
  uses — round trip closed by construction, security-reviewed). Nothing writes on mount: a
  bare `/settings` stays bare, and a retired bookmark keeps its raw value so the forwarding
  toast can reproduce (see B50 for why cold loads currently lose that toast anyway).
  `MOVED_TABS` became a `Map`, closing the prototype-key lookup (`?tab=constructor` toasted
  "undefined has its own page now") and the unchecked `Record` index in one move; the deep
  review then found every route in that table unpinned (repointing both at `/trash` stayed
  green) — all three destinations now have invoke-and-assert-URL cases with literal oracles.
  **B44** — BOTH Budgets tables (not one: the page has two, and only Category Limits was
  panning — the brief's premise, corrected by the builder) render as card lists below `md` on
  the shared JS gate. Monthly cards horizontal (bounded token), category cards stacked
  (100-char names wrap via `[overflow-wrap:anywhere]` with `leading-5` pinned against
  tailwind-merge reviving Label's `leading-none` — the review's "set solid" Important was
  REFUTED by measuring the merged output: `text-sm` deletes `leading-none` in the conflict
  table, so the token is a pin, not a repair). Admin/member gating mirrored; em-dash-never-zero
  through the shared `readOnlyAmount` (the cards' `?? ''` arm and the table's `undefined` arm
  meet in one guard); `inputMode="decimal"`; the Category Limits description names the
  currency (an admin's phone view otherwise showed none). Desktop DOM-diffed at HEAD vs base:
  byte-identical except the caption and the Annual-total `font-mono`.
  **B45** — the filed "~4px overlap" was wrong in mechanism and worse in fact: the five
  activity switches' 44px tap bands TILED at exactly zero clearance (24px row + `gap-5` 20px
  = 44px pitch), seam resolved by paint order. Stack moved to `coarse:gap-6` → +4px clearance,
  fine-pointer rhythm untouched; the regression test derives band and pitch from rendered
  class strings (border-2 is 2px — its own scale), the pattern that cannot go stale the way
  the old comment did. Coarse-emulation browser pass: every seam midpoint hits no switch,
  every switch answers its own band.
  Process: four-reviewer battery (1 Important — refuted; all other findings fixed in three
  waves), ~45 builder mutants watched die pre-commit, isolated deep review MERGEABLE (12/13
  full-suite re-kills; the survivor became the route-pin test above), live browser pass at
  360/640/780x360 + proven-coarse. Suites 2048 → 2090.

- **B43 — Enter on the closed category trigger saves the row: CLOSED as intended behavior**
  (owner decision 2026-08-14; no code change — the decision record and the test-comment update
  ride `fix/phone-residue-sheets-url-budgets-switches`). The owner uses Enter on the closed
  trigger as the fast keyboard save; rerouting it to open the picker would cost a Tab to reach
  Save, and Space already opens the picker. The B33-era pin test
  (`_ControlEnterOnTheClosedTriggerStillSaves`) stands and now documents a decided contract,
  not an open question. Enter on an OPEN picker still picks without saving (B33). What was
  non-obvious: the "defect" framing assumed pickers must open on Enter; the owner's actual
  keyboard flow treats the trigger as one more field you save from, which is exactly what the
  pre-B33 comment claimed was intentional — it was right.

- **B26 + B33 + B34 + B41 + B42 — the phone/tablet batch** (2026-08-14, on
  `fix/phone-batch-recurring-settings-tablet`; five parallel builders with disjoint file
  ownership; merged 2026-08-14 as PR #140, squash `2bea69f`, released v0.44.1). Per item:
  **B26** — the Patterns tab's recurring-expenses table (102px pan at 360) became a JS-gated
  card list below `md` (`useIsMobileViewport`, matching the app's other five card lists); the
  tab's pre-existing pointer-derived gate was renamed `heatmapIsCalendar` so the two forks
  can't be confused; one lifted dismiss handler feeds both presentations, and dismiss buttons
  are named per entry on both.
  **B33** — the capture-phase key guard that ate Enter in the edit sheet's category Select was
  DELETED (measured: it protected nothing — Radix's layer gating already keeps Escape from
  dismissing the sheet, and its justifying comment was false); the twin guard in
  `TransactionRow` had the same defect but IS load-bearing (the row's Enter-saves handler) and
  was rebuilt as a controlled Select (`categoryPickerOpen` state) with a bubble-phase,
  timing-independent guard. Enter now picks an option in both; the closed-trigger open+save
  oddity was deliberately preserved, pinned, and filed as B43.
  **B34** — reproduced this time (happy-dom models detached-element focus faithfully; a
  same-value re-pick served as the positive control), and the diagnosis deepened: Radix
  restores focus from a `setTimeout(0)` after the commit AND `preventDefault()`s the event,
  cancelling its own body-fallback — so the skeleton's unmount dropped keyboard users to
  `<body>`. Fixed structurally: one return, the header (welcome + Month/Year/Today) rendered
  by every state, only the content below swaps. `keepPreviousData` was REJECTED against the
  recorded B4 stale-count incident; data-fetching semantics unchanged. The error state now
  renders under the functional header (a failed period is recoverable by picking another).
  **B41** — Currencies (129px pan) and API tokens (308px pan) got the #137 card-list
  treatment; the one-time token reveal became a read-only textarea that wraps all 38
  characters, and the reveal dialog + its state moved into a `NewTokenRevealProvider` hoisted
  ABOVE the md layout fork, so a rotation is a structural no-op — this also closed a real
  window where a rotation between the create POST and its response orphaned a never-displayed
  full-access token (pinned by test). Residual to re-measure with real data in a browser: the
  Budgets Limit column pan claim (fixture-inflated when first seen).
  **B42** — the shell's nav rows gained the `coarse:` 44px floor (base height + the square's
  width half when collapsed; fine-pointer sizing byte-identical), and the collapsed nav's
  `overflow-hidden` became x-clip + y-scroll so the taller column cannot cut Settings/Log out
  off a short viewport. The users-table half needed no shell change: the overflow was the SUM
  of three nowrap action buttons in the last column's min-content; `flex-wrap` on that cell
  drops it to the widest single button.
  Verified pre-commit by a four-reviewer battery (code, UI/UX, design-system, security on the
  token reveal): zero Critical; all findings fixed in-branch (fix-wave commits on this same
  entry's branch). Every builder shipped byte-copy mutation proofs for its new tests.

- **B10 — refunds cannot offset a category** (2026-08-14, on `feat/b10-signed-amounts`;
  MERGED via PR #139, squash `e25dee6`, released as **v0.44.0**, branch deleted; 11 commits,
  73 files). Refunds are negative `amount_cents` on the same category — the July "signed
  amounts, not a linked offset row" decision became code. Migration 019 rebuilds the
  transactions table (`CHECK(amount_cents != 0)`; zero stays illegal) and folds in **B1
  step 2**: a `booked_rate REAL NULL` column, populated only when a foreign amount is
  actually converted — no backfill, never a hash input. Entry is via an explicit Refund
  toggle (owner's choice, 2026-08-14) — the toggle is the ONLY sign channel; typed minuses
  stay rejected as the typo guard. Everything nets: dashboard, budgets, all 13 aggregates,
  exports (summary rows key on has-live-rows, `HAVING COUNT(t.id) > 0`), over-budget alerts
  (a refund clears a latch), heatmap (has-rows via `txn_count`, refunded-day state + legend
  chip). Two in-stage semantics decisions, both from standing doctrine and disclosed in the
  PR body: a pure sign flip on a foreign row KEEPS the booked magnitude and rate
  (browser-verified under a deliberately moved rate), and a sign-disagreeing amount/original
  wire pair is refused with 400 instead of half-discarded. Verified by a 5-reviewer battery,
  an isolated deep review (one blocker — a SCHEMA.md regen owed by a migration comment edit —
  fixed; 22/23 mutants watched die, the survivor proven equivalent), ~90 mutants killed
  total, suites 1834 → 1960, and a live browser pass at 360/1440.

- **B27 — the intensity scale returns the DARKEST stop for a zero total** (fixed on
  `feat/b10-signed-amounts` in `11deaf3`, 2026-08-14, as part of B10's heatmap has-rows
  rework; MERGED via PR #139, squash `e25dee6`). The signed-amounts change forced the real
  fix the entry asked for: `buildIntensityScale` was rewritten to count its population and
  map non-positive totals to an EXPLICIT palest stop — the `?? n` darkest fallback is gone,
  so the safety no longer depends on callers gating first. The consumer gates themselves
  moved from `total > 0` to has-rows (`txn_count`) in the same commit, which is exactly the
  gate-relaxation the original entry warned would paint empty days black. Mutation-tested:
  reintroducing darkest-for-≤0 fails three heatmapGrid tests (re-executed post-commit).

- **B22 — web tsconfig lacks an explicit `strict`** (`a20b7e4`, 2026-08-14, on
  `chore/backlog-catch-up-and-strict`; MERGED via PR #138, squash `c58eeae`). Pinned with zero new errors, exactly as the corrected
  entry predicted — TypeScript 6.0.2 was already compiling the project under strict by default,
  so the pin's value is that a TypeScript downgrade or a CLI `--strict false` can no longer
  remove the guarantee silently. Verified by a clean-slate `tsc -b` (tsbuildinfo removed first;
  exit 0). One placement correction: the flag went into `tsconfig.app.json` and
  `tsconfig.node.json`, not the root `tsconfig.json` the entry originally named — the root is a
  solution-style file (`files: []`) whose `compilerOptions` do not reach the referenced
  projects, so the named placement would have pinned nothing. The original entry's probe
  evidence (TS18047/TS7006 fire by default, clean under `--strict false`) is in this file's git
  history. Closed on the same branch that documented it, so `a20b7e4` was a pre-squash hash;
  the squash `c58eeae` was stamped post-merge, per convention.

- **B31 + B32 + B35 + B37 + B38 — the browser pass and its fix batch** (2026-08-10/14, on
  `fix/settings-users-manage-dialog-and-export-overflow`; MERGED via PR #137, squash `a8c484f`,
  released as **v0.43.0**, branch deleted). A 15-area browser pass against the built container
  at the household's real device states (360 phone, ~720 portrait tablet, 1130 coarse tablet
  landscape, 1440 fine desktop), then a six-agent fix batch, a three-reviewer battery
  (14 findings — 11 fixed, 1 closed against the `touch-target.ts` doctrine, 2 duplicates), four
  post-commit mutants killed, and a full in-browser re-verification of every fix on the exact
  merge tip. Owner-reported bugs confirmed fixed: the Import/Export overflow (**B35** — 360=360,
  zero pan) and the Users table's 321px hidden actions column (card list + Manage dialog, member
  gating correct including API 403s).
  - **B37 / B31** — `Button` and `SelectTrigger` carry `coarse:min-h-11`: a POINTER-gated floor,
    proven in both directions on the same element (44px at 1130 coarse, 32px at 1440 fine).
    `Input` took the same floor (~41 sites were 40px), and `SelectItem`'s #131 floor was REGATED
    from unconditional to `coarse:` (owner decision 2026-08-14, matching DropdownMenuItem; the
    "ungated" rationale comment rewritten to record the reversal, tests flipped). `Switch` keeps
    its 24px pill and carries the target on a coarse-gated pseudo-element — fixed TWICE:
    absolute pseudo insets resolve against the PADDING box, so `border-2` shrinks it to 20px and
    `-inset-y-3` (not `-2.5`) lands 44 exact; the first fix shipped on a wrong z-index diagnosis
    and was amended away after re-probing.
  - **B38** — Categories got the B9 card list, a delete CONFIRM where there was none (two taps
    to permanent deletion — proven empirically when a census agent tapped Delete "to measure it"
    and killed a real category; the FK guard limits the blast to transaction-free categories),
    the 409 sentence surfaced as a toast, save failures moved off a banner that sat behind the
    sheet overlay, and editor-sheet focus restore.
  - **B32** — `<Route path="*">` → a real Page-not-found inside the shell, echoed path bounded
    with `[overflow-wrap:anywhere]`.
  - **Beyond the numbered items:** the Manage dialog's dead scroll (a CSS grid auto row keeps
    its natural height under a max-height clamp, so the inner `overflow-y-auto` wrapper existed
    but never engaged — `grid-rows-[minmax(0,1fr)]` on Dialog/AlertDialog Content); every
    trigger-less confirm's focus drop to `<body>` on Cancel/Escape (explicit `onCloseAutoFocus`
    restores, anchors re-queried by data-id at close time because stored elements go stale
    across refetches); TransactionRow's unconditional menu-close opt-out (plain Escape now
    returns to the trigger; only a menu ACTION opts out); light mode (the 20-slot chart palette
    retuned to ≥5.0:1 as text on white — the worst chip was 1.45:1 — and the wordmark badge
    moved off `--primary / 0.4`, which inverted across themes, onto a per-theme `--logo-badge`
    token; the palette validator vendored to `web/scripts/validate_palette.js`); and the 18px
    login/register cross-links brought to 44.
  - **Filed out of the same pass, not fixed:** B39 (palette pair-separation, structural), B40
    (Escape mid-save), B41 (Currencies/API-tokens pans), B42 (tablet-landscape shell gaps), and
    B26's re-measure. Suite grew 1736 → 1834; `tsc -b` and eslint clean. Residual, disclosed:
    the dark badge text sits at 4.33:1 — a logo, WCAG-exempt, polish only.

- **B24 + B29 — /quick's 360px overflow, and the 32px Select options** (PR #131, squash
  `09d1836`, 2026-08-09, released as **v0.42.1**). CategoryChips labels are bounded
  (`min-h-11 max-w-full` + `[overflow-wrap:anywhere]` — wrapping, never truncating), which fixed
  the /quick pan (scrollWidth 506 against a 345 clientWidth, one 489.5px chip) and the home
  screen's version of the same defect; Select menus are capped to Radix's available width with
  option text wrapping; the option row took the 44px touch floor (B29's headline — later regated
  `coarse:` in #137, see that entry); and choosing an option returns focus to the trigger
  instead of `<body>`. The focus half's real cause was OUR fork's `preventDefault` default
  cancelling Radix's own restore — a "bare" Select had isolated nothing because the bare
  component still rendered through our wrapper.

**B9 — Mobile shell, all three slices — SHIPPED**
*(#126 squash `661ad46` **v0.40.0** · #128 `f308c8f` **v0.41.0** · #130 `cbaa4d0` **v0.42.0**
· follow-up #131 `09d1836` **v0.42.1**. Kept at full length because this was the project's
largest stage; the D-numbered defect list below is the closure evidence.)*

**Slice 1 SHIPPED 2026-08-08** — `feat/b9-mobile-shell-slice1`, PR #126, squash `661ad46`,
**v0.40.0**. Carried the B13 / B18-ask-1 / B19 riders (owner: *"fit issues together"*).
Browser-verified at 390×844 on the rebuilt container.

**Slice 2 SHIPPED 2026-08-08** — PR #128, squash `f308c8f`, **v0.41.0**. The phone's panning
tables became card lists, including the dashboard. The slice-2 re-measure had said it "may prove
unnecessary"; measuring said otherwise. **Correction (2026-08-10):** this entry then claimed
"there are no panning tables left on the phone" — FALSE. Slice 2 covered the MAIN APP's tables;
Settings (Users 321px, Currencies 129px) and Categories still panned at 360, which became #137
and B41.

**Slice 3 SHIPPED 2026-08-09** on `feat/reports-mobile-heatmap` — PR #130, squash `cbaa4d0`,
**v0.42.0**. All thirteen defects below plus the heatmap rebuild. The recharts 2.15.4→3.10.1
bump landed FIRST and separately (PR #129, squash `222caba`, **v0.41.1**), deliberately
unbundled: the bump's only real evidence is a before/after browser comparison of eleven charts,
and that baseline is unreadable if the same branch is simultaneously redesigning those charts.

**What slice 3 actually became, stated plainly**, because it is far more than the entry above
described: the heatmap rebuild (D1/D5) plus twelve other display defects, a **44px touch floor on
every tab strip in the app** — they were all 32px, `h-10` minus `p-1`, including both of
QuickAdd's on the daily capture surface — and a **six-site timezone defect** in unrelated files,
where every transaction date rendered a day early west of GMT behind six tests that recomputed
their expectation with the same wrong expression. Three reviews ran (design, UX, and an isolated
adversarial pass that mutation-tested ~65 mutants and returned NOT MERGEABLE on documentation
grounds while confirming every functional claim). Verification targets moved from 390px to
**360px** once the owner supplied the household's real devices — a Galaxy S24 is the narrowest
and a Tab S10 FE at ~720px portrait takes the PHONE layout.

**The finding worth carrying out of this stage:** three separate defects were protected by
comments explaining why they were fine — a dark-mode contrast claim ("both directions hold
because the tokens swap together"), six comment blocks describing a design the same branch had
deleted, and date tests whose comment called the self-referential oracle deliberate. Each read as
considered design and each stopped someone looking.

**The original finding (verified: reproduced** in a browser at 390×844 against the running
container**):** the shell had zero responsive breakpoints — a permanent sidebar plus page
padding left ~262px of content on a 390px phone, dropping to ~70px if the sidebar toggle was hit
(effectively bricked, persisting across launches); tooltips do not work on touch, so nine
unlabelled sidebar icons conveyed nothing; Reports and Settings overflowed by ~290px; dialogs
had no height limit. The phone was also capture-only — quick-add always dated to today and could
not edit, so correcting yesterday's wrong amount required a laptop (resolved by slice 2's card
lists plus the shared edit sheet).

**Done in slices, re-measured after each:**
- *Slice 1:* the shell and four shared components — sidebar to a slide-out below tablet width
  with visible labels, reduced mobile padding, dialog height limit, scrollable tab strips.
  Fixed the Reports/Settings overflow and the 70px state.
- *Slice 2:* the table pages. Seven columns cannot fit 262px; these needed stacked cards, not
  narrower tables. (The original entry guessed this slice "may prove unnecessary"; the
  measurement contradicted the guess. Recorded because the guess was the confident one.)
- *Slice 3:* Reports charts, re-measured before being built — slice 1's shell fix and its
  `min-w-0` grid fix had already removed the page-level overflow, and what actually remained
  was the following:

- **D1 — the spending heatmap has never been operable by anything but a mouse.** *Reproduced.*
  53 columns × 371 cells at **3×3 CSS px** on a phone. The cell is a bare `<div>` inside a Radix
  `TooltipTrigger asChild`: `tabIndex: -1`, no `role`, no `aria-label`, and a synthesized touch
  tap produces no tooltip. There are no month or weekday labels **at any width** — at 1440px the
  card's entire text content is `"Spending Heatmap 2026 No spend Less More"`. So this is not a
  mobile sizing bug with an a11y side-effect; it is a component that is mouse-only-fine on
  desktop and inoperable everywhere else. Enlarging the cell without giving it an interaction
  model would ship a target you can hit and still learn nothing from. **Shipped in #130** as the
  owner-chosen rebuild: phone = month grid + year strip + tap-a-day sheet (7 columns is the only
  day-level layout that clears 44px at 360); desktop keeps the year grid.
- **D2 — Budget vs Actual thins its month labels non-uniformly, and how badly depends on the
  width.** *Reproduced.* **Corrected 2026-08-09** — this entry first said "7 of 12, unevenly",
  then a single measurement at 390px suggested "6 of 12, evenly". A width sweep showed both
  readings are real and neither describes the defect: **12 of 12 at 1440, NINE at 420
  (`Jan Feb Mar Apr Jun Jul Sep Oct Dec` — May, Aug and Nov gone), six at 390.** recharts'
  `preserveEnd` walks the axis dropping individual labels, so the stride changes mid-axis and
  counting bars off a label lands on the wrong month. **The uneven stride is the damage; the
  count is not.** Root cause: the `XAxis` carried no `interval` while its two siblings did — a
  **wiring-seam** defect, since the helper and its unit test were already correct and simply
  never connected.
- **D3 — four charts paint a tick label outside the SVG.** *Reproduced.* Not a mobile defect and
  not one chart: `Sep'25` sat at **−18.3px at 390 and −6.1px at 1440** on Income vs Expenses,
  Category Trends clipped at −9.8px **despite already carrying the `padding` treatment its
  siblings use**, the Dashboard's Cash Flow chart had the same shape, and Net Cash Flow overhung
  the RIGHT edge. A −30° end-anchored label hangs off its tick leftward by ~35px; only a chart
  rendering a wide `YAxis` had a gutter to absorb it.
- **D4 — three tables render unbounded user text.** *Reproduced.* One 500-char description drove
  Top Merchants to **3,464px inside a 293px** container. Data-dependent and **not
  mobile-specific** — desktop is identically exposed. Same exposure in Recurring Expenses and in
  the Dashboard's surviving desktop table (the phone card path got the fix in slice 2; the table
  did not). A later measurement found the description was not even the binding column — the
  **category name** on the same cell's second line is also user-supplied, also capped at 100
  chars server-side, and was completely unbounded.
- **D5 — the hardcoded `repeat(53, 1fr)` template can receive 54 columns.** *Reproduced by
  mirroring the function over 1900–2100:* leap years starting on a Sunday (1928, 1956, 1984,
  2012, 2040, 2068, 2096) emit 378 cells. Reachable today only by importing rows dated in one of
  those years — and `web/src/lib/dates.ts:161-169` documents a non-contiguous ledger with 1984
  rows as a supported case — otherwise it goes live in 2040. Latent, not burning; fixed with the
  D1 rebuild.
- **D6 — the Savings year-over-year chart has D2's defect too.** *Reproduced.* Labels 6 of 12,
  evenly, so it reads as less broken than Budget vs Actual's uneven 7. Same root cause.
- **D7 — gradient-filled series render a colourless legend swatch.** *Read.* A `url(#…)` fill
  carries no solid colour in the legend payload, `ChartLegendContent` sets
  `backgroundColor: item.color`, React omits an undefined style, and the swatch renders
  transparent. Tracks FILL TYPE, not recharts version — solid-stroked series render correctly in
  both versions.
- **D8 — `ChartLegendContent` keys legend items by `item.value`.** *Read.* Two entries sharing a
  value would collide on the React key. Latent; current configs have distinct labels.
- **D9 — the legend overflows the card on both sides once there are ~10 series.** *Reproduced —
  reported by the owner from his live app, and structurally invisible in dev (2 categories
  against his 9).* recharts fixes the legend wrapper's width; `ChartLegendContent` rendered
  `flex … justify-center` with `flex-wrap: nowrap`, so the excess was pushed out of **both**
  edges symmetrically and the first and last chips were cut in half.
- **D11 — Category Breakdown's labels overlap at a realistic category count.** *Reproduced.*
  Fixed height against a data-driven row count. The repo already had the idiom — Tag Analysis
  derives its height from its row count — and this chart never got it. Height alone was not
  enough: **recharts wraps an over-wide `YAxis type="category"` label and the extra lines grow
  into the next row**, so a 100-char name rendered five lines tall.
- **D12 — the Reports tables waste horizontal space on cell padding.** *Owner-reported.* The
  shared `TableCell p-4` is the residual pan on a phone once the description is bounded.
- **D13 — Savings Goal Progress is squeezed at phone width.** *Owner-reported.* The percentage
  ring and the Goal/Saved/Remaining figures sat side by side, leaving the figures ~90px.

**D2, D3, D4, D6, D7, D8, D9, D11, D12, D13 shipped** on the slice-3 branch in `2d220fc` and
`2950a55`; **D1 and D5 shipped** with the heatmap rebuild in the same PR — along with a defect
none of them named: **every tab strip in the app rendered 32px triggers, not the 44px this
project adopted for touch** (`h-10` minus `p-1`), including QuickAdd's two strips on the daily
capture surface. Slice 1's "44px floor" never covered tab strips.

**The generalisable finding**, worth more than any individual fix: for ROTATED tick labels,
collision is governed by the line's **height**, not its width — adjacent labels clear only when
`spacing × sin(angle) ≥ ink height` — so shortening a label does nothing, and the tick font is
the strongest lever because it appears in both that term and the rotational overhang. Fixing the
clipping with padding *narrows the plot*, which *worsens* collision, so the two invariants must
be asserted together or the second regression ships looking like a fix. Also: **a viewport-keyed
tick cap cannot work here** — this grid is `md:grid-cols-2`, so every chart halves at 768px and
measures worse clearance there than on a phone. Chart width is not monotonic in viewport width.
The measurements are in the header of `web/src/components/reports/chartAxis.seam.test.ts`.

- **B13 — Trash list shows no creator attribution** (built on `feat/b9-mobile-shell-slice1`;
  MERGED via PR #126, squash `661ad46`, released as **v0.40.0**, branch deleted). The finding:
  the main transactions list carried `created_by` after B6, but `deletedTransactionResponse`
  carried `user_id` only, so Trash showed a number where every other surface showed a name.
  - **What shipped:** both halves — the LEFT JOIN on the `ListDeleted*` queries and the wire
    field, plus removal of the frontend's deliberate `Omit<Transaction, 'created_by'>` guard
    (its whole purpose was to make the gap a compile error until it closed).
  - **Not obvious, kept:** the orphaned-creator arm — a row whose creator no longer exists —
    is **mutation-pinned on both queries**. A LEFT JOIN's null branch is exactly the arm that
    an INNER JOIN silently deletes rows through, and no fixture exercises it by accident.

- **B19 — Quick page shows no creator attribution** (owner request; built on
  `feat/b9-mobile-shell-slice1`; MERGED via PR #126, squash `661ad46`, released as **v0.40.0**).
  Display-only gap: `created_by` was already on the wire and `useRecentTransactions` already
  fetched the household-wide list, so the panel was already showing the other member's rows —
  unattributed. `RecentlyAdded.test.tsx` fixtures even set the field; the component ignored it.
  - **What shipped:** the same metadata-with-icon idiom as `TransactionRow`, on saved rows only.
  - **Deliberately excluded:** pending offline rows stay unattributed. They have no server
    identity yet, and inventing one client-side would render an attribution that a failed sync
    could contradict.

- **B8 — Backups carry a "verified" marker that two paths never earn** (`68e3ea6` + docs
  rider `bd510d0` + `62d52b5` + `54348c2` + ordering pin `16df627`, 2026-08-08, on
  `fix/b8-backup-verified-marker`; MERGED via PR #125, squash `3262643`, released as
  **v0.39.1**, branch deleted). The finding: pre-migration snapshots and manual CLI backups
  received the `.sha256` trust sidecar with no verification — the rollback anchor for the
  riskiest operation the app performs was blessed before anything had checked the source
  database on that boot.
  - **What shipped:** `backup.Run` is now stat-guard → baseline → Snapshot → Verify →
    sidecar on every path. A missing source is refused before go-sqlite3 can create a
    phantom on open; a 0-byte source is refused explicitly (an empty DB VACUUMs to exactly
    4096 bytes — the size floor alone never catches the input side). The Verify query
    budget is sized from the source (floor 5 min, 64 MiB/min, ceiling 24 h) so the
    fail-closed migration path cannot boot-loop a large legacy DB — the B3-class hazard;
    `MaxSize` = 10× the live source. First boot: `ExpectTransactionsAbsent` is an
    ASSERTION (the backup must also lack the table; contradictory params rejected before
    I/O), never a skip. Verify failure deletes the rejected copy, surfaces
    `ErrVerifyFailed` through `SnapshotForMigration`'s re-wrap, refuses to migrate with
    wording distinct from a write failure, and exits the CLI non-zero with no file left
    behind. Newly DETECTED old failure: a `DB_PATH` pointing at an empty file went
    exit-0 → exit-1.
  - **Not obvious, kept:** existence guard ≠ validity guard — `os.Stat` proves presence,
    not usability, and both holes produced verified sidecars for backups of nothing; the
    params-struct builder assignments (`QueryBudget`, `MaxSize`) were each deletable
    repo-green until `TestSourceBaseline_WiresSizeDerivedParams` (wiring-seam instance
    seven); the baseline-before-Snapshot ordering has no behavioural seam and is pinned
    via go/ast (`run_ordering_pin_test.go`, the repo's third source-pin precedent); a
    build-failing mutant is not a kill (the 0-byte guard's first mutant left `srcInfo`
    unused). Two-reviewer battery 8/8 findings fixed; isolated deep review MERGEABLE,
    10/10 kill-list mutants plus 6 more re-executed post-commit; container e2e of the
    real `docker exec` flow, 5/5 exact outputs. Disclosed, unpinned: the remove-failure
    cleanup arms (no test seam — the claim side is pinned instead) and the stat guard's
    permission-error arm.

- **B7 — Container healthcheck catches a dead or read-only database** (piece 1 `a16b19d`
  2026-08-07, piece 2 `3cff135` + SCHEMA.md regen `9cfe6d1` 2026-08-08, on
  `fix/b7-healthcheck-data`; MERGED via PR #124, squash `b469651`, released as **v0.39.0**,
  branch deleted). The finding: the Dockerfile HEALTHCHECK scraped `/api/health`, which proves
  only that the web server answers — Dockhand faithfully reported the container healthy while
  its database was corrupt or unwritable, and the first real signal was the daily backup tick,
  up to 24h later.
  - **Piece 1** — the HEALTHCHECK scrapes `/healthz/data`
    (`wget -q -O /dev/null`, `--interval=30s --timeout=10s --start-period=60s --retries=3`),
    pinned by a static Dockerfile test (`internal/api/healthcheck_pin_test.go`, same pattern
    as the version-stamp pin). Measured on a throwaway container: three overwritten data pages
    → 503 (`quick_check: "database disk image is malformed"`) → unhealthy after the retry
    budget with `RestartCount=0` (reports, never crash-loops), while `/api/health` answered
    200 on the same boot — the bug, measured rather than argued. Explicit GET, not `--spider`
    (chi answers HEAD 405; the flag's contract never promises GET); timeout 5s→10s because the
    probe now does real reads behind the one-connection pool.
  - **Piece 2** — a write probe closes the read-only blind spot: migration
    `018_health_write_probe.sql` adds a one-row table with `CHECK (id = 1)` (the storage bound
    lives in the schema, not the caller), and `probeWritable` upserts it once per request
    after the cursor-draining read sub-checks. Failure surfaces SQLite's error string verbatim
    in a `write_probe` field, flips the endpoint to 503, and logs one line. A read-only
    database — exactly the failure B2's restore bug produced — now turns the container
    unhealthy in ~90s instead of hiding ≤24h behind the backup tick. A real write, not
    `BEGIN IMMEDIATE; ROLLBACK`: the cheap gesture never puts a byte on disk, so it cannot see
    `SQLITE_FULL` or `SQLITE_IOERR`. E2E on a throwaway container: chmod-0444 DB + restart →
    503 `attempt to write a readonly database` with `quick_check: "ok"` beside it, pinning
    attribution to the probe alone.
  - **Not obvious, kept:** `/healthz/data` was ALREADY an unauthenticated write path before
    the probe — the checkpoint sweep sets `last_verified_at`, so "this endpoint is read-only"
    was a false premise; `quick_check` dominates the endpoint's DB cost (4.49 ms vs the
    probe's 0.035 ms, ~0.7%); the entrypoint ownership guard self-heals a root-OWNED database
    at boot, but a spendrop-owned mode-0444 file passes the guard — the write probe is what
    catches that class. Three-reviewer battery (data/code/security), every finding fixed
    in-branch or filed (**B17**, the pre-existing flood contention, was measured during this
    work); 7 mutants killed post-commit; one CI red mid-PR (a migration-comment edit without
    `make docs` — the SCHEMA.md drift gate works as designed). Untested, disclosed: the
    `ctx.Err()` gate on the probe's log line has no log-output assertion.

- **B6 — Cheap batch, all 12 open items** (`e46af7e..04fc60c`, twelve commits plus this
  record, 2026-08-07, on `fix/b6-cheap-batch`; MERGED via PR #119, squash `f57a613`, released
  as **v0.38.0**, branch deleted). Built by five implementation agents in three waves, then a
  five-reviewer battery (code / data-correctness / security / UI-UX / design), a two-part fix
  wave, an isolated-worktree deep review (verdict MERGEABLE, 8/8 kill-list mutants re-executed),
  and a browser pass on `:3535` (SW purged first). What each item became, and what was NOT obvious:
  - **B6a** — the stated consequence was WRONG: over-budget alerts read *category* budgets and
    never the monthly total, so a stale monthly budget never drove an alert. The real semantic:
    a month with no budget row falls back to `default_budget` in Reports, so "clear" means "use
    the default" — an intent the API could not express (no DELETE existed; PUT rejects 0 by
    design — zero is a value, deletion is the unset). New `DELETE /api/budgets/{year}/{month}`,
    admin-only, idempotent, mirroring the category-budget delete. **C1, the battery's one
    Critical (UI-UX review):** `MonthlyBudgetsSection` had NO role gating while the sibling
    section did — members were offered saves and clears that could only 403, and the new real
    DELETE would have added un-dischargeable dirty prompts on top. Now read-only for members
    (year picker deliberately stays — it navigates the read view). Browser-verified in both roles.
  - **B6c** — skeleton fixed with held-over rows + the Dashboard dim idiom. The subtle half:
    the stale-count window had to gate BOTH total-scoped controls (Replace All *and*
    Select-all-matching) — gating only the obvious button reopens B4's count-vs-scope shape
    through the side door (found by review, not by the implementer). Residual, deliberate:
    search still fires one request per keystroke; debouncing is a separate behaviour decision.
  - **B6d** — banner clears on that row's next successful save + manual dismiss. Semantics are
    "last failure", not "open failures" (a success on row B retires row A's banner) — accepted,
    the failed row's edit state is the real signal.
  - **B6e/B6f** — the README bullet claimed the Homepage over-budget field was "hard-wired to 0,
    reserved for a future feature" and advised deleting it; the same README's field table
    documented it computed. Three roadmap items marked shipped (alerts, per-category budgets,
    API tokens).
  - **B6g** — both local `.claude/CLAUDE.md` rules rewritten in place: ON-vs-WHERE placement is
    defensive, not load-bearing (the `HAVING total_cents > 0` is the live protection — the
    code's own comment at `export_handlers.go:687-695` says so); and single-row mutations use
    the store while four bulk paths use raw SQL deliberately, with the true invariant being
    audit-in-same-tx + tombstone exclusion + checkpoint hook.
  - **B6h** — HALF the claim had drifted: the tags branch was already conditional (`c3d5290`);
    only update-by-filter's no-tags branch cleared unconditionally. Fixed inside the single
    UPDATE via CASE comparisons against pre-update values. The SQL restatement of the hash
    normalization is provably over-clear-only — SQLite's ASCII `lower()`/space-only `trim()`
    are strictly weaker than Go's folds, so SQL-equal ⟹ Go-digest-equal (security review fuzzed
    300k Unicode-hostile pairs: zero dangerous keeps). Identity fields are exactly
    date/amount/description/category; tags and notes never touch the hash.
  - **B6i** — the shared predicate lives in `export_handlers.go` with FOUR consumers including
    the XLSX export (widened deliberately — export matches the list). Foreign amounts match
    exact cents, never substring, so "1500" cannot sweep 1,500,000 into delete-by-filter. The
    regexp anchors are load-bearing: without them ParseFloat accepts `1e5`/`-100` — found as a
    surviving mutant during implementation, then pinned; the fuzz target's seed corpus alone
    catches the int64-minimum laundering shape. Delete-by-filter lockstep pinned in `6283a09`.
  - **B6j** — `created_by` carries **display_name** under an honest key: push notifications
    already said "Elie added…", so shipping the username would have made the table disagree
    with the phones; `/users` is admin-only so a client cannot resolve names itself. LEFT JOIN
    (a deleted account renders "", "Unknown" in the UI), always-present key (no omitempty —
    the key-absence trap), spoof-proof (name sourced from the session user; probed). Deep
    review's one SURVIVOR: batch-create could emit `""` untested — "the compiler asks the
    question" is a signature, not a test; closed in `6283a09`. **display_name is user-editable
    and NOT unique** — any future filter/group on "who" must key `user_id`. Trash's identical
    blindness filed as B13.
  - **B6k** — panel renders an empty state instead of unmounting; focus lands on the announced
    heading. The empty state follows the *table-panel* idiom (Transactions/Trash single muted
    line), NOT Savings' icon pattern as first claimed — two sanctioned registers exist, match
    by surface type. `role="status"` was dropped in review: a conditionally-mounted live
    region mostly doesn't announce and can double-announce against the toast.
  - **B6l** — walk bounded by the earliest restored row's date, degrading to the full walk if
    any restored row lacks a usable date. The gates are a THIRD deliberately-distinct liveness
    variant (`restored > 0` + an `unboundedFloor` flag) — batch-update's and update-by-filter's
    gates still differ and none may be unified. The `loadErr != nil` degrade arm is
    structurally untestable (RestoreTx re-runs the identical read on the same tx) and its site
    comment says so. The two production comments that pointed at this backlog entry were
    deleted with the fix.
  - **B6m** — all four fixed, plus the SAME WCAG 2.5.3 defect found in BulkEditDialog (the
    dialog users hit first), whose old test matched both the aria-label and the visible text
    and passed either way. Trigger and confirm now carry distinct accessible names; focus is
    anchored after any success that unmounts its own trigger — bulk edit had the identical
    focus-strand one door over (review catch), plus the dialog TITLE was still unpluralized.
  Also from the battery: security M1 independently rediscovered B12 (cross-referenced above);
  M2 filed as B14. **Same-day follow-up on the owner's "fix now instead of waiting"
  (`78745e1..04fc60c`):** the search term now debounces 250ms into the query key (one fetch
  per typing pause; the input itself stays instant), and the window where the box and the
  committed term disagree (`searchPending`) joins `showingPrevious` in a single
  `filterScopeUnsettled` gate on every total-scoped control — a quick type-then-Enter can
  never fire a filter write against a term the box no longer shows. The table dim and
  aria-busy moved to key-changes only, so a partner's save no longer pulses an open table
  (visually or to a screen reader). Building the Enter gate exposed that the committed
  Enter-path guard test was VACUOUS — happy-dom never delivers user-event's `'{Enter}'` to
  React `onKeyDown`, so the test passed with the guard deleted; rebuilt on
  `fireEvent.keyDown` with a positive control, and the nine other files using the idiom are
  filed as B15. An independent worktree re-execution of the full frontend mutant set
  (12/12 killed; both directional pairs died to DIFFERENT tests) confirmed the frontend's
  claimed mutation coverage is real; its one survivor was the deliberately-unreachable
  Select-all-matching handler backstop, now documented as unreachable defence-in-depth
  rather than claimed as the primary gate. Accepted residuals, on the record: ~24px dismiss
  target (matches the chip idiom; revisit in B9), B6h's one-directional ASCII over-clear,
  and B6d's last-failure banner semantics.
- **B4 — "Replace All (4)" renamed more than 4** (`cb8cbc4..0902485`, merged 2026-08-07
  via PR #118, squash `923fe66`, released v0.37.2). **Verified: reproduced** in the browser
  before and after. Replace All now stages a description-only patch through the same
  `update-by-filter` + `buildFilterQuery()` machinery bulk edit uses — the button's count and
  the write's scope come from one serialization — and always confirms via the existing
  BulkEditConfirmDialog before sending. The `bulk-rename` endpoint (which matched on search
  alone and dropped every other filter) is deleted; its unique test coverage (search
  case-insensitivity, LIKE wildcard escaping, admin household-wide description patch,
  updated_at bump) was transferred onto update-by-filter FIRST, plus a previously-missing
  empty-description 400 test. **What was not obvious: the fix needed no new backend behavior
  at all** — update-by-filter already validated, scoped, hash-cleared, audited, and
  checkpoint-skipped description patches correctly; B4 was purely the frontend talking to the
  wrong endpoint. A six-reviewer battery (adversarial-deep, data-correctness, security, UI/UX,
  design, final) found nothing blocking; its fix wave hardened three transferred tests
  (control row, timestamp direction, and a previously-untested `runUpdateByFilterTags`
  member-ownership path — a full-suite mutation survivor), prefixed the failure toast, and
  removed a dead guard-map entry. A stale PWA bundle PUTting the deleted route falls into
  chi's `{id}` sibling and gets a harmless 400 with no write — self-heals on the next launch.
  Found while designing: B12 (bulk counts overstate a member's blast radius), filed
  separately; dialog polish items went to B6m.

- **B3 — A failed upgrade fills the disk** (`08d25ab..201fec5`, merged 2026-08-07
  via PR #117, squash `c44a01c`, released v0.37.1). **Verified: read** (was `reported`;
  confirmed at `migrate.go:93-131` and `snapshot.go:42-53`). The failure path in `RunMigrations`
  now prunes the snapshot directory too, not just the success path, so a crash-looping migration
  under Docker restart converges instead of leaving one full DB copy per attempt.
  **What was not obvious: the prune itself already existed and was correct — only its call site
  was success-only.** The prune existed but was a plain newest-keep; the fix adds the
  failure-path call and teaches the prune two exemptions (bracket anchor + version floor). And
  the backlog's own suggested
  fixes ("prune regardless of outcome, or cap the directory") would have made things worse mid-incident:
  each migration commits in its own transaction and `applyPendingMigrations` applies them in
  sorted order, stopping at the first failure — so a partial apply leaves the EARLIER migrations
  committed while the failing one and everything after it stay pending, and only the snapshot
  taken before the first attempt is still a pristine pre-upgrade copy. Pruning
  indiscriminately on the failure path could delete exactly the file an operator needs to
  recover with. The shipped fix pins that snapshot and prunes around it, bounding a crash-loop
  at `migrationSnapshotKeep` (3) files total, one of which is the pinned pristine copy, instead
  of unbounded growth. Reviewing the branch then found the first version of the pin could
  rotate away from the file it was protecting — it keyed on the HIGHEST pending migration, so
  shipping a new migration during a crash loop released the pristine copy to be pruned
  mid-incident; amended in the same branch to additionally pin the oldest snapshot at or above
  the FIRST pending migration, which a new migration cannot move.
  `snapshot.go`'s seconds-precision filename layout was already deliberate for this: its comment
  anticipates a same-minute restart on immediate Docker retry and exists specifically so
  crash-loop snapshots don't collide and fail to write.

- **B5 — Delete has no confirmation, and members have no Trash** (`ea5f51b..d84f5cd`,
  merged 2026-08-02 via PR #115, squash `ecfa32a`, released v0.37.0). Desktop row delete now
  shows a "Moved to Trash" toast with **Undo** (10s), and a failed delete shows an error toast
  instead of failing silently. The phone capture panel's saved-row delete toast gained the same
  Undo. Trash opened to members: a sidebar entry and badge scoped to their own rows, and a Trash
  page listing only their own tombstoned rows with per-row Restore and batch "Restore N".
  Per-row Purge, "Purge all", and "Restore all" stay admin-only, enforced at both the router
  (`restore-all` / `{id}/purge` / `trash` — there is no `purge-all` segment; emptying the trash is
  `DELETE /transactions/trash`) and the handlers (list/count scoping, owner-or-admin
  restore, batch ownership skip). Bulk-delete dialog and toast copy fixed in the same branch
  (closes B6b) — rows move to Trash and are restorable, not "cannot be undone." TDD'd throughout,
  6 mutants killed in mutation testing, browser-verified end to end for both an admin and a member
  session.
  **Two corrections to the original finding, worth keeping.** The backlog's claim that "the phone
  surface does confirm" was wrong — the phone never confirmed a delete, it only toasted; the real
  gap was desktop's missing feedback and recovery path, not a phone/desktop asymmetry. Separately,
  desktop delete was also silently swallowing FAILED deletes (an unhandled promise rejection, no
  error surfaced to the user) — found while fixing the confirmation gap, and fixed alongside it.
  A review-battery fix wave (`43ee1a6..d84f5cd`) closed three gaps the first pass left open: a
  restore of a tombstoned row that isn't the caller's now 404s for members instead of leaking
  whether the id exists (closes the id-oracle hole); batch restore surfaces a real DB fault as a
  500 instead of masking it as "none of those were yours"; and the batch-restore response gained
  a `skipped` count so a member can tell a refused id from a restored one.

- **B1 step 1 — an edit re-priced a foreign row at today's rate** (`8dc95b4`, 2026-08-02,
  merged via PR #112, squash `4ae8143`). Restating the same foreign money now carries the stored base value forward.
  Changing the amount, the currency, or switching to/from base all still re-price — that is the
  user changing the money. Also closed an unreported half: a tags-only save after a rate change
  moved the amount, which cleared `content_hash` and dropped the row out of import dedupe.
  **The decision lives in the store, on the tx-scoped `before` row, and runs BEFORE the hash
  decision** — both orderings are load-bearing and pinned by mutants. Deciding it in the handler
  would use a row read outside any transaction; see [[handler-preread-is-not-store-read]].
  **Correction to the review's framing, worth keeping:** the lost-update interleave (two people
  editing one row, one edit overwritten) is *pre-existing* on this full-replace endpoint and is
  NOT fixed by this commit — verified at HEAD. What the guard could have introduced, and does
  not, is a stored base value matching neither the request nor the row the tx just read.
  **Step 2 (snapshot the rate per row): CLOSED 2026-08-14** — the `booked_rate` column
  shipped inside B10's migration 019 (PR #139, squash `e25dee6`); see B10 in Closed.
  **Decision 1 — a "re-price at the current rate" action: DEFERRED to step 2, 2026-08-02.** With
  no per-row rate recorded, such an action could only mean "recalculate everything at today's
  number" and could not show which rows it would touch or what they were booked at — which is the
  rejected effective-dated rate table triggered by hand. Once step 2 lands it becomes honest.
  The gap meanwhile: a mistyped rate is only recoverable by undiscoverable folklore, now
  documented in the code beside the rule that causes it. See
  [[foreign-row-booked-rate-is-unrecorded]].
  **Decision 2 — the `≈ $x` preview: FIXED** on `fix/edit-preview-shows-what-saving-stores`
  (stacked on this PR). It showed today's conversion in the edit form, which after this commit
  disagrees with what saving stores. It now shows the stored value with an `(as recorded)`
  qualifier when the two differ, and switches to a live conversion the moment the amount is
  actually edited. Browser-verified end to end: rate moved 89,000 → 100,000, preview held at
  `≈ $16.85 (as recorded)` rather than $15.00, typing 2,000,000 switched it to `≈ $20.00`, and a
  description-only save stored $16.85.
  **Follow-up found while fixing it, NOT done:** Save is still disabled and `toCreatePayload`
  still throws when a row's currency has no rate (`TransactionRow.tsx:305`, `currency.ts:72`).
  After the freeze, a description-only edit of a foreign row needs no rate at all — the server
  carries the value forward — so such a row is uneditable for a reason the server no longer
  shares. Small, and worth doing.

- **B11 — a save waited on the other phone's notification** (`032ba40`, 2026-08-02, merged via PR #112, squash `4ae8143`).
  Push delivery moved off the request path. The gates (type toggle, quiet hours, over-budget
  bypass, actor exclusion) stayed on the request goroutine; only the part that talks to a push
  service detached. The delivery loop was proven byte-identical by diffing the old function body
  against the concatenated new halves, so no gate moved or was dropped.
  **Three things the fix needed that were not obvious.** Detaching while still holding
  `r.Context()` would abort every send the instant the response was written — a fast save with
  zero notifications, and *green tests*: mutating `context.WithoutCancel` away fails exactly ONE
  test in the whole package, because every other push test builds its own uncancelled context.
  A shutdown drain waits for pending deliveries after the HTTP server stops and before the DB
  handle closes (a 404/410 prune is a write); its call site in `main.go` is source-pinned by
  `cmd/spendrop/shutdown_drain_test.go` because deleting that one line otherwise leaves the suite
  green. And the digest's "sent today" marker had to learn the difference between *nothing owed*
  and *owed but dropped* — see [[async-split-redefines-done]].
  **Accepted, not fixed:** activity notifications share a collapse topic, so concurrent fan-outs
  can reach the gateway out of order; serialising delivery would let one stalled gateway delay
  everything behind it.
  **Also accepted — DECIDED by the owner 2026-08-02, do not re-raise:** a dropped `over_budget`
  fan-out leaves its latch set, so that crossing is not re-announced until spend drops under and
  re-crosses. Owner's answer: *"one time notification should be enough, i can always see it on
  the web."* Reaching the drop needs 64 simultaneous fan-outs, which two people entering rows by
  hand will never produce, and the alternative (retry on drop) risks the worse bug of notifying
  on every save. Note the owner has never actually crossed a budget in practice, so the
  notification's real-world behaviour is unobserved, not confirmed.

- **B2 — the documented restore procedure did not work** (`e681ed6`, 2026-08-02, merged via PR #112, squash `4ae8143`).
  Every step now runs in a throwaway container with the app stopped, instead of `docker exec`
  against a container that is by definition restart-looping. Three further defects, each verified
  against a real deployment rather than reasoned about:
  **The rollback was lossy** — step 4 saved the live DB as `.bak` then deleted its `-wal`, which
  in WAL mode holds committed transactions not yet folded in (measured: 4 KB `.bak` against
  630 KB of discarded `-wal`). The safety net was broken in the only direction it would be used.
  **A restored database stayed root-owned** — the guard checked `/app/data`, which a restore never
  touches. It now probes the paths the server must write, derived the way the server derives them.
  A custom `BACKUP_DIR` previously left the app booting normally while every backup failed
  silently, which is the worst shape a backup bug can take.
  **`docker run -v spendrop-data:/data` addressed nothing** — compose prefixes the volume with the
  project name, so it mounted a new empty volume and the drill died on step 2. Fixed in the README
  only: adding a `name:` key would have repointed existing deployments at an empty volume, inside
  a backup procedure. See [[compose-bare-volume-not-addressable]].
  CI now asserts both halves against the real image — a restored DB is openable, AND a healthy
  boot does not trigger a recursive chown (the lazy fix passes the first and regresses the second).

- **v0.36.0 (PR #102, 2026-08-02)** — export memory 250→83 MiB with over-long cells marked
  rather than silently trimmed; text limits enforced in characters not bytes on both sides of the
  wire; imports can no longer file a row under a category the user did not choose; a
  `chi.Walk` + `go/ast` guard that fails the build when a new route can write unbounded ledger
  text. Plus five defects found in passing: API token names bounded stricter than their own
  column, the export row buffer silently mixing rows when its hand-written reset drifted, an
  inline description cap of 200 against everything else's 500, a bulk-edit dialog that swallowed
  validation errors, and an audit User-Agent byte cut that made its own character-based
  truncation a no-op.
