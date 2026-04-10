BINARY := lastwatt
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build build-pi install uninstall clean test

# Build for current platform
build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/lastwatt

# Cross-compile for Raspberry Pi (ARM64)
build-pi:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-arm64 ./cmd/lastwatt

# Install systemd service (run as root)
install: build
	install -d -o mcd -g mcd /var/lib/lastwatt
	install -m 644 configs/lastwatt.service /etc/systemd/system/$(BINARY).service
	systemctl daemon-reload
	@echo "Run: systemctl enable --now lastwatt"

uninstall:
	systemctl disable --now lastwatt || true
	rm -f /etc/systemd/system/$(BINARY).service
	systemctl daemon-reload

test:
	go test ./...

clean:
	rm -f $(BINARY) $(BINARY)-arm64
