swagger:
	go install github.com/go-swagger/go-swagger/cmd/swagger@latest
	mkdir -p ./internal/generated/openapi 
	swagger generate model \
	-f docs/swagger-v3.yaml \
	-t ./internal/generated/openapi

protobuf:
	protoc \
	-I api/proto \
	--go_out=internal/generated/proto \
	--go_opt=paths=source_relative \
	api/proto/emails/email.proto

dev run:
	docker compose -f docker-compose.dev.yaml up --build

notification build:
	mkdir -p ./bin
	go build -o ./bin/notification ./cmd/notification