VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/phenixrizen/conductor/internal/version.Version=$(VERSION) \
           -X github.com/phenixrizen/conductor/internal/version.Commit=$(COMMIT)
GO_FILES := $(shell find . -name '*.go' -not -path './web/*')

.PHONY: help build build-go web-install web-build web-typecheck web-dev run dev test test-web lint fmt generate docker clean

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

build: web-build build-go ## build the single binary with the embedded UI

build-go: ## build the Go binary using whatever UI is in internal/web/dist
	@touch internal/web/dist/.gitkeep
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/conductor ./cmd/conductor

web-install: ## reproducible npm install
	cd web && npm ci

web-build: ## generate the static SPA and copy it into internal/web/dist
	cd web && npm run generate
	find internal/web/dist -mindepth 1 -not -name .gitkeep -delete
	cp -r web/.output/public/. internal/web/dist/
	touch internal/web/dist/.gitkeep

web-typecheck: ## vue-tsc type check
	cd web && npm run typecheck

web-dev: ## nuxt dev server on :3000 proxying to :8080
	cd web && npm run dev

run: ## run the API server in dev mode against conductor.example.json
	go run ./cmd/conductor serve --config conductor.example.json --dev --log-level debug

dev: ## instructions for two-process development
	@echo "Terminal 1: make run      (Go API on :8080, --dev)"
	@echo "Terminal 2: make web-dev  (Nuxt on :3000, proxies /api and /ws)"

test: ## race-enabled Go tests
	go test -race -count=1 ./...

test-web: web-typecheck ## frontend checks

lint: ## gofmt and go vet
	@out="$$(gofmt -l $(GO_FILES))"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

fmt: ## gofmt all Go files
	gofmt -w $(GO_FILES)

generate: ## go generate (regenerates the web bundle)
	go generate ./internal/web

docker: ## build the container image
	docker build -t conductor:$(VERSION) .

clean:
	rm -rf bin web/.nuxt web/.output
	find internal/web/dist -mindepth 1 -not -name .gitkeep -delete
