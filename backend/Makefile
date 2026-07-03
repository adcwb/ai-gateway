.PHONY: build run wire fmt lint test docker-build clean

APP_NAME = ai-gateway
BINARY   = server
CONFIG   = configs/config.yaml

build:
	go build -o $(BINARY) ./cmd/server

run: build
	./$(BINARY) -conf $(CONFIG)

wire:
	wire ./cmd/server

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...

test:
	go test ./...

docker-build:
	docker build -t $(APP_NAME):latest .

clean:
	rm -f $(BINARY)
