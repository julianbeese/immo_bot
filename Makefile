.PHONY: build run test clean docker docker-up docker-down

# Build the binary
build:
	go build -o immobot ./cmd/immobot

# Run locally
run: build
	./immobot -config configs/config.yaml

# Run once (single poll cycle)
run-once: build
	./immobot -config configs/config.yaml -once

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -f immobot
	rm -rf data/

# Build Docker image
docker:
	docker build -f deployments/Dockerfile -t immobot .

# Start with Docker Compose
docker-up:
	cd deployments && docker-compose up -d

# Stop Docker Compose
docker-down:
	cd deployments && docker-compose down

# View logs
docker-logs:
	cd deployments && docker-compose logs -f

# Format code
fmt:
	go fmt ./...

# Run linter
lint:
	golangci-lint run

# Tidy dependencies
tidy:
	go mod tidy
