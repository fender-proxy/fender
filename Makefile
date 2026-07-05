BINARY   := fender
VERSION  := 0.1.0
BIN_DIR  := ./bin
LDFLAGS  := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install run test clean

## build: compile fender into ./bin/fender
build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) .

## install: install fender into $GOPATH/bin (or $HOME/go/bin)
install:
	go install $(LDFLAGS) .

## run: run fender locally in debug mode (does not install)
run:
	go run . --log-level debug

## test: run all unit tests
test:
	go test -v ./...

## clean: remove build artefacts
clean:
	rm -rf $(BIN_DIR)

## help: print available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
