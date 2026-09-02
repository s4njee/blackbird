VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOFLAGS ?= -trimpath

LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildDate=$(DATE)

.PHONY: build build-go build-web dev test vet compose-config compose-smoke compose-named-smoke compose-macos-paths clean

## build: frontend then backend in one step (single-binary artifact)
build: build-web build-go

build-web:
	cd web && npm ci && npm run build

build-go:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/blackbird ./cmd/blackbird

## dev: run the Vite dev server (proxies /api and /ws to a running backend)
dev:
	cd web && npm run dev

test:
	go test ./...

vet:
	go vet ./...

compose-config:
	docker compose config -q

compose-smoke:
	./deploy/smoke-test.sh

compose-named-smoke:
	./deploy/named-volume-smoke.sh

compose-macos-paths:
	./deploy/verify-macos-paths.sh

clean:
	rm -rf bin
