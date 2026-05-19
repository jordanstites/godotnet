# godotnet Makefile

GO         ?= go
PROTOC     ?= protoc
PROTO_DIR   = internal/proto
PROTO_FILES = $(wildcard $(PROTO_DIR)/*.proto)

# Put $GOPATH/bin on PATH so protoc finds protoc-gen-go.
GOBIN      := $(shell $(GO) env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.PHONY: all test test-race vet build proto clean

all: vet test

test:
	$(GO) test ./...

test-race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

proto:
	$(GO) generate ./$(PROTO_DIR)/...

clean:
	$(GO) clean ./...
