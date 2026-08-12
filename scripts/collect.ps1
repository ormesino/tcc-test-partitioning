<#
.SYNOPSIS
    Builds the package-duration characterization for one Go project.

.DESCRIPTION
    Runs N package-level `go test -json` probes with `-count=1`, `-p 1`,
    `-parallel 1`, and an explicitly injected GOMAXPROCS=1. The default is ten
    probes, matching the final experimental protocol.

    Each probe writes three files under data/probe/<ProjectName>/:
        run_NN.json       - unmodified stdout NDJSON consumed by cmd/analyze
        run_NN.err        - stderr retained for diagnostics
        run_NN.meta.json  - command, timestamps, exit code, timeout evidence,
                            and effective GOMAXPROCS evidence

    After collection, cmd/validateprobes checks probe integrity. If validation
    succeeds, cmd/analyze retains only packages that passed every accepted probe
    and uses their median durations to write
    data/characterization/<ProjectName>.json. Finally, cmd/auditdurations
    independently reconciles the probe durations and characterization output.

.PARAMETER ProjectPath
    Path to the Go project root. The directory is expected to contain go.mod.

.PARAMETER ProjectName
    Short identifier used in directory and file names, such as cli, hugo,
    goreleaser, or grpc-go.

.PARAMETER Runs
    Number of probes to collect. Default: 10.

.PARAMETER TimeoutMinutes
    Timeout passed to `go test -timeout`. Default: 50 minutes.

.PARAMETER Pattern
    Package pattern passed to `go test`. Default: ./...

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

# Resolve project-relative paths from the scripts directory.
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$probeDir = Join-Path $repoRoot "data/probe/$ProjectName"
$outDir   = Join-Path $repoRoot 'data/characterization'
$outFile  = Join-Path $outDir   "$ProjectName.json"

if (-not (Test-Path $ProjectPath)) {
    throw "ProjectPath does not exist: $ProjectPath"
}
if (-not (Test-Path (Join-Path $ProjectPath 'go.mod'))) {
    Write-Warning "go.mod was not found under $ProjectPath; continuing anyway."
}

New-Item -ItemType Directory -Force -Path $probeDir | Out-Null
New-Item -ItemType Directory -Force -Path $outDir   | Out-Null

# Real probe collection requires the Go executable to be available on PATH.
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    throw "The 'go' executable was not found on PATH. Install Go before collecting probes."
}

Write-Host "==> Project:  $ProjectName"
Write-Host "    Path:     $ProjectPath"
Write-Host "    Runs:     $Runs"
Write-Host "    Timeout:  ${TimeoutMinutes}m"
Write-Host "    Probe:    $probeDir"
Write-Host "    Output:   $outFile"
Write-Host ''

Push-Location $repoRoot
try {
    $gomaxprocsEvidence = (& go run ./cmd/preflight | ConvertFrom-Json)
    if ($LASTEXITCODE -ne 0 -or $gomaxprocsEvidence.gomaxprocs_effective -ne 1) {
        throw 'GOMAXPROCS preflight failed.'
    }
}
finally {
    Pop-Location
}
Write-Host "    GOMAXPROCS: configured=1 effective=$($gomaxprocsEvidence.gomaxprocs_effective) policy=$($gomaxprocsEvidence.gomaxprocs_policy)"
Write-Host ''

