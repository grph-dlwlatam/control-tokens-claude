// Convierte un .res (salida de rc.exe) en un .syso COFF que el enlazador de Go acepta.
// Solo admite tipos y nombres numéricos (suficiente para ícono y versión).
// Uso: go run ./tools/syso recursos.res rsrc_windows_amd64.syso
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

type recurso struct {
	tipo, nombre uint16
	idioma       uint16
	datos        []byte
}

func leerRes(b []byte) ([]recurso, error) {
	var out []recurso
	le := binary.LittleEndian
	for off := 0; off+8 <= len(b); {
		dataSize := int(le.Uint32(b[off:]))
		headerSize := int(le.Uint32(b[off+4:]))
		h := b[off+8 : off+headerSize]
		leerID := func(p []byte) (uint16, int, error) {
			if le.Uint16(p) == 0xFFFF {
				return le.Uint16(p[2:]), 4, nil
			}
			return 0, 0, fmt.Errorf("nombre de recurso no numérico no soportado")
		}
		tipo, n1, err := leerID(h)
		if err != nil {
			return nil, err
		}
		nombre, n2, err := leerID(h[n1:])
		if err != nil {
			return nil, err
		}
		p := (n1 + n2 + 3) &^ 3
		idioma := le.Uint16(h[p+6:]) // DataVersion(4) MemoryFlags(2) LanguageId(2)
		inicio := off + headerSize
		if tipo != 0 || dataSize != 0 {
			out = append(out, recurso{tipo, nombre, idioma, b[inicio : inicio+dataSize]})
		}
		off = (inicio + dataSize + 3) &^ 3
	}
	return out, nil
}

func main() {
	res, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	recs, err := leerRes(res)
	if err != nil {
		panic(err)
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].tipo != recs[j].tipo {
			return recs[i].tipo < recs[j].tipo
		}
		return recs[i].nombre < recs[j].nombre
	})

	// Agrupar tipo -> nombres.
	tipos := []uint16{}
	porTipo := map[uint16][]recurso{}
	for _, r := range recs {
		if _, ok := porTipo[r.tipo]; !ok {
			tipos = append(tipos, r.tipo)
		}
		porTipo[r.tipo] = append(porTipo[r.tipo], r)
	}

	// Calcular tamaños del árbol: raíz, un directorio por tipo, uno por nombre, entradas de datos.
	const dirHdr, dirEnt, dataEnt = 16, 8, 16
	tam := dirHdr + dirEnt*len(tipos)
	offTipo := map[uint16]int{}
	for _, t := range tipos {
		offTipo[t] = tam
		tam += dirHdr + dirEnt*len(porTipo[t])
	}
	offNombre := make([]int, len(recs))
	for i := range recs {
		offNombre[i] = tam
		tam += dirHdr + dirEnt
	}
	offData := make([]int, len(recs))
	for i := range recs {
		offData[i] = tam
		tam += dataEnt
	}
	offBlob := make([]int, len(recs))
	for i, r := range recs {
		tam = (tam + 7) &^ 7
		offBlob[i] = tam
		tam += len(r.datos)
	}

	sec := make([]byte, (tam+7)&^7)
	le := binary.LittleEndian
	dir := func(off, ids int) {
		le.PutUint16(sec[off+14:], uint16(ids)) // NumberOfIdEntries
	}
	ent := func(off int, id uint16, destino int, sub bool) {
		le.PutUint32(sec[off:], uint32(id))
		v := uint32(destino)
		if sub {
			v |= 0x80000000
		}
		le.PutUint32(sec[off+4:], v)
	}
	dir(0, len(tipos))
	idx := 0
	for ti, t := range tipos {
		ent(dirHdr+dirEnt*ti, t, offTipo[t], true)
		dir(offTipo[t], len(porTipo[t]))
		for ni := range porTipo[t] {
			r := recs[idx]
			ent(offTipo[t]+dirHdr+dirEnt*ni, r.nombre, offNombre[idx], true)
			dir(offNombre[idx], 1)
			ent(offNombre[idx]+dirHdr, r.idioma, offData[idx], false)
			idx++
		}
	}
	var relocs []uint32
	for i, r := range recs {
		le.PutUint32(sec[offData[i]:], uint32(offBlob[i])) // RVA relativa a la sección (se reubica)
		le.PutUint32(sec[offData[i]+4:], uint32(len(r.datos)))
		relocs = append(relocs, uint32(offData[i]))
		copy(sec[offBlob[i]:], r.datos)
	}

	// COFF: cabecera + 1 sección + datos + reubicaciones + tabla de símbolos + tabla de cadenas.
	const fileHdr, secHdr = 20, 40
	pRaw := fileHdr + secHdr
	pRel := pRaw + len(sec)
	pSym := pRel + 10*len(relocs)
	var o bytes.Buffer
	w := func(v any) { _ = binary.Write(&o, le, v) }
	w(uint16(0x8664))
	w(uint16(1))
	w(uint32(0))
	w(uint32(pSym))
	w(uint32(1))
	w(uint16(0))
	w(uint16(0))
	o.Write([]byte(".rsrc\x00\x00\x00"))
	w(uint32(0))
	w(uint32(0))
	w(uint32(len(sec)))
	w(uint32(pRaw))
	w(uint32(pRel))
	w(uint32(0))
	w(uint16(len(relocs)))
	w(uint16(0))
	w(uint32(0x40000040)) // INITIALIZED_DATA | MEM_READ
	o.Write(sec)
	for _, r := range relocs {
		w(r)
		w(uint32(0)) // símbolo 0: la propia sección
		w(uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	o.Write([]byte(".rsrc\x00\x00\x00"))
	w(uint32(0))
	w(int16(1))
	w(uint16(0))
	o.WriteByte(3) // IMAGE_SYM_CLASS_STATIC
	o.WriteByte(0)
	w(uint32(4)) // tabla de cadenas vacía
	if err := os.WriteFile(os.Args[2], o.Bytes(), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("%d recursos -> %s\n", len(recs), os.Args[2])
}
