# scripts/start_frontend.ps1
# Vite 前端开发服务器（热重载）。
# 启动方式：PS> .\scripts\start_frontend.ps1

$ErrorActionPreference = "Stop"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location (Join-Path $repoRoot "frontend")
Write-Host "[start_frontend] Working dir: $(Get-Location)"

if (-not (Test-Path "node_modules")) {
    Write-Host "[start_frontend] node_modules not found, installing dependencies..."
    npm install
}

$env:VITE_DEV_PROXY_TARGET = "http://localhost:8080"
Write-Host "[start_frontend] VITE_DEV_PROXY_TARGET=$($env:VITE_DEV_PROXY_TARGET)"
Write-Host "[start_frontend] Frontend will run at http://localhost:5173"
Write-Host "[start_frontend] npm run dev"

npm run dev