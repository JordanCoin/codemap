.PHONY: all venv install run clean

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

run: install
	cd scanner && go run main.go "$(ABS_DIR)" | ../$(PYTHON) ../renderer/render.py

clean:
	rm -rf $(VENV_DIR)
