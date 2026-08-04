package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jfrmorales/deckman/internal/deck"
)

// Los handlers de ROMs nunca reciben rutas: solo sistema y nombre. La ruta la
// arma deck.romSystemDir a partir de lo que hay en la Deck. Ver el comentario
// de deck.nombreSuelto para el porque.

func (s *Server) handleRomsList(w http.ResponseWriter, r *http.Request) {
	system := r.URL.Query().Get("system")
	if system == "" {
		writeErr(w, fmt.Errorf("elige el sistema"), http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	roms, err := c.ListROMs(r.Context(), system)
	if err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "roms": roms})
}

func (s *Server) handleRomsDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		System string `json:"system"`
		Name   string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	if err := c.DeleteROM(r.Context(), req.System, req.Name); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleRomsRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		System  string `json:"system"`
		Name    string `json:"name"`
		NewName string `json:"newName"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	if err := c.RenameROM(r.Context(), req.System, req.Name, req.NewName); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleRomsDownload arranca la descarga como trabajo largo. Bajar una ROM
// tarda minutos: hacerlo dentro de la peticion la deja colgada, sin forma de
// cancelarla y a merced del tiempo de espera del navegador.
func (s *Server) handleRomsDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		System string `json:"system"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	if req.URL == "" || req.System == "" {
		writeErr(w, fmt.Errorf("hacen falta la URL y el sistema de destino"), http.StatusBadRequest)
		return
	}
	// El nombre se valida ya, antes de abrir el trabajo: si la URL no sirve,
	// mejor decirlo en el acto que en el panel de progreso.
	if _, err := deck.NombreDesdeURL(req.URL); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}

	j, err := s.startJob("download-rom", "Descargando ROM en "+req.System, func(ctx context.Context, report deck.ProgressFunc) error {
		return c.DownloadROM(ctx, req.URL, req.System, report)
	})
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID})
}

// handleRomsSearch no necesita la Deck: la busqueda sale de archive.org.
func (s *Server) handleRomsSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, fmt.Errorf("escribe que quieres buscar"), http.StatusBadRequest)
		return
	}
	results, err := searchArchiveOrg(r.Context(), q)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "results": results})
}
