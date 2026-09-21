.PHONY: all build test run serve release clean

BIN := build/breaklist
ifeq ($(OS),Windows_NT)
BIN := build/breaklist.exe
endif

all: build

build:
	go build -ldflags '-w -s' -o $(BIN) ./cmd/breaklist

test:
	go vet ./...
	go test ./...

# Start the GUI + scheduler from the repo folder (uses ./breaklist.json).
serve: build
	$(BIN) serve

# Build one report and exit.
run: build
	$(BIN) generate

release:
	goreleaser release --snapshot --clean

clean:
	rm -rf build dist output
