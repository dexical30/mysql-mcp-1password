.PHONY: build test itest

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/mysql-mcp-1password ./cmd/mysql-mcp-1password

test:
	go test ./...

itest:
	go test -tags integration ./...
