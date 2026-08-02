.PHONY: setup fmt lint test coverage build up down demo logs clean

setup:
	go mod download

fmt:
	gofmt -w ./cmd ./internal ./migrations

lint:
	test -z "$$(gofmt -l ./cmd ./internal ./migrations)"
	go vet ./...

test:
	go test -race -coverprofile=coverage.out ./...

coverage: test
	go tool cover -html=coverage.out -o coverage.html

build:
	go build -trimpath -o bin/watchdog ./cmd/watchdog
	go build -trimpath -o bin/demo-target ./cmd/demo-target

up:
	cp -n .env.example .env
	docker compose up --detach --build

down:
	docker compose down --remove-orphans

demo:
	bash scripts/controlled-failure-demo.sh

logs:
	docker compose logs --follow watchdog

clean:
	go clean
	rm -rf bin coverage.out coverage.html tmp

