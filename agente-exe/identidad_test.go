package main

import "testing"

func TestValidarIdentidad(t *testing.T) {
	casos := []struct {
		nombre, correo string
		valido         bool
	}{
		{"Ana Ruiz", "ana.ruiz@dlwlatam.com", true},
		{"Ana Ruiz Pérez", "ANA.RUIZ@DLWLATAM.COM", true},
		{"Ana", "ana@dlwlatam.com", false},                  // falta apellido
		{"Ana Ruiz", "ana.ruiz@gmail.com", false},           // otro dominio
		{"Ana Ruiz", "ana.ruiz@dlwlatam.com.pe", false},     // dominio parecido
		{"Ana Ruiz", "ana.ruiz@fake-dlwlatam.com", false},   // dominio parecido
		{"Ana Ruiz", "ana ruiz@dlwlatam.com", false},        // espacio
		{"Ana Ruiz", "", false},
	}
	for _, c := range casos {
		msj := validarIdentidad(c.nombre, c.correo)
		if (msj == "") != c.valido {
			t.Errorf("%q / %q: esperado válido=%v, mensaje=%q", c.nombre, c.correo, c.valido, msj)
		}
	}
}
