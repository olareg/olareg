# commands
COMMANDS?=olareg
BINARIES?=$(addprefix bin/,$(COMMANDS))
IMAGES?=$(addprefix docker-,$(COMMANDS))
ARTIFACT_PLATFORMS?=linux-amd64 linux-arm64 linux-ppc64le linux-s390x darwin-amd64 darwin-arm64 windows-amd64.exe
ARTIFACTS?=$(foreach cmd,$(addprefix artifacts/,$(COMMANDS)),$(addprefix $(cmd)-,$(ARTIFACT_PLATFORMS)))
TEST_PLATFORMS?=linux/386,linux/amd64,linux/arm/v6,linux/arm/v7,linux/arm64,linux/ppc64le,linux/s390x
# build vars
VCS_REPO?="https://github.com/olareg/olareg.git"
VCS_REF?=$(shell git rev-list -1 HEAD)
ifneq ($(shell git status --porcelain 2>/dev/null),)
  VCS_REF := $(VCS_REF)-dirty
endif
VCS_TAG?=$(shell git describe --tags --abbrev=0 2>/dev/null || true)
LD_FLAGS?=-s -w -extldflags -static -buildid= -X \"github.com/olareg/olareg/internal/version.vcsTag=$(VCS_TAG)\"
GO_BUILD_FLAGS?=-trimpath -ldflags "$(LD_FLAGS)"
# docker opts
DOCKERFILE_EXT?=$(shell if docker build --help 2>/dev/null | grep -q -- '--progress'; then echo ".buildkit"; fi)
DOCKER_ARGS?=--build-arg "VCS_REF=$(VCS_REF)"
# OS paths
GOPATH?=$(shell go env GOPATH)
PWD:=$(shell pwd)
# tools
VER_BUMP?=$(shell command -v version-bump 2>/dev/null)
VER_BUMP_CONTAINER?=sudobmitch/version-bump:edge
ifeq "$(strip $(VER_BUMP))" ''
	VER_BUMP=docker run --rm \
		-v "$(shell pwd)/:$(shell pwd)/" -w "$(shell pwd)" \
		-u "$(shell id -u):$(shell id -g)" \
		$(VER_BUMP_CONTAINER)
endif
MARKDOWN_LINT_VER?=v0.11.0
GOMAJOR_VER?=v0.10.0
GOSEC_VER?=v2.18.2
GO_VULNCHECK_VER?=v1.0.1
OSV_SCANNER_VER?=v1.5.0
SYFT?=$(shell command -v syft 2>/dev/null)
SYFT_CMD_VER:=$(shell [ -x "$(SYFT)" ] && echo "v$$($(SYFT) version | awk '/^Version: / {print $$2}')" || echo "0")
SYFT_VERSION?=v0.99.0
SYFT_CONTAINER?=anchore/syft:v0.99.0@sha256:07d598b6a95280ed6ecc128685192173a00f370b5326cf50c62500d559075e1d
ifneq "$(SYFT_CMD_VER)" "$(SYFT_VERSION)"
	SYFT=docker run --rm \
		-v "$(shell pwd)/:$(shell pwd)/" -w "$(shell pwd)" \
		-u "$(shell id -u):$(shell id -g)" \
		$(SYFT_CONTAINER)
endif
STATICCHECK_VER?=v0.4.6

.PHONY: .FORCE
.FORCE:

.PHONY: all
all: fmt goimports vet test lint binaries ## Full build of Go binaries (including fmt, vet, test, and lint)

.PHONY: fmt
fmt: ## go fmt
	go fmt ./...

goimports: $(GOPATH)/bin/goimports
	$(GOPATH)/bin/goimports -w -format-only -local github.com/olareg .

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: test
test: ## go test
	go test -cover -race ./...

.PHONY: lint
lint: lint-go lint-goimports lint-md lint-gosec ## Run all linting

.PHONY: lint-go
lint-go: $(GOPATH)/bin/staticcheck .FORCE ## Run linting for Go
	$(GOPATH)/bin/staticcheck -checks all ./...

