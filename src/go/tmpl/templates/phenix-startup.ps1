Get-ChildItem '/phenix/startup/*.ps1' | ForEach-Object {
  & $_.FullName
}