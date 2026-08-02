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

### B1 — Editing an LBP transaction re-prices it at today's rate
**Verified: reproduced.** Created a 1,500,000 LBP transaction at rate 89,000, changed the rate,
edited *only the description* — the stored value moved **$16.85 → $15.00** while the LBP figure
stayed 1,500,000.

**Why it happens.** A transaction stores the foreign amount and the dollar value, but not the
rate used. `resolveCurrency` (`internal/api/transaction_handlers.go:231`) re-derives the dollar
value from the *current* rate, and the update handler calls it on every save
(`transaction_handlers.go:651`). The frontend does the same thing on its side
(`web/src/lib/currency.ts:53-66`, `web/src/components/TransactionRow.tsx:89-105`). No migration
of the 17 defines a per-transaction rate column.

**Currently dormant, and armed.** The LBP rate has never been changed since setup (2026-04-12),
so recalculating has always produced the same number. Production shows 6 edits to LBP rows and
**0 silently re-priced**. The first rate change arms it for all 88 LBP rows.

**What is NOT affected** (verified): display reads the stored `transaction.amount` and does not
recompute; server-side the live rate is used in exactly one place, the write path. Changing the
rate does not move any existing value, report, or export. Only *saving an edit* does.

**Fix — two steps, decided 2026-08-02:**
1. *Now, no schema change:* on update, if the foreign amount is unchanged, keep the stored dollar
   value instead of recalculating. An edit then cannot move money.
2. *Later, folded into B7 (refunds migration):* store the rate on the transaction, so each row
   carries the rate it used. For auditability — step 1 already fixes the bug.

**Rejected: a rate table with effective dates.** It creates two sources of truth that can
disagree, and correcting a typo in the table would silently re-value recorded transactions —
rebuilding this exact bug with more machinery. Accounting systems snapshot the rate onto the
record for the same reason an invoice prints its FX rate.

**Effort:** small (step 1). **Blocks:** nothing, but do it before the first LBP rate change.

---

### B2 — The documented restore procedure does not work
**Verified: read** (the ownership mechanism confirmed directly; the full drill not executed).

`README.md:589-615` is the only restore procedure, and it fails three ways:

1. **The restored database is unwritable.** Step 4 copies the backup in as root.
   `entrypoint.sh:19-23` decides whether to fix ownership by checking the owner of the
   *directory* — which a restore never changes — so it skips the `chown` and the restored file
   stays root-owned. The app cannot write, and the container restart-loops.
   **What you'd see:** "attempt to write a readonly database" and a restarting container, which
   reads exactly like corruption — so the natural next move is to try another backup, which
   fails identically. No data is lost; the ledger is just unreachable until someone finds a fix
   that appears nowhere in the docs.
2. **The first two steps cannot run during the emergency they exist for.** They use
   `docker exec`, which will not attach to a restarting container.
3. **Nothing tests it.** The backup tests prove the file is a valid database, not that the app
   boots on it. No restore round-trip exists in CI or anywhere else.

**Fix:** correct the README procedure (under an hour). Optionally follow with an automated
restore round-trip test — recommended, but not a blocker.

**Effort:** trivial for the docs. **Why it ranks here:** it is the last line of defence for a
household whose only copy of years of finances lives on one NAS, and the README currently says
*"a backup you have never restored from is a wish, not a backup"* above a drill that doesn't work.

---

## Then

### B3 — A failed upgrade fills the disk
**Verified: reported.** `internal/database/migrate.go:94-104`. A full database snapshot is taken
before every migration and old ones are pruned only after a *successful* run. A failing migration
exits, Docker restarts, another snapshot is taken, nothing is pruned — roughly 60 copies an hour
into the same volume as the live database and all backups. This exact crash-loop has happened
before (the TrueNAS boot loop). Once the disk is full the database cannot be written even after
the migration is fixed, turning a recoverable upgrade failure into a second incident.
**Effort:** trivial — prune regardless of outcome, or cap the directory.

### B4 — "Replace All (4)" renames more than 4
**Verified: reported.** The button counts rows matching the current filters
(`web/src/pages/Transactions.tsx:751`); the request sends only the search text
(`Transactions.tsx:418-435` → `internal/api/transaction_handlers.go:1319-1329`). Date range,
category, amount, tags and type are dropped. Filter to Groceries in January, search "spinney",
click **Replace All (4)**, and every matching description in the whole ledger is rewritten
across all years and both members. No confirmation, no undo; the toast reports the real number
afterwards. Descriptions feed import duplicate-detection, so it propagates.
**Effort:** medium — send the filters, or make the count honest and add a confirmation.

### B5 — Delete has no confirmation, and members have no Trash
**Verified: reported.** `web/src/components/TransactionRow.tsx:379-383` deletes immediately, one
item below Edit, with no prompt and no "moved to trash" feedback. The phone surface *does*
confirm; desktop does not. Trash routes are admin-only (`internal/api/router.go:217-225`), so a
member's misclick is unrecoverable without an admin.
**Effort:** small. **Check first:** whether the second household account is admin or member.

### B6 — Cheap batch
All **reported**, none independently verified. Grouped because they are individually trivial:

