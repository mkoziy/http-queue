# Plan: golangci-lint Setup

## Overview
Set up a strict, curated golangci-lint configuration for the Go HTTP queue project before implementation begins. Add Makefile targets so linting is required by `make build` while keeping `make test` independent for fast iteration.

## Success Criteria
- [ ] `.golangci.yml` enables a strict curated linter set without relying on `enable-all`
- [ ] `Makefile` includes `lint`, `build`, and `test` targets, with `build` depending on `lint`
- [ ] `golangci-lint run ./...` completes successfully after module bootstrap
- [ ] `make build` runs lint before `go build ./...`
- [ ] `make test` runs `go test -race ./...` without invoking lint

### Task 1: Bootstrap Go Module for Linting
- [x] Run `go mod init github.com/mkoziy/http-queue` if `go.mod` does not already exist
- [x] Run `go mod tidy` to create a valid baseline module state
- [x] Verify `go list ./...` works, accounting for the repository currently having no Go source packages

### Task 2: Add Strict golangci-lint Configuration
- [ ] Create `.golangci.yml` at the repository root
- [ ] Enable curated linters: `errcheck`, `staticcheck`, `gosimple`, `unused`, `govet`, `ineffassign`, `stylecheck`, `gocritic`, `revive`, `nilnil`, `nilerr`, `wrapcheck`, `contextcheck`, `exhaustive`, `bodyclose`, `gochecknoinits`, and `goerr113`
- [ ] Add linter settings for `exhaustive`, `wrapcheck`, and `revive`
- [ ] Configure issue limits with `max-issues-per-linter: 0` and `max-same-issues: 0`

### Task 3: Add Makefile Targets
- [ ] Create a root `Makefile`
- [ ] Add `.PHONY` declarations for `lint`, `build`, and `test`
- [ ] Add `lint` target running `golangci-lint run ./...`
- [ ] Add `build` target depending on `lint` and running `go build ./...`
- [ ] Add `test` target running `go test -race ./...` without depending on `lint`

### Task 4: Update Main HTTP Queue Plan
- [ ] Modify `docs/plans/20260521-http-queue.md`
- [ ] Update the Bootstrap task to mention golangci-lint must pass before implementation proceeds
- [ ] Update the Makefile section so it reflects `build` depending on `lint`
- [ ] Keep `make test` documented as independent from lint

### Task 5: Verify Lint Setup
- [ ] Run `golangci-lint version` and confirm the installed binary is available
- [ ] Run `golangci-lint run ./...`
- [ ] Run `make lint`
- [ ] Run `make build` and confirm lint executes before build
- [ ] Run `make test` and confirm it only runs tests with the race detector
