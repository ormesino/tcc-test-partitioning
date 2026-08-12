<#
.SYNOPSIS
    Runs cold- and/or warm-cache campaigns for selected subject projects.

.DESCRIPTION
    Runs the selected projects and cache regimes using the final campaign
    configuration files under benchmarks/. Cold campaigns run before warm
    campaigns when both regimes are selected. Each campaign receives a
    timestamped log with its command, progress, exit status, and elapsed time.

    The canonical experiment is already complete; this wrapper is retained for
    auditing and reproducing the accepted campaign protocol.

.PARAMETER TimeoutMinutes
    Timeout per logical repetition in minutes. Each repetition represents one
    project, algorithm, and worker-count combination. Default: 90.

.PARAMETER Repetitions
    Number of logical repetitions for each algorithm and worker count.
    Default: 5, matching the accepted final protocol.

.PARAMETER EnvironmentLabel
    Label recorded in environment.json. Default: gcp-primary.

.PARAMETER Projects
    Projects to run. Accepted values: cli, goreleaser, grpc-go, and hugo.

.PARAMETER Regimes
    Cache regimes to run. Accepted values: cold and warm.

.EXAMPLE
    pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1

.EXAMPLE
    pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1 `
        -Projects cli -Regimes cold

.EXAMPLE
    pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1 `
        -Projects grpc-go -Regimes warm -TimeoutMinutes 90
#>
[CmdletBinding()]
param(
    [int]$TimeoutMinutes = 90,
    [int]$Repetitions = 5,
    [string]$EnvironmentLabel = 'gcp-primary',
    [ValidateSet('cli', 'goreleaser', 'grpc-go', 'hugo')]
    [string[]]$Projects = @('cli', 'goreleaser', 'grpc-go', 'hugo'),
    [ValidateSet('cold', 'warm')]
    [string[]]$Regimes = @('cold', 'warm')
)

$ErrorActionPreference = "Continue"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$startTime = Get-Date
$logDir = Join-Path $repoRoot "logs/campaigns/$(Get-Date -Format 'yyyyMMdd-HHmmss')"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null

$logFile = Join-Path $logDir "campaign_run.log"
$failures = New-Object System.Collections.Generic.List[string]
$successes = New-Object System.Collections.Generic.List[string]

function Write-Log {
    param([string]$Message)
    $ts = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $line = "[$ts] $Message"
    Write-Host $line
    Add-Content -Path $logFile -Value $line -Encoding UTF8
}

function Run-Step {
    param(
        [string]$Name,
        [string]$Command,
        [string[]]$Arguments,
        [int]$CampaignIndex,
        [int]$CampaignTotal
    )

    $stepStart = Get-Date
    Write-Log "CAMPAIGN START ${CampaignIndex}/${CampaignTotal}: $Name"
    Write-Log "  Command: $Command $($Arguments -join ' ')"
    Write-Log "  The benchmark reports the start and end of each combination in this campaign."

    $stepLog = Join-Path $logDir "$($Name -replace '[^a-zA-Z0-9_-]','_').log"

    try {
        # Consume pipeline output inside the function so it is displayed
        # immediately without becoming part of the Run-Step return value.
        & $Command @Arguments 2>&1 |
            Tee-Object -FilePath $stepLog |
            ForEach-Object { Write-Host $_ }
        $exitCode = $LASTEXITCODE
    } catch {
        $exitCode = 1
        Write-Log "  EXCEPTION: $_"
    }

    $stepEnd = Get-Date
    $elapsed = $stepEnd - $stepStart

    if ($exitCode -eq 0) {
        Write-Log "CAMPAIGN COMPLETE ${CampaignIndex}/${CampaignTotal}: $Name (${elapsed})"
    } else {
        Write-Log "CAMPAIGN FAILED ${CampaignIndex}/${CampaignTotal}: $Name (exit code: $exitCode, ${elapsed})"
    }
    Write-Log ""

    return $exitCode
}