lint-goimports: $(GOPATH)/bin/goimports
	@if [ -n "$$($(GOPATH)/bin/goimports -l -format-only -local github.com/olareg .)" ]; then \
		echo $(GOPATH)/bin/goimports -d -format-only -local github.com/olareg .; \
		$(GOPATH)/bin/goimports -d -format-only -local github.com/olareg .; \
		exit 1; \
	fi

.PHONY: lint-gosec
lint-gosec: $(GOPATH)/bin/gosec .FORCE ## Run gosec
	$(GOPATH)/bin/gosec -terse ./...

.PHONY: lint-md
lint-md: .FORCE ## Run linting for markdown
	docker run --rm -v "$(PWD):/workdir:ro" davidanson/markdownlint-cli2:$(MARKDOWN_LINT_VER) \
	  "**/*.md" "#vendor"

.PHONY: vulnerability-scan
vulnerability-scan: osv-scanner vulncheck-go ## Run all vulnerability scanners

.PHONY: osv-scanner
osv-scanner: $(GOPATH)/bin/osv-scanner .FORCE ## Run OSV Scanner
	$(GOPATH)/bin/osv-scanner -r .

.PHONY: vulncheck-go
vulncheck-go: $(GOPATH)/bin/govulncheck .FORCE ## Run govulncheck
	$(GOPATH)/bin/govulncheck ./...

.PHONY: vendor
vendor: ## Vendor Go modules
	go mod vendor

.PHONY: binaries
binaries: $(BINARIES) ## Build Go binaries

bin/%: .FORCE
	CGO_ENABLED=0 go build ${GO_BUILD_FLAGS} -o bin/$* ./cmd/$*

.PHONY: artifacts
artifacts: $(ARTIFACTS) ## Generate artifacts

.PHONY: artifact-pre
artifact-pre:
	mkdir -p artifacts

artifacts/%: artifact-pre .FORCE
	@set -e; \
	target="$*"; \
	command="$${target%%-*}"; \
	platform_ext="$${target#*-}"; \
	platform="$${platform_ext%.*}"; \
	export GOOS="$${platform%%-*}"; \
	export GOARCH="$${platform#*-}"; \
	echo export GOOS=$${GOOS}; \
	echo export GOARCH=$${GOARCH}; \
	echo go build ${GO_BUILD_FLAGS} -o "$@" ./cmd/$${command}/; \
	CGO_ENABLED=0 go build ${GO_BUILD_FLAGS} -o "$@" ./cmd/$${command}/; \
	$(SYFT) packages -q "file:$@" --source-name "$${command}" -o cyclonedx-json >"artifacts/$${command}-$${platform}.cyclonedx.json"; \
	$(SYFT) packages -q "file:$@" --source-name "$${command}" -o spdx-json >"artifacts/$${command}-$${platform}.spdx.json"

# docker images

.PHONY: docker
docker: $(IMAGES) ## Build Docker images

docker-%: .FORCE
	docker build -t olareg/$* -f build/Dockerfile.$*$(DOCKERFILE_EXT) $(DOCKER_ARGS) .
	docker build -t olareg/$*:alpine -f build/Dockerfile.$*$(DOCKERFILE_EXT) --target release-alpine $(DOCKER_ARGS) .

.PHONY: oci-image
oci-image: $(addprefix oci-image-,$(COMMANDS)) ## Build reproducible images to an OCI Layout

oci-image-%: .FORCE
	PATH="$(PWD)/bin:$(PATH)" build/oci-image.sh -r scratch -i "$*" -p "$(TEST_PLATFORMS)"
	PATH="$(PWD)/bin:$(PATH)" build/oci-image.sh -r alpine  -i "$*" -p "$(TEST_PLATFORMS)" -b "alpine:3"

.PHONY: test-docker
test-docker: $(addprefix test-docker-,$(COMMANDS)) ## Build multi-platform docker images (but do not tag)

