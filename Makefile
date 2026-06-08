# Makefile para tcc-test-partitioning.
# Funciona em PowerShell (via `make` do GNU/Chocolatey) ou em bash.

GO ?= go
BIN_DIR := bin
SYNTH_DIR := data/synthetic
REPORTS_DIR := reports

.PHONY: all build test fmt vet tidy clean \
        gendata demo simulate-example bench-example \
        check ci

all: check build

## --- desenvolvimento ----------------------------------------------

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

test:
	$(GO) test ./...

# Atalho recomendado antes de commit.
check: fmt vet test

# Pipeline determinística para CI (sem `fmt`, que altera arquivos).
ci: vet test

## --- builds --------------------------------------------------------

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/ ./cmd/...

clean:
	@rm -rf $(BIN_DIR)

## --- fixtures e exemplos ------------------------------------------

# Gera os três datasets sintéticos em data/synthetic/.
gendata:
	$(GO) run ./cmd/gendata -profile all -output-dir $(SYNTH_DIR)

# Demonstração visual + relatório JSON.
demo: gendata
	@mkdir -p $(REPORTS_DIR)
	$(GO) run ./cmd/demo --output-json $(REPORTS_DIR)/demo.json

# Exemplo de simulate sobre o dataset "moderate".
simulate-example: gendata
	@mkdir -p $(REPORTS_DIR)
	$(GO) run ./cmd/partitioner --mode simulate \
		--algorithm lpt --workers 4 \
		--data-file $(SYNTH_DIR)/moderate.json \
		--output-json $(REPORTS_DIR)/simulate-moderate-lpt-w4.json

# Matriz experimental completa em modo simulate (config de exemplo).
bench-example: gendata
	$(GO) run ./cmd/benchmark --config benchmarks/example-config.json
