.PHONY: build install run run-only test check

build:
	go build -o bin/factory ./cmd/factory

install:
	./install.sh

run: 
	go run ./...

test:
	go test ./...

check: test
	go vet ./...
