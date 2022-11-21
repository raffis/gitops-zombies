VERSION?=$(shell grep 'VERSION' cmd/main.go | awk '{ print $$4 }' | head -n 1 | tr -d '"')
DEV_VERSION?=0.0.0-$(shell git rev-parse --abbrev-ref HEAD)-$(shell git rev-parse --short HEAD)-$(shell date +%s)
# Architecture to use envtest with
ENVTEST_ARCH ?= amd64

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

rwildcard=$(foreach d,$(wildcard $(addsuffix *,$(1))),$(call rwildcard,$(d)/,$(2)) $(filter $(subst *,%,$(2)),$(d)))

all: lint test build

tidy:
	go mod tidy -compat=1.19

fmt:
	go fmt ./...

test:
	go test -coverprofile coverage.out -v ./...

GOLANGCI_LINT = $(GOBIN)/golangci-lint
golangci-lint: ## Download golint locally if necessary.
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint@v1.50.1)

lint: golangci-lint
	golangci-lint run

vet:
	go vet ./...

build:
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.VERSION=$(VERSION)" -o ./bin/gitops-zombies ./cmd

.PHONY: install
install:
	CGO_ENABLED=0 go install ./cmd

# go-install-tool will 'go install' any package $2 and install it to $1
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
env -i bash -c "GOBIN=$(GOBIN) PATH=$(PATH) GOPATH=$(shell go env GOPATH) GOCACHE=$(shell go env GOCACHE) go install $(2)" ;\
rm -rf $$TMP_DIR ;\
}
endef
