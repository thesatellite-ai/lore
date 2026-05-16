# lore installer for Windows (PowerShell).
#
# Usage:
#   irm https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.ps1 | iex
#
# Downloads the latest released binary from GitHub Releases, installs it
# to %LOCALAPPDATA%\lore, adds that dir to the user PATH, and
# installs the Claude skill bundle to %USERPROFILE%\.claude\skills\lore.

$ErrorActionPreference = "Stop"

$Repo    = "thesatellite-ai/lore"
$Binary  = "lore"
$InstallDir = Join-Path $env:LOCALAPPDATA "lore"
$SkillDest  = Join-Path $env:USERPROFILE ".claude\skills\lore"

$arch = if ([Environment]::Is64BitOperatingSystem) {
  if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else { throw "Unsupported architecture" }

$rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$tag = $rel.tag_name
if (-not $tag) { throw "No release found at https://github.com/$Repo/releases" }

$asset = "${Binary}_windows_${arch}.zip"
$url   = "https://github.com/$Repo/releases/download/$tag/$asset"

Write-Host "Downloading $Binary $tag for windows/$arch..."
$tmp = Join-Path $env:TEMP ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
$zip = Join-Path $tmp "$asset"
Invoke-WebRequest -Uri $url -OutFile $zip
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
Write-Host "Installed. Next steps:"
Write-Host "  cd <your-project>"
Write-Host "  $Binary init                 # create .lore/ + sqlite db"
Write-Host "  $Binary setup                # build FTS5 search index"
Write-Host "  $Binary directive install    # add the agent-directive block to CLAUDE.md / AGENTS.md"
Write-Host ""
Write-Host "The Claude skill is at $SkillDest (restart Claude Code to load it)."
Write-Host "Verify: $Binary version"