$runFiles = New-Object System.Collections.Generic.List[string]
for ($i = 1; $i -le $Runs; $i++) {
    $tag     = '{0:D2}' -f $i
    $file     = Join-Path $probeDir "run_$tag.json"
    $errFile  = Join-Path $probeDir "run_$tag.err"
    $metaFile = Join-Path $probeDir "run_$tag.meta.json"

    Write-Host "  [$tag/$Runs] GOMAXPROCS=1 go test -json -p 1 -parallel 1 -count=1 -timeout ${TimeoutMinutes}m $Pattern"

    Push-Location $ProjectPath
    try {
        # Keep unmodified stdout NDJSON separate from diagnostic stderr. The
        # metadata sidecar preserves the exit code, which cannot be recovered
        # reliably from NDJSON alone.
        $utf8 = New-Object System.Text.UTF8Encoding($false)
        $previousGOMAXPROCS = [Environment]::GetEnvironmentVariable('GOMAXPROCS', 'Process')
        try {
            [Environment]::SetEnvironmentVariable('GOMAXPROCS', '1', 'Process')
            $startedAt = Get-Date
            $outLines = @(& go test -json "-p" "1" "-parallel" "1" "-count=1" "-timeout" "${TimeoutMinutes}m" $Pattern 2> $errFile)
            $exitCode = $LASTEXITCODE
            $finishedAt = Get-Date
        }
        finally {
            [Environment]::SetEnvironmentVariable('GOMAXPROCS', $previousGOMAXPROCS, 'Process')
        }
        [System.IO.File]::WriteAllLines($file, $outLines, $utf8)

        # A real `go test -timeout` expiration returns a non-zero exit code and
        # emits the specific panic below. Messages such as "deadline exceeded"
        # may be normal test output, especially in grpc-go, and do not by
        # themselves indicate a process timeout.
        $combinedDiagnostic = (($outLines -join "`n") + "`n")
        if (Test-Path $errFile) {
            $combinedDiagnostic += Get-Content -Raw -LiteralPath $errFile
        }
        $timedOut = ($exitCode -ne 0) -and
            ($combinedDiagnostic -match '(?i)panic:\s+test timed out after\s+')
        $meta = [ordered]@{
            command = "GOMAXPROCS=1 go test -json -p 1 -parallel 1 -count=1 -timeout ${TimeoutMinutes}m $Pattern"
            started_at = $startedAt.ToUniversalTime().ToString('o')
            finished_at = $finishedAt.ToUniversalTime().ToString('o')
            exit_code = $exitCode
            timed_out = $timedOut
            gomaxprocs_configured = 1
            gomaxprocs_effective = $gomaxprocsEvidence.gomaxprocs_effective
            gomaxprocs_policy = $gomaxprocsEvidence.gomaxprocs_policy
            child_environment_applied = $true
        }
        [System.IO.File]::WriteAllText($metaFile, ($meta | ConvertTo-Json -Depth 4), $utf8)
    }
    finally {
        Pop-Location
    }

    if ((Test-Path $errFile) -and ((Get-Item $errFile).Length -gt 0)) {
        Write-Host "       (non-empty stderr: $errFile)" -ForegroundColor Yellow
    }

    $runFiles.Add($file)
}

Write-Host ''
$validationFile = Join-Path $probeDir 'validation.json'
Write-Host "==> Validating probe integrity"
Push-Location $repoRoot
try {
    & go run ./cmd/validateprobes `
        -project-path $ProjectPath `
        -pattern $Pattern `
        -expected-runs $Runs `
        -require-gomaxprocs 1 `
        -output $validationFile `
        @runFiles
    if ($LASTEXITCODE -ne 0) {
        throw "Probe validation failed. Review $validationFile before aggregation."
    }
}
finally {
    Pop-Location
}

Write-Host ''
Write-Host "==> Aggregating $($runFiles.Count) probes -> $outFile"

Push-Location $repoRoot
try {
    & go run ./cmd/analyze -output $outFile @runFiles
    if ($LASTEXITCODE -ne 0) {
        throw "cmd/analyze failed (exit=$LASTEXITCODE)"
    }
}
finally {
    Pop-Location
}

$durationAuditFile = Join-Path $probeDir 'duration-audit.json'
Write-Host "==> Auditing durations -> $durationAuditFile"
Push-Location $repoRoot
try {
    & go run ./cmd/auditdurations `
        -characterization $outFile `
        -output $durationAuditFile `
        @runFiles
    if ($LASTEXITCODE -ne 0) {
        throw "Duration audit failed. Review $durationAuditFile."
    }
}
finally {
    Pop-Location
}

Write-Host ''
Write-Host "==> Complete. Characterization saved to $outFile"
