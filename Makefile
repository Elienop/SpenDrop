# SpenDrop build/dev helpers.
#
# Most day-to-day work uses `go run ./cmd/spendrop`, `docker compose up`,
# and `npm run dev` directly. The targets here exist for chores that are
# easy to forget to run by hand — chiefly, regenerating the committed
# schema documentation after a migration lands.

.PHONY: docs

# Regenerate docs/SCHEMA.md from the files under internal/database/migrations/.
# The tool is deterministic: running `make docs` twice produces an identical
# file. CI enforces this (see .github/workflows/pr.yml) so a forgotten regen
# fails the PR rather than rotting silently.
docs:
	go run ./cmd/schema-doc > docs/SCHEMA.md
