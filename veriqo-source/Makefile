.PHONY: all build test bench lint vet fuzz coverage clean generate \
        race chaos ci sbom vulncheck

GOFLAGS   := -trimpath
TESTFLAGS := -count=1 -timeout=120s
FUZZTIME  := 30s
COVERDIR  := .coverage
BINARY    := bin/veriqod
CTL_BIN   := bin/veriqoctl

all: vet lint test build

# ─── Build ───────────────────────────────────────────────────────────────────
build:
	@mkdir -p bin
	go build $(GOFLAGS) -o $(BINARY)   ./cmd/veriqod/...
	go build $(GOFLAGS) -o $(CTL_BIN) ./cmd/veriqoctl/...

# ─── Test ────────────────────────────────────────────────────────────────────
test:
	go test $(TESTFLAGS) ./...

race:
	go test $(TESTFLAGS) -race ./...

bench:
	go test -bench=. -benchmem -run='^$$' ./...

fuzz:
	go test -fuzz=FuzzWALRoundtrip     -fuzztime=$(FUZZTIME) ./pkg/storage/wal/...
	go test -fuzz=FuzzEvidenceAppend   -fuzztime=$(FUZZTIME) ./pkg/storage/evidence/...
	go test -fuzz=FuzzPolicyEval       -fuzztime=$(FUZZTIME) ./pkg/security/policy/...
	go test -fuzz=FuzzRaftEntry        -fuzztime=$(FUZZTIME) ./pkg/consensus/raft/...

chaos:
	go test $(TESTFLAGS) -run=TestChaos -v -count=5 ./pkg/consensus/raft/...
	go test $(TESTFLAGS) -run=TestChaos -v -count=5 ./pkg/os/...

coverage:
	@mkdir -p $(COVERDIR)
	go test $(TESTFLAGS) -coverprofile=$(COVERDIR)/cover.out -covermode=atomic ./...
	go tool cover -html=$(COVERDIR)/cover.out -o $(COVERDIR)/cover.html
	go tool cover -func=$(COVERDIR)/cover.out | tail -1

# ─── Quality gates ───────────────────────────────────────────────────────────
vet:
	go vet ./...

lint:
	@if command -v golangci-lint &>/dev/null; then \
		golangci-lint run --timeout=5m ./...; \
	else \
		echo "golangci-lint not found — run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

vulncheck:
	@if command -v govulncheck &>/dev/null; then \
		govulncheck ./...; \
	else \
		echo "govulncheck not found — run: go install golang.org/x/vuln/cmd/govulncheck@latest"; \
	fi

sbom:
	@if command -v cyclonedx-gomod &>/dev/null; then \
		cyclonedx-gomod app -output bom.cdx.xml -licenses -json ./cmd/veriqod; \
	else \
		echo "cyclonedx-gomod not found — run: go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest"; \
	fi

# ─── CI ──────────────────────────────────────────────────────────────────────
ci: vet test race coverage lint vulncheck sbom build

# ─── Codegen ─────────────────────────────────────────────────────────────────
generate:
	go generate ./...

# ─── Clean ───────────────────────────────────────────────────────────────────
clean:
	rm -rf bin $(COVERDIR) bom.cdx.xml
