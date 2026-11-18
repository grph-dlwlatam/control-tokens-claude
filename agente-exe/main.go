// GestorTokens.exe — agente portable del Gestor de Tokens (DLW Latam).
//
// Lee los registros locales de Claude Code (~/.claude/projects/**/*.jsonl), suma tokens
// por día y modelo y envía SOLO esos totales a Supabase. Nunca envía prompts, código ni rutas.
// No requiere administrador: se copia a %LOCALAPPDATA%\GestorTokens y se programa cada 30 min.
//
// Uso:
//
//	GestorTokens.exe                doble clic: instala, envía y muestra el resultado
//	GestorTokens.exe --silencioso   ejecución programada (sin ventanas)
//	GestorTokens.exe --desinstalar  quita la tarea programada y los archivos
//	GestorTokens.exe --dry-run      escribe en un archivo lo que enviaría, sin enviar
//	GestorTokens.exe --completo     reenvía todo el historial
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	version       = "2.1.0"
	nombreTarea   = "GestorTokensDLW"
	nombreExe     = "GestorTokens.exe"
	diasRecalculo = 7
	tituloVentana = "Gestor de Tokens · DLW"
)

// Configuración embebida (se puede sobrescribir al compilar con -ldflags "-X main.supabaseURL=...").
var (
	supabaseURL    = "https://xsyrryiwxwexxlsnhrup.supabase.co"
	supabaseKey    = "sb_publishable_s7okdKo9rYI6Ys-HjqdioA_3TyUVL9y"
	cuentaObjetivo = "fabricadetesting@dlwlatam.com"
)

var (
	flags      = map[string]bool{}
	dirInstal  = filepath.Join(os.Getenv("LOCALAPPDATA"), "GestorTokens")
	archivoLog = filepath.Join(dirInstal, "agente.log")
)

func main() {
	for _, a := range os.Args[1:] {
		flags[strings.TrimLeft(strings.ToLower(a), "-")] = true
	}
	_ = os.MkdirAll(dirInstal, 0o755)

	if flags["desinstalar"] {
		desinstalar()
		return
	}

	interactivo := !flags["silencioso"] && !flags["dry-run"]
	instalado := ""
	if interactivo {
		var err error
		if instalado, err = instalar(); err != nil {
			logf("ERROR instalando: %v", err)
		}
	}

	res, err := medir()
	if err != nil {
		logf("ERROR: %v", err)
		if !flags["silencioso"] {
			mensaje("No se pudo enviar el consumo:\n\n"+err.Error()+"\n\nDetalle en:\n"+archivoLog, true)
		}
		os.Exit(1)
	}
	if !flags["silencioso"] {
		mensaje(res+instalado, false)
	}
}

// ---------- instalación ----------

func instalar() (string, error) {
	actual, err := os.Executable()
	if err != nil {
		return "", err
	}
	destino := filepath.Join(dirInstal, nombreExe)
	if !strings.EqualFold(filepath.Clean(actual), filepath.Clean(destino)) {
		if err := copiar(actual, destino); err != nil {
			return "", fmt.Errorf("copiando a %s: %w", destino, err)
		}
	}
	tr := fmt.Sprintf(`"%s" --silencioso`, destino)
	if out, err := ejecutar("schtasks", "/create", "/f", "/sc", "minute", "/mo", "30", "/tn", nombreTarea, "/tr", tr); err != nil {
		logf("schtasks no disponible (%v: %s); se usara inicio de sesion", err, strings.TrimSpace(out))
		inicio := filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\Startup`, nombreTarea+".lnk.cmd")
		_ = os.WriteFile(inicio, []byte(fmt.Sprintf("@start \"\" \"%s\" --silencioso\r\n", destino)), 0o644)
		return "\n\nSe enviará automáticamente al iniciar sesión.", nil
	}
	return "\n\nQuedó programado: se enviará solo cada 30 minutos.", nil
}

func desinstalar() {
	_, _ = ejecutar("schtasks", "/delete", "/f", "/tn", nombreTarea)
	_ = os.Remove(filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\Startup`, nombreTarea+".lnk.cmd"))
	for _, f := range []string{"agente.log", "estado.json"} {
		_ = os.Remove(filepath.Join(dirInstal, f))
	}
	// Si el .exe que corre es el instalado no puede borrarse a sí mismo: se borra 3 s después de salir.
	if err := os.Remove(filepath.Join(dirInstal, nombreExe)); err != nil {
		cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
		// CmdLine crudo: cmd.exe no entiende el escapado de comillas que hace Go con los argumentos.
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000,
			CmdLine: fmt.Sprintf(`cmd.exe /c "ping -n 4 127.0.0.1 >nul & del /f /q "%s" & rmdir /s /q "%s""`, filepath.Join(dirInstal, nombreExe), dirInstal)}
		_ = cmd.Start()
	}
	_ = os.Remove(dirInstal)
	mensaje("Gestor de Tokens desinstalado de esta máquina.", false)
}

