// Ventana nativa de Windows (Win32, sin dependencias) para que la persona se identifique
// con su nombre completo y su correo @dlwlatam.com la primera vez que ejecuta el agente.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// Una ventana Win32 solo recibe mensajes en el hilo del sistema que la creó. Sin esto, el
// planificador de Go puede mover la goroutine a otro hilo y la ventana deja de responder
// (se "cuelga" mientras se escribe). Fijar el hilo en init garantiza que main corre siempre en el mismo.
func init() { runtime.LockOSThread() }

type identidad struct {
	Nombre string `json:"nombre"`
	Correo string `json:"correo"`
}

var reCorreoDLW = regexp.MustCompile(`(?i)^[a-z0-9._%+-]+@dlwlatam\.com$`)

func archivoIdentidad() string { return filepath.Join(dirInstal, "identidad.json") }

func leerIdentidad() *identidad {
	b, err := os.ReadFile(archivoIdentidad())
	if err != nil {
		return nil
	}
	var id identidad
	if json.Unmarshal(b, &id) != nil || !reCorreoDLW.MatchString(id.Correo) {
		return nil
	}
	return &id
}

func guardarIdentidad(id identidad) error {
	b, _ := json.MarshalIndent(id, "", "  ")
	return os.WriteFile(archivoIdentidad(), b, 0o644)
}

// validarIdentidad devuelve un mensaje de error para mostrar o "" si los datos son válidos.
func validarIdentidad(nombre, correo string) string {
	partes := strings.Fields(nombre)
	switch {
	case len(partes) < 2:
		return "Escribe tu nombre completo (nombre y apellido)."
	case len([]rune(nombre)) > 120:
		return "El nombre es demasiado largo."
	case correo == "":
		return "Escribe tu correo de DLW."
	case !reCorreoDLW.MatchString(correo):
		return "El correo debe terminar en @dlwlatam.com."
	}
	return ""
}

// ---------- Win32 ----------

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassEx   = user32.NewProc("RegisterClassExW")
	pCreateWindowEx    = user32.NewProc("CreateWindowExW")
	pDefWindowProc     = user32.NewProc("DefWindowProcW")
	pGetMessage        = user32.NewProc("GetMessageW")
	pTranslateMessage  = user32.NewProc("TranslateMessage")
	pDispatchMessage   = user32.NewProc("DispatchMessageW")
	pIsDialogMessage   = user32.NewProc("IsDialogMessageW")
	pPostQuitMessage   = user32.NewProc("PostQuitMessage")
	pDestroyWindow     = user32.NewProc("DestroyWindow")
	pSendMessage       = user32.NewProc("SendMessageW")
	pGetWindowText     = user32.NewProc("GetWindowTextW")
	pGetWindowTextLen  = user32.NewProc("GetWindowTextLengthW")
	pSetWindowText     = user32.NewProc("SetWindowTextW")
	pShowWindow        = user32.NewProc("ShowWindow")
	pSetFocus          = user32.NewProc("SetFocus")
	pGetSystemMetrics  = user32.NewProc("GetSystemMetrics")
	pLoadCursor        = user32.NewProc("LoadCursorW")
	pLoadIcon          = user32.NewProc("LoadIconW")
	pSetProcessDPIAwre = user32.NewProc("SetProcessDPIAware")
	pGetDC             = user32.NewProc("GetDC")
	pReleaseDC         = user32.NewProc("ReleaseDC")
	pSetForeground     = user32.NewProc("SetForegroundWindow")
	pInvalidateRect    = user32.NewProc("InvalidateRect")
	pCreateFont        = gdi32.NewProc("CreateFontW")
	pCreateSolidBrush  = gdi32.NewProc("CreateSolidBrush")
	pSetTextColor      = gdi32.NewProc("SetTextColor")
	pSetBkColor        = gdi32.NewProc("SetBkColor")
	pGetDeviceCaps     = gdi32.NewProc("GetDeviceCaps")
	pGetModuleHandle   = kernel32.NewProc("GetModuleHandleW")
)

