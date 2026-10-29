.PHONY: build install run run-only test check

build:
	go build -o bin/factory ./cmd/factory

install:
	go install ./cmd/factory

run: 
	go run ./...

test:
	go test ./...

check: test
	go vet ./...
