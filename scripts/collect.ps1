<#
.SYNOPSIS
    Coleta N execucoes de `go test -json` em um projeto Go e gera o
    JSON de caracterizacao consumido por cmd/partitioner.

.DESCRIPTION
    Implementa o pipeline definido pelas ADRs 006-008 e ADR-017:
      - ADR-006: pacotes que falham ou sao skipados sao excluidos
                 (filtragem feita por cmd/analyze).
      - ADR-007: executa N rodadas com `-count=1` (default N=10).
      - ADR-008: mediana entre as rodadas como duracao canonica.
      - ADR-017: mede cada pacote sob `-p 1 -parallel 1`, alinhando
                 a caracterizacao historica ao modelo sequencial por
                 worker usado pelo executor.

    Cada rodada gera dois arquivos em data/probe/<ProjectName>/:
        run_NN.json  ← stdout puro (NDJSON consumido por cmd/analyze)
        run_NN.err   ← stderr (compile errors, warnings — diagnostico)
    Ao final, cmd/analyze agrega todas as rodadas em
        data/characterization/<ProjectName>.json

.PARAMETER ProjectPath
    Caminho absoluto da raiz do projeto Go (deve conter go.mod).

.PARAMETER ProjectName
    Identificador curto usado nos diretorios e nomes de arquivos
    (ex.: cli, hugo, goreleaser, grpc-go).

.PARAMETER Runs
    Numero de execucoes. Default: 10.

.PARAMETER TimeoutMinutes
    Timeout passado ao `go test -timeout`. Default: 50.

.PARAMETER Pattern
    Padrao de pacotes passado ao `go test`. Default: ./...

.EXAMPLE
    pwsh scripts/collect.ps1 -ProjectPath C:\src\cli -ProjectName cli

.EXAMPLE
    pwsh scripts/collect.ps1 -ProjectPath C:\src\hugo -ProjectName hugo `
        -Runs 10 -TimeoutMinutes 45
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $ProjectPath,
    [Parameter(Mandatory)] [string] $ProjectName,
    [int]    $Runs           = 10,
    [int]    $TimeoutMinutes = 50,
    [string] $Pattern        = './...'
)

$ErrorActionPreference = 'Stop'

# Resolve diretorios relativos ao repositorio (scripts/ esta na raiz).
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$probeDir = Join-Path $repoRoot "data/probe/$ProjectName"
$outDir   = Join-Path $repoRoot 'data/characterization'
$outFile  = Join-Path $outDir   "$ProjectName.json"

if (-not (Test-Path $ProjectPath)) {
    throw "ProjectPath nao existe: $ProjectPath"
}
if (-not (Test-Path (Join-Path $ProjectPath 'go.mod'))) {
    Write-Warning "go.mod nao encontrado em $ProjectPath (prosseguindo mesmo assim)."
}

New-Item -ItemType Directory -Force -Path $probeDir | Out-Null
New-Item -ItemType Directory -Force -Path $outDir   | Out-Null

# Verifica que go esta no PATH (a coleta real depende disso).
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    throw "binario 'go' nao encontrado no PATH. Instale o Go antes da coleta."
}

Write-Host "==> Projeto:  $ProjectName"
Write-Host "    Path:     $ProjectPath"
Write-Host "    Runs:     $Runs"
Write-Host "    Timeout:  ${TimeoutMinutes}m"
Write-Host "    Probe:    $probeDir"
Write-Host "    Output:   $outFile"
Write-Host ''

$runFiles = New-Object System.Collections.Generic.List[string]
for ($i = 1; $i -le $Runs; $i++) {
    $tag     = '{0:D2}' -f $i
    $file    = Join-Path $probeDir "run_$tag.json"
    $errFile = Join-Path $probeDir "run_$tag.err"

    Write-Host "  [$tag/$Runs] go test -json -p 1 -parallel 1 -count=1 -timeout ${TimeoutMinutes}m $Pattern"

    Push-Location $ProjectPath
    try {
        # stdout (NDJSON puro) e stderr (compile errors, warnings)
        # vao para arquivos distintos. analyze.go consome apenas o
        # arquivo NDJSON; o .err fica para diagnostico humano quando
        # algum pacote some da caracterizacao (ADR-006).
        # `go test` retorna exit != 0 quando ha testes falhando; nao
        # tratamos como erro aqui — ADR-006 filtra esses pacotes em
        # cmd/analyze.
        $utf8 = New-Object System.Text.UTF8Encoding($false)
        $outLines = & go test -json "-p" "1" "-parallel" "1" "-count=1" "-timeout" "${TimeoutMinutes}m" $Pattern 2> $errFile
        [System.IO.File]::WriteAllLines($file, $outLines, $utf8)
    }
    finally {
        Pop-Location
    }

    if ((Test-Path $errFile) -and ((Get-Item $errFile).Length -gt 0)) {
        Write-Host "       (stderr nao vazio: $errFile)" -ForegroundColor Yellow
    }

    $runFiles.Add($file)
}

Write-Host ''
Write-Host "==> Agregando $($runFiles.Count) rodadas -> $outFile"

Push-Location $repoRoot
try {
    & go run ./cmd/analyze -output $outFile @runFiles
    if ($LASTEXITCODE -ne 0) {
        throw "cmd/analyze falhou (exit=$LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

Write-Host ''
Write-Host "==> Concluido. Caracterizacao salva em $outFile"