const (
	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsBorder       = 0x00800000
	esAutoHScroll  = 0x0080
	bsDefPush      = 0x0001
	wsExClientEdge = 0x00000200
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmCommand      = 0x0111
	wmSetFont      = 0x0030
	wmCtlColorStat = 0x0138
	wmCtlColorEdit = 0x0133
	emSetCueBanner = 0x1501
	swShow         = 5
	idOK           = 1
	idCancel       = 2
	idError        = 100
	idTitulo       = 101
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type msgW struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func textoDe(h uintptr) string {
	n, _, _ := pGetWindowTextLen.Call(h)
	buf := make([]uint16, n+1)
	pGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return strings.TrimSpace(syscall.UTF16ToString(buf))
}

// pedirIdentidad muestra la ventana y devuelve los datos, o nil si la persona cancela.
func pedirIdentidad(previa *identidad) *identidad {
	pSetProcessDPIAwre.Call()
	hdc, _, _ := pGetDC.Call(0)
	dpi, _, _ := pGetDeviceCaps.Call(hdc, 88) // LOGPIXELSX
	pReleaseDC.Call(0, hdc)
	if dpi == 0 {
		dpi = 96
	}
	px := func(v int) uintptr { return uintptr(v * int(dpi) / 96) }

	hInst, _, _ := pGetModuleHandle.Call(0)
	fuente := func(alto, peso int) uintptr {
		f, _, _ := pCreateFont.Call(uintptr(-int(px(alto))), 0, 0, 0, uintptr(peso), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))
		return f
	}
	fNormal, fTitulo, fChica := fuente(15, 400), fuente(20, 700), fuente(13, 400)
	fondo, _, _ := pCreateSolidBrush.Call(0x00FFFFFF)

	var hNombre, hCorreo, hError uintptr
	var resultado *identidad

	wndProc := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case wmCtlColorStat:
			hdcCtl := wp
			pSetBkColor.Call(hdcCtl, 0x00FFFFFF)
			if lp == hError {
				pSetTextColor.Call(hdcCtl, 0x003B3BD0) // rojo (BGR)
			} else {
				pSetTextColor.Call(hdcCtl, 0x00542C1E) // azul marino DLW (BGR de #1e2c54)
			}
			return fondo
		case wmCommand:
			switch wp & 0xFFFF {
			case idOK:
				nombre, correo := textoDe(hNombre), strings.ToLower(textoDe(hCorreo))
				if msj := validarIdentidad(nombre, correo); msj != "" {
					pSetWindowText.Call(hError, uintptr(unsafe.Pointer(u16(msj))))
					pInvalidateRect.Call(hError, 0, 1)
					if strings.Contains(msj, "correo") {
						pSetFocus.Call(hCorreo)
					} else {
						pSetFocus.Call(hNombre)
					}
					return 0
				}
				resultado = &identidad{Nombre: strings.Join(strings.Fields(nombre), " "), Correo: correo}
				pDestroyWindow.Call(hwnd)
				return 0
			case idCancel:
				pDestroyWindow.Call(hwnd)
				return 0
			}
		case wmClose:
			pDestroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			pPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wp, lp)
		return r
	})

	icono, _, _ := pLoadIcon.Call(hInst, 1) // ícono DLW embebido (recurso 1)
	cursor, _, _ := pLoadCursor.Call(0, 32512)
	clase := u16("GestorTokensIdentidad")
	wc := wndClassEx{lpfnWndProc: wndProc, hInstance: hInst, hIcon: icono, hIconSm: icono, hCursor: cursor, hbrBackground: fondo, lpszClassName: clase}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	ancho, alto := px(480), px(372)
	sx, _, _ := pGetSystemMetrics.Call(0)
	sy, _, _ := pGetSystemMetrics.Call(1)
	hwnd, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(clase)), uintptr(unsafe.Pointer(u16("Gestor de Tokens · DLW"))),
		wsOverlapped|wsCaption|wsSysMenu|wsMinimizeBox, (sx-ancho)/2, (sy-alto)/2, ancho, alto, 0, 0, hInst, 0)

	control := func(clase, texto string, estilo uintptr, exEstilo uintptr, x, y, w, h int, id uintptr, f uintptr) uintptr {
		c, _, _ := pCreateWindowEx.Call(exEstilo, uintptr(unsafe.Pointer(u16(clase))), uintptr(unsafe.Pointer(u16(texto))),
			wsChild|wsVisible|estilo, px(x), px(y), px(w), px(h), hwnd, id, hInst, 0)
		pSendMessage.Call(c, wmSetFont, f, 1)
		return c
	}
	control("STATIC", "Identifícate para registrar tu consumo", 0, 0, 24, 18, 420, 28, idTitulo, fTitulo)
	control("STATIC", "Gestor de Tokens de Claude Code · DLW Latam. Solo se registran totales de tokens; nunca prompts ni código.", 0, 0, 24, 50, 420, 36, 0, fChica)
	control("STATIC", "Nombre completo", 0, 0, 24, 96, 420, 20, 0, fNormal)
	hNombre = control("EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge, 24, 118, 420, 30, 10, fNormal)
	control("STATIC", "Correo de DLW (debe terminar en @dlwlatam.com)", 0, 0, 24, 160, 420, 20, 0, fNormal)
	hCorreo = control("EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge, 24, 182, 420, 30, 11, fNormal)
	hError = control("STATIC", "", 0, 0, 24, 220, 420, 22, idError, fChica)
	control("BUTTON", "Continuar", wsTabStop|bsDefPush, 0, 236, 258, 100, 34, idOK, fNormal)
	control("BUTTON", "Cancelar", wsTabStop, 0, 344, 258, 100, 34, idCancel, fNormal)

	pSendMessage.Call(hNombre, emSetCueBanner, 1, uintptr(unsafe.Pointer(u16("Ej.: Ana Ruiz Pérez"))))
	pSendMessage.Call(hCorreo, emSetCueBanner, 1, uintptr(unsafe.Pointer(u16("nombre.apellido@dlwlatam.com"))))
	if previa != nil {
		pSetWindowText.Call(hNombre, uintptr(unsafe.Pointer(u16(previa.Nombre))))
		pSetWindowText.Call(hCorreo, uintptr(unsafe.Pointer(u16(previa.Correo))))
	}

	pShowWindow.Call(hwnd, swShow)
	pSetForeground.Call(hwnd)
	pSetFocus.Call(hNombre)

	var m msgW
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		// IsDialogMessage da Tab entre campos y Enter = Continuar.
		if ok, _, _ := pIsDialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&m))); ok != 0 {
			continue
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	return resultado
}
