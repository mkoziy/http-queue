# Plan: Prod Dockerfile and Release Makefile

## Overview
Add a production Docker image build for `http-queue` and Makefile commands for local image builds and releases. The release flow will create and push a version tag, then build and push multi-architecture images to GHCR.

## Context
- Go module: `github.com/mkoziy/http-queue`
- Current Makefile has `lint`, `build`, `test`, and `run`
- App listens on `PORT` and defaults Badger storage to `/tmp/http-queue`
- GitHub remote points to `github.com:mkoziy/http-queue.git`, so default GHCR image can be `ghcr.io/mkoziy/http-queue`

## Success Criteria
- [ ] `make docker-build` builds a production container image locally
- [ ] `make release VERSION=1.0.0` creates and pushes git tag `1.0.0`
- [ ] `make release VERSION=1.0.0` builds and pushes multi-arch images to `ghcr.io/mkoziy/http-queue`
- [ ] `make test` passes
- [ ] `docker run --rm -e ADMIN_USER=admin -e ADMIN_PASS=secret -p 8080:8080 ghcr.io/mkoziy/http-queue:1.0.0` starts the service

### Task 1: Add production Docker build files
**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

- [x] Create a multi-stage `Dockerfile` using the project’s Go version from `go.mod`
- [x] Build a static production binary with `CGO_ENABLED=0`
- [x] Use a small runtime image and run the service as a non-root user
- [x] Expose port `8080` and set the binary as the container entrypoint
- [x] Add `.dockerignore` entries for `.git`, build artifacts, temp files, local Badger data, and editor files

### Task 2: Add Docker build targets to Makefile
**Files:**
- Modify: `Makefile`

- [x] Add configurable variables: `VERSION`, `IMAGE`, `PLATFORMS`, and `DOCKERFILE`
- [x] Add a `docker-build` target that builds a local image tagged as `$(IMAGE):$(VERSION)`
- [x] Add a `docker-build-multi` or equivalent target using `docker buildx build`
- [x] Ensure Docker targets depend on existing verification where appropriate, such as `test` or `build`

### Task 3: Add release target with tag publishing
**Files:**
- Modify: `Makefile`

- [x] Add a `release` target requiring `VERSION=...`
- [x] Validate the version format, e.g. `1.0.0`
- [x] Fail fast if the git working tree is dirty or the tag already exists
- [x] Create an annotated git tag for `$(VERSION)`
- [x] Push the tag to `origin`

### Task 4: Push multi-architecture images to GHCR
**Files:**
- Modify: `Makefile`

- [ ] Extend `release` to build and push images to GHCR after pushing the git tag
- [ ] Use `docker buildx build --platform $(PLATFORMS) --push`
- [ ] Tag pushed images as `$(IMAGE):$(VERSION)` and `$(IMAGE):latest`
- [ ] Default `PLATFORMS` to `linux/amd64,linux/arm64`
- [ ] Document that the user must already be authenticated with `docker login ghcr.io`

### Task 5: Document Docker and release workflow
**Files:**
- Modify: `README.md`

- [ ] Add a Docker section showing `make docker-build`
- [ ] Add a sample `docker run` command with required `ADMIN_USER` and `ADMIN_PASS`
- [ ] Add a release section showing `make release VERSION=1.0.0`
- [ ] Mention default GHCR image name and supported platforms
- [ ] Mention GHCR authentication prerequisite before release
