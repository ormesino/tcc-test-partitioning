$ErrorActionPreference = "Continue"

$Report = "reports\triage.csv"
"repo,clone_ok,build_ok,n_packages,n_test_packages,last_commit,size_mb" | Out-File -Encoding utf8 -FilePath $Report

$repos = Get-Content "repos\repos.txt" | Where-Object { $_.Trim() -ne "" }

foreach ($repo in $repos) {
  $name = ($repo -split "/")[-1]
  $dir = "repos\$name"

  $clone_ok = "no"
  $build_ok = "no"
  $n_packages = "-"
  $n_test_packages = "-"
  $last_commit = "-"
  $size_mb = "-"

  if (-not (Test-Path $dir)) {
    $cloneOutput = git clone --depth=50 "https://github.com/$repo.git" $dir 2>&1
    if ($LASTEXITCODE -eq 0) {
      $clone_ok = "yes"
    } else {
      Write-Host "Failed to clone $repo : $cloneOutput"
      "$repo,$clone_ok,$build_ok,$n_packages,$n_test_packages,$last_commit,$size_mb" | Out-File -Append -Encoding utf8 -FilePath $Report
      continue
    }
  } else {
    $clone_ok = "yes"
  }

  if ($clone_ok -eq "yes") {
    Push-Location $dir

    $size_mb = [math]::Round((Get-ChildItem -Recurse -File | Measure-Object -Property Length -Sum).Sum / 1MB, 2)
    $last_commit = git log -1 --format=%cd --date=short 2>$null
    if (-not $last_commit) { $last_commit = "-" }

    $buildOutput = go build ./... 2>&1
    if ($LASTEXITCODE -eq 0) {
      $build_ok = "yes"
    }

    $packages = go list ./... 2>$null
    if ($packages) {
      $n_packages = ($packages | Measure-Object -Line).Lines
    } else {
      $n_packages = 0
    }

    $testPackages = go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... 2>$null | Where-Object { $_.Trim() -ne "" }
    if ($testPackages) {
      $n_test_packages = ($testPackages | Measure-Object).Count
    } else {
      $n_test_packages = 0
    }

    Pop-Location
  }

  Write-Host "Processing $name"

  "$repo,$clone_ok,$build_ok,$n_packages,$n_test_packages,$last_commit,$size_mb" | Out-File -Append -Encoding utf8 -FilePath $Report
}