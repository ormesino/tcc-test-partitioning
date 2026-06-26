# Makefile for tcc-test-partitioning.
# Works in PowerShell (via GNU Make / Chocolatey) or in Bash.

GO ?= go
BIN_DIR := bin
SYNTH_DIR := data/synthetic
REPORTS_DIR := reports

.PHONY: all build test fmt vet tidy clean \
        gendata demo simulate-example bench-example \
        check ci run-campaigns clean-logs clean-reports

all: check build

## --- development ---------------------------------------------------

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./cmd/... ./internal/... ./data/synthetic

# Recommended shortcut before committing.
check: fmt vet test

# Deterministic pipeline for CI (without `fmt`, which alters files).
ci: vet test

## --- builds --------------------------------------------------------

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/ ./cmd/...

clean:
	@rm -rf $(BIN_DIR)

clean-logs:
	@rm -rf logs/campaigns/*

clean-reports:
	@rm -rf $(REPORTS_DIR)/*
	@rm -rf benchmarks/results/*

## --- fixtures and examples -----------------------------------------

# Generates the three synthetic datasets in data/synthetic/.
gendata:
	$(GO) run ./cmd/gendata -profile all -output-dir $(SYNTH_DIR)

# Visual demonstration + JSON report.
demo: gendata
	@mkdir -p $(REPORTS_DIR)
	$(GO) run ./cmd/demo --output-json $(REPORTS_DIR)/demo.json

# Simulate example on the "moderate" synthetic dataset.
simulate-example: gendata
	@mkdir -p $(REPORTS_DIR)
	$(GO) run ./cmd/partitioner --mode simulate \
		--algorithm lpt --workers 4 \
		--data-file $(SYNTH_DIR)/moderate.json \
		--output-json $(REPORTS_DIR)/simulate-moderate-lpt-w4.json

# Full experimental matrix in simulate mode (example config).
bench-example: gendata
	$(GO) run ./cmd/benchmark --config benchmarks/example-config.json

## --- experimental campaigns (PowerShell wrappers) ------------------

# Runs the complete campaign execution script (cold and warm).
run-campaigns:
	pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1
