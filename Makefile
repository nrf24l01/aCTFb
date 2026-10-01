gen_models:
	go install github.com/go-swagger/go-swagger/cmd/swagger@latest
	mkdir -p ./internal/generated/openapi 
	swagger generate model \
	-f docs/swagger-v3.yaml \
	-t ./internal/generated/openapi