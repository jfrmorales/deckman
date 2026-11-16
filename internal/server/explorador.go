package server

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jfrmorales/deckman/internal/detect"
)

// Explorador de ficheros de este PC, para elegir que se envia.

// --- explorador local ---

type browseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
	// Native: ejecutable nativo de Linux. Solo se rellena si se pide (exes=1).
	Native bool `json:"native,omitempty"`
}

// handleBrowse lista el sistema de ficheros del PC. Hace falta porque el
// selector del navegador nunca entrega la ruta real de una carpeta, y para
// copiar un juego necesitamos exactamente eso.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	// Al elegir el ejecutable, el explorador necesita saber cuales lo son. Un
	// .exe se ve por el nombre, pero un binario de Linux no lleva extension:
	// hay que mirar la cabecera, y eso solo se hace cuando se pide porque es
	// abrir cada fichero de la carpeta.
	marcarNativos := r.URL.Query().Get("exes") == "1"

	if p == "" {
		if home, err := os.UserHomeDir(); err == nil {
			p = home
		} else {
			p = "/"
		}
	}
	if p == "@roots" {
		writeJSON(w, map[string]any{"path": "@roots", "parent": "", "entries": listRoots()})
		return
	}

	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}

	out := make([]browseEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // ocultos fuera, solo hacen ruido
		}
		full := filepath.Join(p, e.Name())
		be := browseEntry{Name: e.Name(), Path: full, IsDir: e.IsDir()}
		if fi, err := e.Info(); err == nil && !e.IsDir() {
			be.Size = fi.Size()
			be.Native = marcarNativos && detect.LanzableEn(full)
		}
		out = append(out, be)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	parent := filepath.Dir(p)
	if parent == p {
		parent = "@roots"
	}
	writeJSON(w, map[string]any{"path": p, "parent": parent, "entries": out})
}

// place es un acceso rapido de la barra lateral del explorador.
type place struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"` // home, dir, drive, recent
}

// handlePlaces devuelve los sitios habituales: la carpeta personal, las
// carpetas conocidas, las unidades montadas y la ultima usada. Sin esto hay
// que navegar desde la raiz cada vez.
func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	var out []place
	seen := map[string]bool{}
	add := func(name, p, kind string) {
		if p == "" || seen[p] {
			return
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return
		}
		seen[p] = true
		out = append(out, place{Name: name, Path: p, Kind: kind})
	}

	s.mu.RLock()
	last := s.cfg.LastLocal
	s.mu.RUnlock()
	add("Última usada", last, "recent")

	home, _ := os.UserHomeDir()
	add("Carpeta personal", home, "home")
	if home != "" {
		// Nombres en ingles y en espanol: depende de como este configurado el
		// sistema, y no merece la pena leer user-dirs.dirs para esto.
		for _, d := range []struct{ label, dir string }{
			{"Descargas", "Downloads"}, {"Descargas", "Descargas"},
			{"Escritorio", "Desktop"}, {"Escritorio", "Escritorio"},
			{"Documentos", "Documents"}, {"Documentos", "Documentos"},
			{"Juegos", "Games"}, {"Juegos", "Juegos"},
		} {
			add(d.label, filepath.Join(home, d.dir), "dir")
		}
	}

	if runtime.GOOS == "windows" {
		for _, root := range listRoots() {
			add(root.Name, root.Path, "drive")
		}
	} else {
		add("Sistema de ficheros", "/", "drive")
	}

	// Unidades externas montadas. Solo si tienen algo dentro: en Linux
	// /media y /run/media existen siempre, y /run/media/<usuario> aparece
	// aunque no haya ningun disco conectado. Listarlos vacios es solo ruido.
	// /Volumes es el sitio de macOS; ahi el disco de arranque aparece como
	// un enlace a / que mountedUnder se salta (no es carpeta), y ya esta
	// listado arriba como "Sistema de ficheros".
	for _, base := range []string{"/run/media", "/media", "/mnt", "/Volumes"} {
		for _, p := range mountedUnder(base) {
			add(filepath.Base(p), p, "drive")
		}
	}

	writeJSON(w, map[string]any{"places": out})
}

// mountedUnder devuelve los puntos de montaje reales que cuelgan de base.
//
// En Linux los discos externos aparecen en /run/media/<usuario>/<disco>, asi
// que hay que bajar ese nivel; y una carpeta vacia significa que no hay nada
// montado, no que exista una unidad.
func mountedUnder(base string) []string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(base, e.Name())
		sub, err := os.ReadDir(p)
		if err != nil || len(sub) == 0 {
			continue // vacia: no hay disco ahi
		}
		// Un nivel intermedio por usuario, tipico de /run/media.
		if base == "/run/media" {
			for _, x := range sub {
				if !x.IsDir() {
					continue
				}
				q := filepath.Join(p, x.Name())
				if inner, err := os.ReadDir(q); err == nil && len(inner) > 0 {
					out = append(out, q)
				}
			}
			continue
		}
		out = append(out, p)
	}
	return out
}

func listRoots() []browseEntry {
	var roots []browseEntry
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			d := string(c) + `:\`
			if _, err := os.Stat(d); err == nil {
				roots = append(roots, browseEntry{Name: d, Path: d, IsDir: true})
			}
		}
		return roots
	}
	roots = append(roots, browseEntry{Name: "/", Path: "/", IsDir: true})
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, browseEntry{Name: "Carpeta personal", Path: home, IsDir: true})
	}
	for _, m := range []string{"/mnt", "/media", "/run/media", "/Volumes"} {
		if _, err := os.Stat(m); err == nil {
			roots = append(roots, browseEntry{Name: m, Path: m, IsDir: true})
		}
	}
	return roots
}
