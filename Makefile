# Installs buildbox for the current user. Sticks to GNU Make 3.81 (what macOS
# ships) and install flags that BSD and GNU install share.

PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
# Must match the fallback in config.go.
CONFIG_DIR ?= $(or $(XDG_CONFIG_HOME),$(HOME)/.config)/buildbox
CONFIG = $(CONFIG_DIR)/config.toml

.PHONY: help build install config uninstall check lint hooks test

help:
	@echo "make build      Compile ./buildbox"
	@echo "make install    Install buildbox to $(BINDIR)"
	@echo "make config     Create $(CONFIG) from buildbox.toml or buildbox.toml.example"
	@echo "make uninstall  Remove $(BINDIR)/buildbox and keep the config"
	@echo "make test       Run unit tests"
	@echo "make check      Compile, test, and lint. The hook and GitHub Actions run this"
	@echo "make lint       Run golangci-lint"
	@echo "make hooks      Use .githooks so commits must pass make check"
	@echo "Set PREFIX to install elsewhere, e.g. make install PREFIX=/usr/local"

build:
	go build -trimpath -o buildbox .

install: build
	install -d "$(BINDIR)"
	install -m 755 buildbox "$(BINDIR)/buildbox"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; *) echo "Note: $(BINDIR) is not in your PATH." ;; esac

# A separate target so "sudo make install" never writes into root's home.
# An existing config is never overwritten.
config:
	@if [ -f "$(CONFIG)" ]; then \
		echo "$(CONFIG) already exists, leaving it unchanged."; \
	else \
		if [ -f buildbox.toml ]; then src=buildbox.toml; else src=buildbox.toml.example; fi; \
		install -d "$(CONFIG_DIR)"; \
		install -m 644 "$$src" "$(CONFIG)"; \
		echo "Created $(CONFIG) from $$src."; \
		if [ "$$src" = buildbox.toml.example ]; then echo "Edit it before running buildbox."; fi; \
	fi

uninstall:
	rm -f "$(BINDIR)/buildbox"
	@echo "Kept $(CONFIG_DIR). Remove it yourself if you no longer need it."

# The pre-commit hook and GitHub Actions both run this.
check: build test lint

test:
	go test ./...

lint:
	golangci-lint run ./...
	golangci-lint fmt --diff

hooks:
	git config core.hooksPath .githooks
	chmod +x .githooks/pre-commit
