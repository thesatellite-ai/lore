# lore installer for Windows (PowerShell).
#
# Usage:
#   irm https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.ps1 | iex
#
# Downloads the latest release's archive (lore_windows_<arch>.zip) from
# GitHub Releases, checks it against the release's checksums.txt, installs
# lore.exe to %LOCALAPPDATA%\lore, adds that dir to the user PATH, and
# installs the agent skill bundle to %USERPROFILE%\.claude\skills\lore.

$ErrorActionPreference = "Stop"

$Repo    = "thesatellite-ai/lore"
$Binary  = "lore"
$InstallDir = Join-Path $env:LOCALAPPDATA "lore"
$SkillDest  = Join-Path $env:USERPROFILE ".claude\skills\lore"

$arch = if ([Environment]::Is64BitOperatingSystem) {
  if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else { throw "Unsupported architecture" }

$asset = "${Binary}_windows_${arch}.zip"
# /releases/latest/download/ redirects to the newest asset with no call
# to the rate-limited GitHub API.
$base  = "https://github.com/$Repo/releases/latest/download"
$url   = "$base/$asset"

Write-Host "Downloading latest $Binary for windows/$arch..."
$tmp = Join-Path $env:TEMP ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
$zip = Join-Path $tmp "$asset"
$sums = Join-Path $tmp "checksums.txt"
Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $zip
Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums

# Check the archive's sha256 against its line in checksums.txt (catches a
# corrupt or truncated download) before anything is installed.
$want = $null
foreach ($line in Get-Content $sums) {
  $parts = $line -split '\s+'
  if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $asset) { $want = $parts[0].ToLower() }
}
$got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
if (-not $want -or $want -ne $got) {
  Remove-Item -Recurse -Force $tmp
  throw "sha256 of $asset does not match checksums.txt; nothing installed."
}
Write-Host "sha256 ok"

Expand-Archive -Path $zip -DestinationPath $tmp -Force

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item (Join-Path $tmp "$Binary.exe") (Join-Path $InstallDir "$Binary.exe") -Force

# Add install dir to user PATH if missing.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$InstallDir*") {
  [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
  Write-Host "Added $InstallDir to user PATH (restart your shell)."
}

# Install the Claude skill bundle shipped inside the archive.
$skillSrc = Join-Path $tmp "skills"
if (Test-Path $skillSrc) {
  Write-Host "Installing Claude skill to $SkillDest..."
  New-Item -ItemType Directory -Force -Path $SkillDest | Out-Null
  Copy-Item "$skillSrc\*" $SkillDest -Recurse -Force
}

Remove-Item -Recurse -Force $tmp

Write-Host ""
Write-Host "Installed. Next steps (in a new terminal):"
Write-Host "  $Binary version                          # check the install"
Write-Host "  $Binary setup                            # optional: guided setup of this machine (identity, GitHub, stores)"
Write-Host "  $Binary setup join --invite `"<invite>`"   # joining a team: the invite line or store url your admin sent"
Write-Host "  cd <your-project>; $Binary init          # start lore in a code repo"
Write-Host ""
Write-Host "The agent skill is at $SkillDest (restart your coding agent to load it);"
Write-Host "$Binary skill install --all adds it for your other coding tools."
Write-Host "Upgrading from 0.1.17? Every teammate installs 0.1.18 first, then one person runs $Binary migrate run in each repo."
