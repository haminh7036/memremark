PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
SYSTEMD_USER_DIR ?= $(HOME)/.config/systemd/user

.PHONY: all build build-web test install uninstall patch-hooks patch-antigravity patch-claude service-enable service-start service-stop service-status clean

all: test build

build-web:
	cd web && npm run build

build:
	go build -o bin/memremarkd ./cmd/memremarkd
	go build -o bin/memremark-hook-claude ./cmd/memremark-hook-claude
	go build -o bin/memremark-hook-agy ./cmd/memremark-hook-agy
	go build -o bin/memremark-mcp ./cmd/memremark-mcp
	go build -o bin/memremark-ui ./cmd/memremark-ui

test:
	go test -v -race ./...

install:
	./install.sh --cli=all

patch-hooks:
	./install.sh --no-build --no-service --cli=all

patch-antigravity:
	./install.sh --no-build --no-service --cli=antigravity-cli

patch-claude:
	./install.sh --no-build --no-service --cli=claude-code

uninstall:
	./install.sh --uninstall

service-enable:
	systemctl --user enable --now memremarkd.service

service-start:
	systemctl --user start memremarkd.service

service-stop:
	systemctl --user stop memremarkd.service

service-status:
	systemctl --user status memremarkd.service

clean:
	rm -rf bin/
