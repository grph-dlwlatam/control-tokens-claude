package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
)

// escalar reduce una imagen cuadrada por promedio de área (premultiplicado).
func escalar(src *image.NRGBA, n int) *image.NRGBA {
	s := src.Bounds().Dx()
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	f := float64(s) / float64(n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a, c float64
			for sy := int(float64(y) * f); sy < int(float64(y+1)*f); sy++ {
				for sx := int(float64(x) * f); sx < int(float64(x+1)*f); sx++ {
					p := src.NRGBAAt(sx, sy)
					fa := float64(p.A) / 255
					r += float64(p.R) * fa
					g += float64(p.G) * fa
					b += float64(p.B) * fa
					a += fa
					c++
				}
			}
			if a > 0 {
				dst.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a / c * 255)})
			}
		}
	}
	return dst
}

// escribirICO guarda un .ico con entradas PNG (Windows Vista+).
func escribirICO(ruta string, img *image.NRGBA, tamanos []int) error {
	var datos [][]byte
	for _, t := range tamanos {
		var buf bytes.Buffer
		m := img
		if t != img.Bounds().Dx() {
			m = escalar(img, t)
		}
		if err := png.Encode(&buf, m); err != nil {
			return err
		}
		datos = append(datos, buf.Bytes())
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, []uint16{0, 1, uint16(len(tamanos))})
	off := 6 + 16*len(tamanos)
	for i, t := range tamanos {
		d := byte(t)
		if t >= 256 {
			d = 0
		}
		out.Write([]byte{d, d, 0, 0})
		binary.Write(&out, binary.LittleEndian, []uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, []uint32{uint32(len(datos[i])), uint32(off)})
		off += len(datos[i])
	}
	for _, d := range datos {
		out.Write(d)
	}
	return os.WriteFile(ruta, out.Bytes(), 0o644)
}
