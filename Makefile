GO ?= go
BINARY ?= common-vpn
VERSION ?= dev

.PHONY: all build build-linux release-linux test fmt clean

all: test build

build:
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY) ./cmd/common-vpn

build-linux:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY)-linux-amd64 ./cmd/common-vpn
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY)-linux-arm64 ./cmd/common-vpn
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY)-linux-armv7 ./cmd/common-vpn
	CGO_ENABLED=0 GOOS=linux GOARCH=mips GOMIPS=softfloat $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY)-linux-mips ./cmd/common-vpn
	CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY)-linux-mipsle ./cmd/common-vpn

release-linux: build-linux
	@set -eu; for arch in amd64 arm64 armv7 mips mipsle; do \
		package="dist/package-$$arch"; \
		rm -rf "$$package"; mkdir -p "$$package"; \
		cp "dist/$(BINARY)-linux-$$arch" "$$package/common-vpn"; \
		tar -czf "dist/common-vpn-linux-$$arch.tar.gz" -C "$$package" common-vpn; \
		rm -rf "$$package"; \
	done

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf dist
