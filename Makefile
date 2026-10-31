BIN     := cooler-lcd
PKG     := ./cmd/cooler-lcd
DIST    := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/volcie/cooler-lcd-linux/internal/version.Version=$(VERSION) \
	-X github.com/volcie/cooler-lcd-linux/internal/version.Commit=$(COMMIT) \
	-X github.com/volcie/cooler-lcd-linux/internal/version.Date=$(DATE)

# go-hid is cgo-only, so every target needs a C toolchain and libudev for the
# target architecture. Native builds use the host compiler; arm64 needs the
# aarch64 cross compiler plus an arm64 sysroot containing libudev.
CROSS_amd64 :=
CROSS_arm64 := aarch64-linux-gnu-

PLATFORMS := linux-amd64 linux-arm64

.PHONY: build all test vet fmt lint install clean dist $(PLATFORMS)

# build produces a binary for the host architecture only, named by platform so
# it matches the CI artifacts.
HOSTPLATFORM := linux-$(shell go env GOARCH)

build:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(BIN)-$(HOSTPLATFORM) $(PKG)

all: $(PLATFORMS)

linux-amd64:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(BIN)-$@ $(PKG)

linux-arm64:
	@mkdir -p $(DIST)
	@command -v $(CROSS_arm64)gcc >/dev/null 2>&1 || { \
		echo "error: $(CROSS_arm64)gcc not found."; \
		echo "       Install aarch64-linux-gnu-gcc and an arm64 libudev sysroot."; \
		exit 1; }
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=$(CROSS_arm64)gcc \
		go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(BIN)-$@ $(PKG)

test:
	CGO_ENABLED=1 go test ./...

vet:
	CGO_ENABLED=1 go vet ./...

fmt:
	gofmt -w .

lint: vet
	@test -z "$$(gofmt -l .)" || { echo "unformatted files:"; gofmt -l .; exit 1; }

# Build, then let the binary install itself (udev rule + user service).
install: build
	$(DIST)/$(BIN)-$(HOSTPLATFORM) install

clean:
	rm -rf $(DIST)
