@echo off
rem Ejecuta agente.js con Node; si no hay Node, usa el que trae VS Code (modo Electron).
setlocal
set "AQUI=%~dp0"
set "MOTOR="
for /f "delims=" %%i in ('where node 2^>nul') do if not defined MOTOR set "MOTOR=%%i"
if defined MOTOR goto correr

set ELECTRON_RUN_AS_NODE=1
for %%p in ("%LOCALAPPDATA%\Programs\Microsoft VS Code\Code.exe" "%ProgramFiles%\Microsoft VS Code\Code.exe") do (
  if not defined MOTOR if exist %%p set "MOTOR=%%~p"
)
if not defined MOTOR for /f "delims=" %%i in ('where code.cmd 2^>nul') do (
  if not defined MOTOR if exist "%%~dpi..\Code.exe" set "MOTOR=%%~dpi..\Code.exe"
)
if not defined MOTOR (
  echo [ControlTokens] No se encontro Node ni VS Code en esta maquina.
  exit /b 3
)

:correr
"%MOTOR%" "%AQUI%agente.js" %*
exit /b %errorlevel%
