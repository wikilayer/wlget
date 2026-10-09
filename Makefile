.DEFAULT_GOAL := build

STATICCHECK_VERSION ?= v0.8.1

.PHONY: install-tools format comments lint test-build test build formula

install-tools:
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	python3 -m pip install --quiet --upgrade git+https://github.com/botforge-pro/commentcensor.git

format:
	gofmt -w .

comments:
	commentcensor *.go

lint: comments
	go vet ./...
	gofmt -l . | (! grep .)
	staticcheck ./...
	go mod tidy -diff

test-build:
	go build -o /dev/null ./...
	go test -run '^$$' ./...

test:
	go test ./...

build: lint test-build test
	mkdir -p bin
	go build -o bin/wlget .

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
