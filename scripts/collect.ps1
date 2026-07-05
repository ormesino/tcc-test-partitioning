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
      - ADR-017: mede cada pacote sob `-p 1 -parallel 1`. Essas flags
                 serializam pacotes e testes que usam t.Parallel, mas nao
                 limitam goroutines internas; essa semantica esta sob
                 diagnostico separado antes de eventual GOMAXPROCS=1.

    Cada rodada gera tres arquivos em data/probe/<ProjectName>/:
        run_NN.json       ← stdout puro (NDJSON consumido por cmd/analyze)
        run_NN.err        ← stderr (compile errors, warnings — diagnostico)
        run_NN.meta.json  ← comando, timestamps, exit code e indicio de timeout
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
# Test failures are data for pass-only characterization, not PowerShell errors.
# Keep native non-zero exits observable through $LASTEXITCODE so the sidecar is
# always written and the validator can distinguish test failure from truncation.
if (Get-Variable PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

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
    $file     = Join-Path $probeDir "run_$tag.json"
    $errFile  = Join-Path $probeDir "run_$tag.err"
    $metaFile = Join-Path $probeDir "run_$tag.meta.json"

    Write-Host "  [$tag/$Runs] go test -json -p 1 -parallel 1 -count=1 -timeout ${TimeoutMinutes}m $Pattern"

    Push-Location $ProjectPath
    try {
        # stdout (NDJSON puro) e stderr (compile errors, warnings) vao para
        # arquivos distintos. O sidecar preserva o exit code, que nao pode ser
        # reconstruido com seguranca apenas a partir do NDJSON.
        $utf8 = New-Object System.Text.UTF8Encoding($false)
        $startedAt = Get-Date
        $outLines = @(& go test -json "-p" "1" "-parallel" "1" "-count=1" "-timeout" "${TimeoutMinutes}m" $Pattern 2> $errFile)
        $exitCode = $LASTEXITCODE
        $finishedAt = Get-Date
        [System.IO.File]::WriteAllLines($file, $outLines, $utf8)

        $combinedDiagnostic = (($outLines -join "`n") + "`n")
        if (Test-Path $errFile) {
            $combinedDiagnostic += Get-Content -Raw -LiteralPath $errFile
        }
        $meta = [ordered]@{
            command = "go test -json -p 1 -parallel 1 -count=1 -timeout ${TimeoutMinutes}m $Pattern"
            started_at = $startedAt.ToUniversalTime().ToString('o')
            finished_at = $finishedAt.ToUniversalTime().ToString('o')
            exit_code = $exitCode
            timed_out = ($combinedDiagnostic -match '(?i)test timed out after|timed out|deadline exceeded')
        }
        [System.IO.File]::WriteAllText($metaFile, ($meta | ConvertTo-Json -Depth 4), $utf8)
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
$validationFile = Join-Path $probeDir 'validation.json'
Write-Host "==> Validando integridade retroativa dos probes"
Push-Location $repoRoot
try {
    & go run ./cmd/validateprobes `
        -project-path $ProjectPath `
        -pattern $Pattern `
        -expected-runs $Runs `
        -output $validationFile `
        @runFiles
    if ($LASTEXITCODE -ne 0) {
        throw "Validacao dos probes falhou. Consulte $validationFile antes de agregar."
    }
}
finally {
    Pop-Location
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
