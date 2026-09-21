BIN     := bin/agent-tui
# Windows will not execute a file without an .exe extension: PowerShell and cmd
# both refuse to resolve it, silently, so a build that omits it produces a
# binary that appears to do nothing at all.
ifeq ($(OS),Windows_NT)
BIN     := bin/agent-tui.exe
endif
PKG     := ./cmd/agent-tui
GOFLAGS := -trimpath
LDFLAGS := -s -w

.PHONY: all build run test race bench lint fmt tidy install clean

all: build

## build a stripped binary for the host platform
build:
	@mkdir -p bin
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)
	@ls -lh $(BIN) | awk '{print "  " $$9 "  " $$5}'

## run against the current directory
run: build
	./$(BIN)

test:
	go test ./...

race:
	go test -race ./...

## the hot paths: highlighting, indexing, search, transcript rendering
bench:
	go test -run=XXX -bench=. -benchmem ./internal/highlight/ ./internal/search/ ./internal/ui/

lint:
	go vet ./...
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "run 'make fmt'"; exit 1)

fmt:
	gofmt -w .

tidy:
	go mod tidy

install:
	go install $(GOFLAGS) -ldflags="$(LDFLAGS)" $(PKG)

clean:
	rm -rf bin
