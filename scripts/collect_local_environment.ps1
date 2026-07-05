param(
    [string]$OutputRoot = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$ProjectRoot = (Get-Location).Path
$Timestamp = Get-Date -Format "yyyyMMdd-HHmmss"

if ([string]::IsNullOrWhiteSpace($OutputRoot)) {
    $OutputRoot = Join-Path $ProjectRoot "environment\local-notebook"
}

$OutputDirectory = Join-Path $OutputRoot $Timestamp

New-Item `
    -ItemType Directory `
    -Path $OutputDirectory `
    -Force | Out-Null

function Save-Json {
    param(
        [Parameter(Mandatory)]
        [string]$FileName,

        [Parameter(Mandatory)]
        $Value,

        [int]$Depth = 10
    )

    $Path = Join-Path $OutputDirectory $FileName

    $Value |
        ConvertTo-Json -Depth $Depth |
        Set-Content -Path $Path -Encoding UTF8
}

function Save-Command {
    param(
        [Parameter(Mandatory)]
        [string]$FileName,

        [Parameter(Mandatory)]
        [scriptblock]$Command
    )

    $Path = Join-Path $OutputDirectory $FileName

    try {
        $Output = & $Command 2>&1 | Out-String -Width 4096

        $Output.TrimEnd() |
            Set-Content -Path $Path -Encoding UTF8
    }
    catch {
        "ERROR: $($_.Exception.Message)" |
            Set-Content -Path $Path -Encoding UTF8
    }
}

function Get-GitRepositoryState {
    param(
        [Parameter(Mandatory)]
        [string]$Name,

        [Parameter(Mandatory)]
        [string]$Path
    )

    if (-not (Test-Path $Path)) {
        return [ordered]@{
            name   = $Name
            path   = $Path
            exists = $false
            commit = $null
            branch = $null
            dirty  = $null
            status = @()
        }
    }

    $Commit = & git -C $Path rev-parse HEAD 2>$null

    if ($LASTEXITCODE -ne 0) {
        return [ordered]@{
            name   = $Name
            path   = $Path
            exists = $true
            commit = $null
            branch = $null
            dirty  = $null
            status = @("Diretório encontrado, mas não é um repositório Git válido.")
        }
    }

    $Branch = & git -C $Path branch --show-current 2>$null
    $Status = @(& git -C $Path status --porcelain=v1 2>$null)

    return [ordered]@{
        name   = $Name
        path   = $Path
        exists = $true
        commit = $Commit.Trim()
        branch = $Branch.Trim()
        dirty  = $Status.Count -gt 0
        status = $Status
    }
}

# ----------------------------------------------------------------------
# Identificação geral
# ----------------------------------------------------------------------

$Metadata = [ordered]@{
    collected_at_iso8601 = (Get-Date).ToString("o")
    collected_at_utc     = (Get-Date).ToUniversalTime().ToString("o")
    timezone             = (Get-TimeZone).Id
    computer_name        = $env:COMPUTERNAME
    project_directory    = Split-Path $ProjectRoot -Leaf
    collector            = "collect_local_environment.ps1"
}

Save-Json "metadata.json" $Metadata

(Get-Date).ToString("o") |
    Set-Content `
        -Path (Join-Path $OutputDirectory "collected-at.txt") `
        -Encoding UTF8

# ----------------------------------------------------------------------
# Sistema operacional e computador
# ----------------------------------------------------------------------

$OperatingSystem = Get-CimInstance Win32_OperatingSystem |
    Select-Object `
        Caption,
        Version,
        BuildNumber,
        OSArchitecture,
        InstallDate,
        LastBootUpTime,
        LocalDateTime,
        FreePhysicalMemory,
        TotalVirtualMemorySize,
        FreeVirtualMemory

Save-Json "operating-system.json" $OperatingSystem

$ComputerSystem = Get-CimInstance Win32_ComputerSystem |
    Select-Object `
        Manufacturer,
        Model,
        SystemType,
        TotalPhysicalMemory,
        NumberOfProcessors,
        NumberOfLogicalProcessors,
        HypervisorPresent,
        Domain,
        PartOfDomain

Save-Json "computer-system.json" $ComputerSystem

Save-Command "systeminfo.txt" {
    systeminfo
}

# ----------------------------------------------------------------------
# Processador
# ----------------------------------------------------------------------

$Processors = Get-CimInstance Win32_Processor |
    Select-Object `
        Name,
        Manufacturer,
        Description,
        Architecture,
        NumberOfCores,
        NumberOfLogicalProcessors,
        MaxClockSpeed,
        CurrentClockSpeed,
        L2CacheSize,
        L3CacheSize,
        VirtualizationFirmwareEnabled,
        SecondLevelAddressTranslationExtensions

Save-Json "cpu.json" $Processors

# ----------------------------------------------------------------------
# Memória
# ----------------------------------------------------------------------

$MemoryModules = Get-CimInstance Win32_PhysicalMemory |
    Select-Object `
        DeviceLocator,
        BankLabel,
        Manufacturer,
        PartNumber,
        Capacity,
        Speed,
        ConfiguredClockSpeed,
        MemoryType,
        SMBIOSMemoryType,
        FormFactor

Save-Json "memory-modules.json" $MemoryModules

$MemorySummary = [ordered]@{
    total_physical_memory_bytes = $ComputerSystem.TotalPhysicalMemory
    total_physical_memory_gib   = [math]::Round(
        $ComputerSystem.TotalPhysicalMemory / 1GB,
        2
    )
    module_count = @($MemoryModules).Count
}

Save-Json "memory-summary.json" $MemorySummary

# ----------------------------------------------------------------------
# BIOS e placa-mãe
# ----------------------------------------------------------------------

$Bios = Get-CimInstance Win32_BIOS |
    Select-Object `
        Manufacturer,
        Name,
        SMBIOSBIOSVersion,
        Version,
        ReleaseDate,
        SMBIOSMajorVersion,
        SMBIOSMinorVersion

Save-Json "bios.json" $Bios

$BaseBoard = Get-CimInstance Win32_BaseBoard |
    Select-Object `
        Manufacturer,
        Product,
        Version

Save-Json "baseboard.json" $BaseBoard

# ----------------------------------------------------------------------
# Armazenamento
# ----------------------------------------------------------------------

$DiskDrives = Get-CimInstance Win32_DiskDrive |
    Select-Object `
        Model,
        Manufacturer,
        InterfaceType,
        MediaType,
        FirmwareRevision,
        Size,
        Partitions,
        Status

Save-Json "disk-drives.json" $DiskDrives

if (Get-Command Get-PhysicalDisk -ErrorAction SilentlyContinue) {
    $PhysicalDisks = Get-PhysicalDisk |
        Select-Object `
            FriendlyName,
            Manufacturer,
            Model,
            MediaType,
            BusType,
            Size,
            HealthStatus,
            OperationalStatus,
            FirmwareVersion

    Save-Json "physical-disks.json" $PhysicalDisks
}

$Volumes = Get-Volume |
    Select-Object `
        DriveLetter,
        FileSystemLabel,
        FileSystem,
        DriveType,
        HealthStatus,
        OperationalStatus,
        Size,
        SizeRemaining

Save-Json "volumes.json" $Volumes

Save-Command "disk-layout.txt" {
    Get-Disk |
        Format-Table `
            Number,
            FriendlyName,
            PartitionStyle,
            OperationalStatus,
            HealthStatus,
            Size `
            -AutoSize
}

# ----------------------------------------------------------------------
# GPU
# ----------------------------------------------------------------------

$VideoControllers = Get-CimInstance Win32_VideoController |
    Select-Object `
        Name,
        VideoProcessor,
        DriverVersion,
        DriverDate,
        AdapterRAM,
        CurrentHorizontalResolution,
        CurrentVerticalResolution

Save-Json "graphics.json" $VideoControllers

# ----------------------------------------------------------------------
# Energia e bateria
# ----------------------------------------------------------------------

Save-Command "power-active-scheme.txt" {
    powercfg /GETACTIVESCHEME
}

Save-Command "power-schemes.txt" {
    powercfg /LIST
}

Save-Command "power-processor-policy.txt" {
    powercfg /QUERY SCHEME_CURRENT SUB_PROCESSOR
}

$Battery = Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue |
    Select-Object `
        Name,
        Status,
        BatteryStatus,
        EstimatedChargeRemaining,
        EstimatedRunTime,
        DesignVoltage

if ($null -ne $Battery) {
    Save-Json "battery.json" $Battery
}
else {
    "Nenhuma bateria foi retornada pelo Win32_Battery." |
        Set-Content `
            -Path (Join-Path $OutputDirectory "battery.txt") `
            -Encoding UTF8
}

# ----------------------------------------------------------------------
# Sincronização de data e horário
# ----------------------------------------------------------------------

Save-Command "time-status.txt" {
    Get-Date
    Get-TimeZone
    w32tm /query /status
}

# ----------------------------------------------------------------------
# Windows Defender
# ----------------------------------------------------------------------

if (Get-Command Get-MpComputerStatus -ErrorAction SilentlyContinue) {
    $Defender = Get-MpComputerStatus |
        Select-Object `
            AMServiceEnabled,
            AntivirusEnabled,
            AntispywareEnabled,
            BehaviorMonitorEnabled,
            IoavProtectionEnabled,
            NISEnabled,
            OnAccessProtectionEnabled,
            RealTimeProtectionEnabled,
            AntivirusSignatureVersion,
            AntivirusSignatureLastUpdated,
            AMProductVersion

    Save-Json "windows-defender.json" $Defender
}

# ----------------------------------------------------------------------
# Ferramentas
# ----------------------------------------------------------------------

$ToolNames = @(
    "git",
    "go",
    "pwsh",
    "powershell",
    "python",
    "python3",
    "py",
    "gcc",
    "make"
)

$Tools = foreach ($ToolName in $ToolNames) {
    $Command = Get-Command $ToolName -ErrorAction SilentlyContinue

    if ($null -ne $Command) {
        [ordered]@{
            name    = $ToolName
            path    = $Command.Source
            version = $Command.Version.ToString()
        }
    }
    else {
        [ordered]@{
            name    = $ToolName
            path    = $null
            version = $null
        }
    }
}

Save-Json "tool-paths.json" $Tools

Save-Command "git-version.txt" {
    git --version
}

Save-Command "go-version.txt" {
    go version
}

Save-Command "go-env.json" {
    go env -json
}

Save-Json "powershell-version.json" $PSVersionTable

Save-Command "python-version.txt" {
    if (Get-Command python -ErrorAction SilentlyContinue) {
        python --version
    }

    if (Get-Command python3 -ErrorAction SilentlyContinue) {
        python3 --version
    }

    if (Get-Command py -ErrorAction SilentlyContinue) {
        py -0p
    }
}

# ----------------------------------------------------------------------
# Estado dos repositórios
# ----------------------------------------------------------------------

$RepositoryPaths = [ordered]@{
    application = $ProjectRoot
    cli         = Join-Path $ProjectRoot "repos\cli"
    grpc_go     = Join-Path $ProjectRoot "repos\grpc-go"
    goreleaser  = Join-Path $ProjectRoot "repos\goreleaser"
    hugo        = Join-Path $ProjectRoot "repos\hugo"
}

$RepositoryStates = foreach ($Repository in $RepositoryPaths.GetEnumerator()) {
    Get-GitRepositoryState `
        -Name $Repository.Key `
        -Path $Repository.Value
}

Save-Json "repositories.json" $RepositoryStates 15

$RepositoryStates |
    ForEach-Object {
        @(
            "Repository: $($_.name)"
            "Path: $($_.path)"
            "Exists: $($_.exists)"
            "Commit: $($_.commit)"
            "Branch: $($_.branch)"
            "Dirty: $($_.dirty)"
            "Status:"
            ($_.status -join [Environment]::NewLine)
            ""
        ) -join [Environment]::NewLine
    } |
    Set-Content `
        -Path (Join-Path $OutputDirectory "repositories.txt") `
        -Encoding UTF8

# ----------------------------------------------------------------------
# Hashes dos artefatos relevantes
# ----------------------------------------------------------------------

$HashTargets = @(
    "go.mod",
    "go.sum"
)

$ProjectHashes = foreach ($Target in $HashTargets) {
    $FullPath = Join-Path $ProjectRoot $Target

    if (Test-Path $FullPath) {
        Get-FileHash `
            -Path $FullPath `
            -Algorithm SHA256 |
            Select-Object Path, Algorithm, Hash
    }
}

if ($ProjectHashes) {
    $ProjectHashes |
        Export-Csv `
            -Path (Join-Path $OutputDirectory "project-file-hashes.csv") `
            -NoTypeInformation `
            -Encoding UTF8
}

$CharacterizationDirectory = Join-Path $ProjectRoot "data\characterization"

if (Test-Path $CharacterizationDirectory) {
    Get-ChildItem `
        -Path $CharacterizationDirectory `
        -File `
        -Recurse |
        Get-FileHash -Algorithm SHA256 |
        Select-Object Path, Algorithm, Hash |
        Export-Csv `
            -Path (Join-Path $OutputDirectory "characterization-hashes.csv") `
            -NoTypeInformation `
            -Encoding UTF8
}

$BaselineDirectory = Join-Path $ProjectRoot "data\baseline"

if (Test-Path $BaselineDirectory) {
    Get-ChildItem `
        -Path $BaselineDirectory `
        -File `
        -Recurse |
        Get-FileHash -Algorithm SHA256 |
        Select-Object Path, Algorithm, Hash |
        Export-Csv `
            -Path (Join-Path $OutputDirectory "baseline-hashes.csv") `
            -NoTypeInformation `
            -Encoding UTF8
}

# ----------------------------------------------------------------------
# Snapshot de recursos no momento da coleta
# ----------------------------------------------------------------------

$RuntimeSnapshot = [ordered]@{
    collected_at = (Get-Date).ToString("o")

    memory = Get-CimInstance Win32_OperatingSystem |
        Select-Object `
            FreePhysicalMemory,
            TotalVisibleMemorySize,
            FreeVirtualMemory,
            TotalVirtualMemorySize

    processor = Get-CimInstance `
        Win32_PerfFormattedData_PerfOS_Processor |
        Where-Object Name -eq "_Total" |
        Select-Object `
            PercentProcessorTime,
            PercentUserTime,
            PercentPrivilegedTime,
            PercentIdleTime

    system = Get-CimInstance `
        Win32_PerfFormattedData_PerfOS_System |
        Select-Object `
            ProcessorQueueLength,
            Processes,
            Threads,
            SystemCallsPersec
}

Save-Json "runtime-snapshot.json" $RuntimeSnapshot 10

Get-Process |
    Sort-Object CPU -Descending |
    Select-Object -First 30 `
        ProcessName,
        Id,
        CPU,
        WorkingSet64,
        PagedMemorySize64,
        ThreadCount,
        HandleCount |
    Export-Csv `
        -Path (Join-Path $OutputDirectory "top-processes.csv") `
        -NoTypeInformation `
        -Encoding UTF8

# ----------------------------------------------------------------------
# Notas manuais
# ----------------------------------------------------------------------

$Notes = @"
# Notas do ambiente local

Data da coleta: $(Get-Date -Format "yyyy-MM-dd HH:mm:ss zzz")

## Energia

- Notebook conectado à tomada:
- Percentual da bateria:
- Modo de energia do Windows:
- Perfil powercfg ativo:

## Condições da execução

- Temperatura ambiente aproximada:
- Base refrigerada ou suporte:
- Tampa aberta ou fechada:
- Monitor externo:
- Programas abertos:
- Navegador aberto:
- Sincronização em nuvem ativa:
- Atualizações do Windows em andamento:
- Antivírus em tempo real:
- Conexão de rede:

## Projeto

- Execução considerada definitiva:
- Dados considerados históricos:
- Observações:
"@

$Notes |
    Set-Content `
        -Path (Join-Path $OutputDirectory "experiment-notes.md") `
        -Encoding UTF8

Write-Host ""
Write-Host "Coleta concluída."
Write-Host "Diretório: $OutputDirectory"
Write-Host ""
Write-Host "Revise os arquivos antes de compartilhar ou versionar."