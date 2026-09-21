.PHONY: all build test run serve release clean

BIN := build/tickr
ifeq ($(OS),Windows_NT)
BIN := build/tickr.exe
endif

all: build

build:
	go build -ldflags '-w -s' -o $(BIN) ./cmd/tickr

test:
	go vet ./...
	go test ./...

# Start the GUI + scheduler from the repo folder (uses ./tickr.json).
serve: build
	$(BIN) serve

# Build one report and exit.
run: build
	$(BIN) generate

release:
	goreleaser release --snapshot --clean

clean:
	rm -rf build dist output
