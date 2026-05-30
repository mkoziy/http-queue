.PHONY: lint build test run e2e chaos docker-build docker-build-multi release

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

chaos:
	@if [ -n "$$(find . -name '*.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		go run ./tests/chaos -duration=15s -publishers=3 -workers=5 -seed=1; \
	else \
		echo "no Go source files found, skipping chaos"; \
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
		--push \
		.

# ── Release target ────────────────────────────────────────
release: test
	@if [ -z "$(VERSION)" ] || [ "$(VERSION)" = "latest" ]; then \
		echo "ERROR: VERSION is required (e.g. VERSION=0.1.1)"; \
		exit 1; \
	fi
	@if ! echo "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "ERROR: VERSION must be a semver string (e.g. 0.1.1)"; \
		exit 1; \
	fi
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "ERROR: working tree is dirty; commit or stash changes first"; \
		exit 1; \
	fi
	@if [ "$$(git branch --show-current)" != "main" ]; then \
		echo "ERROR: must be on main branch to release"; \
		exit 1; \
	fi
	@if git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null 2>&1; then \
		echo "ERROR: tag $(VERSION) already exists locally"; \
		exit 1; \
	fi
	@if ! git ls-remote origin >/dev/null 2>&1; then \
		echo "ERROR: cannot reach remote origin"; \
		exit 1; \
	fi
	@if git ls-remote --tags origin "refs/tags/$(VERSION)" | grep -qF "refs/tags/$(VERSION)"; then \
		echo "ERROR: tag $(VERSION) already exists on remote"; \
		exit 1; \
	fi
	@if ! command -v gh >/dev/null 2>&1; then \
		echo "ERROR: gh CLI is required for releases"; \
		exit 1; \
	fi
	@if ! gh auth status >/dev/null 2>&1; then \
		echo "ERROR: gh CLI is not authenticated"; \
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
	@echo "Creating and pushing git tag..."
	git tag -a "$(VERSION)" -m "Release $(VERSION)"
	git push origin "$(VERSION)"
	@echo "Creating GitHub release with diff log..."
	@prev_tag="$$(git tag --sort=-version:refname | grep -vx '$(VERSION)' | head -n1)"; \
	notes_file="$$(mktemp)"; \
	trap 'rm -f "$$notes_file"' EXIT; \
	{ \
		echo "# Release $(VERSION)"; \
		echo; \
		if [ -n "$$prev_tag" ]; then \
			echo "## Diff since $$prev_tag"; \
			echo; \
			git log --no-merges --pretty=format:'- %h %s' "$$prev_tag..HEAD"; \
		else \
			echo "## Initial release"; \
			echo; \
			git log --no-merges --pretty=format:'- %h %s'; \
		fi; \
		echo; \
		echo; \
		echo "## Container image"; \
		echo; \
		echo "- \`$(IMAGE):$(VERSION)\`"; \
		echo "- \`$(IMAGE):latest\`"; \
	} > "$$notes_file"; \
	gh release create "$(VERSION)" --title "$(VERSION)" --notes-file "$$notes_file"
	@echo "Release $(VERSION) published: $(IMAGE):$(VERSION)"
