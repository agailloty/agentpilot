# install-runner.ps1 -- install the AgentPilot runner binary from a GitHub release.
#
# Downloads the Windows runner asset from the GitHub Release matching the
# version tag (e.g. v0.1.0), verifies its SHA-256 checksum against the release
# checksums.txt, installs agentpilot-runner.exe and (optionally) writes a
# runner environment file.
#
# Usage (PowerShell 5.1+ or pwsh 7+):
#   .\install-runner.ps1 -Version v0.1.0
#   .\install-runner.ps1 -Version 0.1.0 -Server http://100.64.0.10:8080 -Token $env:RUNNER_TOKEN -Roots "C:\repos"
#   irm https://raw.githubusercontent.com/agailloty/agentpilot/main/scripts/install-runner.ps1 | iex  # then call Install-AgentPilotRunner
#
# Parameters:
#   -Version     Release tag: "v0.1.0", "0.1.0" or "latest" (default: latest)
#   -InstallDir  Where to put agentpilot-runner.exe
#                (default: $env:LOCALAPPDATA\agentpilot\bin)
#   -Server      Backend URL, e.g. http://100.64.0.10:8080
#   -Token       Runner Bearer token secret (scope "runner")
#   -Name        Machine display name (default: computer name)
#   -Roots       Comma-separated project root directories
#   -EnvFile     Runner env file to write
#                (default: $env:LOCALAPPDATA\agentpilot\runner.env)
#   -Repo        GitHub repository (default: agailloty/agentpilot)
#   -Force       Overwrite an existing binary
#
# See docs/INSTALL.md for the full installation guide.

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Version = 'latest',

    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'agentpilot\bin'),

    [string]$Server = '',
    [string]$Token = '',
    [string]$Name = '',
    [string]$Roots = '',

    [string]$EnvFile = (Join-Path $env:LOCALAPPDATA 'agentpilot\runner.env'),

    [string]$Repo = 'agailloty/agentpilot',

    [switch]$Force
)

$ErrorActionPreference = 'Stop'

function Write-Info($msg) { Write-Host "[install-runner] $msg" }
function Write-Err($msg) { Write-Host "[install-runner] ERROR: $msg" -ForegroundColor Red }
function Fail($msg) { Write-Err $msg; throw $msg }

# --- normalize version to a release tag --------------------------------------
if ($Version -eq 'latest') {
    $tag = 'latest'
} elseif ($Version -match '^v') {
    $tag = $Version
} else {
    $tag = "v$Version"
}

if ($tag -eq 'latest') {
    $releaseUrl = "https://github.com/$Repo/releases/latest/download"
} else {
    $releaseUrl = "https://github.com/$Repo/releases/download/$tag"
}

# --- detect platform ----------------------------------------------------------
if ($env:OS -ne 'Windows_NT') {
    Fail "this script installs the Windows runner; on Linux use scripts/install-runner.sh"
}
$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -ne 'AMD64') {
    Fail "unsupported architecture '$arch' -- only the windows_amd64 runner build is published"
}

$asset = 'agentpilot-runner_windows_amd64.zip'
$binaryName = 'agentpilot-runner.exe'

Write-Info "release tag : $tag"
Write-Info "asset       : $asset"
Write-Info "download    : $releaseUrl/$asset"
Write-Info "install dir : $InstallDir"
Write-Info "env file    : $EnvFile"

