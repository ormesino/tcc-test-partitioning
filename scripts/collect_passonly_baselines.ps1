<#
.SYNOPSIS
    Coleta baselines pass-only usando data/characterization/*.json.

.DESCRIPTION
    Executa cmd/partitioner nos modos baseline-seq e baseline-par passando
    --data-file, de modo que o baseline rode exatamente os pacotes presentes
    na caracterizacao usada pelas campanhas.

.EXAMPLE
    pwsh scripts/collect_passonly_baselines.ps1

.EXAMPLE
    pwsh scripts/collect_passonly_baselines.ps1 -WarmOnly
#>
[CmdletBinding()]
param(
    [string[]] $Projects = @('cli', 'grpc-go', 'goreleaser', 'hugo'),
    [int[]] $Workers = @(2, 4, 8),
    [int] $TimeoutMinutes = 60,
    [switch] $ColdOnly,
    [switch] $WarmOnly
)

$ErrorActionPreference = 'Stop'

if ($ColdOnly -and $WarmOnly) {
    throw "Use apenas um entre -ColdOnly e -WarmOnly."
}

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$logDir = Join-Path $repoRoot "logs/baseline-passonly/$timestamp"
$backupDir = Join-Path $repoRoot "archive/baselines-replaced/$timestamp"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
New-Item -ItemType Directory -Force -Path $backupDir | Out-Null

$projectPaths = @{
    'cli'        = 'repos/cli'
    'grpc-go'    = 'repos/grpc-go'
    'goreleaser' = 'repos/goreleaser'
    'hugo'       = 'repos/hugo'
}

$runCold = -not $WarmOnly
$runWarm = -not $ColdOnly

function Invoke-Baseline {
    param(
        [string] $Project,
        [string] $Mode,
        [int] $WorkersValue,
        [bool] $Warm
    )

    # O comando roda a partir de $repoRoot; caminhos relativos mantêm os
    # relatórios portáveis e evitam persistir o diretório pessoal do coletor.
    $projectPath = $projectPaths[$Project]
    $dataFile = "data/characterization/$Project.json"
    $suffix = if ($Warm) { '-warm-passonly' } else { '-passonly' }

    if ($Mode -eq 'baseline-seq') {
        $fileName = "$Project-seq$suffix.json"
        $outFile = Join-Path $repoRoot "data/baseline/$fileName"
        $stagedFile = Join-Path $logDir "$fileName.staged"
        $logFile = Join-Path $logDir "$Project-seq$suffix.log"
        $args = @(
            'run', './cmd/partitioner',
            '--mode', 'baseline-seq',
            '--project-path', $projectPath,
            '--data-file', $dataFile,
            '--timeout', "$TimeoutMinutes",
            '--output', $stagedFile
        )
    } else {
        $fileName = "$Project-par-w$WorkersValue$suffix.json"
        $outFile = Join-Path $repoRoot "data/baseline/$fileName"
        $stagedFile = Join-Path $logDir "$fileName.staged"
        $logFile = Join-Path $logDir "$Project-par-w$WorkersValue$suffix.log"
        $args = @(
            'run', './cmd/partitioner',
            '--mode', 'baseline-par',
            '--workers', "$WorkersValue",
            '--project-path', $projectPath,
            '--data-file', $dataFile,
            '--timeout', "$TimeoutMinutes",
            '--output', $stagedFile
        )
    }

    if ($Warm) {
        $args += '--warm-cache'
    }

    Write-Host "==> $Project $Mode $(if ($Mode -eq 'baseline-par') { "w=$WorkersValue " })$(if ($Warm) { 'warm' } else { 'cold' })"
    & go @args 2>&1 | Tee-Object -FilePath $logFile
    if ($LASTEXITCODE -ne 0) {
        throw "Falha ao coletar $outFile (exit=$LASTEXITCODE). Veja $logFile"
    }
    if (-not (Test-Path -LiteralPath $stagedFile)) {
        throw "Coleta terminou sem produzir o artefato temporario: $stagedFile"
    }

    $report = Get-Content -Raw -LiteralPath $stagedFile | ConvertFrom-Json
    if (-not $report.success) {
        throw "Baseline falhou e nao substituira o artefato atual: $($report.error). Diagnostico: $stagedFile"
    }

    $backupFile = Join-Path $backupDir $fileName
    $previousMoved = $false
    if (Test-Path -LiteralPath $outFile) {
        Move-Item -LiteralPath $outFile -Destination $backupFile
        $previousMoved = $true
    }
    try {
        Move-Item -LiteralPath $stagedFile -Destination $outFile
    }
    catch {
        if ($previousMoved -and -not (Test-Path -LiteralPath $outFile)) {
            Move-Item -LiteralPath $backupFile -Destination $outFile
        }
        throw
    }
    Write-Host "==> Baseline publicado: $outFile"
}

Push-Location $repoRoot
try {
    foreach ($project in $Projects) {
        if (-not $projectPaths.ContainsKey($project)) {
            throw "Projeto desconhecido: $project"
        }

        if ($runCold) {
            Invoke-Baseline -Project $project -Mode 'baseline-seq' -Warm:$false
            foreach ($w in $Workers) {
                Invoke-Baseline -Project $project -Mode 'baseline-par' -WorkersValue $w -Warm:$false
            }
        }

        if ($runWarm) {
            Invoke-Baseline -Project $project -Mode 'baseline-seq' -Warm:$true
            foreach ($w in $Workers) {
                Invoke-Baseline -Project $project -Mode 'baseline-par' -WorkersValue $w -Warm:$true
            }
        }
    }
}
finally {
    Pop-Location
}

Write-Host "==> Baselines pass-only concluidos. Logs: $logDir"
