> **Superseded** by [20260522-golangci-lint-setup-2.md](./20260522-golangci-lint-setup-2.md) — this plan used stale linter names (gosimple, stylecheck, goerr113) and v1 config format.

# golangci-lint Setup — Implementation Plan

## Context

Add a super strict golangci-lint configuration to the http-queue project before any Go code is written. Linting must pass before `make build` succeeds. `make test` is independent (no lint dependency) for fast iteration.

## Design Decisions

- **golangci-lint** — standard Go linter aggregator
- **Curated strict set** (~20 linters) — high signal-to-noise, no enable-all
- **lint is a build prerequisite** — `make build` depends on `make lint`; `make test` does not
- **`.golangci.yml`** for config

## Linter Selection

| Linter | Reason |
|---|---|
| `errcheck` | All errors must be handled |
| `staticcheck` | Deep static analysis |
| `gosimple` | Simplification suggestions |
| `unused` | Dead code detection |
| `govet` | Correctness checks |
| `ineffassign` | Detect useless assignments |
| `stylecheck` | Go style guide enforcement |
| `gocritic` | Idiomatic Go patterns |
| `revive` | Opinionated but practical style rules |
| `nilnil` | Prevents `return nil, nil` footgun |
| `nilerr` | Prevents returning nil error when err != nil |
| `wrapcheck` | Errors from external packages (badger, ulid) must be wrapped |
| `contextcheck` | context.Context must be passed through correctly (sweeper) |
| `exhaustive` | Exhaustive switch on enums (job Status type) |
| `bodyclose` | HTTP response bodies must be closed (integration tests) |
| `gochecknoinits` | No `init()` functions — forces explicit initialization |
| `goerr113` | No `fmt.Errorf` without `%w` — enforces error wrapping |

## Tasks

- [ ] Install golangci-lint (document required version in plan)
- [ ] Create `.golangci.yml` with curated linter set
- [ ] Create `Makefile` with `lint`, `build`, `test` targets (`build` depends on `lint`)
- [ ] Verify `make lint` runs clean on an empty Go module (after `go mod init`)
- [ ] Update existing plan (`20260521-http-queue.md`) bootstrap section to note linter must pass before implementation proceeds

## Makefile Shape

```makefile
.PHONY: lint build test

lint:
	golangci-lint run ./...

build: lint
	go build ./...

test:
	go test -race ./...
```

## `.golangci.yml` Shape

```yaml
linters:
  enable:
    - errcheck
    - staticcheck
    - gosimple
    - unused
    - govet
    - ineffassign
    - stylecheck
    - gocritic
    - revive
    - nilnil
    - nilerr
    - wrapcheck
    - contextcheck
    - exhaustive
    - bodyclose
    - gochecknoinits
    - goerr113

linters-settings:
  exhaustive:
    default-signifies-exhaustive: true  # allow default: case to satisfy exhaustive switch
  wrapcheck:
    ignoreSigRegexps:
      - \.New.*\(  # allow errors.New, fmt.Errorf with %w
  revive:
    rules:
      - name: exported
        disabled: false

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```
