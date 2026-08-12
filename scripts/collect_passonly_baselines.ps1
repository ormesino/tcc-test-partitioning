<#
.SYNOPSIS
    Collects pass-only baselines from data/characterization/*.json.

.DESCRIPTION
    Runs cmd/partitioner in baseline-seq and baseline-par modes with a
    characterization file. This ensures that every baseline executes exactly
    the pass-only package population used by the benchmark campaigns.

    Before measurement, the script validates GOMAXPROCS, characterization
    availability, subject-project commits, and clean working trees. It collects
    cold and/or warm baselines, validates each staged report, backs up an
    existing canonical report, and publishes only successful artifacts.

.PARAMETER Projects
    Subject projects to process. Defaults to all four final projects.

.PARAMETER Workers
    Worker counts used for native parallel baselines. Default: 2, 4, and 8.

.PARAMETER TimeoutMinutes
    Timeout passed to each baseline execution. Default: 60 minutes.

.PARAMETER ColdOnly
    Collects only cold-cache baselines.

.PARAMETER WarmOnly
    Collects only warm-cache baselines.

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
# Handle native-process failures through exit codes and structured reports
# instead of converting them prematurely into RuntimeException instances.
if (Get-Variable PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

if ($ColdOnly -and $WarmOnly) {
    throw "Use only one of -ColdOnly and -WarmOnly."
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

$expectedCommits = @{
    'cli'        = 'da68cb8f6f597cfc3838cf40f89ecc01f4e53233'
    'goreleaser' = 'ce96e79b4883bdea39cf2cf5fe33fa63f5df4dd0'
    'grpc-go'    = 'faa34bf170ceef07b9ada9bcd44dc6e16a55d1f4'
    'hugo'       = '72495f9fba69edadd50a7ecb9ae9fb3d9c46156b'
}

# Fail before the first measurement when the runtime, package population, or
# subject source is incompatible, avoiding a partial 32-baseline matrix.
Push-Location $repoRoot
try {
    & go run ./cmd/preflight | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw 'GOMAXPROCS preflight failed.'
    }
    foreach ($project in $Projects) {
        if (-not $projectPaths.ContainsKey($project)) {
            throw "Unknown project: $project"
        }
        $dataFile = Join-Path $repoRoot "data/characterization/$project.json"
        if (-not (Test-Path -LiteralPath $dataFile)) {
            throw "Missing characterization for ${project}: $dataFile"
        }
        $packages = @(Get-Content -Raw -LiteralPath $dataFile | ConvertFrom-Json)
        if ($packages.Count -eq 0) {
            throw "Empty characterization for ${project}: $dataFile"
        }
        $actualCommit = (& git -C $projectPaths[$project] rev-parse HEAD).Trim()
        if ($LASTEXITCODE -ne 0 -or $actualCommit -ne $expectedCommits[$project]) {
            throw "Incorrect commit for ${project}: actual=$actualCommit expected=$($expectedCommits[$project])"
        }
        if (& git -C $projectPaths[$project] status --porcelain) {
            throw "The ${project} working tree is not clean."
        }
    }
}
finally {
    Pop-Location
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

    # Run from $repoRoot so relative paths keep reports portable and avoid
    # persisting the collector's personal directory.
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
        throw "Failed to collect $outFile (exit=$LASTEXITCODE). Review $logFile."
    }
    if (-not (Test-Path -LiteralPath $stagedFile)) {
        throw "Collection completed without producing the staged artifact: $stagedFile"
    }

    $report = Get-Content -Raw -LiteralPath $stagedFile | ConvertFrom-Json
    if (-not $report.success) {
        throw "The baseline failed and will not replace the current artifact: $($report.error). Diagnostic: $stagedFile"
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
    Write-Host "==> Baseline published: $outFile"
}

Push-Location $repoRoot
try {
    foreach ($project in $Projects) {
        if (-not $projectPaths.ContainsKey($project)) {
            throw "Unknown project: $project"
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

Write-Host "==> Pass-only baseline collection complete. Logs: $logDir"
