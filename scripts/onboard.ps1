param(
  [Parameter(Mandatory = $false)]
  [string]$ProjectRoot = (Get-Location).Path
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $ProjectRoot -PathType Container)) {
  Write-Error "Project root not found: $ProjectRoot"
}

$codemap = Get-Command codemap -ErrorAction SilentlyContinue
if (-not $codemap) {
  $scoop = Get-Command scoop -ErrorAction SilentlyContinue
  if ($scoop) {
    Write-Host "Installing codemap via Scoop..."
    scoop bucket add codemap https://github.com/JordanCoin/scoop-codemap | Out-Null
    scoop install codemap | Out-Null
  } else {
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    if ($winget) {
      Write-Host "Installing codemap via Winget..."
      winget install --id JordanCoin.codemap --exact --accept-package-agreements --accept-source-agreements | Out-Null
    } else {
      Write-Error "codemap is not installed and neither Scoop nor Winget is available. Install codemap first: https://github.com/JordanCoin/codemap#install"
    }
  }
}

Write-Host "Running codemap setup for: $ProjectRoot"
codemap setup "$ProjectRoot"
