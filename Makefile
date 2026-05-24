.PHONY: lint build test run e2e docker-build docker-build-multi release

# ── Configurable variables ─────────────────────────────────
VERSION     ?= latest
IMAGE       ?= ghcr.io/mkoziy/http-queue
PLATFORMS   ?= linux/amd64,linux/arm64
DOCKERFILE  ?= Dockerfile

lint:
	@if [ -n "$$(find . -name '*.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		golangci-lint run ./...; \
	else \
		echo "no Go source files found, skipping lint"; \
	fi

build: lint
	@if [ -n "$$(find . -name '*.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		go build ./...; \
	else \
		echo "no Go source files found, skipping build"; \
	fi

test:
	@if [ -n "$$(find . -name '*.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		go test -race ./...; \
	else \
		echo "no Go source files found, skipping tests"; \
	fi

run:
	@if [ -n "$$(find . -name '*.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		go run .; \
	else \
		echo "no Go source files found, skipping run"; \
	fi

e2e:
	@if [ -n "$$(find . -name '*.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		./scripts/e2e-local.sh; \
	else \
		echo "no Go source files found, skipping e2e"; \
	fi

# ── Docker targets ───────────────────────────────────────
docker-build: test
	docker build \
		-t $(IMAGE):$(VERSION) \
		-f $(DOCKERFILE) \
		.

docker-build-multi: test
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE):$(VERSION) \
		-f $(DOCKERFILE) \
		--load \
		.

# ── Release target ────────────────────────────────────────
release: test
	@if [ -z "$(VERSION)" ] || [ "$(VERSION)" = "latest" ]; then \
		echo "ERROR: VERSION is required (e.g. VERSION=1.0.0)"; \
		exit 1; \
	fi
	@if ! echo "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "ERROR: VERSION must be a semver string (e.g. 1.0.0)"; \
		exit 1; \
	fi
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "ERROR: working tree is dirty; commit or stash changes first"; \
		exit 1; \
	fi
	@if git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null 2>&1; then \
		echo "ERROR: tag $(VERSION) already exists"; \
		exit 1; \
	fi
	@echo "Building and pushing multi-arch images to $(IMAGE)..."
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE):$(VERSION) \
		-t $(IMAGE):latest \
		-f $(DOCKERFILE) \
		--push \
		.
	@echo "Images pushed. Creating and pushing git tag..."
	git tag -a "$(VERSION)" -m "Release $(VERSION)"
	git push origin "$(VERSION)"
	@echo "Release $(VERSION) published: $(IMAGE):$(VERSION)"
