@echo off
setlocal
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0Install-All-Terrains.ps1" %*
exit /b %ERRORLEVEL%
