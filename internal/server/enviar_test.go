package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jfrmorales/deckman/internal/detect"
)

func TestApplyPreferredExe(t *testing.T) {
	dir := t.TempDir()
	winExe := filepath.Join(dir, "Juego.exe")
	linuxExe := filepath.Join(dir, "juego.x86_64")
	script := filepath.Join(dir, "bin", "lanzar.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(winExe, []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linuxExe, []byte("\x7fELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("promociona candidato existente al primer puesto", func(t *testing.T) {
		res := &detect.Result{
			Executables: []detect.Candidate{
				{Rel: "Juego.exe", Native: false},
				{Rel: "juego.x86_64", Native: true},
			},
		}
		got := applyPreferredExe(res, dir, "juego.x86_64")
		if got != "juego.x86_64" {
			t.Fatalf("got = %q, esperaba juego.x86_64", got)
		}
		if len(res.Executables) != 2 || res.Executables[0].Rel != "juego.x86_64" || res.Executables[1].Rel != "Juego.exe" {
			t.Fatalf("orden inesperado: %+v", res.Executables)
		}
	})

	t.Run("añade ejecutable manual fuera de los candidatos", func(t *testing.T) {
		res := &detect.Result{
			Executables: []detect.Candidate{{Rel: "Juego.exe", Native: false}},
		}
		got := applyPreferredExe(res, dir, "bin/lanzar.sh")
		if got != "bin/lanzar.sh" {
			t.Fatalf("got = %q, esperaba bin/lanzar.sh", got)
		}
		if len(res.Executables) != 2 || res.Executables[0].Rel != "bin/lanzar.sh" || !res.Executables[0].Native {
			t.Fatalf("esperaba bin/lanzar.sh nativo primero: %+v", res.Executables)
		}
	})

	t.Run("ignora ejecutable borrado o ruta insegura", func(t *testing.T) {
		res := &detect.Result{
			Executables: []detect.Candidate{{Rel: "Juego.exe", Native: false}},
		}
		if got := applyPreferredExe(res, dir, "no-existe.x86_64"); got != "" {
			t.Fatalf("esperaba vacio para fichero inexistente, obtuve %q", got)
		}
		if got := applyPreferredExe(res, dir, "../fuera.sh"); got != "" {
			t.Fatalf("esperaba vacio para ruta con .., obtuve %q", got)
		}
		if res.Executables[0].Rel != "Juego.exe" {
			t.Fatalf("no deberia cambiar los candidatos: %+v", res.Executables)
		}
	})
}

func TestHandleDetectRecuerdaEjecutablePreferido(t *testing.T) {
	srv := nuevoServidorDePrueba(t)
	dir := filepath.Join(t.TempDir(), "MiJuego")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MiJuego.exe"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	elfCab := make([]byte, 1024)
	copy(elfCab[:4], "\x7fELF")
	elfCab[5] = 1   // little-endian
	elfCab[16] = 2  // ET_EXEC
	elfCab[18] = 62 // EM_X86_64
	if err := os.WriteFile(filepath.Join(dir, "MiJuego.x86_64"), elfCab, 0o755); err != nil {
		t.Fatal(err)
	}

	srv.mu.Lock()
	srv.cfg.SetPreferredExe(dir, "MiJuego.x86_64")
	srv.mu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"localPath": dir,
		"online":    false,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/detect", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleDetect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Local         detect.Result `json:"local"`
		RememberedExe string        `json:"rememberedExe"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.RememberedExe != "MiJuego.x86_64" {
		t.Fatalf("rememberedExe = %q, esperaba MiJuego.x86_64", out.RememberedExe)
	}
	if len(out.Local.Executables) == 0 || out.Local.Executables[0].Rel != "MiJuego.x86_64" || !out.Local.Executables[0].Native {
		t.Fatalf("executables = %+v, esperaba MiJuego.x86_64 nativo primero", out.Local.Executables)
	}
}
