@echo off
rem Quita el agente de consumo de esta maquina.
schtasks /delete /f /tn "ControlTokensClaude" >nul 2>&1
del /q "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\ControlTokensClaude.cmd" >nul 2>&1
rmdir /s /q "%LOCALAPPDATA%\ControlTokensClaude" >nul 2>&1
echo Agente desinstalado.
pause
