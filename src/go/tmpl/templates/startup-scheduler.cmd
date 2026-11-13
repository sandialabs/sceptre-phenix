rem The task's onstart trigger runs it at every boot, so run it here only when it is new.
set phenix_task_existed=
schtasks.exe /query /tn phenix-startup >nul 2>&1 && set phenix_task_existed=1
schtasks.exe /create /sc onstart /ru SYSTEM /rl highest /tn phenix-startup /tr "powershell.exe -ep bypass C:\phenix\phenix-startup.ps1 > C:\phenix\phenix-startup.log" /f
if not defined phenix_task_existed schtasks.exe /run /tn "phenix-startup"
