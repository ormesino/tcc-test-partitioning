<#
.SYNOPSIS
    Rebuilds characterizations for the selected subject projects.

.DESCRIPTION
    Orchestrates scripts/collect.ps1 for any selected subset of the four subject
    repositories under repos/. Existing characterizations are copied to a
    timestamped data/characterization/old/ directory before collection.

    With -ArchiveProbe, the previous probe directory is also moved to a
    timestamped data/probe/old/ directory so run_01.json through run_NN.json are
    not overwritten. A separate execution log is retained for each project.

.PARAMETER Projects
    Subject projects to recharacterize. Defaults to all four final projects.

.PARAMETER Runs
    Number of probes collected for each project. Default: 10.

.PARAMETER TimeoutMinutes
    Timeout passed to each probe execution. Default: 60 minutes.

.PARAMETER ArchiveProbe
    Archives each existing project probe directory before recollection.

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

Write-Host "==> Recharacterization started at $timestamp"
Write-Host "    Projects: $($Projects -join ', ')"
Write-Host "    Runs: $Runs"
Write-Host "    Timeout per probe: ${TimeoutMinutes}m"
Write-Host "    Logs: $logDir"
Write-Host ""

foreach ($project in $Projects) {
    if (-not $projectPaths.ContainsKey($project)) {
        throw "Unknown project: $project"
    }

    $projectPath = Join-Path $repoRoot $projectPaths[$project]
    if (-not (Test-Path $projectPath)) {
        throw "Repository not found for ${project}: $projectPath"
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
    Write-Host "==> [$project] collecting $Runs probes"
    & pwsh -NoProfile -ExecutionPolicy Bypass -File $collectScript `
        -ProjectPath $projectPath `
        -ProjectName $project `
        -Runs $Runs `
        -TimeoutMinutes $TimeoutMinutes 2>&1 |
        Tee-Object -FilePath $logFile

    if ($LASTEXITCODE -ne 0) {
        throw "Failed to recharacterize $project (exit=$LASTEXITCODE). Review $logFile."
    }

    Write-Host "==> [$project] complete. Log: $logFile"
    Write-Host ""
}

Write-Host "==> Recharacterization complete."
Write-Host "    Previous characterizations: $charBackupDir"
if ($ArchiveProbe) {
    Write-Host "    Previous probes: $probeBackupDir"
}
