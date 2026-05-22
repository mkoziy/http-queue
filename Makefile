.PHONY: lint build test run

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
	@if [ -n "$$(find . -name '*_test.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		go test -race ./...; \
	else \
		echo "no test files found, skipping tests"; \
	fi

run:
	@if [ -n "$$(find . -name '*.go' -not -path './.git/*' 2>/dev/null | head -1)" ]; then \
		go run .; \
	else \
		echo "no Go source files found, skipping run"; \
	fi
