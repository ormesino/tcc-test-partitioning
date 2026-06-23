# tcc-test-partitioning

Avaliação empírica de algoritmos de particionamento de testes para suítes Go.
Compara quatro algoritmos — Round-Robin, Quantity, LPT e FFD-Weighted — contra
duas baselines (`go test -p 1` sequencial e `go test -p P` paralelo nativo)
sob o modelo de escalonamento P||Cmax (Graham, 1969).

Projeto de TCC. Sem dependências externas: apenas a biblioteca padrão do Go
(≥ 1.22), inclusive nos testes.

## Estrutura

```
cmd/
  analyze/      agrega N execuções de `go test -json` em PackageInfo (mediana + CV)
  benchmark/    driver experimental: projetos × workers × algoritmos × reps
  demo/         exploração com dados sintéticos (sem `go test` real)
  gendata/      exporta fixtures sintéticas como JSON
  partitioner/  CLI principal: simulate, run, baseline-seq, baseline-par
data/
  synthetic/    fixtures Go que produzem perfis estatísticos conhecidos
internal/
  model/        tipos de domínio (PackageInfo, Partition, PartitionResult)
  partitioner/  implementações dos quatro algoritmos
  executor/     execução paralela de `go test` (CSP, 1 goroutine/worker)
  metrics/      Speedup, Efficiency, Makespan, desvio-padrão da carga
scripts/
  collect.ps1   orquestra a coleta de dados de um projeto real
benchmarks/
  example-config.json  configuração de exemplo para `cmd/benchmark`
docs/             metodologia, ADRs, diário de bordo, proposta
```

## Pré-requisitos

- Go 1.22+ (apenas para os modos `run`, `baseline-seq` e `baseline-par`;
  os modos `simulate` e `demo` rodam sem invocar `go test`).
- PowerShell 7+ se for usar `scripts/collect.ps1`.
- GNU `make` (opcional) para os atalhos do `Makefile`; no Windows,
  via Git Bash ou `choco install make`.
- Python com `pandas` e `matplotlib` apenas se for gerar os gráficos
  de análise com `scripts/generate_charts.py`.

## Pipeline experimental

### 1. Caracterizar projeto real (opcional)

```powershell
pwsh scripts/collect.ps1 -ProjectPath C:\src\cli -ProjectName cli -Runs 10
# → data/probe/cli/run_NN.json (N execuções de `go test -json`)
# → data/characterization/cli.json ([]PackageInfo agregado)
```

### 2. Medir baseline sequencial (T1)

```powershell
go run ./cmd/partitioner --mode baseline-seq `
  --project-path C:\src\cli `
  --output data/baseline/cli-seq.json
```

### 3. Simular ou executar com particionamento

```powershell
# Simulação (deterministica, sem invocar `go test`):
go run ./cmd/partitioner --mode simulate `
  --algorithm lpt --workers 4 `
  --data-file data/characterization/cli.json `
  --baseline-seq-file data/baseline/cli-seq.json `
  --output-json reports/cli-lpt-w4.json

# Execução real:
go run ./cmd/partitioner --mode run `
  --algorithm ffd --workers 4 `
  --data-file data/characterization/cli.json `
  --baseline-seq-file data/baseline/cli-seq.json `
  --project-path C:\src\cli `
  --output-json reports/cli-ffd-w4.json
```

### 4. Matriz experimental completa

```powershell
go run ./cmd/benchmark --config benchmarks/example-config.json
# → benchmarks/results/<timestamp>/{config.json, results.json, raw.csv, aggregate.csv}
```

## Roteiro para avaliadores

Este repositório permite dois níveis de reprodução. O primeiro valida a
ferramenta com dados sintéticos e não exige clonar projetos externos. O
segundo reproduz a campanha empírica sobre um projeto Go real.

### Caminho rápido: validar a ferramenta

```powershell
# 1. Gerar os datasets sintéticos em data/synthetic/.
go run ./cmd/gendata -profile all

# 2. Rodar a demonstração comparativa.
go run ./cmd/demo --output-json reports/demo.json

