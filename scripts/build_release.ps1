# 一键编译并打包 Kiralrc Windows 便携分发包（含 GUI + Python 核心源码 + 词典 + 模型下载脚本）
param(
    [string]$Version = "0.2.0"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Push-Location $Root

try {
    Write-Host "[1/3] 编译 kiralrc-gui.exe ..."
    go build -ldflags="-H windowsgui" -o kiralrc-gui.exe .

    $Stage = Join-Path $Root "dist\stage-v$Version"
    $ZipPath = Join-Path $Root "dist\kiralrc-gui-v$Version-windows-amd64.zip"
    if (Test-Path $Stage) { Remove-Item $Stage -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $Stage | Out-Null

    Write-Host "[2/3] 拷贝便携运行所需文件 ..."
    Copy-Item "kiralrc-gui.exe", "config.toml", "polyphone.toml", "pyproject.toml", "README.md", "LICENSE" -Destination $Stage

    # Python 核心包（排除 legacy 与 __pycache__）
    $PkgDst = Join-Path $Stage "kiralrc"
    New-Item -ItemType Directory -Force -Path $PkgDst | Out-Null
    Get-ChildItem "kiralrc" -Filter "*.py" | Copy-Item -Destination $PkgDst

    # SOFA 拼音词典与原版 LICENSE
    $SofaDictDst = Join-Path $Stage "third_party\SOFA\dictionary"
    New-Item -ItemType Directory -Force -Path $SofaDictDst | Out-Null
    Copy-Item "third_party\SOFA\dictionary\*" -Destination $SofaDictDst
    if (Test-Path "third_party\SOFA\LICENSE") {
        Copy-Item "third_party\SOFA\LICENSE" -Destination (Join-Path $Stage "third_party\SOFA\")
    }

    # 模型下载脚本
    $ScriptsDst = Join-Path $Stage "scripts"
    New-Item -ItemType Directory -Force -Path $ScriptsDst | Out-Null
    Copy-Item "scripts\fetch_models.py" -Destination $ScriptsDst

    # 若本地已准备 tools/uv.exe，顺带打入压缩包实现完全离线引导
    if (Test-Path "tools\uv.exe") {
        $ToolsDst = Join-Path $Stage "tools"
        New-Item -ItemType Directory -Force -Path $ToolsDst | Out-Null
        Copy-Item "tools\uv.exe" -Destination $ToolsDst
    }

    Write-Host "[3/3] 打包 $ZipPath ..."
    if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force }
    Compress-Archive -Path "$Stage\*" -DestinationPath $ZipPath -Force
    Remove-Item $Stage -Recurse -Force

    $Item = Get-Item $ZipPath
    Write-Host "[ok] 已生成 $($Item.FullName) ($([math]::Round($Item.Length / 1MB, 2)) MB)"
} finally {
    Pop-Location
}
