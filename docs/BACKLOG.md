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
released as v0.39.1 — see Closed.*

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

### B20 — users.role integrity rests on one handler
**Verified: read** (found 2026-08-08 during B18 merge-semantics mutation testing; second half
during the session-survival test work). Two hardening gaps in the same surface: (1)
`users.role` has NO CHECK constraint — under a mutated handler, `role = ''` writes cleanly
into SQLite; `handleUpdateUser`'s whitelist plus its merge fallback are the only guards of
that column's well-formedness, and any future path writing role bypasses both. (2)
`handleUpdateUser` discards `DeleteSessionsByUserID`'s error (`_ =`) — a failed DELETE after
a demotion leaves admin-capable cookies alive and still returns 200; untestable today without
a fault-injection seam.
**Effort:** small — a migration adding the CHECK, plus an error check.

### B21 — display_name has no charset gate, and it reaches push-notification bodies
**Verified: read** (B9 security audit 2026-08-08; PRE-EXISTING — B18 only made renames
routine). Unlike `username` (which has a charset gate), `display_name` is length-bounded
only, at both write sites (register and the admin PUT): control characters, newlines, and
bidi overrides are accepted. Every render site is safe (JSX text nodes; no export path
carries it). The one non-text sink: Web Push — `internal/api/notifications.go:79` interpolates
the actor's display name into the body via `fmt.Sprintf`, so a `\n` in a name forges extra
notification lines. Fix in ONE pass: sanitize at both write sites; consider the notification
builder too.
**Effort:** small.

### B22 — web tsconfig lacks an explicit `strict`
**Verified: reproduced** (B9 deep review 2026-08-08; **materially corrected 2026-08-08** during
the recharts bump). `strict` is absent from `web/tsconfig.json` and `web/tsconfig.app.json`.

**The correction, because the original entry would have caused someone to budget a pass that
does not exist.** It read *"Enabling it is its own pass with an unknown error count — do not
fold into another branch. Effort: unknown until tried; likely medium."* That was written from
the absence of the flag, without checking what the flag defaults to. **TypeScript 6.0 defaults
`strict` to true, and `web/package.json` pins `typescript 6.0.2`** — so the project has been
compiling under strict all along. Verified with a standalone probe using the repo's own
compiler:

```
$ tsc --version                          → Version 6.0.2
$ tsc --noEmit probe.ts                  → TS18047 'x' is possibly 'null'
                                           TS7006 Parameter 'y' implicitly has an 'any' type   (exit 2)
$ tsc --noEmit --strict false probe.ts                                                          (exit 0)
```

Corroborated independently by the recharts 2→3 migration: four of its fourteen type errors were
`TS7006` implicit-any, a strict-family diagnostic that cannot fire under a non-strict compiler.

**Revised scope: add `"strict": true` to the two tsconfigs for documentation value, with zero
expected errors.** The instruction not to fold it into another branch no longer applies — it was
a consequence of the unknown error count, and the count is known to be zero.
**Effort:** trivial. Worth doing because the guarantee currently rests on a compiler default
that a future TypeScript downgrade or a `--strict false` in CI would silently remove.

### B23 — offline-capture hold filing on identity change is untested
**Verified: read** (found 2026-08-08 while guarding the stale-verify race). `markNeedsSignIn`
files the queued-capture hold when the session identity changes. The race tests assert it is
NOT called with the new user's id under a stale failure, but nothing pins the positive
semantics — "the hold is filed against the DEPARTED id, if at all." A queue-semantics
question, distinct from the auth race that exposed it.
**Effort:** small.

### B24 — /quick overflows the page horizontally on a 360px phone
**Verified: reproduced** (found 2026-08-09 during B9 slice 3, on the rebuilt container at a
360px viewport). `document.scrollWidth` is **506px against a clientWidth of 345** — the page
itself pans sideways. The cause is a single `CategoryChips` button rendering a long category
name at **489.5px**, unbounded. Same class as the D4 table cells: user-supplied text whose only
ceiling is a server constant (`MaxCategoryNameLength = 100`, `internal/api/limits.go:401`).

