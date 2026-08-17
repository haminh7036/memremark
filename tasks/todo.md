Goal: fix 5 code-review findings in internal/storage + hook cmd, each with a regression test.
Acceptance: go build ./... and go test ./... pass; each fix has a test that fails before / passes after.

[x] Locate existing pattern/impl (storage.go, reader.go busy_timeout pattern, hook main.go/main_test.go)
[x] Fix 1: GetOrCreateWing race -> atomic INSERT ... ON CONFLICT(path) DO UPDATE ... RETURNING id
[x] Fix 2: busy_timeout pragma in storage.Open (+ SetMaxOpenConns(1), needed after empirical test caught a per-connection pragma gap)
[x] Fix 3: dir 0700 + file chmod 0600 in storage.Open
[x] Fix 4: RecentSummaries ORDER BY created_at DESC, id DESC
[x] Fix 5: replace root-dependent HOME test with ENOTDIR-forcing file-as-dir-component test
[x] go build ./... && go test ./... green
[x] commit
[x] write final report to .superpowers/sdd/2026-08-10-core-engine-implementation/final-review-area1-fix-report.md
