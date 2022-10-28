schtasks.exe /create /sc onstart /ru SYSTEM /rl highest /tn phenix-startup /tr "powershell.exe -ep bypass C:\phenix\phenix-startup.ps1 > C:\phenix\phenix-startup.log" /f
schtasks.exe /run /tn "phenix-startup"