**This is the owner's daily capture surface** and the most-used screen on the primary platform,
which is why it outranks its size. Pre-existing — not introduced by slice 3 — and left unfixed
only because `CategoryChips.tsx` was outside the branch's scope.
**Effort:** small — the same bounding idiom the three tables now use.

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

### B26 — Recurring Expenses still pans 113px on a phone
**Verified: reproduced** (2026-08-09, after the D4/D12 fixes). 375.9px of table in a 263px box at
360. The remainder is five columns of header words plus cell padding, **not** the description —
that is now bounded. Reaching zero needs the slice-2 card-list treatment rather than more padding
tuning, which is why it was stopped here.
**Effort:** medium — it is a presentation change, not a spacing tweak.

### B27 — the intensity scale returns the DARKEST stop for a zero total
**Verified: reproduced** (executed the function during B9 slice 3, 2026-08-09).
`buildIntensityScale`'s out-of-population fallback is `rank.get(total) ?? n`, and a zero-total day
is genuinely out of population — the heatmap endpoint emits no row for a day with no expenses, so
`lookup.get(date) ?? 0` hands the scale a value it was never built from. Measured: `scale(0)` → 1
(the darkest stop), `scale(10)` → 0.3.

**It is invisible only because both consumers gate on the total before reading it** — the chip
paints under `total > 0`, and `chipTextClass` returns on `total === 0` before the opacity is
consulted. So this is a **live coupling, not dead code**: dropping either gate paints every empty
day black. Documented at the site and pinned by `buildIntensityScale.outOfPopulation`, which
pins the CURRENT behaviour — so the test would not catch a gate being removed.

**Fix:** return the palest stop (or a sentinel) for a value outside the population, so the guard
does not depend on callers remembering to check first.
**Effort:** small — production logic, deliberately not stacked onto the slice-3 branch.

### B28 — four sheets scroll their own header away
**Verified: reproduced for one, read for three** (B9 slice 3, 2026-08-09). `ui/sheet.tsx` wraps
ALL children — `SheetHeader` included — in its `overflow-y-auto` body scroller, so a sheet with a
long body loses its title and any summary figure on scroll. Measured on the heatmap's day sheet
before the fix: the day's total left the visible box after **60px** of scroll with 8 of 20 rows
still on screen.

Slice 3 added an optional `header` slot rendered OUTSIDE the scroller and used it for the day
sheet. Four consumers still pass `SheetHeader` as a child: `MobileNav` (a `border-b p-4` header,
where the border makes the scroll-away most visible), `TransactionEditSheet`, `Categories`, and
the Transactions `Filters` sheet. `TransactionEditSheet` was MEASURED and its form never
overflows (`scrollHeight === clientHeight`, `maxScroll: 0`), so its defect is latent; the other
two were not measured.
**Effort:** small per consumer — the opt-in is one prop.

### B29 — every Select in the app has 32px options
**Verified: reproduced** (measured at a 360px viewport, 2026-08-09, while building the Settings
phone picker). `SelectItem`'s stock `py-1.5` around `text-sm` gives a **32px** option row. The
TRIGGER meets the 44px floor everywhere; the option that is actually tapped to choose does not —
the wrong half to get right.

Fixed at the Settings call site only. It is app-wide: **13 non-test consumers** import
`SelectItem`. Changing `ui/select.tsx` moves every menu in the app at once, which is why it was
not folded into a branch that had already been reviewed.

**Correction (2026-08-09).** This entry originally said "QuickAdd's category picker is the one to
check first, since it is on the daily capture surface". **That is false** — `QuickAdd.tsx` imports
`CategoryChips`, not `Select`, and is not among the 13 consumers. B29 does **not** touch the daily
capture surface; **B24 alone does**. The claim was written from memory rather than from a grep and
was caught only when it was about to be handed to an agent as a premise. The consumers that do sit
on a frequently-used path are `TransactionRow`, `TransactionEditSheet`, `Dashboard` and
`PaginationBar`.