func copiar(origen, destino string) error {
	in, err := os.Open(origen)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := destino + ".nuevo"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	_ = os.Remove(destino)
	return os.Rename(tmp, destino)
}

// ---------- medición ----------

type fila struct {
	Dia           string `json:"dia"`
	Modelo        string `json:"modelo"`
	Input         int64  `json:"input"`
	Output        int64  `json:"output"`
	CacheCreation int64  `json:"cache_creation"`
	CacheRead     int64  `json:"cache_read"`
	Mensajes      int64  `json:"mensajes"`
}

type estado struct {
	HistorialEnviado bool   `json:"historialEnviado"`
	UltimoEnvio      string `json:"ultimoEnvio"`
}

func medir() (string, error) {
	// Una PC puede tener varias carpetas de Claude Code (CLAUDE_CONFIG_DIR), cada una con su cuenta.
	// Solo se suman las carpetas cuya sesión es la cuenta compartida.
	perfiles := perfilesClaude()
	var carpetas, resumen []string
	for _, p := range perfiles {
		coincide := p.cuenta == strings.ToLower(cuentaObjetivo)
		resumen = append(resumen, fmt.Sprintf("%s=%s(%v)", filepath.Base(p.dir), ocultar(p.cuenta), coincide))
		if coincide || flags["dry-run"] {
			carpetas = append(carpetas, filepath.Join(p.dir, "projects"))
		}
	}
	logf("carpetas Claude: %s", strings.Join(resumen, " "))
	if len(carpetas) == 0 {
		msg := "Ninguna carpeta de Claude Code de esta máquina tiene la sesión de la cuenta compartida (" + cuentaObjetivo + ").\n\nCarpetas encontradas:\n"
		for _, p := range perfiles {
			msg += fmt.Sprintf("  %s → %s\n", filepath.Base(p.dir), ocultar(p.cuenta))
		}
		logf("Sin carpetas con la cuenta compartida. No se envia nada.")
		return msg + "\nNo se envió nada.", nil
	}

	est := estado{}
	if b, err := os.ReadFile(filepath.Join(dirInstal, "estado.json")); err == nil {
		_ = json.Unmarshal(b, &est)
	}
	desde := ""
	if !flags["completo"] && est.HistorialEnviado {
		desde = time.Now().AddDate(0, 0, -(diasRecalculo - 1)).Format("2006-01-02")
	}

	host, _ := os.Hostname()
	usuario := os.Getenv("USERNAME")
	sum := sha256.Sum256([]byte(strings.ToLower(machineGUID() + "|" + host + "|" + usuario)))
	id := hex.EncodeToString(sum[:])[:24]

	archivos, filas, sesiones := recolectar(desde, carpetas)
	logf("maquina=%s usuario=%s id=%s archivos=%d filas=%d sesiones=%d desde=%s", host, usuario, id, archivos, len(filas), len(sesiones), valor(desde, "inicio"))

	payload := map[string]any{"machine_id": id, "hostname": host, "usuario": usuario, "version": version, "filas": filas, "sesiones": sesiones}
	if flags["dry-run"] {
		b, _ := json.MarshalIndent(payload, "", "  ")
		salida := filepath.Join(dirInstal, "dry-run.json")
		if err := os.WriteFile(salida, b, 0o644); err != nil {
			return "", err
		}
		fmt.Println(string(b))
		return "Simulación: " + salida, nil
	}

	// Envío por lotes: hasta 2000 filas diarias y 300 sesiones por llamada.
	const loteF, loteS = 2000, 300
	for n := 0; n == 0 || n*loteF < len(filas) || n*loteS < len(sesiones); n++ {
		f0, f1 := min(n*loteF, len(filas)), min((n+1)*loteF, len(filas))
		s0, s1 := min(n*loteS, len(sesiones)), min((n+1)*loteS, len(sesiones))
		parte := map[string]any{"machine_id": id, "hostname": host, "usuario": usuario, "version": version,
			"filas": filas[f0:f1], "sesiones": sesiones[s0:s1]}
		if err := enviar(parte); err != nil {
			return "", err
		}
	}
	est.HistorialEnviado = true
	est.UltimoEnvio = time.Now().Format(time.RFC3339)
	if b, err := json.MarshalIndent(est, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dirInstal, "estado.json"), b, 0o644)
	}
	logf("OK: %d filas enviadas.", len(filas))
	return fmt.Sprintf("Consumo enviado correctamente.\n\nEquipo: %s · %s\nDías/modelos reportados: %d", host, usuario, len(filas)), nil
}

