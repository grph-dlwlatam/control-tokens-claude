package main

// Lectura automática de la cuota del plan: el mismo dato que muestra /usage en Claude Code.
//
// Usa la credencial que Claude Code ya guarda en la carpeta del perfil (.credentials.json) SOLO para
// esa consulta. La credencial no se envía a Supabase, no se guarda en otro lugar ni se renueva: si
// está vencida, la lectura se omite y se toma en la siguiente ejecución. A Supabase viajan únicamente
// porcentajes y fechas de reinicio.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const urlUso = "https://api.anthropic.com/api/oauth/usage"

type ventanaUso struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type respuestaUso struct {
	FiveHour       *ventanaUso `json:"five_hour"`
	SevenDay       *ventanaUso `json:"seven_day"`
	SevenDayOpus   *ventanaUso `json:"seven_day_opus"`
	SevenDaySonnet *ventanaUso `json:"seven_day_sonnet"`
	Limits         []struct {
		Kind     string  `json:"kind"`
		Percent  float64 `json:"percent"`
		ResetsAt string  `json:"resets_at"`
		IsActive bool    `json:"is_active"`
		Scope    *struct {
			Model *struct {
				DisplayName string `json:"display_name"`
			} `json:"model"`
		} `json:"scope"`
	} `json:"limits"`
	Breakdown *struct {
		Rows []struct {
			Key            string  `json:"key"`
			DisplayName    string  `json:"display_name"`
			PercentOfLimit float64 `json:"percent_of_limit"`
		} `json:"rows"`
	} `json:"seven_day_breakdown"`
}

var errSinCredencial = errors.New("sin credencial vigente")

// credencialesDe devuelve el token de acceso de un perfil de Claude Code si sigue vigente.
func credencialesDe(dirPerfil string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dirPerfil, ".credentials.json"))
	if err != nil {
		return "", errSinCredencial
	}
	var c struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(b, &c) != nil || c.ClaudeAiOauth.AccessToken == "" {
		return "", errSinCredencial
	}
	// Margen de 2 minutos; si venció, Claude Code la renovará la próxima vez que se use.
	if c.ClaudeAiOauth.ExpiresAt > 0 && time.UnixMilli(c.ClaudeAiOauth.ExpiresAt).Before(time.Now().Add(2*time.Minute)) {
		return "", errSinCredencial
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

// leerCuota consulta el uso del plan para el perfil indicado y arma el resumen que se envía.
func leerCuota(dirPerfil string) (map[string]any, error) {
	token, err := credencialesDe(dirPerfil)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("GET", urlUso, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GestorTokens/"+version)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("sin conexión con el servicio de uso: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("servicio de uso HTTP %d", resp.StatusCode)
	}
	var u respuestaUso
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil {
		return nil, fmt.Errorf("respuesta de uso no reconocida: %w", err)
	}
	if u.SevenDay == nil || u.SevenDay.Utilization == nil {
		return nil, errors.New("la respuesta no trae el límite semanal")
	}

	out := map[string]any{
		"semanal_pct":      *u.SevenDay.Utilization,
		"semanal_reinicio": u.SevenDay.ResetsAt,
	}
	if u.FiveHour != nil && u.FiveHour.Utilization != nil {
		out["sesion_pct"] = *u.FiveHour.Utilization
		out["sesion_reinicio"] = u.FiveHour.ResetsAt
	}
	detalle := map[string]any{}
	var limites []map[string]any
	for _, l := range u.Limits {
		x := map[string]any{"tipo": l.Kind, "pct": l.Percent, "reinicio": l.ResetsAt, "activo": l.IsActive}
		if l.Scope != nil && l.Scope.Model != nil && l.Scope.Model.DisplayName != "" {
			x["modelo"] = l.Scope.Model.DisplayName
		}
		limites = append(limites, x)
	}
	for nombre, v := range map[string]*ventanaUso{"Opus": u.SevenDayOpus, "Sonnet": u.SevenDaySonnet} {
		if v != nil && v.Utilization != nil {
			limites = append(limites, map[string]any{"tipo": "weekly_scoped", "modelo": nombre, "pct": *v.Utilization, "reinicio": v.ResetsAt, "activo": true})
		}
	}
	if len(limites) > 0 {
		detalle["limites"] = limites
	}
	if u.Breakdown != nil {
		desglose := map[string]float64{}
		for _, r := range u.Breakdown.Rows {
			if r.Key != "" {
				desglose[strings.ToLower(r.Key)] = r.PercentOfLimit
			}
		}
		detalle["desglose"] = desglose
	}
	out["detalle"] = detalle
	return out, nil
}
