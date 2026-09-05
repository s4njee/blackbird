VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOFLAGS ?= -trimpath

LDFLAGS = -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildDate=$(DATE)

.PHONY: build build-go build-web dev test test-web lint-web e2e vet staticcheck bench bench-update compose-config compose-smoke theme-smoke compose-named-smoke compose-macos-paths clean

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

## test-web: frontend typecheck + vitest unit suites
test-web:
	cd web && npm run typecheck && npm test

## lint-web: eslint + prettier check + stylelint for the frontend
lint-web:
	cd web && npm run lint && npm run lint:css && npm run format:check

## e2e: Playwright browser suite against a fakertorrent-backed server
## (builds web/dist first — the test server embeds it). Against the Compose
## appliance instead: cd web && E2E_BASE_URL=http://<host>:8222 npm run e2e
e2e:
	cd web && npm run build && npm run e2e

vet:
	go vet ./...

## staticcheck: zero-finding gate (POL-8.8) — same version CI pins.
staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

## bench: performance fixtures (PERF-6.6) — poll cycle per session size,
## delta computation, and WebSocket encoding, with allocations.
bench:
	go test -run='^$$' -bench='BenchmarkPollCycle|BenchmarkComputeDelta|BenchmarkListDecode|BenchmarkDeltaEncoding' -benchmem -benchtime=500ms -count=1 ./internal/poller/ ./internal/api/

## bench-update: re-record checked-in perf baselines for this platform
## (runs the guard in record mode on a quiet machine, then commit the file).
bench-update:
	PERF_GUARD=1 PERF_UPDATE=1 go test -run TestPerfRegression -count=1 ./internal/perf/

## bench-guard: the CI perf gate (isolated step, not part of go test ./...).
bench-guard:
	PERF_GUARD=1 go test -run TestPerfRegression -count=1 ./internal/perf/

compose-config:
	docker compose config -q

compose-smoke:
	./deploy/smoke-test.sh

## theme-smoke: disposable Compose appliance + Playwright dark/light loads
## with zero console errors (THM-9.5).
theme-smoke:
	./deploy/theme-smoke.sh

compose-named-smoke:
	./deploy/named-volume-smoke.sh

compose-macos-paths:
	./deploy/verify-macos-paths.sh

clean:
	rm -rf bin
