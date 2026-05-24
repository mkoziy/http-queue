.PHONY: lint build test run e2e docker-build docker-build-multi

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

docker-build-multi:
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE):$(VERSION) \
		-f $(DOCKERFILE) \
		.