Related and worth doing in the same pass: **Radix Select drops focus to `<body>` on selection** —
verified three ways (user-event, a 2s `waitFor` to rule out late arrival, and real Chrome key
events), and reproduced on a bare Select with nothing to swap, so it is the primitive rather than
any consumer. Settings' picker now restores focus explicitly via `onCloseAutoFocus`; **every other
Select in the app still drops it.** Same shape as the recorded Sheet-without-a-Trigger finding.
**Effort:** small — one component, but verify every consumer at 360 and 1440.

### B30 — the Settings section is not reflected in the URL
**Verified: read** (2026-08-09). `location.search` stays empty when a section is chosen, on both
the desktop tab strip and the new phone picker. A `?tab=` value is honoured *into* the page but
never written *out* of it, so a section cannot be bookmarked or shared and a back-navigation does
not restore it. Pre-existing and shared by both branches, not introduced by the phone picker.
**Effort:** small.

### B31 — Select TRIGGERS are 32–40px, app-wide
**Verified: measured** (Chrome, 2026-08-09, true device metrics at 360px and 1440px, after B29's
option fix landed). B29's own filing claimed "the TRIGGER meets the 44px floor everywhere" and used
that to argue only the option was wrong. **That claim was false.** `SelectTrigger`'s stock class is
`h-10` = 40px, and several call sites shrink it further:

| Trigger | Height | Where |
|---|---|---|
| ColorThemePicker | **32px** | desktop Sidebar footer only (`Sidebar.tsx:224`, `hidden md:flex`, and only when expanded) — NOT the app header, and NOT every page. Its other mount, `MobileNav.tsx:248`, already passes `h-11`. Corrected 2026-08-09; the original "app header, every page" was wrong. |
| PaginationBar "Rows per page" | **32px** at 1440, 44px at 360 | Transactions |
| Reports "Time Period" / "Budget Year" | **36px** | all four Reports tabs |
| Dashboard "Month" / "Year" | **36px** | `/` |
| Budgets year/month | **40px** | Budgets |
| Categories type, BulkEdit category, TransactionRow inline, TransactionEditSheet | **40px** | various |
| Settings section picker | 44px | the only one that meets the floor (`h-11`, set in B9 slice 3) |

So the touch-floor gap was *both* halves, not just the option. Deliberately NOT folded into the
B29 branch: `SelectTrigger` carries `h-10` as a fixed height that call sites override with their
own `h-9`/`h-11`, so moving it to a floor changes chrome density on nine surfaces at two widths
and needs its own verification pass — the same reasoning that kept the option fix off the slice-3
branch. **Effort:** medium. Do not fix by adding `min-h-11` blindly; several of these sit in tight
toolbars where a taller control reflows the row.

### B33 — Enter cannot pick a category in the phone edit sheet
**Verified: reproduced by me** (Chrome, 360px, real key events, 2026-08-09). In
`TransactionEditSheet` the category Select opens on Enter and moves on ArrowDown, but Enter to
choose does **nothing**: 21 options stay mounted, the trigger's value stays `FoodS`, focus stays on
an option, and the sheet stays open. Mouse and Space both work. The same Select inside
`BulkEditDialog` handles Enter correctly, so it is the call site, not the primitive.

Cause (reported by the build agent, code site confirmed): the `onKeyDownCapture` guard at
`TransactionEditSheet.tsx:244`. React synthetic events propagate along the REACT tree, so a
portalled `SelectContent` is still a React descendant of that div, and `stopPropagation()` in the
CAPTURE phase kills Radix's own key handler before it runs. An identical guard sits at
`TransactionRow.tsx:184`.

Proposed one-word fix — `onKeyDownCapture` → `onKeyDown` — is the agent's, **not verified by me**;
the guard exists to keep Enter from submitting the form, so the bubble-phase version needs its own
test proving both halves (Select commits, form does not submit). **NOT FIXED:** a distinct defect
with a subtle fix, on a branch that had already grown twice. **Effort:** small, plus a real test.