Push-Location $repoRoot
try {
    Write-Log "============================================="
    Write-Log "THESIS CAMPAIGNS - FULL EXECUTION"
    Write-Log "============================================="
    Write-Log "Overall start: $startTime"
    Write-Log "Main log: $logFile"
    Write-Log "Timeout per repetition: $TimeoutMinutes min"
    Write-Log "Logical repetitions per combination: $Repetitions"
    Write-Log "Environment label: $EnvironmentLabel"
    Write-Log "Projects: $($Projects -join ', ')"
    Write-Log "Regimes: $($Regimes -join ', ')"
    Write-Log ""

    $coldConfigs = @(
        @{ Project = "cli";        Name = "cold-cli";        Config = "benchmarks/campaign_cli.json" },
        @{ Project = "goreleaser"; Name = "cold-goreleaser"; Config = "benchmarks/campaign_goreleaser.json" },
        @{ Project = "grpc-go";    Name = "cold-grpc-go";    Config = "benchmarks/campaign_grpc_go.json" },
        @{ Project = "hugo";       Name = "cold-hugo";       Config = "benchmarks/campaign_hugo.json" }
    ) | Where-Object { $_.Project -in $Projects }

    $campaignTotal = @($Projects | Select-Object -Unique).Count * @($Regimes | Select-Object -Unique).Count
    $campaignIndex = 0
    Write-Log "Total selected campaigns: $campaignTotal"
    Write-Log ""

    if ('cold' -in $Regimes) {
        Write-Log "=== COLD CAMPAIGNS ==="
        foreach ($c in $coldConfigs) {
            Write-Log "  Cold regime: each worker uses a temporary isolated GOCACHE"

            $campaignIndex++
            $exitCode = Run-Step -Name $c.Name `
                -Command "go" `
                -Arguments @("run", "./cmd/benchmark", "--config", $c.Config, "--timeout-minutes", "$TimeoutMinutes", "--repetitions", "$Repetitions", "--environment-label", $EnvironmentLabel) `
                -CampaignIndex $campaignIndex `
                -CampaignTotal $campaignTotal
            if ($exitCode -ne 0) {
                $failures.Add($c.Name) | Out-Null
            } else {
                $successes.Add($c.Name) | Out-Null
            }
        }
    }

    $warmConfigs = @(
        @{ Project = "cli";        Name = "warm-cli";        Config = "benchmarks/campaign_cli_warm.json" },
        @{ Project = "goreleaser"; Name = "warm-goreleaser"; Config = "benchmarks/campaign_goreleaser_warm.json" },
        @{ Project = "grpc-go";    Name = "warm-grpc-go";    Config = "benchmarks/campaign_grpc_go_warm.json" },
        @{ Project = "hugo";       Name = "warm-hugo";       Config = "benchmarks/campaign_hugo_warm.json" }
    ) | Where-Object { $_.Project -in $Projects }

    if ('warm' -in $Regimes) {
        Write-Log "=== WARM CAMPAIGNS ==="
        foreach ($c in $warmConfigs) {
            $campaignIndex++
            $exitCode = Run-Step -Name $c.Name `
                -Command "go" `
                -Arguments @("run", "./cmd/benchmark", "--config", $c.Config, "--timeout-minutes", "$TimeoutMinutes", "--repetitions", "$Repetitions", "--environment-label", $EnvironmentLabel) `
                -CampaignIndex $campaignIndex `
                -CampaignTotal $campaignTotal
            if ($exitCode -ne 0) {
                $failures.Add($c.Name) | Out-Null
            } else {
                $successes.Add($c.Name) | Out-Null
            }
        }
    }

    $endTime = Get-Date
    $totalElapsed = $endTime - $startTime

    Write-Log "============================================="
    Write-Log "EXECUTION COMPLETE"
    Write-Log "Start:    $startTime"
    Write-Log "End:      $endTime"
    Write-Log "Total duration: $totalElapsed"
    Write-Log "Logs: $logDir"
    Write-Log "Successful campaigns: $($successes.Count)/$campaignTotal"
    if ($successes.Count -gt 0) {
        Write-Log "Successes: $($successes -join ', ')"
    }
    if ($failures.Count -gt 0) {
        Write-Log "Failures: $($failures -join ', ')"
    } else {
        Write-Log "Failures: none"
    }
    Write-Log "============================================="

    Write-Host ""
    Write-Host "Review results under benchmarks/results/ and logs under $logDir"

    if ($failures.Count -gt 0) {
        exit 1
    }
    exit 0
}
finally {
    Pop-Location
}
