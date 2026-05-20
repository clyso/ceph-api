.PHONY: help check lint e2e-test proto gate full-gate ceph-ref ceph-ref-versions

CEPH_REF_DIR := third_party/ceph
CEPH_REF_COMMIT := v19.2.3
CEPH_REF_EXTRA_TAGS ?= v18.2.7 v20.0.0

help:
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*?##/ { printf "  %-22s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

check: ## fmt + vet + unit tests
	go fmt ./...
	CGO_ENABLED=0 go vet ./...
	CGO_ENABLED=0 go test ./pkg/...

lint: ## golangci-lint
	CGO_ENABLED=0 go tool golangci-lint run

e2e-test: ## e2e tests in Docker (-tid)
	CGO_ENABLED=0 go test ./test/... -tid

proto: ## regenerate gRPC stubs and OpenAPI
	cd api && go tool buf generate

gate: check lint
full-gate: gate e2e-test

ceph-ref:
	@git submodule update --init --filter=blob:none --depth=1 $(CEPH_REF_DIR) 2>/dev/null || true
	@git -C $(CEPH_REF_DIR) fetch --filter=blob:none --depth=1 origin tag $(CEPH_REF_COMMIT) 2>/dev/null || true
	@git -C $(CEPH_REF_DIR) checkout $(CEPH_REF_COMMIT)

ceph-ref-versions: ceph-ref
	@for t in $(CEPH_REF_EXTRA_TAGS); do \
		git -C $(CEPH_REF_DIR) fetch --filter=blob:none --depth=1 origin tag $$t || true; \
	done
