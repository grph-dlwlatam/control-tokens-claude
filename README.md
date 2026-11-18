# Control de consumo de Claude Code por máquina

La cuenta de Claude Code es compartida, así que Anthropic no distingue quién consume. Esta herramienta mide el consumo desde cada máquina y lo muestra en un dashboard central.

```
[VM analista] --agente--> HTTPS --> [Supabase] <-- dashboard.html (líder)
```

- **Agente** (`agente/`): lee los registros locales de Claude Code (`%USERPROFILE%\.claude\projects\**\*.jsonl`), suma los tokens por día y modelo, y envía **solo esos totales**. No envía prompts, código, nombres de archivos ni rutas de proyecto. No requiere permisos de administrador ni instalar nada. Usa Node si existe y, si no, el Node que trae VS Code. Envía con `curl.exe` de Windows.
- **Identidad de máquina**: un hash de `MachineGuid` + nombre del equipo + usuario de Windows. Se combinan los tres porque las VMs clonadas suelen repetir el `MachineGuid`.
- **Dashboard** (`dashboard/dashboard.html`): un solo archivo HTML. La líder lo abre con doble clic, inicia sesión y asigna nombres a cada máquina.

## 1. Crear el receptor en Supabase (una vez, unos 10 minutos)

1. Crear un proyecto gratuito en https://supabase.com.
2. Ir a **SQL Editor > New query**, pegar el contenido de [supabase/esquema.sql](supabase/esquema.sql) y pulsar **Run**.
3. Ir a **Authentication > Users > Add user** y crear el usuario de la líder (correo y contraseña, marcando *Auto confirm*).
4. Ir a **Authentication > Sign In / Providers** y **desactivar "Allow new users to sign up"**. Si queda activado, cualquiera con la clave pública podría registrarse y ver los datos.
5. Ir a **Project Settings > API** y copiar:
   - la **Project URL** (`https://xxxx.supabase.co`);
   - la clave pública: **anon** (legacy) o **publishable** (`sb_publishable_...`). **Nunca** la `service_role` ni la `secret`.

## 2. Configurar

- En `agente/config.json` poner `supabaseUrl` y `supabaseKey`.
- En `dashboard/dashboard.html` editar las dos constantes de la sección `CONFIGURACIÓN` con los mismos valores.

La clave pública solo permite llamar a `registrar_consumo()`. Con ella no se pueden leer ni borrar datos.

## 3. Distribuir a los analistas

Copiar la carpeta `agente/` a la carpeta compartida (la misma por donde se reparte el `.vsix`). Cada analista ejecuta **una vez** `instalar.cmd` con doble clic. El instalador:

1. copia el agente a `%LOCALAPPDATA%\ControlTokensClaude`;
2. envía todo el historial de consumo;
3. programa un envío cada 30 minutos (tarea programada `ControlTokensClaude`). Si la política de la VM bloquea las tareas programadas, el envío se hace al iniciar sesión.

Para retirarlo: `desinstalar.cmd`. El log queda en `%LOCALAPPDATA%\ControlTokensClaude\agente.log`.

Prueba sin enviar nada: `ejecutar.cmd --dry-run` muestra exactamente lo que se enviaría.

### Alternativa: un solo `GestorTokens.exe` (recomendada si el antivirus lo permite)

Código en [agente-exe/](agente-exe/) (Go, sin dependencias). La configuración va embebida. Se compila con:

```
cd agente-exe
go build -trimpath -ldflags "-H windowsgui -s -w" -o ../dist/GestorTokens.exe .
```

- **Doble clic:** se copia a `%LOCALAPPDATA%\GestorTokens`, envía el historial, programa la tarea `GestorTokensDLW` cada 30 minutos y muestra el resultado. No requiere administrador.
- `GestorTokens.exe --desinstalar` quita la tarea y los archivos. `--dry-run` deja en `%LOCALAPPDATA%\GestorTokens\dry-run.json` lo que enviaría.
- Solo envía si Claude Code está conectado con la cuenta compartida (`cuentaObjetivo` en `main.go`).
- No está firmado. Si SmartScreen, el antivirus o AppLocker lo bloquean, usar la versión `.cmd`.

## 4. Dashboard

Doble clic en `dashboard.html`, iniciar sesión con el usuario del paso 1.4 y escribir el nombre de cada persona junto a su máquina. Las máquinas con el mismo nombre se suman como una sola persona.

Para verlo sin datos reales: abrir `dashboard.html#demo`.

**Costo equivalente** = lo que costaría el consumo a precio de API. Con suscripción no se cobra por token. Es la medida comparable de quién gasta más de la cuota compartida. Los precios están en la constante `PRECIOS` del HTML.

## Limitaciones

- El control funciona por confianza: quien desinstale el agente deja de reportar. La columna *Último reporte* lo marca en ámbar (más de 24 h) o rojo (más de 72 h).
- Solo mide Claude Code (CLI o extensión de VS Code), no el chat de claude.ai.
- La red del cliente inspecciona HTTPS. Por eso el agente usa `curl --ssl-no-revoke` (verificado desde una VM de Luz del Sur: hay salida a `supabase.com`, sin proxy).
