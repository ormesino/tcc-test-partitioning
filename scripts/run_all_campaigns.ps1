<#
.SYNOPSIS
    Executa todas as campanhas cold e warm dos 4 projetos do TCC.

.DESCRIPTION
    Script para execucao noturna sem intervencao manual.
    Ordem: 4 campanhas cold, depois 4 campanhas warm.
    Cada etapa e logada com timestamp e resultado.

.PARAMETER TimeoutMinutes
    Timeout por campanha em minutos. Default: 90.

.EXAMPLE
    pwsh -ExecutionPolicy Bypass -File scripts/run_all_campaigns.ps1
#>
[CmdletBinding()]
param(
    [int]$TimeoutMinutes = 90
)

$ErrorActionPreference = "Continue"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$startTime = Get-Date
$logDir = Join-Path $repoRoot "logs/campaigns/$(Get-Date -Format 'yyyyMMdd-HHmmss')"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null

$logFile = Join-Path $logDir "campaign_run.log"
$failures = New-Object System.Collections.Generic.List[string]

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
        [string[]]$Args
    )

    $stepStart = Get-Date
    Write-Log "INICIO: $Name"
    Write-Log "  Comando: $Command $($Args -join ' ')"

    $stepLog = Join-Path $logDir "$($Name -replace '[^a-zA-Z0-9_-]','_').log"

    try {
        & $Command @Args 2>&1 | Tee-Object -FilePath $stepLog
        $exitCode = $LASTEXITCODE
    } catch {
        $exitCode = 1
        Write-Log "  EXCECAO: $_"
    }

    $stepEnd = Get-Date
    $elapsed = $stepEnd - $stepStart

    if ($exitCode -eq 0) {
        Write-Log "SUCESSO: $Name (${elapsed})"
    } else {
        Write-Log "FALHA: $Name (exit code: $exitCode, ${elapsed})"
    }
    Write-Log ""

    return $exitCode
}

Push-Location $repoRoot
try {
    Write-Log "============================================="
    Write-Log "CAMPANHAS TCC - EXECUCAO COMPLETA"
    Write-Log "============================================="
    Write-Log "Inicio geral: $startTime"
    Write-Log "Log principal: $logFile"
    Write-Log "Timeout por campanha: $TimeoutMinutes min"
    Write-Log ""

    Write-Log "=== ETAPA 1: CAMPANHAS COLD ==="

    $coldConfigs = @(
        @{ Name = "cold-cli";        Config = "benchmarks/campaign_cli.json" },
        @{ Name = "cold-goreleaser"; Config = "benchmarks/campaign_goreleaser.json" },
        @{ Name = "cold-grpc-go";    Config = "benchmarks/campaign_grpc_go.json" },
        @{ Name = "cold-hugo";       Config = "benchmarks/campaign_hugo.json" }
    )

    foreach ($c in $coldConfigs) {
        Write-Log "  Limpando caches do Go antes da campanha $($c.Name)"
        & go clean -cache
        & go clean -testcache

        $exitCode = Run-Step -Name $c.Name `
            -Command "go" `
            -Args @("run", "./cmd/benchmark", "--config", $c.Config, "--timeout-minutes", "$TimeoutMinutes")
        if ($exitCode -ne 0) {
            $failures.Add($c.Name) | Out-Null
        }
    }

    Write-Log "=== ETAPA 2: CAMPANHAS WARM ==="

    $warmConfigs = @(
        @{ Name = "warm-cli";        Config = "benchmarks/campaign_cli_warm.json" },
        @{ Name = "warm-goreleaser"; Config = "benchmarks/campaign_goreleaser_warm.json" },
        @{ Name = "warm-grpc-go";    Config = "benchmarks/campaign_grpc_go_warm.json" },
        @{ Name = "warm-hugo";       Config = "benchmarks/campaign_hugo_warm.json" }
    )

    foreach ($c in $warmConfigs) {
        $exitCode = Run-Step -Name $c.Name `
            -Command "go" `
            -Args @("run", "./cmd/benchmark", "--config", $c.Config, "--timeout-minutes", "$TimeoutMinutes")
        if ($exitCode -ne 0) {
            $failures.Add($c.Name) | Out-Null
        }
    }

    $endTime = Get-Date
    $totalElapsed = $endTime - $startTime

    Write-Log "============================================="
    Write-Log "EXECUCAO COMPLETA"
    Write-Log "Inicio:  $startTime"
    Write-Log "Termino: $endTime"
    Write-Log "Duracao total: $totalElapsed"
    Write-Log "Logs em: $logDir"
    if ($failures.Count -gt 0) {
        Write-Log "Falhas: $($failures -join ', ')"
    }
    Write-Log "============================================="

    Write-Host ""
    Write-Host "Verifique os resultados em benchmarks/results/ e os logs em $logDir"

    if ($failures.Count -gt 0) {
        exit 1
    }
    exit 0
}
finally {
    Pop-Location
}