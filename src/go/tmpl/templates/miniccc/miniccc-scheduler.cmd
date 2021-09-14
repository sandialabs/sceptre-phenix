C:\minimega\miniccc.exe -install manual-start -logfile C:\minimega\miniccc.log -level info
schtasks /create /tn "miniccc" /sc onstart /rl highest /tr "net start miniccc" /f
schtasks /run /tn "miniccc"
