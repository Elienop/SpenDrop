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
`feat/delete-undo-member-trash` — see Closed.*

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

### B6 — Cheap batch
All **reported**, none independently verified. Grouped because they are individually trivial:

| | Item | Why it matters |
|---|---|---|
| B6a | Clearing a monthly budget silently does nothing — says "No changes to save" and keeps driving alerts. The Category Limits editor directly below does the opposite. | Two editors on one page with contradictory rules; the top one lies |
| B6b | ~~Bulk-delete says "This cannot be undone" — false; rows go to Trash and nothing purges them~~ **Closed with B5** (`cae004c`, 2026-08-02) — dialog and toast copy now say rows move to Trash and are restorable | Discourages a legitimate workflow, hides the real recovery path |
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

**ANSWERED 2026-08-02: the owner runs Dockhand, which monitors every stack for uptime and
container health.** So the alerting channel already exists and nothing needs to be chosen or
installed. What it is currently watching is the problem: `Dockerfile:72` points HEALTHCHECK at
`/api/health`, which only proves the web server answers. Dockhand faithfully reports a container
as healthy while its database is unusable.

**So B7 becomes two pieces of different size:**
1. *Small, do it:* point HEALTHCHECK at `/healthz/data` instead. It already runs
   `PRAGMA quick_check`, the cached full integrity result, transaction counts, schema version and
   the checkpoint freshness sweep, and it 503s on a real problem. Dockhand's existing monitoring
   then inherits all of that for free. Safe under `restart: unless-stopped` — an unhealthy
   container is not restarted, so a degraded check reports rather than crash-loops. Keep the
   interval at ≥30s; the endpoint's own comment warns against making it a hot path.
2. *Still open, and the subtler half:* **a read-only database would still report healthy.**
   Every sub-check in `/healthz/data` is a READ — `quick_check` passes fine on a database that
   cannot be written. That is exactly the failure mode B2's restore bug produced ("attempt to
   write a readonly database"). The first thing that actually flips the endpoint is a failed
   backup, up to 24h later. Catching it promptly needs a cheap write probe as a sub-check.
   Without it, wiring up piece 1 is a real improvement but leaves the specific failure the
   owner is most likely to hit still invisible.

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

- ~~Is push enabled in production?~~ **Answered 2026-08-02: yes, and both members receive
  notifications.** Promoted to B11 — it is a live defect, not a question.
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

- **B5 — Delete has no confirmation, and members have no Trash** (`ea5f51b..cae004c`,
  2026-08-02, unreleased, on `feat/delete-undo-member-trash`, PR pending). Desktop row delete now
  shows a "Moved to Trash" toast with **Undo** (10s), and a failed delete shows an error toast
  instead of failing silently. The phone capture panel's saved-row delete toast gained the same
  Undo. Trash opened to members: a sidebar entry and badge scoped to their own rows, and a Trash
  page listing only their own tombstoned rows with per-row Restore and batch "Restore N".
  Per-row Purge, "Purge all", and "Restore all" stay admin-only, enforced at both the router
  (`restore-all` / `purge` / `purge-all`) and the handlers (list/count scoping, owner-or-admin
  restore, batch ownership skip). Bulk-delete dialog and toast copy fixed in the same branch
  (closes B6b) — rows move to Trash and are restorable, not "cannot be undone." TDD'd throughout,
  6 mutants killed in mutation testing, browser-verified end to end for both an admin and a member
  session.
  **Two corrections to the original finding, worth keeping.** The backlog's claim that "the phone
  surface does confirm" was wrong — the phone never confirmed a delete, it only toasted; the real
  gap was desktop's missing feedback and recovery path, not a phone/desktop asymmetry. Separately,
  desktop delete was also silently swallowing FAILED deletes (an unhandled promise rejection, no
  error surfaced to the user) — found while fixing the confirmation gap, and fixed alongside it.

- **B1 step 1 — an edit re-priced a foreign row at today's rate** (`8dc95b4`, 2026-08-02,
  unreleased). Restating the same foreign money now carries the stored base value forward.
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

- **B11 — a save waited on the other phone's notification** (`032ba40`, 2026-08-02, unreleased).
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

- **B2 — the documented restore procedure did not work** (`e681ed6`, 2026-08-02, unreleased).
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
