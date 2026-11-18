@echo off
rem Instalador del agente de consumo de Claude Code (no requiere administrador).
rem Copia el agente a %LOCALAPPDATA%\ControlTokensClaude, envia el historial y lo programa cada 30 minutos.
setlocal
set "ORIGEN=%~dp0"
set "DEST=%LOCALAPPDATA%\ControlTokensClaude"
set "TAREA=ControlTokensClaude"

echo.
echo === Control de consumo de Claude Code ===
echo Solo se envian totales de tokens por dia y modelo. No se envian prompts, codigo ni archivos.
echo.

if not exist "%DEST%" mkdir "%DEST%"
copy /y "%ORIGEN%agente.js"    "%DEST%\" >nul || goto error
copy /y "%ORIGEN%ejecutar.cmd" "%DEST%\" >nul || goto error
copy /y "%ORIGEN%config.json"  "%DEST%\" >nul || goto error

if exist "%DEST%\identidad.json" goto enviar
echo Identificate para registrar tu consumo (solo una vez).
:pedir
set "NOMBRE="
set "CORREO="
set /p "NOMBRE=  Nombre completo: "
set /p "CORREO=  Correo DLW (@dlwlatam.com): "
set "NOMBRE=%NOMBRE:"=%"
echo %NOMBRE%| findstr /c:" " >nul || (echo   Escribe tu nombre completo ^(nombre y apellido^). & goto pedir)
echo %CORREO%| findstr /r /i /x "[a-z0-9._+-]*@dlwlatam\.com" >nul || (echo   El correo debe terminar en @dlwlatam.com. & goto pedir)
> "%DEST%\identidad.json" echo {"nombre":"%NOMBRE%","correo":"%CORREO%"}
echo.
:enviar
echo [1/2] Enviando historial de consumo...
call "%DEST%\ejecutar.cmd" --completo
if errorlevel 1 (
  echo.
  echo No se pudo enviar. Revisa "%DEST%\agente.log" y avisa a la lider.
  goto fin
)

echo [2/2] Programando envio automatico cada 30 minutos...
schtasks /create /f /sc minute /mo 30 /tn "%TAREA%" /tr "conhost.exe --headless \"%DEST%\ejecutar.cmd\"" >nul 2>&1
if errorlevel 1 (
  rem Si la politica bloquea tareas programadas, se ejecuta al iniciar sesion.
  > "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\%TAREA%.cmd" echo @start "" /min "%DEST%\ejecutar.cmd"
  echo     Tareas programadas no permitidas: se enviara al iniciar sesion.
) else (
  echo     Tarea programada creada.
)

echo.
echo Listo. Ya puedes cerrar esta ventana.
goto fin

:error
echo Error copiando archivos a "%DEST%".
:fin
echo.
pause
