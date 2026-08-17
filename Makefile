PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
SYSTEMD_USER_DIR ?= $(HOME)/.config/systemd/user

.PHONY: all build test install uninstall service-enable service-start service-stop service-status clean

all: test build

build:
	go build -o bin/memremarkd ./cmd/memremarkd
	go build -o bin/memremark-hook-claude-sessionstart ./cmd/memremark-hook-claude-sessionstart
	go build -o bin/memremark-hook-antigravity-preinvocation ./cmd/memremark-hook-antigravity-preinvocation

test:
	go test -v -race ./...

install: build
	mkdir -p $(BINDIR)
	install -m 755 bin/memremarkd $(BINDIR)/memremarkd
	install -m 755 bin/memremark-hook-claude-sessionstart $(BINDIR)/memremark-hook-claude-sessionstart
	install -m 755 bin/memremark-hook-antigravity-preinvocation $(BINDIR)/memremark-hook-antigravity-preinvocation
	mkdir -p $(SYSTEMD_USER_DIR)
	install -m 644 systemd/memremarkd.service $(SYSTEMD_USER_DIR)/memremarkd.service
	systemctl --user daemon-reload

uninstall:
	systemctl --user stop memremarkd.service || true
	systemctl --user disable memremarkd.service || true
	rm -f $(SYSTEMD_USER_DIR)/memremarkd.service
	systemctl --user daemon-reload
	rm -f $(BINDIR)/memremarkd
	rm -f $(BINDIR)/memremark-hook-claude-sessionstart
	rm -f $(BINDIR)/memremark-hook-antigravity-preinvocation

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
