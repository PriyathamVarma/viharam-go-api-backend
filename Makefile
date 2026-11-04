.DEFAULT_GOAL := help
.PHONY: help build run test vet fmt check

help:
	@echo "Targets: build run test vet fmt check"

build:
	go build -o bin/viharam-api ./cmd/api

run:
	go run ./cmd/api

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

check: vet test build
