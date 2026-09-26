@echo off
rem Build the Robot binaries. Run from anywhere: deploy\build-robot.bat
rem Windows output: deploy\output\robot.exe
rem Linux cross-build output: deploy\output\robot-linux-amd64
setlocal enabledelayedexpansion
cd /d "%~dp0.."

where go >nul 2>nul
if errorlevel 1 (
  echo [build] Go toolchain not found in PATH.
  exit /b 1
)

set COMMIT=unknown
for /f "delims=" %%i in ('git rev-parse --short HEAD 2^>nul') do set COMMIT=%%i
set VERSION=dev
for /f "delims=" %%i in ('git describe --tags --always 2^>nul') do set VERSION=%%i

set OUT=deploy\output
if not exist "%OUT%" mkdir "%OUT%"

echo [build] windows/amd64 version=%VERSION%+%COMMIT%
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=%VERSION%+%COMMIT%" -o "%OUT%\robot.exe" .\cmd\robot
if errorlevel 1 (
  echo [build] windows build failed.
  exit /b 1
)

echo [build] linux/amd64 version=%VERSION%+%COMMIT%
set GOOS=linux
go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=%VERSION%+%COMMIT%" -o "%OUT%\robot-linux-amd64" .\cmd\robot
if errorlevel 1 (
  echo [build] linux build failed.
  exit /b 1
)

echo [build] done: %OUT%
