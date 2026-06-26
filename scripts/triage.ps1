$ErrorActionPreference = "Continue"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$reposRoot = Join-Path $repoRoot 'repos'
$reportsRoot = Join-Path $repoRoot 'reports'
New-Item -ItemType Directory -Force -Path $reposRoot | Out-Null
New-Item -ItemType Directory -Force -Path $reportsRoot | Out-Null

$Report = Join-Path $reportsRoot 'triage.csv'
$run_started_at = (Get-Date).ToUniversalTime().ToString("o")
"run_started_at,repo,clone_started_at,clone_finished_at,clone_ok,build_started_at,build_finished_at,build_ok,n_packages,n_test_packages,last_commit,last_commit_hash,size_mb" | Out-File -Encoding utf8 -FilePath $Report

$reposList = Join-Path $reposRoot 'repos.txt'
$repos = Get-Content $reposList | Where-Object { $_.Trim() -ne "" }

foreach ($repo in $repos) {
  $name = ($repo -split "/")[-1]
  $dir = Join-Path $reposRoot $name

  $clone_ok = "no"
  $build_ok = "no"
  $clone_started_at = "-"
  $clone_finished_at = "-"
  $build_started_at = "-"
  $build_finished_at = "-"
  $n_packages = "-"
  $n_test_packages = "-"
  $last_commit = "-"
  $last_commit_hash = "-"
  $size_mb = "-"

  if (-not (Test-Path $dir)) {
    $clone_started_at = (Get-Date).ToUniversalTime().ToString("o")
    $cloneOutput = git clone --depth=50 "https://github.com/$repo.git" $dir 2>&1
    $clone_finished_at = (Get-Date).ToUniversalTime().ToString("o")
    if ($LASTEXITCODE -eq 0) {
      $clone_ok = "yes"
    }
    else {
      Write-Host "Failed to clone $repo : $cloneOutput"
      "$run_started_at,$repo,$clone_started_at,$clone_finished_at,$clone_ok,$build_started_at,$build_finished_at,$build_ok,$n_packages,$n_test_packages,$last_commit,$last_commit_hash,$size_mb" | Out-File -Append -Encoding utf8 -FilePath $Report
      continue
    }
  }
  else {
    $clone_ok = "yes"
  }

  if ($clone_ok -eq "yes") {
    Push-Location $dir

    $size_mb = [math]::Round((Get-ChildItem -Recurse -File | Measure-Object -Property Length -Sum).Sum / 1MB, 2)
    $last_commit = git log -1 --format=%cd --date=short 2>$null
    if (-not $last_commit) { $last_commit = "-" }
    $last_commit_hash = git rev-parse HEAD 2>$null
    if (-not $last_commit_hash) { $last_commit_hash = "-" }

    $build_started_at = (Get-Date).ToUniversalTime().ToString("o")
    $buildOutput = go build ./... 2>&1
    $build_finished_at = (Get-Date).ToUniversalTime().ToString("o")
    if ($LASTEXITCODE -eq 0) {
      $build_ok = "yes"
    }

    $packages = go list ./... 2>$null
    if ($packages) {
      $n_packages = ($packages | Measure-Object -Line).Lines
    }
    else {
      $n_packages = 0
    }

    $testPackages = go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... 2>$null | Where-Object { $_.Trim() -ne "" }
    if ($testPackages) {
      $n_test_packages = ($testPackages | Measure-Object).Count
    }
    else {
      $n_test_packages = 0
    }

    Pop-Location
  }

  Write-Host "Processing $name"

  "$run_started_at,$repo,$clone_started_at,$clone_finished_at,$clone_ok,$build_started_at,$build_finished_at,$build_ok,$n_packages,$n_test_packages,$last_commit,$last_commit_hash,$size_mb" | Out-File -Append -Encoding utf8 -FilePath $Report
}
