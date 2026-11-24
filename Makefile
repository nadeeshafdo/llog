# Built from git metadata; falls back gracefully outside a repo checkout.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell git log -1 --format=%cd --date=short 2>/dev/null || date -u +%F)
LDFLAGS := -X main.appVersion=$(VERSION) -X main.appCommit=$(COMMIT) -X main.appDate=$(DATE)

.PHONY: build install test vet clean

build:
	go build -ldflags "$(LDFLAGS)" -o llog .

install:
	go install -ldflags "$(LDFLAGS)" .

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f llog