| | Item | Why it matters |
|---|---|---|
| B6a | Clearing a monthly budget silently does nothing — says "No changes to save" and keeps driving alerts. The Category Limits editor directly below does the opposite. | Two editors on one page with contradictory rules; the top one lies |
| B6b | Bulk-delete says "This cannot be undone" — false; rows go to Trash and nothing purges them | Discourages a legitimate workflow, hides the real recovery path |
| B6c | Transactions table blinks to a skeleton on every keystroke and re-fetches | Most-used screen for finding an old transaction |
| B6d | A row-level error banner appears once and never clears, with no dismiss | Later *successful* edits happen under a banner saying they failed |
| B6e | README recommends deleting the "Over budget" Homepage field as unimplemented — it shipped and works | Following the docs removes a working alert |
| B6f | README Roadmap lists three shipped features as unbuilt | — |
| B6g | Two rules in `.claude/CLAUDE.md` are factually wrong (see *Corrections* below) | Bad premises propagate into agent briefs |
| B6h | Bulk edit clears the duplicate-detection fingerprint even when no value changed | Re-importing the same sheet later can silently double rows |
| B6i | Search matches description only — not category, notes, or the foreign amount | Empty result is indistinguishable from "doesn't exist" |
| B6j | Nothing shows who entered a transaction, though the app knows | A member learns a row is her spouse's only after Save returns "forbidden" |

### B7 — No external signal when something breaks
**Verified: reported.** The Docker health check only proves the web server is listening and never
touches the database. `/healthz/data` is thorough but nothing in the deployment reads it, and it
reports "ok" while the database is unwritable for up to a day (the first thing that flips it is a
failed backup, and backups run every 24h). Push notifications work to both phones; no operational
failure uses them. If the container dies overnight, the first sign is someone opening the app and
seeing what looks like an offline shell — while backups have silently stopped.
**Effort:** small — the signal and the channel both exist and are simply not connected.
**Needs input:** whether an external uptime monitor already watches the box (see below).

### B8 — Backups carry a "verified" marker that two paths never earn
**Verified: reported.** Scheduled backups are genuinely checked. Pre-migration snapshots and
manual backups get the same trust marker with no verification, and the README states the marker
means integrity and row-count checks passed. The pre-migration snapshot is the rollback anchor
for the riskiest operation the app performs, and is taken before anything has checked the source
database on that boot.
**Effort:** small — run the same check on both paths, and correct the README.

---

## Queued stages

### B9 — Mobile shell (next stage, owner-decided)
**Verified: reproduced** in a browser at 390×844 against the running container.
The shell has zero responsive breakpoints: a permanent sidebar plus page padding leaves ~262px of
content on a 390px phone, dropping to ~70px if the sidebar toggle is hit — effectively bricked,
and it persists across launches. Tooltips do not work on touch, so nine unlabelled sidebar icons
convey nothing. Reports and Settings overflow by ~290px. Dialogs have no height limit (bites in
landscape or with the keyboard up, not in portrait today).

**Also:** the phone is capture-only. Quick-add always dates to today and cannot edit; the
recently-added list only offers delete, capped at six rows. Correcting yesterday's wrong amount
requires a laptop.

**Do it in slices, and re-measure after the first:**
- *Slice 1 (1–2 days):* the shell and four shared components — sidebar to a slide-out below
  tablet width with visible labels, reduced mobile padding, dialog height limit, scrollable tab
  strips. This alone fixes the Reports/Settings overflow and the 70px state.
- *Slice 2 (multi-week):* the table pages. Seven columns cannot fit 262px; these need stacked
  cards, not narrower tables.
- *Slice 3:* Reports charts.

Several pages land within ~26px of correct once the shell stops taking a third of the screen, so
**slice 2 may prove unnecessary — measure before committing to it.** Reassess the native-Android
question after slice 1; the current phone experience is not evidence about what the web app can be.

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

- **Is the second household account admin or member?** Decides how sharp B5 is — a member has no
  Trash and therefore no undo.
- **Is push enabled in production?** Off by default. If on, a push-gateway outage (plausible on
  Lebanese connectivity) makes every transaction save hang up to 30s per subscribed device and
  then fail, even though the row was already saved. Worth checking regardless of B7.
- **Does anything already monitor the box from outside?** If yes, B7 shrinks considerably.
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

Two rules in `.claude/CLAUDE.md` are wrong and were briefed to agents as fact:

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

- **v0.36.0 (PR #102, 2026-08-02)** — export memory 250→83 MiB with over-long cells marked
  rather than silently trimmed; text limits enforced in characters not bytes on both sides of the
  wire; imports can no longer file a row under a category the user did not choose; a
  `chi.Walk` + `go/ast` guard that fails the build when a new route can write unbounded ledger
  text. Plus five defects found in passing: API token names bounded stricter than their own
  column, the export row buffer silently mixing rows when its hand-written reset drifted, an
  inline description cap of 200 against everything else's 500, a bulk-edit dialog that swallowed
  validation errors, and an audit User-Agent byte cut that made its own character-based
  truncation a no-op.