### B34 — Dashboard's Month/Year still drop focus to `<body>`
**Verified: reported by the build agent, NOT independently reproduced by me.** Both widths,
keyboard and mouse, and unaffected by the B29 central fix because the trigger's DOM node is
*replaced* during the value change — `useDashboard` keys on `['dashboard', year, month]`, so a
period change flips `isLoading` and `Dashboard.tsx:282` returns the whole-page skeleton, leaving
Radix to focus a detached element. Agent's proposed `placeholderData: keepPreviousData` would also
activate the already-written `refetching && 'opacity-60'` at `Dashboard.tsx:328`, currently dead
for period changes. **NOT FIXED** — it changes data-fetching semantics, and this repo has a
recorded incident where `keepPreviousData` staled a count. Treat the diagnosis as unverified until
someone reproduces it.

### B35 — Settings → Import / Export overflows 36px at 360
**Verified: reported by the build agent, NOT independently reproduced by me.** A
`whitespace-nowrap` Button with its right edge at 396 against a 360 viewport. Pre-existing and
unrelated to the Select work. **Effort:** small.

### B36 — a member can take another member's display name, and attribution follows
**Verified: reproduced by the security audit of `PATCH /api/auth/me`** (2026-08-09), and the
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
**Effort:** small. **Owner has not yet decided whether to take it.**

### B37 — `Button` has no touch floor; most tap targets miss 44px on a tablet in landscape
**Verified: measured** (Chrome, true device metrics, `(pointer: coarse)` confirmed `true` in-page,
2026-08-09), at 1130px — the Galaxy Tab S10 FE in landscape, which is above `md` and therefore
renders the DESKTOP tree while still being a touch screen.

| Page | buttons under 44px |
|---|---|
| `/transactions` | **115 of 132** |
| `/categories` | 25 of 26 |
| `/` (Dashboard) | 9 of 12 — incl. "Today" at 36px beside a now-44px Select, tops misaligned 4px |
| `/budgets` | 7 of 11 |

At 360 the phone tree is largely fine (**1 of 65** on `/transactions`), so this is specifically the
tablet-landscape gap. The B31 Select floor made it visible rather than causing it: a floored Select
beside an unfloored Button is where the raggedness shows.
**Owner approved taking it on the B36/B37 branch, 2026-08-09.** Mechanism is the same `coarse:`
variant B31 registered; the hard part is `Button`'s several size variants and the icon buttons,
which need BOTH axes.

### B38 — the Categories page never got the B9 phone treatment
**Verified: measured** at 360 (2026-08-09). **22 of 23 buttons under 44px**, row actions at
**32px**, and its table hides **394px** behind a sideways scroll (`Name`, `Type`, `Actions`) —
worse than the Users table that prompted this work. B9 slice 2 converted the main app's tables to
phone card lists and Settings was the known gap; Categories was missed by both.
**Owner approved taking it on this branch, 2026-08-09** — same card-list treatment as Users.

### B32 — an unknown route renders a blank shell, not a 404
**Verified: reproduced** (2026-08-09). The dashboard lives at `/`; navigating to `/dashboard` — a
plausible URL a user would type or bookmark — renders the full app chrome with an **empty** main
container: no content, no 404, no redirect, and no console error. Found by walking into it while
verifying B29 and briefly mistaking it for a rendering defect in the branch.
**Effort:** small — a catch-all route. **Note:** worth checking whether the SW precache serves the
shell for arbitrary paths, since that is what makes the blank state look like a successful load.

---

## Queued stages

### B9 — Mobile shell (all three slices built; slice 3 awaiting merge)

**Slice 1 SHIPPED 2026-08-08** — `feat/b9-mobile-shell-slice1`, PR #126, squash `661ad46`,
**v0.40.0**. Carried the B13 / B18-ask-1 / B19 riders (owner: *"fit issues together"*).
Browser-verified at 390×844 on the rebuilt container.