# --- download + verify --------------------------------------------------------
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("agentpilot-install-" + [System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $checksumsPath = Join-Path $tmp 'checksums.txt'
    $assetPath = Join-Path $tmp $asset

    Write-Info 'downloading checksums.txt'
    try {
        Invoke-WebRequest -Uri "$releaseUrl/checksums.txt" -OutFile $checksumsPath -UseBasicParsing
    } catch {
        Fail "failed to download checksums.txt for tag '$tag' -- check that the release exists at https://github.com/$Repo/releases"
    }

    Write-Info "downloading $asset"
    try {
        Invoke-WebRequest -Uri "$releaseUrl/$asset" -OutFile $assetPath -UseBasicParsing
    } catch {
        Fail "failed to download $asset for tag '$tag' -- check that the release exists at https://github.com/$Repo/releases"
    }

    $expected = $null
    foreach ($line in (Get-Content $checksumsPath)) {
        $parts = $line -split '\s+'
        if ($parts.Count -ge 2 -and $parts[1] -eq $asset) { $expected = $parts[0].ToLowerInvariant(); break }
    }
    if (-not $expected) { Fail "$asset not listed in checksums.txt for tag '$tag'" }

    $actual = (Get-FileHash -Algorithm SHA256 -Path $assetPath).Hash.ToLowerInvariant()
    if ($expected -ne $actual) {
        Fail "SHA-256 mismatch for $asset (expected $expected, got $actual) -- aborting, file not installed"
    }
    Write-Info "SHA-256 verified: $actual"

    # --- extract + install ----------------------------------------------------
    $extract = Join-Path $tmp 'extract'
    Expand-Archive -Path $assetPath -DestinationPath $extract -Force
    $src = Join-Path $extract $binaryName
    if (-not (Test-Path $src)) { Fail "archive does not contain $binaryName" }

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $dest = Join-Path $InstallDir $binaryName
    if ((Test-Path $dest) -and -not $Force) {
        Fail "$dest already exists (use -Force to overwrite)"
    }
    Copy-Item $src $dest -Force
    Write-Info "installed $dest"

    try {
        $installedVersion = (& $dest --version 2>$null | Select-Object -First 1)
    } catch { $installedVersion = $null }
    if (-not $installedVersion) { $installedVersion = '(version check skipped)' }
    Write-Info "check: $installedVersion"

    # --- optional runner env file ---------------------------------------------
    if ($Server -or $Token -or $Name -or $Roots) {
        $envDir = Split-Path $EnvFile -Parent
        New-Item -ItemType Directory -Path $envDir -Force | Out-Null
        $lines = @()
        if (Test-Path $EnvFile) {
            $managed = '^(AGENTPILOT_SERVER_URL|AGENTPILOT_RUNNER_TOKEN|AGENTPILOT_MACHINE_NAME|AGENTPILOT_PROJECT_ROOTS)='
            $lines = Get-Content $EnvFile | Where-Object { $_ -notmatch $managed }
        }
        if ($Server) { $lines += "AGENTPILOT_SERVER_URL=$Server" }
        if ($Token)  { $lines += "AGENTPILOT_RUNNER_TOKEN=$Token" }
        if ($Name)   { $lines += "AGENTPILOT_MACHINE_NAME=$Name" }
        if ($Roots)  { $lines += "AGENTPILOT_PROJECT_ROOTS=$Roots" }
        $lines | Set-Content -Path $EnvFile -Encoding ASCII
        # restrict the env file to the current user only
        try {
            $acl = Get-Acl $EnvFile
            $acl.SetAccessRuleProtection($true, $false)
            $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
                [System.Security.Principal.WindowsIdentity]::GetCurrent().Name,
                'FullControl', 'Allow')
            $acl.AddAccessRule($rule)
            Set-Acl $EnvFile $acl
        } catch { Write-Host '[install-runner] WARNING: could not restrict ACL on env file' -ForegroundColor Yellow }
        Write-Info "wrote runner configuration to $EnvFile"
    }

    # --- next steps -----------------------------------------------------------
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$InstallDir*") {
        Write-Host "[install-runner] NOTE: $InstallDir is not in your user PATH -- add it with:" -ForegroundColor Yellow
        Write-Host "  [Environment]::SetEnvironmentVariable('Path', `$userPath + ';$InstallDir', 'User')"
    }

    $nextSteps = @(
        ''
        "[install-runner] Done. Installed: $dest ($installedVersion)"
        ''
        'Next steps:'
        '  1. Make sure the server is running and reachable, e.g.:'
        '       curl.exe -H "Authorization: Bearer <mobile-token>" http://<server-ip>:8080/healthz'
        "  2. Start the runner (config was written to $EnvFile):"
        "       `$lines = Get-Content $EnvFile; foreach (`$l in `$lines) { if (`$l -match '^([^=]+)=(.*)$') { Set-Item -Path ('env:' + `$matches[1]) -Value `$matches[2] } }"
        '       agentpilot-runner.exe'
        '     or explicitly:'
        '       agentpilot-runner.exe --server http://<server-ip>:8080 --token <runner-token> --roots C:\path\to\projects'
        '  3. To keep it running at logon, create a Scheduled Task that starts'
        '     agentpilot-runner.exe with the env file loaded (see docs/INSTALL.md).'
        ''
        "Full guide: https://github.com/$Repo/blob/main/docs/INSTALL.md"
    )
    Write-Host ($nextSteps -join "`n")
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
