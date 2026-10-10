# Until the process is killed, append every process start, with its command
# line and its parent's, to the TSV file named by the first argument.
param([string]$Out)

# A process that exits within milliseconds is gone before this runs, so its
# command reads "(exited)" and only its name and parent identify it.
function Describe($id) {
  $p = Get-CimInstance Win32_Process -Filter "ProcessId=$id"
  if ($p) { "$($p.Name) $($p.CommandLine)" -replace '\s+', ' ' } else { '(exited)' }
}

function LogError($err) {
  "$((Get-Date).ToUniversalTime().ToString('o'))`terror: $err" | Add-Content $Out
}

"time`tpid`tname`tcommand`tparent_pid`tparent" | Set-Content $Out
try {
  Register-CimIndicationEvent -SourceIdentifier start -Query 'SELECT * FROM Win32_ProcessStartTrace'
} catch {
  LogError $_
  exit 1
}
while ($true) {
  $e = Wait-Event
  Remove-Event -EventIdentifier $e.EventIdentifier
  try {
    $t = $e.SourceEventArgs.NewEvent
    @(
      $e.TimeGenerated.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fffZ')
      $t.ProcessID
      $t.ProcessName
      Describe $t.ProcessID
      $t.ParentProcessID
      Describe $t.ParentProcessID
    ) -join "`t" | Add-Content $Out
  } catch {
    LogError $_
  }
}
