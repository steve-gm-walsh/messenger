@echo off
REM Standardized runner for messenger (Go library: chat-bot plumbing for the house apps)
REM   run.bat setup   - Download modules and build every package
REM   run.bat test    - gofmt check, go vet, tidy check, staticcheck, govulncheck, go test -race
REM   run.bat clean   - Remove test output and the in-repo Go caches
REM   run.bat build   - Build every package (a library: there is no exe)
REM   run.bat [bare]  - Build, then say how the apps consume it

setlocal
cd /d "%~dp0"
REM Rule 2: the Go toolchain cache is the environment, shared with every other Go repo here.

if /i "%1"=="setup" (goto :setup)
if /i "%1"=="test" (goto :test)
if /i "%1"=="clean" (goto :clean)
if /i "%1"=="build" (goto :build)

go build ./...
if errorlevel 1 exit /b %errorlevel%
echo messenger is a library; nothing to launch.
echo Apps require github.com/steve-gm-walsh/messenger at a tag - see README.md.
exit /b 0

:setup
go mod download
if errorlevel 1 exit /b %errorlevel%
go build ./...
if errorlevel 1 exit /b %errorlevel%
echo Setup complete. Run 'run.bat test'.
exit /b 0

:test
gofmt -l . 1>gofmt.tmp 2>nul
for /f %%A in ("gofmt.tmp") do set GOFMT_SIZE=%%~zA
if not "%GOFMT_SIZE%"=="0" (
  echo gofmt needs to run on:
  type gofmt.tmp
  del gofmt.tmp
  exit /b 1
)
del gofmt.tmp
go vet ./...
if errorlevel 1 exit /b %errorlevel%
go mod tidy -diff
if errorlevel 1 exit /b %errorlevel%
where staticcheck >nul 2>nul
if not errorlevel 1 (
  staticcheck ./...
  if errorlevel 1 exit /b %errorlevel%
)
where govulncheck >nul 2>nul
if not errorlevel 1 (
  govulncheck -scan package ./...
  if errorlevel 1 exit /b %errorlevel%
)
go test -race ./...
exit /b %errorlevel%

:clean
if exist gofmt.tmp del /q gofmt.tmp
if exist .go rmdir /s /q .go
go clean -testcache
echo Clean complete.
exit /b 0

:build
go build ./...
exit /b %errorlevel%
