.DEFAULT_GOAL := build

STATICCHECK_VERSION ?= v0.8.1

# CI builds with the Go that go.mod names, and staticcheck reads only the
# package format of the Go it runs on, so lint here runs on that same Go
# rather than whichever one the machine was last upgraded to.
CI_GO := go$(shell awk '/^go /{print $$2}' go.mod)

# A Debian container with no D-Bus, no Secret Service and no browser: the
# machine reached over SSH that wlget has to sign in from without either.
LINUX_IMAGE := golang:$(patsubst go%,%,$(CI_GO))-bookworm

NFPM_VERSION ?= v2.47.0
LINUX_ARCHES := amd64 arm64

.PHONY: install-tools format comments lint test-build test test-linux test-e2e build packages test-packages formula apt

install-tools:
	GOTOOLCHAIN=$(CI_GO) go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	python3 -m pip install --quiet --upgrade git+https://github.com/botforge-pro/commentcensor.git

format:
	gofmt -w .

comments:
	commentcensor *.go

lint: comments
	go vet ./...
	gofmt -l . | (! grep .)
	GOTOOLCHAIN=$(CI_GO) staticcheck ./...
	go mod tidy -diff

test-build:
	go build -o /dev/null ./...
	go test -run '^$$' ./...

test:
	go test ./...

test-linux:
	docker run --rm -v "$(CURDIR)":/src -w /src -e GOFLAGS=-buildvcs=false $(LINUX_IMAGE) go test -count=1 ./...

# Signs in from another device against a Wikilayer server built from
# WIKILAYER_SRC, inside the same kind of container.
WIKILAYER_SRC ?= $(CURDIR)/../wikilayer

test-e2e:
	GO_VERSION=$(patsubst go%,%,$(CI_GO)) WIKILAYER_SRC=$(WIKILAYER_SRC) \
	    docker compose -f e2e/compose.yaml up --abort-on-container-exit --exit-code-from wlget; \
	    status=$$?; docker compose -f e2e/compose.yaml down -v >/dev/null 2>&1; exit $$status

build: lint test-build test
	mkdir -p bin
	go build -o bin/wlget .

# Linux archives and .deb packages for each of LINUX_ARCHES in dist/, with a
# .sha256 beside each: `make packages VERSION=0.2.1`.
packages:
	@test -n "$(VERSION)" || { echo "usage: make packages VERSION=<version>"; exit 1; }
	rm -rf dist
	@set -e; for arch in $(LINUX_ARCHES); do \
	    mkdir -p dist/linux_$$arch; \
	    CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/linux_$$arch/wlget .; \
	    tar -czf dist/wlget_$(VERSION)_linux_$$arch.tar.gz -C dist/linux_$$arch wlget -C ../.. LICENSE README.md; \
	    rm -rf dist/package; mkdir -p dist/package; cp dist/linux_$$arch/wlget dist/package/wlget; \
	    PACKAGE_ARCH=$$arch VERSION=$(VERSION) go run github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION) package \
	        --config packaging/nfpm.yaml --packager deb --target dist/wlget_$(VERSION)_$$arch.deb; \
	done
	cd dist && for f in *.tar.gz *.deb; do shasum -a 256 "$$f" > "$$f.sha256"; done

# Checks that each .deb names its own architecture, then installs the one for
# this machine in a clean Debian container, which has no CA certificates of its
# own, and runs it.
test-packages: packages
	docker run --rm -v "$(CURDIR)/dist":/dist:ro debian:bookworm sh -c \
	    'set -e; for arch in $(LINUX_ARCHES); do test "$$(dpkg-deb --field /dist/wlget_$(VERSION)_$$arch.deb Architecture)" = $$arch; done; \
	     test ! -e /etc/ssl/certs/ca-certificates.crt; \
	     apt-get update -qq && apt-get install -qq -y /dist/wlget_$(VERSION)_$$(dpkg --print-architecture).deb >/dev/null; \
	     test -s /etc/ssl/certs/ca-certificates.crt || { echo "wlget installed without the CA certificates it needs for https"; exit 1; }; \
	     test "$$(wlget -version)" = "$(VERSION)" && wlget -h 2>&1 | head -1'

# Publishes the release's .deb packages to the APT repository at
# wikilayer/apt, which signs the index: `make apt VERSION=0.2.1`.
apt:
	@test -n "$(VERSION)" || { echo "usage: make apt VERSION=<released version>"; exit 1; }
	@set -e; \
	    since=$$(date -u +%Y-%m-%dT%H:%M:%SZ); \
	    gh workflow run publish.yml --repo wikilayer/apt -f repository=wikilayer/wlget -f tag=v$(VERSION); \
	    run=""; for _ in 1 2 3 4 5 6 7 8 9 10; do \
	        sleep 3; \
	        run=$$(gh run list --repo wikilayer/apt --workflow publish.yml --event workflow_dispatch --created ">=$$since" --limit 1 --json databaseId --jq '.[0].databaseId // empty'); \
	        test -z "$$run" || break; \
	    done; \
	    test -n "$$run" || { echo "the Publish run did not appear in wikilayer/apt"; exit 1; }; \
	    gh run watch "$$run" --repo wikilayer/apt --exit-status

# Publishes the Homebrew formula for an existing tag to wikilayer/homebrew-tap,
# building from the tag's source archive: `make formula VERSION=0.1.0`.
formula:
	@test -n "$(VERSION)" || { echo "usage: make formula VERSION=<released version>"; exit 1; }
	@set -e; \
	    archive=https://github.com/wikilayer/wlget/archive/refs/tags/v$(VERSION).tar.gz; \
	    curl -fsSL -o wlget.tar.gz "$$archive"; \
	    sha=$$(shasum -a 256 wlget.tar.gz | cut -d ' ' -f 1); \
	    rm wlget.tar.gz; \
	    sed -e "s/@VERSION@/$(VERSION)/g" -e "s/@SHA256@/$$sha/g" packaging/homebrew/wlget.rb.in > wlget.rb; \
	    path=repos/wikilayer/homebrew-tap/contents/Formula/wlget.rb; \
	    current=$$(gh api "$$path" --jq .sha 2>/dev/null || true); \
	    gh api --method PUT "$$path" -f "message=Update wlget to v$(VERSION)" \
	        -f "content=$$(base64 < wlget.rb | tr -d '\n')" $${current:+-f "sha=$$current"} --jq .commit.html_url
