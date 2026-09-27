# dmcode installer for Windows. Run it in PowerShell:
#   irm https://raw.githubusercontent.com/dedomorozoff/dmcode/main/install.ps1 | iex
$ErrorActionPreference = 'Stop'

$repo = 'dedomorozoff/dmcode'
$bin  = 'dmcode'

# The release workflow only builds windows-amd64.
if (-not [Environment]::Is64BitOperatingSystem) {
  throw 'dmcode: собран только под 64-битный Windows; 32-битной сборки нет'
}
$arch = 'amd64'

# A directory already on PATH, so the binary is usable right after install.
$target = @("$env:LOCALAPPDATA\Programs\dmcode", "$HOME\bin") |
  Where-Object {
    $candidate = $_
    ($env:PATH -split ';') -contains $candidate
  } | Select-Object -First 1
if (-not $target) { $target = "$env:LOCALAPPDATA\Programs\dmcode" }

New-Item -ItemType Directory -Force -Path $target | Out-Null

$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$tag     = $release.tag_name
$asset   = $release.assets |
  Where-Object { $_.name -eq "$bin-windows-$arch.exe" } |
  Select-Object -First 1
if (-not $asset) { throw "dmcode: в релизе $tag нет $bin-windows-$arch.exe" }

$out = Join-Path $target "$bin.exe"
Write-Host "dmcode: ставлю $tag (windows/$arch) -> $out"
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $out

# Add the install dir to the user PATH when it is missing, so `dmcode` resolves
# in future shells without the user editing anything by hand.
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if ($userPath -notlike "*$target*") {
  [Environment]::SetEnvironmentVariable('PATH', "$userPath;$target", 'User')
  $env:PATH = "$env:PATH;$target"
  Write-Host "dmcode: добавил $target в PATH текущего пользователя"
}

Write-Host ''
Write-Host 'Готово. Открой новый терминал и запусти: dmcode'
Write-Host ''
Write-Host 'Провайдера подберём сами: локальный Ollama/LM Studio, бесплатный'
Write-Host 'хост без ключа или мастер /setup при первом старте.'
