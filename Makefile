.PHONY: build run test vet tidy docker

build:
	go build -o bin/server ./cmd/server

run: build
	./bin/server -config config.yaml

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

docker:
	docker build -t ci-webhook-codex-agent .
