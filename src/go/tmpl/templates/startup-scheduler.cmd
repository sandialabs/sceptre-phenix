schtasks /create /tn "phenix-startup" /sc onlogon /rl highest /tr "powershell.exe -file C:\phenix\phenix-startup.ps1 > C:\phenix\phenix-startup.log" /F
schtasks /run /tn "phenix-startup"