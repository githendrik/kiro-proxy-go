.PHONY: build run test clean install

BINARY_NAME=kiro-proxy
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o $(BINARY_NAME) .

install:
	go build -ldflags "-X main.Version=$(VERSION)" -o $(shell brew --prefix)/bin/kiro-proxy .

run:
	go run .

test:
	go test ./...

clean:
	rm -f $(BINARY_NAME)
