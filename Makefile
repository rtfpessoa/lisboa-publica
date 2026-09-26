.PHONY: generate check-generated build test run
generate:
	./scripts/generate.sh
check-generated:
	./scripts/check-generated.sh
build:
	cd frontend && npm ci && npm run build
	go build -o bin/server ./cmd/server
test:
	go test -race ./...
	go vet ./...
run:
	go run ./cmd/server
