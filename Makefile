# Chaos Gateway — developer entry points. `make help` lists the targets.
#
# Test levels (docs/development.md, plan §4.5):
#   make test             level 0: unit tests, no root, every commit
#   make test-testbed     levels 1/1b: the namespace testbed, directly or in a VM, chosen automatically
#   make test-vm          level 1b: always in a QEMU VM (the unprivileged devcontainer)
#   make test-privileged  level 1: directly (needs a privileged container or a VM)

SHELL := /bin/bash
.DEFAULT_GOAL := help

GO      ?= go
PKG     := github.com/Andste82/chaos-gateway
VENV    := .venv
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT) -X $(PKG)/internal/version.Date=$(DATE)

# the race detector needs cgo, i.e. a C compiler
RACE := $(shell command -v gcc >/dev/null 2>&1 && echo -race)

.PHONY: help tools generate generate-go generate-web generate-python \
        check-spec check-generated check-clients lint test fuzz test-web test-testbed test-vm test-appliance \
        test-privileged test-arm64 test-e2e build build-web dev image clean

help: ## list the targets
	@awk -F ':.*## ' '/^[a-zA-Z0-9_-]+:.*## /{printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ---- setup ------------------------------------------------------------------------------------

# The tooling is installed on demand: the targets below depend on these stamp files, so a clean
# checkout works with `make test` or `make check-spec` right away. `make tools` installs both.
PYTOOLS := $(VENV)/.installed
WEBDEPS := web/node_modules/.installed

$(PYTOOLS): tools/requirements.txt
	python3 -m venv $(VENV)
	$(VENV)/bin/pip install -q -r tools/requirements.txt
	touch $@

$(WEBDEPS): web/package-lock.json
	cd web && npm ci
	touch $@

tools: $(PYTOOLS) $(WEBDEPS) ## install the Python tooling (.venv) and the web dependencies

# ---- code generation (api/openapi.yaml is the source of truth) ---------------------------------

generate: generate-go generate-web generate-python ## regenerate all code from api/openapi.yaml

generate-go: ## Go model types and Gin server interface (committed)
	$(GO) tool oapi-codegen -config api/oapi-codegen-model.yaml api/openapi.yaml
	$(GO) tool oapi-codegen -config api/oapi-codegen-server.yaml api/openapi.yaml

generate-web: $(WEBDEPS) ## Vue Query hooks, Zod schemas and the plain TypeScript client (not committed)
	cd web && npx orval --config orval.config.ts

generate-python: $(PYTOOLS) ## Python client (not committed)
	PATH="$(CURDIR)/$(VENV)/bin:$$PATH" $(VENV)/bin/openapi-python-client generate \
	  --path api/openapi.yaml --output-path clients/python/chaosgw-client \
	  --config clients/python/config.yml --overwrite

# ---- checks -----------------------------------------------------------------------------------

check-spec: $(PYTOOLS) ## the spec validates and the examples match their schemas
	$(VENV)/bin/python api/examples/validate.py

check-generated: generate-go ## the committed generated Go code is current and everything compiles
	git ls-files --error-unmatch internal/model/model.gen.go internal/apiserver/server.gen.go >/dev/null
	git diff --exit-code -- internal/model internal/apiserver
	$(GO) build ./...

check-clients: generate-web generate-python ## the generated TypeScript and Python clients compile
	cd web && npx vue-tsc --noEmit -p tsconfig.json
	web/node_modules/.bin/tsc -p clients/typescript/tsconfig.json
	$(VENV)/bin/pip install -q clients/python/chaosgw-client
	$(VENV)/bin/python -c "import chaosgw_client; print('python client imports:', chaosgw_client.__name__)"

lint: $(WEBDEPS) ## golangci-lint and the TypeScript type check
	golangci-lint run ./...
	cd web && npm run typecheck

# ---- tests ------------------------------------------------------------------------------------

test: test-web ## level 0: Go unit tests (no root) and the web unit tests
	$(GO) test $(RACE) -count=1 ./...

# Fuzz targets, one `go test -fuzz` run each (Go runs one target per invocation). FUZZTIME is per
# target: the default 100s makes the three targets of the executor decoder and connection handling
# 5 minutes in CI; the nightly workflow uses a longer time.
FUZZTIME ?= 100s
FUZZ_TARGETS := FuzzDecode FuzzFrame FuzzConn

fuzz: ## fuzz the executor's operation decoder and request handling (FUZZTIME per target)
	@for t in $(FUZZ_TARGETS); do \
	  echo "== $$t ($(FUZZTIME))"; \
	  $(GO) test -run '^$$' -fuzz "^$$t$$" -fuzztime $(FUZZTIME) ./internal/executor || exit 1; \
	done

test-web: $(WEBDEPS)
	cd web && npm test

test-testbed: ## the namespace testbed: directly where possible, else in a VM
	$(GO) run ./tools/testvm run $(ARGS)

test-appliance: ## level 2: the gateway in a VM from the Ubuntu cloud images (needs root, QEMU, /dev/kvm and CHAOSGW_APPLIANCE_IMAGE_TAR)
	$(GO) test -tags appliance -count=1 -timeout 80m -v -run TestSmokeOnCleanUbuntuHosts ./internal/appliance

test-vm: ## the namespace testbed in a QEMU VM (level 1b)
	$(GO) run ./tools/testvm run -mode vm $(ARGS)

test-privileged: ## the namespace testbed directly (level 1; needs privileges)
	$(GO) run ./tools/testvm run -mode direct $(ARGS)

test-arm64: ## the unit tests as arm64 binaries under qemu-user
	GOARCH=arm64 CGO_ENABLED=0 $(GO) test -count=1 -exec qemu-aarch64 ./...

test-e2e: $(WEBDEPS) ## Playwright end-to-end tests (needs `npx playwright install chromium` once)
	cd web && npm run test:e2e

# ---- build ------------------------------------------------------------------------------------

build: build-web ## build chaosgw and chaosctl into bin/ and the web app into web/dist
	CGO_ENABLED=0 $(GO) build -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

build-web: $(WEBDEPS)
	cd web && npm run build

dev: $(WEBDEPS) ## web dev server with a proxy to the API (CHAOSGW_API, default https://127.0.0.1:8443)
	cd web && npm run dev

image: ## multi-arch container image (no push)
	docker buildx build --platform linux/amd64,linux/arm64 -f deploy/Dockerfile \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t chaos-gateway:$(VERSION) .

clean: ## remove build output and generated, uncommitted code
	rm -rf bin web/dist web/src/api/generated clients/typescript/src clients/python/chaosgw-client
