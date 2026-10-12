<#
.SYNOPSIS
  Installs the Heartbeat agent as a Windows service.

.EXAMPLE
  # From an elevated PowerShell:
  & ([scriptblock]::Create((irm https://SERVER/install/agent.ps1))) -Server https://SERVER -Token ABCD-1234

.PARAMETER Server        Heartbeat server URL.
.PARAMETER Token         One-time enrolment token from "Add host".
.PARAMETER CaFingerprint Pin the server CA (self-signed TLS mode).
.PARAMETER BinaryPath    Use a local heartbeat-agent.exe instead of downloading.
.PARAMETER Name          Display name for the host.
.PARAMETER WriteJobs     Who may authorise jobs that change this host: server (default), operator or none.
.PARAMETER ReadOnly      Shorthand for -WriteJobs none.
.PARAMETER OperatorKey   Operator public key(s) (from "hb-sign keygen-operator") for -WriteJobs operator.
.PARAMETER InsecureDownload Skip TLS validation for the binary download only.
.PARAMETER Uninstall     Remove the agent.
#>
param(
  [string]$Server,
  [string]$Token,
  [string]$CaFingerprint = "",
  [string]$BinaryPath = "",
  [string]$Name = "",
  [ValidateSet("", "server", "operator", "none")][string]$WriteJobs = "",
  [switch]$ReadOnly,
  [string[]]$OperatorKey = @(),
  [switch]$InsecureDownload,
  [switch]$Uninstall
)

$ErrorActionPreference = "Stop"
$InstallDir = Join-Path $env:ProgramFiles "Heartbeat Agent"
$DataDir = Join-Path $env:ProgramData "Heartbeat Agent"
$Exe = Join-Path $InstallDir "heartbeat-agent.exe"
$Config = Join-Path $DataDir "agent.yaml"

function Say($m) { Write-Host "==> $m" }

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Run this script from an elevated (Administrator) PowerShell."
}

if ($Uninstall) {
  Say "removing Heartbeat agent"
  if (Test-Path $Exe) {
    & $Exe service stop --config $Config 2>$null | Out-Null
    & $Exe service uninstall --config $Config 2>$null | Out-Null
  }
  Get-NetFirewallRule -DisplayName "Heartbeat block *" -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
  Remove-Item -Recurse -Force $InstallDir, $DataDir -ErrorAction SilentlyContinue
  Say "done"
  return
}

if (-not $Server -or -not $Token) { throw "-Server and -Token are required" }
$Server = $Server.TrimEnd('/')
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

New-Item -ItemType Directory -Force -Path $InstallDir, $DataDir | Out-Null
# Only SYSTEM and Administrators may read or write the agent state (keys, update staging).
icacls $DataDir /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null

if (Test-Path $Exe) {
  & $Exe service stop --config $Config 2>$null | Out-Null
  Start-Sleep -Seconds 2
}

if ($BinaryPath) {
  Copy-Item -Force $BinaryPath $Exe
} else {
  $url = "$Server/download/agent/windows/amd64"
  Say "downloading agent from $url"
  $tmp = "$Exe.download"
  if ($InsecureDownload) {
    if ($PSVersionTable.PSVersion.Major -ge 6) {
      Invoke-WebRequest -Uri $url -OutFile $tmp -SkipCertificateCheck -UseBasicParsing
    } else {
      $prev = [Net.ServicePointManager]::ServerCertificateValidationCallback
      [Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
      try { (New-Object Net.WebClient).DownloadFile($url, $tmp) }
      finally { [Net.ServicePointManager]::ServerCertificateValidationCallback = $prev }
    }
  } else {
    Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing
  }
  Move-Item -Force $tmp $Exe
}

& $Exe version | Out-Null
if ($LASTEXITCODE -ne 0) { throw "agent binary does not run on this system" }

Say "registering with $Server"
$regArgs = @("register", "--server", $Server, "--token", $Token, "--config", $Config, "--state-dir", $DataDir)
if ($CaFingerprint) { $regArgs += @("--ca-fingerprint", $CaFingerprint) }
if ($Name) { $regArgs += @("--name", $Name) }
if ($ReadOnly) { $WriteJobs = "none" }
if ($WriteJobs) { $regArgs += @("--write-jobs", $WriteJobs) }
foreach ($k in $OperatorKey) { $regArgs += @("--operator-key", $k) }
& $Exe @regArgs
if ($LASTEXITCODE -ne 0) { throw "registration failed" }

Say "installing service"
& $Exe service uninstall --config $Config 2>$null | Out-Null
& $Exe service install --config $Config
if ($LASTEXITCODE -ne 0) { throw "service install failed" }
& $Exe service start --config $Config
# Restart on failure (used by self-update rollback).
sc.exe failure HeartbeatAgent reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null
Start-Sleep -Seconds 3
$svc = Get-Service HeartbeatAgent -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -eq "Running") {
  Say "Heartbeat agent is running. It should appear in the web UI within a minute."
} else {
  throw "agent service did not start; see the Application event log"
}
