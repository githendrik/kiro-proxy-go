.PHONY: build run test clean

BINARY_NAME=kiro-proxy-go

build:
	go build -o $(BINARY_NAME) .

run:
	go run .

test:
	go test ./...

clean:
	rm -f $(BINARY_NAME)
