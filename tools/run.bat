@echo off
setlocal
pushd "%~dp0"
if errorlevel 1 exit /b 1
"%~dp0wrpl-acmi.exe" %*
set "wrpl_exit=%errorlevel%"
popd
pause
exit /b %wrpl_exit%
