# Development helpers. Run `make` to rebuild bs onto your PATH.

# XDG_BIN_HOME isn't in the XDG spec, but it's the common convention;
# the spec itself only names ~/.local/bin for user binaries.
BIN_DIR ?= $(or $(XDG_BIN_HOME),$(HOME)/.local/bin)

.PHONY: install
install:
	go build -o $(BIN_DIR)/bs ./cmd/bs

# Re-records the README GIF. Needs VHS (brew install vhs).
.PHONY: demo
demo: install
	vhs assets/demo.tape

.PHONY: test
test:
	go test -race ./...

# Needs golangci-lint (brew install golangci-lint).
.PHONY: lint
lint:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	golangci-lint run ./...

# Builds every release binary into dist/ without publishing anything.
# Needs GoReleaser (brew install goreleaser).
.PHONY: snapshot
snapshot:
	goreleaser release --snapshot --clean
