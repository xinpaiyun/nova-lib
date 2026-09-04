.PHONY: tidy build test

tidy:
	go mod tidy

build:
	GOFLAGS=-p=2 GOMAXPROCS=2 go build ./...

test:
	GOMAXPROCS=2 go test ./...
