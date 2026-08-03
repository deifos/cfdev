[CmdletBinding()]
param(
    [string]$Version = "latest",
    [string]$InstallDirectory = (Join-Path $env:LOCALAPPDATA "cfdev\bin")
)

$ErrorActionPreference = "Stop"
$cfdevArchitecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
switch ($cfdevArchitecture) {
    "x64" { $cfdevReleaseArchitecture = "amd64" }
    "arm64" { $cfdevReleaseArchitecture = "arm64" }
    default { throw "cfdev does not publish a Windows build for $cfdevArchitecture." }
}

$cfdevAsset = "cfdev-windows-$cfdevReleaseArchitecture.exe"
$cfdevTag = $Version.TrimStart("v")
if ($cfdevTag -eq "latest") {
    $cfdevReleaseBase = "https://github.com/deifos/cfdev/releases/latest/download"
} else {
    $cfdevReleaseBase = "https://github.com/deifos/cfdev/releases/download/v$cfdevTag"
}

$cfdevBinaryDownload = [System.IO.Path]::GetTempFileName()
$cfdevChecksumsDownload = [System.IO.Path]::GetTempFileName()
try {
    Write-Host "Downloading cfdev for Windows $cfdevReleaseArchitecture..."
    Invoke-WebRequest -Uri "$cfdevReleaseBase/$cfdevAsset" -OutFile $cfdevBinaryDownload
    Invoke-WebRequest -Uri "$cfdevReleaseBase/checksums.txt" -OutFile $cfdevChecksumsDownload

    $cfdevChecksumLine = Get-Content -LiteralPath $cfdevChecksumsDownload | Where-Object { $_ -match "\s+$([regex]::Escape($cfdevAsset))$" } | Select-Object -First 1
    if (-not $cfdevChecksumLine) {
        throw "The release does not include a checksum for $cfdevAsset."
    }
    $cfdevExpectedHash = ($cfdevChecksumLine -split "\s+")[0]
    $cfdevActualHash = (Get-FileHash -LiteralPath $cfdevBinaryDownload -Algorithm SHA256).Hash
    if ($cfdevActualHash -ne $cfdevExpectedHash) {
        throw "The downloaded cfdev binary failed checksum verification."
    }

    New-Item -ItemType Directory -Path $InstallDirectory -Force | Out-Null
    $cfdevDestination = Join-Path $InstallDirectory "cfdev.exe"
    $cfdevStagedDestination = Join-Path $InstallDirectory "cfdev.exe.new"
    Copy-Item -LiteralPath $cfdevBinaryDownload -Destination $cfdevStagedDestination -Force
    Move-Item -LiteralPath $cfdevStagedDestination -Destination $cfdevDestination -Force

    $cfdevUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $cfdevPathEntries = @($cfdevUserPath -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($cfdevPathEntries -notcontains $InstallDirectory) {
        [Environment]::SetEnvironmentVariable("Path", (($cfdevPathEntries + $InstallDirectory) -join ";"), "User")
    }
    if (($env:Path -split ";") -notcontains $InstallDirectory) {
        $env:Path = $env:Path + ";" + $InstallDirectory
    }

    & $cfdevDestination --version
    Write-Host "cfdev is installed. Open a new terminal and run: cfdev setup"
} finally {
    Remove-Item -LiteralPath $cfdevBinaryDownload -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $cfdevChecksumsDownload -Force -ErrorAction SilentlyContinue
}
