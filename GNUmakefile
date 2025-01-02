default: testacc

# Run acceptance tests
.PHONY: testacc
testacc:
	TF_ACC=1 go test ./... -v -cover -timeout 120m

# Build the provider
.PHONY: build
build:
	go build -v -o bin/terraform-provider-controltower .