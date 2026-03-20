SHELL=/usr/bin/env bash

project_name=computing-provider

unexport GOFLAGS

GOCC?=go

PKG=github.com/swanchain/computing-provider-v2/build

ldflags=-X=$(PKG).CurrentCommit=+git.$(subst -,.,$(shell git describe --always --match=NeVeRmAtCh --dirty 2>/dev/null || git rev-parse --short HEAD 2>/dev/null))

# Mainnet inference URL overrides
mainnet_ldflags=-X $(PKG).DefaultInferenceURL=https://api.swanchain.io/v1 \
                -X $(PKG).DefaultInferenceWSURL=wss://api-ws.swanchain.io \
                -X $(PKG).DefaultInferenceAPIURL=https://api.swanchain.io/v1

all: testnet
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

testnet: GOFLAGS+= -ldflags="$(ldflags) -X $(PKG).NetWorkTag=testnet"
testnet: computing-provider

mainnet: GOFLAGS+= -ldflags="$(ldflags) -X $(PKG).NetWorkTag=mainnet $(mainnet_ldflags)"
mainnet: computing-provider

# Darwin ARM64 (Apple Silicon) builds
darwin-arm64:
	rm -rf computing-provider-darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=testnet" -o computing-provider-darwin-arm64 ./cmd/computing-provider
.PHONY: darwin-arm64

darwin-arm64-mainnet:
	rm -rf computing-provider-darwin-arm64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=mainnet $(mainnet_ldflags)" -o computing-provider-darwin-arm64 ./cmd/computing-provider
.PHONY: darwin-arm64-mainnet

# Linux AMD64 builds
linux-amd64:
	rm -rf computing-provider-linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=testnet" -o computing-provider-linux-amd64 ./cmd/computing-provider
.PHONY: linux-amd64

linux-amd64-mainnet:
	rm -rf computing-provider-linux-amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=mainnet $(mainnet_ldflags)" -o computing-provider-linux-amd64 ./cmd/computing-provider
.PHONY: linux-amd64-mainnet

# Linux ARM64 builds
linux-arm64:
	rm -rf computing-provider-linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=testnet" -o computing-provider-linux-arm64 ./cmd/computing-provider
.PHONY: linux-arm64

linux-arm64-mainnet:
	rm -rf computing-provider-linux-arm64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOCC) build -ldflags="$(ldflags) -X $(PKG).NetWorkTag=mainnet $(mainnet_ldflags)" -o computing-provider-linux-arm64 ./cmd/computing-provider
.PHONY: linux-arm64-mainnet