# 3. Rodar a matriz sintética de exemplo.
go run ./cmd/benchmark --config benchmarks/example-config.json
```

Esse fluxo executa os quatro algoritmos (`Round-Robin`, `Quantity`,
`LPT` e `FFD-Weighted`) para diferentes quantidades de workers e grava
relatórios estruturados em JSON/CSV. Ele é suficiente para inspecionar
a interface, o formato dos dados e o comportamento planejado dos
algoritmos.

### Caminho completo: gerar seus próprios dados

1. Clone ou disponibilize um projeto Go em um diretório local, por
   exemplo `C:\src\cli`.

2. Caracterize a suíte de testes. O script executa N rodadas de
   `go test -json -p 1 -parallel 1 -count=1`, salva os eventos brutos em
   `data/probe/<nome>/` e gera `data/characterization/<nome>.json`
   com mediana e CV por pacote.

```powershell
pwsh scripts/collect.ps1 -ProjectPath C:\src\cli -ProjectName cli -Runs 10
```

3. Meça o baseline sequencial. Esse arquivo fornece o T1 usado no
   cálculo de speedup empírico.

```powershell
go run ./cmd/partitioner --mode baseline-seq `
  --project-path C:\src\cli `
  --output data/baseline/cli-seq.json
```

4. Para uma campanha com cache quente, meça também o T1 no mesmo
   regime. O `--warm-cache` pré-compila os binários de teste antes
   da medição, de modo que o baseline e as execuções particionadas
   comparem tempos da mesma natureza.

```powershell
go run ./cmd/partitioner --mode baseline-seq --warm-cache `
  --project-path C:\src\cli `
  --output data/baseline/cli-seq-warm.json
```

5. Execute uma simulação pontual, sem rodar testes reais.

```powershell
go run ./cmd/partitioner --mode simulate `
  --algorithm all --workers 4 `
  --data-file data/characterization/cli.json `
  --output-json reports/cli-simulate-w4.json
```

6. Execute uma medição real particionada.

```powershell
go run ./cmd/partitioner --mode run --warm-cache `
  --algorithm ffd --workers 4 `
  --data-file data/characterization/cli.json `
  --baseline-seq-file data/baseline/cli-seq-warm.json `
  --project-path C:\src\cli `
  --output-json reports/cli-ffd-w4-warm.json
```

7. Para reproduzir a matriz experimental completa, ajuste uma config
   em `benchmarks/` e rode o driver:

```powershell
go run ./cmd/benchmark --config benchmarks/campaign_cli_warm.json
```

A saída é criada em `benchmarks/results/<projeto>/<timestamp>/` com:

- `config.json`: cópia auditável da configuração usada.
- `results.json`: relatório completo estruturado.
- `raw.csv`: uma linha por repetição.
- `aggregate.csv`: medianas e estatísticas por algoritmo/workers.

8. Opcionalmente gere gráficos acadêmicos:

```powershell
python scripts/generate_charts.py benchmarks/results/cli/<timestamp>
```

## Exploração sem ambiente Go real

```powershell
# Gera os JSONs sintéticos:
go run ./cmd/gendata -profile all

# Demonstração visual (4 algoritmos × 3 datasets × 3 worker counts):
go run ./cmd/demo --output-json reports/demo.json
```

## Testes

```powershell
go test ./...
```

Cobertura: contratos genéricos (entrada vazia, 1 worker, P > N, preservação
de pacotes) + propriedades específicas de cada algoritmo (cíclico, blocos
contíguos, ordem por duração decrescente, peso `Duration*(1+CV)`, limite de
Graham 4/3 − 1/(3p)) + métricas (Speedup, Efficiency, desvio).

## Decisões de projeto

Veja `docs/decisoes-tecnicas.md` para o registro completo de ADRs.
Pontos centrais:

- **ADR-007** — N = 10 execuções por coleta; mediana como valor canônico.
- **ADR-010** — peso FFD = `Duration × (1 + CV)`.
- **ADR-011** — duas baselines obrigatórias: `-p 1` (T1) e `-p P` (paralelo nativo).
- **ADR-012** — strategy pattern via interface `Partitioner`.
- **ADR-017 e ADR-018** — forçamos a simulação a ser sequencial por worker para fidelidade teórica, mas implementamos `"warm_cache": true` na config para separar a execução dos testes da compilação.

## Convenções de JSON

- Snake_case nos campos.
- Durações em nanossegundos com sufixo `_ns` (`int64`).
- `time.Time` em RFC3339.
- Campos opcionais marcados com `omitempty`.

