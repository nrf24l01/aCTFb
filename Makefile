swagger:
	# Pinned: the committed models were generated with v0.36.7. A different
	# generator version rewrites unrelated files (for example required array
	# items), which would bury a real spec change in noise.
	go install github.com/go-swagger/go-swagger/cmd/swagger@v0.36.7
	mkdir -p ./internal/generated/openapi
	rm -rf ./internal/generated/openapi/models/*
	swagger generate model \
	-f docs/swagger.yaml \
	-t ./internal/generated/openapi

protobuf:
	protoc \
	-I api/proto \
	--go_out=internal/generated/proto \
	--go_opt=paths=source_relative \
	api/proto/emails/email.proto

# Keep both historical entrypoints working: `make dev` and `make run`.
.PHONY: dev run
dev:
	docker compose -f docker-compose.dev.yaml up --build

run: dev

# Keep both historical entrypoints working: `make notification` and `make build`.
.PHONY: notification build
notification:
	mkdir -p ./bin
	go build -o ./bin/notification ./cmd/notification

build: notification

.PHONY: storage-init
storage-init:
	mkdir -p ./bin
	go build -o ./bin/storage-init ./cmd/storage-init

.PHONY: retention
retention:
	mkdir -p ./bin
	go build -o ./bin/retention ./cmd/retention

.PHONY: backend
backend:
	mkdir -p ./bin
	go build -o ./bin/backend ./cmd/backend

# Build every worker that exists today into ./bin. Missing worker binaries
# (for example a future cmd/instance-worker) are intentionally not listed so
# this target never pretends a component is ready.
.PHONY: events-dispatcher file-scanner plugin-runner ad-worker instance-worker workers
events-dispatcher:
	mkdir -p ./bin
	go build -o ./bin/events-dispatcher ./cmd/events-dispatcher

file-scanner:
	mkdir -p ./bin
	go build -o ./bin/file-scanner ./cmd/file-scanner

plugin-runner:
	mkdir -p ./bin
	go build -o ./bin/plugin-runner ./cmd/plugin-runner

ad-worker:
	mkdir -p ./bin
	go build -o ./bin/ad-worker ./cmd/ad-worker

instance-worker:
	mkdir -p ./bin
	go build -o ./bin/instance-worker ./cmd/instance-worker

workers: events-dispatcher file-scanner plugin-runner ad-worker instance-worker

# Local gates used by CI. None of these mutate operator keys or .env files.
.PHONY: vet test build-all
vet:
	go vet ./...

test:
	go test ./...

build-all:
	go build ./...

.PHONY: swagger protobuf
