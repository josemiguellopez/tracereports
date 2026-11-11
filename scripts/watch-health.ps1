<#
.SYNOPSIS
  Watches the TraceReports server from the machine that runs it and logs when it stops answering.

.DESCRIPTION
  Every few seconds it asks http://localhost:8080/healthz (the same path Docker publishes for the
  tests). It writes a line only when the answer fails or is slow, plus one "ok" line per hour, so
  the file stays small.

  When a test says "POST /api/v1/runs failed: timed out", compare its time with this file:
    - a FAIL or SLOW line at that time: the server, or Docker on this machine, was not answering;
    - only "ok" around that time: this machine answered fine, so the request got stuck on the way
      (the network between the test machine and this one).
  The server log (docker compose logs) adds a "slow request" line when TraceReports itself was slow.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts\watch-health.ps1
  powershell -ExecutionPolicy Bypass -File scripts\watch-health.ps1 -Url http://localhost:8080/healthz -Every 10
#>
param(
    [string]$Url = "http://localhost:8080/healthz",
    [int]$Every = 10,          # seconds between checks
    [int]$Timeout = 5,         # seconds to wait for an answer
    [int]$SlowMs = 1000,       # an answer slower than this is logged as SLOW
    [string]$Log = (Join-Path $env:ProgramData "TraceReports\health.log"),
    [int]$MaxMB = 20           # the log is rotated (health.log.1) past this size
)

$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path (Split-Path $Log) | Out-Null

function Write-Line([string]$text) {
    if ((Test-Path $Log) -and ((Get-Item $Log).Length -gt $MaxMB * 1MB)) {
        Move-Item -Force $Log "$Log.1"
    }
    $line = "{0}  {1}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $text
    Add-Content -Path $Log -Value $line -Encoding utf8
}

Write-Line "start: checking $Url every $Every s (timeout $Timeout s, slow above $SlowMs ms)"
$lastOk = [datetime]::MinValue
while ($true) {
    $watch = [System.Diagnostics.Stopwatch]::StartNew()
    try {
        $resp = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec $Timeout
        $ms = $watch.ElapsedMilliseconds
        if ($resp.StatusCode -ne 200) {
            Write-Line "FAIL  HTTP $($resp.StatusCode) in $ms ms"
        } elseif ($ms -gt $SlowMs) {
            Write-Line "SLOW  200 in $ms ms"
        } elseif (((Get-Date) - $lastOk).TotalMinutes -ge 60) {
            Write-Line "ok    200 in $ms ms"
            $lastOk = Get-Date
        }
    } catch {
        Write-Line ("FAIL  after {0} ms: {1}" -f $watch.ElapsedMilliseconds, $_.Exception.Message)
    }
    Start-Sleep -Seconds $Every
}