type perfil struct{ dir, cuenta string }

// perfilesClaude busca carpetas de configuración de Claude Code en el perfil del usuario:
// la por defecto (~/.claude, cuenta en ~/.claude.json) y cualquier otra carpeta con
// "projects" y ".claude.json" (las creadas con CLAUDE_CONFIG_DIR, p. ej. ~/.claude-dlw).
func perfilesClaude() []perfil {
	casa, _ := os.UserHomeDir()
	cands := []string{filepath.Join(casa, ".claude")}
	if entradas, err := os.ReadDir(casa); err == nil {
		for _, e := range entradas {
			if e.IsDir() {
				cands = append(cands, filepath.Join(casa, e.Name()))
			}
		}
	}
	cands = append(cands, filepath.Join(casa, ".config", "claude"))
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		cands = append(cands, d)
	}
	vistos, out := map[string]bool{}, []perfil{}
	for _, d := range cands {
		k := strings.ToLower(filepath.Clean(d))
		if vistos[k] {
			continue
		}
		vistos[k] = true
		if st, err := os.Stat(filepath.Join(d, "projects")); err != nil || !st.IsDir() {
			continue
		}
		rutas := []string{filepath.Join(d, ".claude.json")}
		if strings.EqualFold(filepath.Clean(d), filepath.Join(casa, ".claude")) {
			rutas = []string{filepath.Join(casa, ".claude.json"), filepath.Join(d, ".claude.json")}
		} else if _, err := os.Stat(rutas[0]); err != nil {
			continue // no es una carpeta de Claude Code
		}
		out = append(out, perfil{d, cuentaDe(rutas)})
	}
	return out
}

func ocultar(cuenta string) string {
	if cuenta == "" {
		return "sin sesión"
	}
	if i := strings.Index(cuenta, "@"); i >= 0 {
		if strings.EqualFold(cuenta, cuentaObjetivo) {
			return cuenta
		}
		return "***" + cuenta[i:]
	}
	return "***"
}

func cuentaDe(rutas []string) string {
	for _, r := range rutas {
		b, err := os.ReadFile(r)
		if err != nil {
			continue
		}
		var j struct {
			OauthAccount struct {
				EmailAddress string `json:"emailAddress"`
			} `json:"oauthAccount"`
		}
		if json.Unmarshal(b, &j) == nil && j.OauthAccount.EmailAddress != "" {
			return strings.ToLower(j.OauthAccount.EmailAddress)
		}
	}
	return ""
}

type uso struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
}

type registro struct {
	Type        string `json:"type"`
	Timestamp   string `json:"timestamp"`
	RequestID   string `json:"requestId"`
	UUID        string `json:"uuid"`
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	Entrypoint  string `json:"entrypoint"`
	IsSidechain bool   `json:"isSidechain"`
	AiTitle     string `json:"aiTitle"`
	CustomTitle string `json:"customTitle"`
	Message     *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *uso   `json:"usage"`
	} `json:"message"`
}

type mensajeUso struct {
	dia, modelo, sesion string
	sub                 bool
	u                   uso
}

type usoModelo struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheCreation int64 `json:"cache_creation"`
	CacheRead     int64 `json:"cache_read"`
}

