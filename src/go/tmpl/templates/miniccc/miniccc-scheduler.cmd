schtasks /create /tn "miniccc" /sc onlogon /rl highest /tr "C:\minimega\miniccc.exe -serial \\.\Global\cc" /F
schtasks /run /tn "miniccc"
