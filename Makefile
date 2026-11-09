VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test lint run dist clean

build:
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/tg-secretary ./cmd/tg-secretary

test:
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

run: build
	./bin/tg-secretary serve

# Cross-compile release archives into dist/, e.g. `make dist VERSION=v0.1.0`.
dist:
	scripts/dist.sh $(VERSION)

clean:
	rm -rf bin dist
