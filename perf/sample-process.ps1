# Samples CPU and memory of one process to CSV while a load test runs.
#
#   powershell -File perf/sample-process.ps1 -ProcessId 1234 -Out perf/results/process.csv -Seconds 1260
#
# cpu_pct is the share of the whole machine (100 = all cores busy).
param(
  [Parameter(Mandatory)] [int] $ProcessId,
  [Parameter(Mandatory)] [string] $Out,
  [int] $Seconds = 1260,
  [int] $Interval = 5
)

$cores = [Environment]::ProcessorCount
"time,cpu_pct,working_set_mb,private_mb,threads,handles" | Out-File -Encoding utf8 $Out
$p = Get-Process -Id $ProcessId -ErrorAction Stop
$prevCpu = $p.TotalProcessorTime.TotalSeconds
$prevAt = Get-Date
$end = $prevAt.AddSeconds($Seconds)

while ((Get-Date) -lt $end) {
  Start-Sleep -Seconds $Interval
  try { $p = Get-Process -Id $ProcessId -ErrorAction Stop } catch { break } # process exited
  $now = Get-Date
  $cpu = $p.TotalProcessorTime.TotalSeconds
  $pct = 100 * ($cpu - $prevCpu) / ($now - $prevAt).TotalSeconds / $cores
  $prevCpu, $prevAt = $cpu, $now
  [string]::Format([cultureinfo]::InvariantCulture, "{0},{1:F1},{2:F1},{3:F1},{4},{5}",
    $now.ToString("HH:mm:ss"), $pct, ($p.WorkingSet64 / 1MB), ($p.PrivateMemorySize64 / 1MB),
    $p.Threads.Count, $p.HandleCount) | Out-File -Append -Encoding utf8 $Out
}
