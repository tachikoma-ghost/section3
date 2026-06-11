BINARY := bin/section3
VERSION := $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)
BUILD_ENV := CGO_ENABLED=0

RELEASE_DIR := .release
RELEASE_SERVER := tachikoma@signalshell.com
SIGN_KEY := $(HOME)/.config/section3/release-signing.key
PLATFORMS := linux/amd64 linux/arm64

.PHONY: build test clean release

build:
	mkdir -p bin
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./... -count=1

## Build, sign, and publish a new release to signalshell.com/releases/section3/
release:
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Error: git working tree is not clean. Commit or stash changes first."; \
		git status --short; \
		exit 1; fi
	@echo "Current version: $(VERSION)"
	@NEW_VERSION=$$(( $(VERSION) + 1 )); \
	echo $$NEW_VERSION > VERSION; \
	echo "Releasing version $$NEW_VERSION..."; \
	rm -rf $(RELEASE_DIR)/$$NEW_VERSION; \
	mkdir -p $(RELEASE_DIR)/$$NEW_VERSION; \
	for PLATFORM in $(PLATFORMS); do \
		GOOS=$${PLATFORM%/*}; GOARCH=$${PLATFORM#*/}; \
		BIN=$(RELEASE_DIR)/$$NEW_VERSION/section3-$$GOOS-$$GOARCH; \
		echo "  Building $$GOOS/$$GOARCH..."; \
		GOOS=$$GOOS GOARCH=$$GOARCH $(BUILD_ENV) go build \
			-ldflags="-s -w -X main.version=$$NEW_VERSION -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)" \
			-o $$BIN . || exit 1; \
		minisign -S -s $(SIGN_KEY) -m $$BIN -x $$BIN.minisig; \
		sha256sum $$BIN | awk '{print $$1}' > $$BIN.sha256; \
	done; \
	printf '{"version":%d,"published":"%s"}\n' $$NEW_VERSION "$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		> $(RELEASE_DIR)/latest.json; \
	echo "  Uploading release $$NEW_VERSION..."; \
	rsync --mkpath -av --chmod=D755,F644 $(RELEASE_DIR)/$$NEW_VERSION/ $(RELEASE_SERVER):releases/section3/$$NEW_VERSION/; \
	rsync --mkpath -av --chmod=F644 $(RELEASE_DIR)/latest.json $(RELEASE_SERVER):releases/section3/latest.json; \
	git add -u && git commit -m "release v$$NEW_VERSION" && git tag "v$$NEW_VERSION"; \
	echo "  Released version $$NEW_VERSION (committed + tagged)"

clean:
	rm -rf bin $(RELEASE_DIR)
	go clean
