gen_models:
	swagger generate model \
	-f docs/swagger-v2.yaml \
	-t ./internal/generated/openapi