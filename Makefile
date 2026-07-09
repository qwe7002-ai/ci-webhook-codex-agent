.PHONY: build run test vet tidy docker deb

VERSION ?= 0.0.0-dev
ARCH    ?= amd64

build:
	go build -o bin/server ./cmd/server

run: build
	./bin/server --config config.yaml

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

docker:
	docker build -t ci-webhook-codex-agent .

# Build a .deb locally (needs nfpm: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest).
# Usage: make deb VERSION=0.1.0 ARCH=amd64
deb:
	mkdir -p dist out
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -ldflags "-s -w" -o dist/ci-webhook-codex-agent ./cmd/server
	ARCH=$(ARCH) VERSION=$(VERSION) nfpm package -f packaging/nfpm.yaml -p deb -t out/
	@ls -l out/*.deb
