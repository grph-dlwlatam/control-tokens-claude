// Genera el ícono del agente (PNG 256x256): cuadrado azul DLW con el logo blanco centrado.
// Uso: go run ./tools/icono <logo-blanco.png> <salida.png> [salida.ico]
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	logo, err := png.Decode(f)
	f.Close()
	if err != nil {
		panic(err)
	}

	const n = 256
	navy := color.NRGBA{0x1e, 0x2c, 0x54, 0xff}
	out := image.NewNRGBA(image.Rect(0, 0, n, n))

	// Fondo: cuadrado redondeado con borde suavizado.
	radio := 48.0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			a := cobertura(float64(x)+.5, float64(y)+.5, n, radio)
			if a > 0 {
				c := navy
				c.A = uint8(a * 255)
				out.SetNRGBA(x, y, c)
			}
		}
	}

	// Logo escalado al 80% del ancho, centrado (promedio por área).
	lb := logo.Bounds()
	ancho := 0.80 * n
	esc := ancho / float64(lb.Dx())
	alto := float64(lb.Dy()) * esc
	x0, y0 := (n-ancho)/2, (n-alto)/2
	for y := int(y0); y < int(math.Ceil(y0+alto)); y++ {
		for x := int(x0); x < int(math.Ceil(x0+ancho)); x++ {
			sx0, sy0 := (float64(x)-x0)/esc, (float64(y)-y0)/esc
			sx1, sy1 := sx0+1/esc, sy0+1/esc
			var r, g, b, a, cnt float64
			for sy := int(sy0); sy < int(math.Ceil(sy1)) && sy < lb.Dy(); sy++ {
				for sx := int(sx0); sx < int(math.Ceil(sx1)) && sx < lb.Dx(); sx++ {
					if sx < 0 || sy < 0 {
						continue
					}
					c := color.NRGBAModel.Convert(logo.At(lb.Min.X+sx, lb.Min.Y+sy)).(color.NRGBA)
					fa := float64(c.A) / 255
					r += float64(c.R) * fa
					g += float64(c.G) * fa
					b += float64(c.B) * fa
					a += fa
					cnt++
				}
			}
			if cnt == 0 || a == 0 {
				continue
			}
			alpha := a / cnt
			lr, lg, lbv := r/a, g/a, b/a
			fondo := out.NRGBAAt(x, y)
			mezcla := func(l float64, f uint8) uint8 { return uint8(l*alpha + float64(f)*(1-alpha)) }
			out.SetNRGBA(x, y, color.NRGBA{mezcla(lr, fondo.R), mezcla(lg, fondo.G), mezcla(lbv, fondo.B), fondo.A})
		}
	}

	o, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer o.Close()
	if err := png.Encode(o, out); err != nil {
		panic(err)
	}
	if len(os.Args) > 3 {
		if err := escribirICO(os.Args[3], out, []int{256, 64, 48, 32, 24, 16}); err != nil {
			panic(err)
		}
	}
}

// cobertura aproximada (0..1) de un píxel dentro de un cuadrado redondeado de lado n.
func cobertura(px, py float64, n int, r float64) float64 {
	cx := math.Max(r, math.Min(float64(n)-r, px))
	cy := math.Max(r, math.Min(float64(n)-r, py))
	d := math.Hypot(px-cx, py-cy)
	return math.Max(0, math.Min(1, r-d+.5))
}
