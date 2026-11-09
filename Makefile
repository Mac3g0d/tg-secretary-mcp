.PHONY: build test lint run

build:
	CGO_ENABLED=0 go build -o bin/tg-secretary ./cmd/tg-secretary

test:
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

run: build
	./bin/tg-secretary serve
