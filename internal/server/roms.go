package server

import (
	"context"
	"github.com/jfrmorales/deckman/internal/i18n"
	"net/http"

	"github.com/jfrmorales/deckman/internal/deck"
)

// Los handlers de ROMs nunca reciben rutas: solo sistema y nombre. La ruta la
// arma deck.romSystemDir a partir de lo que hay en la Deck. Ver el comentario
// de deck.nombreSuelto para el porque.

// handleRomsSystems devuelve solo los sistemas que tienen ROMs.
//
// El inventario general (inv.RomSystems) lista TODAS las carpetas porque para
// enviar una ROM hace falta poder elegir un sistema aun vacio. Para gestionar
// la coleccion es al reves: EmuDeck crea 181 carpetas y en una Deck real solo
// cuatro tenian juegos.
func (s *Server) handleRomsSystems(w http.ResponseWriter, r *http.Request) {
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	romsDir, sistemas, err := c.ROMSystems(r.Context())
	if err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	// Que sistemas ademas sabemos scrapear, para no ofrecer el boton donde no
	// va a hacer nada.
	conArte := make(map[string]bool, len(sistemas))
	for _, sis := range sistemas {
		conArte[sis.Name] = deck.SistemaScrapeable(sis.Name)
	}
	writeJSON(w, map[string]any{
		"ok": true, "romsDir": romsDir, "systems": sistemas, "scrapeable": conArte,
	})
}

func (s *Server) handleRomsScrape(w http.ResponseWriter, r *http.Request) {
	var req struct {
		System  string `json:"system"`
		Rehacer bool   `json:"rehacer"`
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
	if req.System == "" {
		writeErr(w, i18n.Errorf("elige el sistema"), http.StatusBadRequest)
		return
	}
	if !deck.SistemaScrapeable(req.System) {
		writeErr(w, i18n.Errorf("no se buscan caratulas de %q: libretro no tiene ese sistema", req.System), http.StatusBadRequest)
		return
	}

	j, err := s.startJob("scrape", "Buscando caratulas de "+req.System, func(ctx context.Context, report deck.ProgressFunc) error {
		res, err := c.ScrapeSystem(ctx, req.System, req.Rehacer, report)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.ultimoScrape = &res
		s.mu.Unlock()
		return nil
	})
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID})
}

// handleRomsScrapeResumen devuelve el detalle del ultimo scrapeo: cuantas
// caratulas se bajaron y, sobre todo, que juegos se quedaron sin ninguna. Eso
// no cabe en la barra de progreso y es lo unico accionable del resultado.
func (s *Server) handleRomsScrapeResumen(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	res := s.ultimoScrape
	s.mu.RUnlock()
	if res == nil {
		writeErr(w, i18n.Errorf("todavia no se ha buscado ninguna caratula"), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "resumen": res})
}

func (s *Server) handleRomsList(w http.ResponseWriter, r *http.Request) {
	system := r.URL.Query().Get("system")
	if system == "" {
		writeErr(w, i18n.Errorf("elige el sistema"), http.StatusBadRequest)
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
		writeErr(w, i18n.Errorf("hacen falta la URL y el sistema de destino"), http.StatusBadRequest)
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
//
// El parametro «system» acota la busqueda a esa plataforma. Sin el, busca en
// todas: buscar «sonic» teniendo elegido arcade no deberia devolver la version
// de PSP, pero a veces uno quiere ver todo lo que hay y elegir.
func (s *Server) handleRomsSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, i18n.Errorf("escribe que quieres buscar"), http.StatusBadRequest)
		return
	}
	results, err := searchArchiveOrg(r.Context(), q, r.URL.Query().Get("system"))
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "results": results})
}
