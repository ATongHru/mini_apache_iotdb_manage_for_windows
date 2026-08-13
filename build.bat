@echo off
setlocal
cd /d "%~dp0"
REM Force fully offline dependency resolution. All required modules are in third_party/.
set "GOPROXY=off"
set "GOSUMDB=off"
echo Building MiniApacheIoTDBManager.exe from local dependencies (offline)...
go version
if errorlevel 1 goto :failed
go build -mod=mod -trimpath -ldflags="-s -w" -o MiniApacheIoTDBManager.exe .
if errorlevel 1 goto :failed
echo Build succeeded: %CD%\MiniApacheIoTDBManager.exe
pause
exit /b 0
:failed
echo Build failed.
pause
exit /b 1
