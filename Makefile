.PHONY: all build build-mcp run deps grammars coverage clean

all: build

build:
	go build -o codemap .

build-mcp:
	go build -o codemap-mcp ./cmd/codemap-mcp/

DIR ?= .
ABS_DIR := $(shell cd "$(DIR)" && pwd)
SKYLINE_FLAG := $(if $(SKYLINE),--skyline,)
ANIMATE_FLAG := $(if $(ANIMATE),--animate,)
DEPS_FLAG := $(if $(DEPS),--deps,)

run: build
	./codemap $(SKYLINE_FLAG) $(ANIMATE_FLAG) $(DEPS_FLAG) "$(ABS_DIR)"

# Build tree-sitter grammar libraries (one-time setup for deps mode)
grammars:
	cd scanner && ./build-grammars.sh

# Dependency graph mode - shows functions and imports per file
deps: build grammars
	./codemap --deps "$(ABS_DIR)"

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

clean:
	rm -f codemap codemap-mcp
	rm -rf scanner/.grammar-build
	rm -rf scanner/grammars
