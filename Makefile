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

.PHONY: coverage

# Produce the coverage and test-execution reports SonarQube reads (see
# sonar-project.properties):
#   coverage.out              — Go cover profile over every package
#   go-test-report.json       — `go test -json` stream (test count / pass / duration)
#   web/coverage/lcov.info    — vitest (v8) over web/src
#   web/coverage/sonar-report.xml — vitest-sonar-reporter (Generic Test Execution)
# All four are gitignored. `sonar-scan` (the host wrapper) runs this target
# before uploading an analysis; run it by hand to inspect coverage locally.
# The vitest step needs the same Node the rest of the frontend toolchain uses;
# on a host whose Node ships the experimental global localStorage (26+), set
# NODE_OPTIONS=--no-experimental-webstorage in the environment first — it is
# deliberately not baked in here because the flag does not exist on older Node.
# `go list` filter: web/node_modules ships a stray Go package (flatted) that
# `./...` would otherwise sweep into the profile.
coverage:
	go test -json $$(go list ./... | grep -v /node_modules/) -coverprofile=coverage.out > go-test-report.json
	@printf 'go tests: %s passed, %s failed\n' "$$(grep -c '"Action":"pass".*"Test":' go-test-report.json)" "$$(grep -c '"Action":"fail".*"Test":' go-test-report.json)"
	cd web && npm run test:coverage
