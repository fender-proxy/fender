BINARY   := fender
VERSION  := 0.1.0
BIN_DIR  := ./bin
LDFLAGS  := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install run test clean

## build-frontend: build and save the fender-frontend OCI image tarball
build-frontend:
	@mkdir -p internal/proxy/assets
	docker build -t fender-frontend:local ./fender-frontend
	docker save -o internal/proxy/assets/fender-frontend.tar fender-frontend:local

## build: compile fender into ./bin/fender
build: build-frontend
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) .

## install: install fender into $GOPATH/bin (or $HOME/go/bin)
install: build-frontend
	go install $(LDFLAGS) .

## run: run fender locally in debug mode (does not install)
run:
	go run . --log-level debug

## test: run all unit tests
test:
	go test -v ./...

## test-e2e: run the end-to-end test locally using 'act'
test-e2e:
	@command -v act >/dev/null 2>&1 || { echo >&2 "Error: 'act' is not installed. Please install it from https://github.com/nektos/act"; exit 1; }
	act -W .github/workflows/e2e.yml --container-daemon-socket unix:///var/run/docker.sock

## clean: remove build artefacts
clean:
	rm -rf $(BIN_DIR)

## help: print available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
