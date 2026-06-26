<#
.SYNOPSIS
    Reexecuta a caracterizacao dos projetos selecionados.

.DESCRIPTION
    Orquestra scripts/collect.ps1 para os quatro repositorios em repos/,
    preservando caracterizacoes anteriores em data/characterization/old/.
    Opcionalmente arquiva tambem os probes antigos para evitar sobrescrita de
    run_01.json...run_NN.json.

.EXAMPLE
    pwsh scripts/recharacterize_all.ps1 -Runs 10 -TimeoutMinutes 60

.EXAMPLE
    pwsh scripts/recharacterize_all.ps1 -Projects cli,grpc-go,goreleaser -Runs 10
#>
[CmdletBinding()]
param(
    [string[]] $Projects = @('cli', 'grpc-go', 'goreleaser', 'hugo'),
    [int] $Runs = 10,
    [int] $TimeoutMinutes = 60,
    [switch] $ArchiveProbe
)

$ErrorActionPreference = 'Stop'

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$collectScript = Join-Path $repoRoot 'scripts/collect.ps1'
$charDir = Join-Path $repoRoot 'data/characterization'
$probeDir = Join-Path $repoRoot 'data/probe'
$charBackupDir = Join-Path $charDir "old/$timestamp"
$probeBackupDir = Join-Path $probeDir "old/$timestamp"
$logDir = Join-Path $repoRoot "logs/recharacterization/$timestamp"

$projectPaths = @{
    'cli'        = 'repos/cli'
    'grpc-go'    = 'repos/grpc-go'
    'goreleaser' = 'repos/goreleaser'
    'hugo'       = 'repos/hugo'
}

New-Item -ItemType Directory -Force -Path $charBackupDir | Out-Null
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
if ($ArchiveProbe) {
    New-Item -ItemType Directory -Force -Path $probeBackupDir | Out-Null
}

Write-Host "==> Recaracterizacao iniciada em $timestamp"
Write-Host "    Projetos: $($Projects -join ', ')"
Write-Host "    Runs: $Runs"
Write-Host "    Timeout por run: ${TimeoutMinutes}m"
Write-Host "    Logs: $logDir"
Write-Host ""

foreach ($project in $Projects) {
    if (-not $projectPaths.ContainsKey($project)) {
        throw "Projeto desconhecido: $project"
    }

    $projectPath = Join-Path $repoRoot $projectPaths[$project]
    if (-not (Test-Path $projectPath)) {
        throw "Repositorio nao encontrado para ${project}: $projectPath"
    }

    $charFile = Join-Path $charDir "$project.json"
    if (Test-Path $charFile) {
        Copy-Item -LiteralPath $charFile -Destination (Join-Path $charBackupDir "$project.json") -Force
    }

    $projectProbeDir = Join-Path $probeDir $project
    if ($ArchiveProbe -and (Test-Path $projectProbeDir)) {
        Move-Item -LiteralPath $projectProbeDir -Destination (Join-Path $probeBackupDir $project) -Force
    }

    $logFile = Join-Path $logDir "$project.log"
    Write-Host "==> [$project] coletando $Runs rodadas"
    & pwsh -NoProfile -ExecutionPolicy Bypass -File $collectScript `
        -ProjectPath $projectPath `
        -ProjectName $project `
        -Runs $Runs `
        -TimeoutMinutes $TimeoutMinutes 2>&1 |
        Tee-Object -FilePath $logFile

    if ($LASTEXITCODE -ne 0) {
        throw "Falha na recaracterizacao de $project (exit=$LASTEXITCODE). Veja $logFile"
    }

    Write-Host "==> [$project] concluido. Log: $logFile"
    Write-Host ""
}

Write-Host "==> Recaracterizacao concluida."
Write-Host "    Caracterizacoes antigas: $charBackupDir"
if ($ArchiveProbe) {
    Write-Host "    Probes antigos: $probeBackupDir"
}