test-docker-%:
	docker buildx build --platform="$(TEST_PLATFORMS)" -f build/Dockerfile.$*.buildkit .
	docker buildx build --platform="$(TEST_PLATFORMS)" -f build/Dockerfile.$*.buildkit --target release-alpine .

# utilities for managing the project

.PHONY: util-golang-major
util-golang-major: $(GOPATH)/bin/gomajor ## check for major dependency updates
	$(GOPATH)/bin/gomajor list

.PHONY: util-golang-update
util-golang-update: ## update go module versions
	go get -u -t ./...
	go mod tidy
	[ ! -d vendor ] || go mod vendor

.PHONY: util-release-preview
util-release-preview: $(GOPATH)/bin/gorelease ## preview changes for next release
	git checkout main
	./.github/release.sh -d
	gorelease

.PHONY: util-release-run
util-release-run: ## generate a new release
	git checkout main
	./.github/release.sh

.PHONY: util-version-check
util-version-check: ## check all dependencies for updates
	$(VER_BUMP) check

.PHONY: util-version-update
util-version-update: ## update versions on all dependencies
	$(VER_BUMP) update

# various Go tools

$(GOPATH)/bin/gomajor: .FORCE
	@[ -f "$(GOPATH)/bin/gomajor" ] \
	&& [ "$$($(GOPATH)/bin/gomajor version | grep '^version' | cut -f 2 -d ' ')" = "$(GOMAJOR_VER)" ] \
	|| go install github.com/icholy/gomajor@$(GOMAJOR_VER)

$(GOPATH)/bin/goimports: .FORCE
	@[ -f "$(GOPATH)/bin/goimports" ] \
	||	go install golang.org/x/tools/cmd/goimports@latest

$(GOPATH)/bin/gorelease: .FORCE
	@[ -f "$(GOPATH)/bin/gorelease" ] \
	|| go install golang.org/x/exp/cmd/gorelease@latest

$(GOPATH)/bin/gosec: .FORCE
	@[ -f $(GOPATH)/bin/gosec ] \
	&& [ "$$($(GOPATH)/bin/gosec -version | grep '^Version' | cut -f 2 -d ' ')" = "$(GOSEC_VER)" ] \
	|| go install -ldflags '-X main.Version=$(GOSEC_VER) -X main.GitTag=$(GOSEC_VER)' \
	    github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VER)

$(GOPATH)/bin/staticcheck: .FORCE
	@[ -f $(GOPATH)/bin/staticcheck ] \
	&& [ "$$($(GOPATH)/bin/staticcheck -version | cut -f 3 -d ' ' | tr -d '()')" = "$(STATICCHECK_VER)" ] \
	|| go install "honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VER)"

$(GOPATH)/bin/govulncheck: .FORCE
	@[ $$(go version -m $(GOPATH)/bin/govulncheck | \
		awk -F ' ' '{ if ($$1 == "mod" && $$2 == "golang.org/x/vuln") { printf "%s\n", $$3 } }') = "$(GO_VULNCHECK_VER)" ] \
	|| CGO_ENABLED=0 go install "golang.org/x/vuln/cmd/govulncheck@$(GO_VULNCHECK_VER)"

$(GOPATH)/bin/osv-scanner: .FORCE
	@[ $$(go version -m $(GOPATH)/bin/osv-scanner | \
		awk -F ' ' '{ if ($$1 == "mod" && $$2 == "github.com/google/osv-scanner") { printf "%s\n", $$3 } }') = "$(OSV_SCANNER_VER)" ] \
	|| CGO_ENABLED=0 go install "github.com/google/osv-scanner/cmd/osv-scanner@$(OSV_SCANNER_VER)"

.PHONY: help
help: # Display help
	@awk -F ':|##' '/^[^\t].+?:.*?##/ { printf "\033[36m%-30s\033[0m %s\n", $$1, $$NF }' $(MAKEFILE_LIST)