// sesion resume una conversación de Claude Code. No incluye prompts ni respuestas:
// solo el título que Claude Code le asigna, la carpeta, el horario y los tokens.
type sesion struct {
	ID          string                `json:"id"`
	Inicio      string                `json:"inicio,omitempty"`
	Fin         string                `json:"fin,omitempty"`
	Titulo      string                `json:"titulo,omitempty"`
	Carpeta     string                `json:"carpeta,omitempty"`
	Entrypoint  string                `json:"entrypoint,omitempty"`
	PorModelo   map[string]*usoModelo `json:"por_modelo"`
	Mensajes    int                   `json:"mensajes"`
	MensajesSub int                   `json:"mensajes_subagente"`

	ini, fin                       time.Time
	tituloAI, tituloPropio, cwdSub string
}

// claveSesion: <proyecto>/<id>.jsonl o <proyecto>/<id>/subagents/<agente>.jsonl
func claveSesion(p string) string {
	dir := filepath.Dir(p)
	if strings.EqualFold(filepath.Base(dir), "subagents") {
		return filepath.Base(filepath.Dir(dir))
	}
	return strings.TrimSuffix(filepath.Base(p), ".jsonl")
}

// carpetaRelativa quita el perfil del usuario de la ruta (no se envía el nombre de usuario).
func carpetaRelativa(cwd string) string {
	if cwd == "" {
		return ""
	}
	casa, _ := os.UserHomeDir()
	if rel, err := filepath.Rel(casa, cwd); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return "~"
		}
		return `~\` + rel
	}
	return cwd
}

func recolectar(desde string, carpetas []string) (int, []fila, []sesion) {
	var desdeT time.Time
	if desde != "" {
		desdeT, _ = time.ParseInLocation("2006-01-02", desde, time.Local)
	}

	// 1) Agrupar archivos por sesión: se relee la sesión completa si alguno de sus archivos cambió.
	grupos := map[string][]string{}
	recientes := map[string]bool{}
	for _, raiz := range carpetas {
		_ = filepath.WalkDir(raiz, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			k := claveSesion(p)
			grupos[k] = append(grupos[k], p)
			if info, err := d.Info(); err == nil && (desdeT.IsZero() || !info.ModTime().Before(desdeT)) {
				recientes[k] = true
			}
			return nil
		})
	}

	archivos := 0
	msgs := map[string]*mensajeUso{}
	sesiones := map[string]*sesion{}
	ses := func(id string) *sesion {
		s, ok := sesiones[id]
		if !ok {
			s = &sesion{ID: id, PorModelo: map[string]*usoModelo{}}
			sesiones[id] = s
		}
		return s
	}
	for k, rutas := range grupos {
		if !recientes[k] {
			continue
		}
		sort.Strings(rutas) // el archivo principal antes que los de subagentes
		for _, p := range rutas {
			datos, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			archivos++
			esSub := strings.Contains(strings.ToLower(p), `\subagents\`) || strings.Contains(p, "/subagents/")
			for _, linea := range bytes.Split(datos, []byte("\n")) {
				esTitulo := bytes.Contains(linea, []byte(`"ai-title"`)) || bytes.Contains(linea, []byte(`"custom-title"`))
				if !esTitulo && !bytes.Contains(linea, []byte(`"usage"`)) {
					continue
				}
				var r registro
				if json.Unmarshal(linea, &r) != nil {
					continue
				}
				idSes := valor(r.SessionID, k)
				switch {
				case r.Type == "ai-title" && r.AiTitle != "":
					ses(idSes).tituloAI = r.AiTitle // el último es el más actual
					continue
				case r.Type == "custom-title" && r.CustomTitle != "":
					ses(idSes).tituloPropio = r.CustomTitle
					continue
				}
				if r.Type != "assistant" || r.Message == nil || r.Message.Usage == nil || r.Message.Model == "" || r.Message.Model == "<synthetic>" {
					continue
				}
				ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
				if err != nil {
					continue
				}
				s := ses(idSes)
				if s.ini.IsZero() || ts.Before(s.ini) {
					s.ini = ts
				}
				if ts.After(s.fin) {
					s.fin = ts
				}
				sub := esSub || r.IsSidechain
				if r.Cwd != "" {
					if !sub && s.Carpeta == "" {
						s.Carpeta = carpetaRelativa(r.Cwd)
					} else if s.cwdSub == "" {
						s.cwdSub = carpetaRelativa(r.Cwd)
					}
				}
				if s.Entrypoint == "" {
					s.Entrypoint = r.Entrypoint
				}
				clave := r.UUID
				if r.Message.ID != "" || r.RequestID != "" {
					clave = r.Message.ID + ":" + r.RequestID
				}
				u := *r.Message.Usage
				if prev, ok := msgs[clave]; ok {
					// Un mensaje aparece en varias líneas: se toma el máximo de cada contador.
					prev.u.Input = max(prev.u.Input, u.Input)
					prev.u.Output = max(prev.u.Output, u.Output)
					prev.u.CacheCreation = max(prev.u.CacheCreation, u.CacheCreation)
					prev.u.CacheRead = max(prev.u.CacheRead, u.CacheRead)
				} else {
					msgs[clave] = &mensajeUso{ts.Local().Format("2006-01-02"), r.Message.Model, idSes, sub, u}
				}
			}
		}
	}

	// 2) Totales diarios (solo días de la ventana) y por sesión (sesión completa).
	agg := map[string]*fila{}
	for _, m := range msgs {
		s := ses(m.sesion)
		um, ok := s.PorModelo[m.modelo]
		if !ok {
			um = &usoModelo{}
			s.PorModelo[m.modelo] = um
		}
		um.Input += m.u.Input
		um.Output += m.u.Output
		um.CacheCreation += m.u.CacheCreation
		um.CacheRead += m.u.CacheRead
		s.Mensajes++
		if m.sub {
			s.MensajesSub++
		}
		if desde != "" && m.dia < desde {
			continue
		}
		kf := m.dia + "|" + m.modelo
		f, ok := agg[kf]
		if !ok {
			f = &fila{Dia: m.dia, Modelo: m.modelo}
			agg[kf] = f
		}
		f.Input += m.u.Input
		f.Output += m.u.Output
		f.CacheCreation += m.u.CacheCreation
		f.CacheRead += m.u.CacheRead
		f.Mensajes++
	}
	out := make([]fila, 0, len(agg))
	for _, f := range agg {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dia != out[j].Dia {
			return out[i].Dia < out[j].Dia
		}
		return out[i].Modelo < out[j].Modelo
	})

	lista := make([]sesion, 0, len(sesiones))
	for _, s := range sesiones {
		if s.Mensajes == 0 {
			continue
		}
		s.Titulo = valor(s.tituloPropio, s.tituloAI)
		s.Carpeta = valor(s.Carpeta, s.cwdSub)
		s.Inicio = s.ini.Format(time.RFC3339)
		s.Fin = s.fin.Format(time.RFC3339)
		lista = append(lista, *s)
	}
	sort.Slice(lista, func(i, j int) bool { return lista[i].Inicio < lista[j].Inicio })
	return archivos, out, lista
}

func enviar(payload map[string]any) error {
	cuerpo, _ := json.Marshal(map[string]any{"p": payload})
	req, err := http.NewRequest("POST", strings.TrimRight(supabaseURL, "/")+"/rest/v1/rpc/registrar_consumo", bytes.NewReader(cuerpo))
	if err != nil {
		return err
	}
	req.Header.Set("apikey", supabaseKey)
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("sin conexión con el servidor: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// ---------- utilidades Windows ----------

var reGUID = regexp.MustCompile(`(?i)MachineGuid\s+REG_SZ\s+(\S+)`)

func machineGUID() string {
	out, err := ejecutar("reg", "query", `HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid")
	if err != nil {
		return ""
	}
	if m := reGUID.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

func ejecutar(nombre string, args ...string) (string, error) {
	cmd := exec.Command(nombre, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mensaje(texto string, esError bool) {
	if flags["sin-mensajes"] {
		fmt.Println(texto)
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	box := user32.NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(texto)
	c, _ := syscall.UTF16PtrFromString(tituloVentana)
	icono := uintptr(0x40) // MB_ICONINFORMATION
	if esError {
		icono = 0x10 // MB_ICONERROR
	}
	_, _, _ = box.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), icono)
}

func logf(formato string, args ...any) {
	linea := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(formato, args...))
	if st, err := os.Stat(archivoLog); err == nil && st.Size() > 1<<20 {
		_ = os.Truncate(archivoLog, 0)
	}
	f, err := os.OpenFile(archivoLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(linea)
}

func valor(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
