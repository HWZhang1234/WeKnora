# scripts/start_backend.ps1
# 在 figure 1 dev 模式下启动 Go 后端（本地原生进程）。
# 前提：先运行 `docker compose -f docker-compose.dev.yml up -d postgres redis docreader`
# 启动方式：
#   PS> .\scripts\start_backend.ps1
# 调试方式（VSCode）：用 .vscode/launch.json 中的 "Debug WeKnora (figure1)" 配置代替本脚本。

$ErrorActionPreference = "Stop"

# 切到仓库根
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $repoRoot
Write-Host "[start_backend] Working dir: $repoRoot"

# CGO 工具链（Windows + mingw64 + sqlite-amalgamation 头文件）
$env:CGO_ENABLED = "1"
$env:CGO_CFLAGS  = "-IC:\Temp\sqlite-amalgamation-3450300 -Wno-deprecated-declarations -Wno-gnu-folding-constant"
$env:CGO_LDFLAGS = "-LC:\Temp\sqlite-amalgamation-3450300"
if (-not ($env:PATH -split ";" | Where-Object { $_ -eq "C:\ProgramData\mingw64\mingw64\bin" })) {
    $env:PATH = "C:\ProgramData\mingw64\mingw64\bin;" + $env:PATH
}

# 加载 .env：把每行 KEY=VALUE 注入到本进程环境
Write-Host "[start_backend] Loading .env"
Get-Content .env | ForEach-Object {
    $line = $_.Trim()
    if ($line -and -not $line.StartsWith("#") -and $line.Contains("=")) {
        $idx = $line.IndexOf("=")
        $k = $line.Substring(0, $idx).Trim()
        $v = $line.Substring($idx + 1).Trim()
        [System.Environment]::SetEnvironmentVariable($k, $v, "Process")
    }
}

# dev.sh 在容器外运行后端时强制把 docker hostname 指向 localhost；这里同样处理
$env:DB_HOST          = "localhost"
$env:DOCREADER_ADDR   = "localhost:50051"
$env:DOCREADER_TRANSPORT = "grpc"
$env:REDIS_ADDR       = "localhost:6379"

# 本地存储目录（避免 /data/files 在 Windows 上指向 C:\data\files 引发权限错乱）
$env:LOCAL_STORAGE_BASE_DIR = (Join-Path $repoRoot ".local-data\files")
New-Item -ItemType Directory -Force -Path $env:LOCAL_STORAGE_BASE_DIR | Out-Null

Write-Host "[start_backend] DB_HOST=$($env:DB_HOST):$($env:DB_PORT) DOCREADER_ADDR=$($env:DOCREADER_ADDR)"
Write-Host "[start_backend] LOCAL_STORAGE_BASE_DIR=$($env:LOCAL_STORAGE_BASE_DIR)"
Write-Host "[start_backend] go run ./cmd/server"

# 启动后端（如果你装了 Air：把下一行改成 `air`）
go run ./cmd/server