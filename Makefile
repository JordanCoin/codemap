.PHONY: all venv install run deps grammars clean

VENV_DIR = venv
PYTHON = $(VENV_DIR)/bin/python3
PIP = $(VENV_DIR)/bin/pip3

all: install run

venv:
	python3 -m venv $(VENV_DIR)

install: venv
	$(PIP) install rich

DIR ?= .
ABS_DIR := $(shell cd "$(DIR)" && pwd)
SKYLINE_FLAG := $(if $(SKYLINE),--skyline,)
ANIMATE_FLAG := $(if $(ANIMATE),--animate,)

run: install
	cd scanner && go run main.go deps.go $(SKYLINE_FLAG) $(ANIMATE_FLAG) "$(ABS_DIR)" | ../$(PYTHON) ../renderer/render.py

# Build tree-sitter grammar libraries (one-time setup for deps mode)
grammars:
	cd scanner && ./build-grammars.sh

# Dependency graph mode - shows functions and imports per file
deps: install grammars
	cd scanner && go run main.go deps.go --deps "$(ABS_DIR)" | ../$(PYTHON) ../renderer/render.py

clean:
	rm -rf $(VENV_DIR)
	rm -rf scanner/.grammar-build
	rm -rf scanner/grammars