**Slice 2 SHIPPED 2026-08-08** — PR #128, squash `f308c8f`, **v0.41.0**. The phone's panning
tables became card lists, including the dashboard. **There are no panning tables left on the
phone.** The slice-2 re-measure had said it "may prove unnecessary"; measuring said otherwise.

**Slice 3 BUILT 2026-08-09** on `feat/reports-mobile-heatmap`, eight commits, awaiting the owner's
push/PR go. All thirteen defects below shipped, plus the heatmap rebuild. Moves to Closed with
the squash hash at merge. The recharts 2.15.4→3.10.1 bump landed FIRST and separately (PR #129,
squash `222caba`, **v0.41.1**), deliberately unbundled: the bump's only real evidence is a
before/after browser comparison of eleven charts, and that baseline is unreadable if the same
branch is simultaneously redesigning those charts.

**What slice 3 actually became, stated plainly**, because it is far more than the entry above
described: the heatmap rebuild (D1/D5) plus twelve other display defects, a **44px touch floor on
every tab strip in the app** — they were all 32px, `h-10` minus `p-1`, including both of
QuickAdd's on the daily capture surface — and a **six-site timezone defect** in unrelated files,
where every transaction date rendered a day early west of GMT behind six tests that recomputed
their expectation with the same wrong expression. Three reviews ran (design, UX, and an isolated
adversarial pass that mutation-tested ~65 mutants and returned NOT MERGEABLE on documentation
grounds while confirming every functional claim). Verification targets moved from 390px to
**360px** once the owner supplied the household's real devices — see B24's note; a Galaxy S24 is
the narrowest and a Tab S10 FE at ~720px portrait takes the PHONE layout.

