# Install or update the CLI from the latest stable GitHub release. No Go needed.
$ErrorActionPreference = 'Stop'
$repo = 'chrisdmacrae/prcourt'
if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
    throw 'Install GitHub CLI (https://cli.github.com), then rerun this script.'
}
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw 'Use install.sh on macOS or Linux.'
}
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    'X64' { 'amd64' }
    'Arm64' { 'arm64' }
    default { throw 'Supported CPU architectures: x64 and arm64.' }
}
$tag = & gh api "repos/$repo/releases/latest" --jq .tag_name
if ($LASTEXITCODE -ne 0) { throw 'Could not check the latest GitHub release.' }
if ($tag -notmatch '^v[0-9]') { throw "Unexpected release tag: $tag" }
$installed = $null
if (Get-Command prcourt -ErrorAction SilentlyContinue) {
    $installed = & prcourt --version 2>$null
}
if ($installed -eq $tag) {
    Write-Output "prcourt $tag is up to date."
    exit 0
}
$bin = Join-Path $HOME '.local/bin'
New-Item -ItemType Directory -Force -Path $bin | Out-Null
$tmp = Join-Path $bin ('.prcourt-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $asset = "prcourt_windows_$arch.exe"
    & gh release download $tag --repo $repo --pattern $asset --pattern checksums.txt --dir $tmp
    if ($LASTEXITCODE -ne 0) { throw 'Could not download the release assets.' }
    $entries = @(Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match "^[0-9a-fA-F]{64}\s+$([regex]::Escape($asset))$" })
    $download = Join-Path $tmp $asset
    $actual = (Get-FileHash -Algorithm SHA256 $download).Hash
    if ($entries.Count -ne 1 -or ($entries[0] -split '\s+')[0] -ne $actual) {
        throw "Checksum verification failed for $asset; leaving the existing CLI unchanged."
    }
    $downloadedVersion = & $download --version
    if ($LASTEXITCODE -ne 0 -or $downloadedVersion -ne $tag) {
        throw 'Downloaded CLI version does not match the release; leaving the existing CLI unchanged.'
    }
    Move-Item -Force $download (Join-Path $bin 'prcourt.exe')
    Write-Output "Installed prcourt $tag at $bin/prcourt.exe"
    Write-Output "Put $bin first in PATH to use this CLI."
} finally {
    Remove-Item -Recurse -Force $tmp
}
