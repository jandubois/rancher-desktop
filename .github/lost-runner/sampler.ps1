# Every ~2 s until the process is killed, append a line of resource counters,
# the five largest processes, and the watched ones to the TSV file named by
# the first argument.
param([string]$Out)

$counters = '\Processor(_Total)\% Processor Time', '\System\Processor Queue Length',
  '\Memory\Available MBytes', '\Memory\Committed Bytes', '\Memory\Commit Limit',
  '\Memory\Pages/sec', '\PhysicalDisk(_Total)\Avg. Disk Queue Length',
  '\Network Interface(*)\Bytes Total/sec'

# The top five misses these unless they grow large, so track them by name.
$watchedNames = '^(WindowsTerminal|OpenConsole|conhost|wsl.*|vmmem.*|msiexec)$'

function Sum($samples, $pattern) {
  ($samples | Where-Object Path -like $pattern | Measure-Object CookedValue -Sum).Sum
}

"time`tcpu_pct`tcpu_queue`tavail_mb`tcommit_mb`tcommit_limit_mb`tpages_s`tdisk_queue`tnet_kb_s`tnode_count`tnode_private_mb`ttop5_ws_mb`twatched" |
  Set-Content $Out
while ($true) {
  try {
    $s = (Get-Counter -Counter $counters -SampleInterval 2 -MaxSamples 1 -ErrorAction SilentlyContinue).CounterSamples
    $procs = Get-Process
    $node = @($procs | Where-Object ProcessName -eq 'node')
    $top = $procs | Sort-Object WorkingSet64 -Descending | Select-Object -First 5 |
      ForEach-Object { '{0}:{1}' -f $_.ProcessName, [int]($_.WorkingSet64 / 1MB) }
    $watched = $procs | Where-Object ProcessName -match $watchedNames | Group-Object ProcessName |
      ForEach-Object { '{0}:{1}:{2}' -f $_.Name, $_.Count, [int](($_.Group | Measure-Object WorkingSet64 -Sum).Sum / 1MB) }
    @(
      (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
      [int](Sum $s '*\% processor time')
      [int](Sum $s '*\processor queue length')
      [int](Sum $s '*\available mbytes')
      [int]((Sum $s '*\committed bytes') / 1MB)
      [int]((Sum $s '*\commit limit') / 1MB)
      [int](Sum $s '*\pages/sec')
      [math]::Round((Sum $s '*\avg. disk queue length'), 1)
      [int]((Sum $s '*\bytes total/sec') / 1KB)
      $node.Count
      [int](($node | Measure-Object PrivateMemorySize64 -Sum).Sum / 1MB)
      $top -join ' '
      $watched -join ' '
    ) -join "`t" | Add-Content $Out
  } catch {
    "$((Get-Date).ToUniversalTime().ToString('o'))`terror: $_" | Add-Content $Out
    Start-Sleep -Seconds 2
  }
}
