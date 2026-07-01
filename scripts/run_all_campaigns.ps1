<#
.SYNOPSIS
    Executa campanhas cold e/ou warm dos projetos selecionados.

.DESCRIPTION
    Executa somente os projetos e regimes selecionados. Quando mais de um for
    escolhido, cold e executado antes de warm. Cada etapa e logada com
    timestamp, progresso e resultado.

.PARAMETER TimeoutMinutes
    Timeout por repeticao em minutos. Cada repeticao corresponde a uma
    combinacao de projeto, algoritmo e numero de workers. Default: 90.

.PARAMETER Projects
    Projetos a executar. Valores aceitos: cli, goreleaser, grpc-go e hugo.

.PARAMETER Regimes
    Regimes a executar. Valores aceitos: cold e warm.

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
    Write-Log "INICIO CAMPANHA ${CampaignIndex}/${CampaignTotal}: $Name"
    Write-Log "  Comando: $Command $($Arguments -join ' ')"
    Write-Log "  O benchmark mostrara inicio e fim de cada combinacao desta campanha."

    $stepLog = Join-Path $logDir "$($Name -replace '[^a-zA-Z0-9_-]','_').log"

    try {
        # Consome a saida do pipeline dentro da funcao para que ela seja
        # exibida imediatamente sem entrar no valor retornado por Run-Step.
        & $Command @Arguments 2>&1 |
            Tee-Object -FilePath $stepLog |
            ForEach-Object { Write-Host $_ }
        $exitCode = $LASTEXITCODE
    } catch {
        $exitCode = 1
        Write-Log "  EXCECAO: $_"
    }

    $stepEnd = Get-Date
    $elapsed = $stepEnd - $stepStart

    if ($exitCode -eq 0) {
        Write-Log "CAMPANHA CONCLUIDA ${CampaignIndex}/${CampaignTotal}: $Name (${elapsed})"
    } else {
        Write-Log "CAMPANHA COM FALHA ${CampaignIndex}/${CampaignTotal}: $Name (exit code: $exitCode, ${elapsed})"
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
    Write-Log "Timeout por repeticao: $TimeoutMinutes min"
    Write-Log "Projetos: $($Projects -join ', ')"
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
    Write-Log "Total de campanhas selecionadas: $campaignTotal"
    Write-Log ""

    if ('cold' -in $Regimes) {
        Write-Log "=== CAMPANHAS COLD ==="
        foreach ($c in $coldConfigs) {
            Write-Log "  Regime cold: cada worker usara um GOCACHE temporario e isolado"

            $campaignIndex++
            $exitCode = Run-Step -Name $c.Name `
                -Command "go" `
                -Arguments @("run", "./cmd/benchmark", "--config", $c.Config, "--timeout-minutes", "$TimeoutMinutes") `
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
        Write-Log "=== CAMPANHAS WARM ==="
        foreach ($c in $warmConfigs) {
            $campaignIndex++
            $exitCode = Run-Step -Name $c.Name `
                -Command "go" `
                -Arguments @("run", "./cmd/benchmark", "--config", $c.Config, "--timeout-minutes", "$TimeoutMinutes") `
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
    Write-Log "EXECUCAO COMPLETA"
    Write-Log "Inicio:  $startTime"
    Write-Log "Termino: $endTime"
    Write-Log "Duracao total: $totalElapsed"
    Write-Log "Logs em: $logDir"
    Write-Log "Campanhas concluidas com sucesso: $($successes.Count)/$campaignTotal"
    if ($successes.Count -gt 0) {
        Write-Log "Sucessos: $($successes -join ', ')"
    }
    if ($failures.Count -gt 0) {
        Write-Log "Falhas: $($failures -join ', ')"
    } else {
        Write-Log "Falhas: nenhuma"
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