**The finding worth carrying out of this stage:** three separate defects were protected by
comments explaining why they were fine — a dark-mode contrast claim ("both directions hold
because the tokens swap together"), six comment blocks describing a design the same branch had
deleted, and date tests whose comment called the self-referential oracle deliberate. Each read as
considered design and each stopped someone looking.

**Verified: reproduced** in a browser at 390×844 against the running container.
The shell has zero responsive breakpoints: a permanent sidebar plus page padding leaves ~262px of
content on a 390px phone, dropping to ~70px if the sidebar toggle is hit — effectively bricked,
and it persists across launches. Tooltips do not work on touch, so nine unlabelled sidebar icons
convey nothing. Reports and Settings overflow by ~290px. Dialogs have no height limit (bites in
landscape or with the keyboard up, not in portrait today).

**Also:** the phone is capture-only. Quick-add always dates to today and cannot edit; the
recently-added list only offers delete, capped at six rows. Correcting yesterday's wrong amount
requires a laptop.

**Done in slices, re-measured after each:**
- *Slice 1:* the shell and four shared components — sidebar to a slide-out below tablet width
  with visible labels, reduced mobile padding, dialog height limit, scrollable tab strips.
  Fixed the Reports/Settings overflow and the 70px state. **Shipped.**
- *Slice 2:* the table pages. Seven columns cannot fit 262px; these needed stacked cards, not
  narrower tables. **Shipped.** (The original entry guessed this slice "may prove unnecessary";
  the measurement contradicted the guess. Recorded because the guess was the confident one.)
- *Slice 3:* Reports charts. **In flight.**

**Slice 3 was re-measured before being built, and the original framing was partly stale.**
Slice 1's shell fix and its `min-w-0` grid fix had already removed the page-level overflow: at
390px all four Reports tabs measure `scrollWidth == clientWidth == 390`, chart surfaces are
308px and contained, and Net Cash Flow renders correctly. What actually remains:

- **D1 — the spending heatmap has never been operable by anything but a mouse.** *Reproduced.*
  53 columns × 371 cells at **3×3 CSS px** on a phone. The cell is a bare `<div>` inside a Radix
  `TooltipTrigger asChild`: `tabIndex: -1`, no `role`, no `aria-label`, and a synthesized touch
  tap produces no tooltip. There are no month or weekday labels **at any width** — at 1440px the
  card's entire text content is `"Spending Heatmap 2026 No spend Less More"`. So this is not a
  mobile sizing bug with an a11y side-effect; it is a component that is mouse-only-fine on
  desktop and inoperable everywhere else. Enlarging the cell without giving it an interaction
  model would ship a target you can hit and still learn nothing from.
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
  rows as a supported case — otherwise it goes live in 2040. Latent, not burning; fixed because
  the file is open.
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

**D2, D3, D4, D6, D7, D8, D9, D11, D12, D13 SHIPPED** on this branch in `2d220fc` and `2950a55`
— along with a defect none of them named: **every tab strip in the app rendered 32px triggers,
not the 44px this project adopted for touch** (`h-10` minus `p-1`), including QuickAdd's two
strips on the daily capture surface. Slice 1's "44px floor" never covered tab strips. D1 and D5
(the heatmap itself) are in progress.

**The generalisable finding**, worth more than any individual fix: for ROTATED tick labels,
collision is governed by the line's **height**, not its width — adjacent labels clear only when
`spacing × sin(angle) ≥ ink height` — so shortening a label does nothing, and the tick font is
the strongest lever because it appears in both that term and the rotational overhang. Fixing the
clipping with padding *narrows the plot*, which *worsens* collision, so the two invariants must
be asserted together or the second regression ships looking like a fix. Also: **a viewport-keyed
tick cap cannot work here** — this grid is `md:grid-cols-2`, so every chart halves at 768px and
measures worse clearance there than on a phone. Chart width is not monotonic in viewport width.
The measurements are in the header of `web/src/components/reports/chartAxis.seam.test.ts`.

Reassess the native-Android question now that the phone experience is no longer evidence of a
web-app limit.

### B10 — Refunds cannot offset a category
**Verified: reproduced** (schema dumped from all 17 migrations applied to a scratch database).
`CHECK(amount_cents > 0)` physically refuses a negative amount. Spend $50 on groceries, get $20
back, and there is no way to make Groceries read $30. Logging the refund as income hides it from
every expense calculation.

**What you'd see:** dashboard spending and remaining budget overstated by the refund; every
category budget overstated; every report tab and the Excel export overstated; the over-budget
push alert fires early in any month containing a refund — a wrong notification on both phones.
Savings figures come out right, because they subtract income from expense.

**Decided:** signed `amount_cents`, not a linked offset row. Do not relitigate.

**Blast radius:** one migration rebuilding the transactions table, two Go validation layers,
three places in import that strip the sign, eleven report queries gaining a zero-or-negative
case, six frontend sites. Expect one immediate visible break: a negative expense renders as
`--$50.00`, because the display component adds a minus sign on top of the formatter's.

**Fold B1 step 2 into this migration** — same table, same rebuild, one risk instead of two.

---

## Needs a decision or a fact from the owner

- ~~Is push enabled in production?~~ **Answered 2026-08-02: yes, and both members receive
  notifications.** Promoted to B11 — it is a live defect, not a question.
- ~~**Does anything already monitor the box from outside?** If yes, B7 shrinks considerably.~~
  **Answered 2026-08-02: yes, Dockhand watches every stack's container health.** That is what
  made B7 a two-line change to what the `HEALTHCHECK` scrapes rather than a new alerting
  channel; both pieces shipped in v0.39.0 — see B7 in Closed.
- **Import + foreign currency:** import accepts an `original_currency` column, so a sheet of
  back-dated LBP rows is valued at the rate current *when you import*. Harmless today (the rate
  has never moved); real once it does. Fixing it properly needs the rate column from B1 step 2
  first, and then a decision about what rate a back-dated import should use.

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
2. **"Mutations go through `TransactionStore` only — never raw SQL."** Four bulk paths do use raw
   SQL. All four are deliberate and documented in place. The rule is wrong, not the code.

Neither is enforced by a test. A reviewer trusting the first could relax the real filtering
condition *and* move the predicate, believing one was safe because the other was checked.

---

## Closed

*(Move items here with their commit hash rather than deleting them.)*

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
  **Step 2 (snapshot the rate per row) is still open**, folded into B10.
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
