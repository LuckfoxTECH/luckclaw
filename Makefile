# luckclaw build targets
# Default: full build with browser tool (~20MB)
# Minimal: -tags nobrowser excludes go-rod (~15MB)

VERSION := 0.0.2

.PHONY: build build-minimal build-armv7 build-armv7-minimal build-arm64 build-arm64-minimal clean
LDFLAGS := -ldflags="-s -w -X=luckclaw/internal/cli.Version=$(VERSION)"

build:
	go build $(LDFLAGS) -o luckclaw ./cmd/luckclaw

build-minimal:
	go build -tags nobrowser $(LDFLAGS) -o luckclaw ./cmd/luckclaw

build-armv7:
	GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o luckclaw-armv7 ./cmd/luckclaw

build-armv7-minimal:
	GOOS=linux GOARCH=arm GOARM=7 go build -tags nobrowser $(LDFLAGS) -o luckclaw-armv7 ./cmd/luckclaw

build-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o luckclaw-arm64 ./cmd/luckclaw

build-arm64-minimal:
	GOOS=linux GOARCH=arm64 go build -tags nobrowser $(LDFLAGS) -o luckclaw-arm64 ./cmd/luckclaw

clean:
	rm -f luckclaw luckclaw-armv7 luckclaw-arm64
