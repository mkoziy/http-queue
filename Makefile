.PHONY: lint build test

lint:
	golangci-lint run ./...

build: lint
	go build ./...

test:
	go test -race ./...
