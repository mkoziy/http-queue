---
description: Publish an http-queue release via the repo Makefile. Validates semver like 0.1.1, requires a clean main branch, pushes multi-arch GHCR images, and creates the GitHub release from the diff log.
argument-hint: "<version like 0.1.1>"
allowed-tools: Bash
---

Publish a repo release using the existing `make release` workflow. Treat `$ARGUMENTS` as the required version string.

## Pre-checks (hard stops)

1. `$ARGUMENTS` must be present and must match `X.Y.Z` like `0.1.1`.
2. `git branch --show-current` must equal `main`.
3. `git status --porcelain` must be empty.
4. `gh auth status` must succeed.
5. Docker must already be authenticated to `ghcr.io`.

If any check fails, stop and report the blocker. Do not stash, reset, or guess.

## Inspect state

Run:

```bash
git branch --show-current
git status --short
git tag --sort=-version:refname | head
```

## Release

Run:

```bash
make release VERSION=$ARGUMENTS
```

Do not expand the Makefile by hand unless the target itself is broken. The Makefile owns tag creation, multi-arch image push, and GitHub release creation.

## Report back

Reply with:

- the version that was released
- whether `ghcr.io/mkoziy/http-queue:$ARGUMENTS` and `:latest` were pushed
- the tag name
- the GitHub release URL if the command prints it

## Don't

- Don't release from any branch other than `main`.
- Don't run ad hoc `git tag`, `docker buildx`, or `gh release create` commands if `make release` is available.
- Don't include unrelated local changes in the release.
