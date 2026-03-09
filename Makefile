SHELL=/usr/bin/env bash

project_name=computing-provider

unexport GOFLAGS

GOCC?=go

PKG=github.com/swanchain/computing-provider-v2/build

ldflags=-X=$(PKG).CurrentCommit=+git.$(subst -,.,$(shell git describe --always --match=NeVeRmAtCh --dirty 2>/dev/null || git rev-parse --short HEAD 2>/dev/null))

# Dev/testnet inference URL overrides
dev_ldflags=-X $(PKG).DefaultInferenceURL=https://inference-dev.swanchain.io \
            -X $(PKG).DefaultInferenceWSURL=wss://inference-ws-dev.swanchain.io \
            -X $(PKG).DefaultInferenceAPIURL=https://inference-dev.swanchain.io/api/v1

all: mainnet
.PHONY: all

computing-provider:
	rm -rf computing-provider
	$(GOCC) build $(GOFLAGS) -o computing-provider ./cmd/computing-provider
.PHONY: computing-provider

install:
	sudo install -C computing-provider /usr/local/bin/computing-provider

clean:
	sudo rm -rf /usr/local/bin/computing-provider
.PHONY: clean

mainnet: GOFLAGS+= -ldflags="$(ldflags) -X $(PKG).NetWorkTag=mainnet"
mainnet: computing-provider

testnet: GOFLAGS+= -ldflags="$(ldflags) -X $(PKG).NetWorkTag=testnet $(dev_ldflags)"
testnet: computing-provider

# Darwin ARM64 (Apple Silicon) builds
darwin-arm64:
	rm -rf computing-provider-darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=mainnet" -o computing-provider-darwin-arm64 ./cmd/computing-provider
.PHONY: darwin-arm64

darwin-arm64-testnet:
	rm -rf computing-provider-darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=testnet $(dev_ldflags)" -o computing-provider-darwin-arm64 ./cmd/computing-provider
.PHONY: darwin-arm64-testnet

# Linux ARM64 builds
linux-arm64:
	rm -rf computing-provider-linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=mainnet" -o computing-provider-linux-arm64 ./cmd/computing-provider
.PHONY: linux-arm64

linux-arm64-testnet:
	rm -rf computing-provider-linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=testnet $(dev_ldflags)" -o computing-provider-linux-arm64 ./cmd/computing-provider
.PHONY: linux-arm64-testnet
