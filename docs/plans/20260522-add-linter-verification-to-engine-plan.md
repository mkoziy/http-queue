# Plan: Add Linter Verification to Engine Plan

## Overview
Update `docs/plans/20260522-http-queue-engine-implementation.md` so every task explicitly runs `make lint` after its task-specific verification step and requires the linter to be green before continuing. Preserve the existing HTTP queue engine implementation scope while making lint a per-task quality gate.

## Context
- `go.mod` declares module `github.com/mkoziy/http-queue` with Go `1.26`.
- `Makefile` defines `make lint`, which runs `golangci-lint run ./...` when Go files exist.
- `.golangci.yml` enables strict linting including `errcheck`, `staticcheck`, `govet`, `revive`, `wrapcheck`, `err113`, and related checks.
- The target plan already includes task-level test commands for most tasks.
- Task 1 and Task 14 already mention `make lint`, but wording should be normalized to require green output before continuing.

## Success Criteria
- [ ] Every `### Task N:` section in `docs/plans/20260522-http-queue-engine-implementation.md` includes an explicit `make lint` checklist item
- [ ] Each task’s lint checklist item states that lint must be green before continuing
- [ ] Existing lint items are normalized without duplicate lint gates in the same task
- [ ] The final verification task still includes `make lint`
- [ ] `rg "### Task|make lint" docs/plans/20260522-http-queue-engine-implementation.md` shows lint coverage for every task

### Task 1: Add Missing Per-Task Linter Gates

**Files:**
- Modify: `docs/plans/20260522-http-queue-engine-implementation.md`

- [x] Review all `### Task N:` sections and identify tasks missing an explicit `make lint` checklist item
- [x] Add `- [ ] Run \`make lint\` and ensure it is green before continuing` after each task’s local test/build verification item
- [x] For tasks without a test command, add the lint checklist item as the final checklist item in that task
- [x] Preserve existing task order, files blocks, feature scope, and implementation wording

### Task 2: Normalize Existing Lint Wording

**Files:**
- Modify: `docs/plans/20260522-http-queue-engine-implementation.md`

- [x] Update Task 1’s existing lint checklist item to consistently say `Run \`make lint\` and ensure it is green before continuing`
- [x] Keep Task 14’s final `make lint` verification item and ensure it clearly requires green output
- [x] Avoid duplicating lint items within the same task
- [x] Confirm each task has exactly one task-level linter gate unless the final verification task intentionally includes the global lint check

### Task 3: Verify Plan Consistency

**Files:**
- Modify: `docs/plans/20260522-http-queue-engine-implementation.md`

- [ ] Count all `### Task` headings and confirm each corresponding task includes `make lint`
- [ ] Check that lint gates appear after implementation/test verification rather than before code changes
- [ ] Ensure markdown formatting remains valid with unchecked `- [ ]` checklist items
- [ ] Run `rg "### Task|make lint" docs/plans/20260522-http-queue-engine-implementation.md` to inspect task/lint coverage visually
