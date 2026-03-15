# RepoView installer for Windows (PowerShell)
# Usage: irm https://raw.githubusercontent.com/nvquanghuy/repoview/master/install.ps1 | iex

$ErrorActionPreference = "Stop"

$repo = "nvquanghuy/repoview"
$installDir = "$env:LOCALAPPDATA\Programs\repoview"

# Detect architecture
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default {
        Write-Error "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE"
        exit 1
    }
}

$zipName = "repoview-windows-$arch.zip"
$url = "https://github.com/$repo/releases/latest/download/$zipName"

Write-Host "Downloading repoview for windows/$arch..."

$tmpDir = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "repoview-install-$(Get-Random)")
try {
    $zipPath = Join-Path $tmpDir $zipName
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing

    # Create install directory
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null

    # Extract
    Expand-Archive -Path $zipPath -DestinationPath $installDir -Force

    Write-Host "Installed repoview to $installDir\repoview.exe"

    # Check if install dir is in PATH
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$installDir*") {
        Write-Host ""
        Write-Host "Add repoview to your PATH by running:"
        Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"$installDir;`$env:Path`", 'User')"
        Write-Host ""
        Write-Host "Then restart your terminal."
    }
}
finally {
    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
}
