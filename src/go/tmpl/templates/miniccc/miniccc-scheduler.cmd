C:\minimega\miniccc.exe -install manual-start -logfile C:\minimega\miniccc.log -level info
schtasks.exe /create /sc onstart /ru SYSTEM /rl highest /tn "miniccc" /tr "net start miniccc" /f
schtasks.exe /run /tn "miniccc"
