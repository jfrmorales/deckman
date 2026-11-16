package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/i18n"
	"github.com/jfrmorales/deckman/internal/meta"
)

// Caratulas de SteamGridDB: buscar, listar, aplicar y quitar.

func (s *Server) metaClient() *meta.Client {
	s.mu.RLock()
	key := s.cfg.GridKey
	s.mu.RUnlock()
	mc := meta.New(key)
	// La lista blanca de servidores de imagenes la conoce el servidor; meta la
	// aplica tambien a cada salto de una redireccion.
	mc.AllowURL = allowedArtworkURL
	return mc
}

// installArtwork baja las portadas de SteamGridDB y las deja en la carpeta
// grid de Steam. Devuelve siempre un texto para el usuario: que falten las
// caratulas no debe hacer fracasar el envio del juego.
func (s *Server) installArtwork(ctx context.Context, c *deck.Client, shortcutID uint32, steamAppID, name string) string {
	mc := s.metaClient()
	if !mc.HasGridKey() {
		return "Sin caratulas: falta la clave de SteamGridDB en Ajustes."
	}

	// Este ctx viene de un trabajo largo y NO trae plazo: el del envio del juego
	// solo se cancela a mano. Las consultas si tienen que tenerlo, o una API que
	// no responde deja el trabajo colgado para siempre.
	lookupCtx, cancelLookup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLookup()

	gameID, err := mc.ResolveGridGame(lookupCtx, steamAppID, name)
	if err != nil {
		return "Sin caratulas: " + err.Error() + "."
	}
	art := mc.FetchArtwork(lookupCtx, gameID)
	if len(art) == 0 {
		return "SteamGridDB no tiene imagenes de este juego."
	}

	enCaliente := c.SteamRunning(ctx) && c.CEFAvailable(ctx)
	var ok int
	var failed []string
	for _, a := range art {
		// Las URL vienen de SteamGridDB, no del navegador, pero se comprueban
		// igual: es la misma lista blanca que impide que deckman acabe bajando
		// cualquier cosa de cualquier sitio.
		if err := allowedArtworkURL(a.URL); err != nil {
			s.logf("caratula %s descartada: %v", a.Kind, err)
			failed = append(failed, a.Kind)
			continue
		}
		data, err := s.downloadArtwork(ctx, mc, a.URL)
		if err != nil {
			failed = append(failed, a.Kind)
			continue
		}
		if enCaliente && deck.SupportsLiveArtwork(a.Kind) {
			if err := c.ApplyArtworkLive(ctx, shortcutID, a.Kind, a.Ext, data); err == nil {
				ok++
				continue
			}
		}
		if _, err := c.WriteArtwork(ctx, shortcutID, a.Kind, a.Ext, data); err != nil {
			failed = append(failed, a.Kind)
			continue
		}
		ok++
	}
	if ok == 0 {
		return "No se pudo instalar ninguna caratula."
	}
	out := fmt.Sprintf("%d imagenes instaladas.", ok)
	if len(failed) > 0 {
		out += " Fallaron: " + strings.Join(failed, ", ") + "."
	}
	return out
}

// downloadArtwork baja una imagen con plazo propio. Generoso a proposito: un
// fondo animado ronda los 45 MB y por una linea lenta tarda un rato.
func (s *Server) downloadArtwork(ctx context.Context, mc *meta.Client, rawURL string) ([]byte, error) {
	dlCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return mc.Download(dlCtx, rawURL)
}

// --- galeria de caratulas ---
//
// Equivalente a lo que hace el plugin de SteamGridDB para Decky: buscar el
// juego, ver las opciones disponibles de cada tipo de arte y elegir una.

// artworkHosts son los unicos servidores de los que aceptamos descargar una
// imagen. La URL llega desde el navegador, y sin esta comprobacion el servidor
// se convertiria en un proxy hacia cualquier sitio.
var artworkHosts = []string{"steamgriddb.com", "cdn2.steamgriddb.com", "cdn.steamgriddb.com"}

func allowedArtworkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return i18n.Errorf("url invalida")
	}
	if u.Scheme != "https" {
		return i18n.Errorf("solo se aceptan descargas por https")
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range artworkHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return nil
		}
	}
	return i18n.Errorf("origen no permitido: %s", host)
}

// handleArtworkGames busca el juego en el catalogo de SteamGridDB.
func (s *Server) handleArtworkGames(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		SteamAppID string `json:"steamAppId"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	mc := s.metaClient()
	if !mc.HasGridKey() {
		writeErr(w, i18n.Errorf("falta la clave de SteamGridDB: ponla en Enviar juego > Ajustes de caratulas"), http.StatusPreconditionFailed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	// Con un appid de Steam la correspondencia es exacta; si no, hay que
	// buscar por nombre y que elija el usuario.
	if req.SteamAppID != "" {
		if id, err := mc.GridGameID(ctx, req.SteamAppID); err == nil {
			writeJSON(w, map[string]any{"games": []meta.GridGame{{ID: id, Name: req.Name}}, "exact": true})
			return
		}
	}
	games, err := mc.SearchGames(ctx, req.Name)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"games": games, "exact": false})
}

// handleArtworkList trae las opciones de un tipo de arte.
func (s *Server) handleArtworkList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GameID   int    `json:"gameId"`
		Kind     string `json:"kind"`
		Animated bool   `json:"animated"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	mc := s.metaClient()
	if !mc.HasGridKey() {
		writeErr(w, i18n.Errorf("falta la clave de SteamGridDB"), http.StatusPreconditionFailed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	opts, err := mc.ListArtwork(ctx, req.GameID, req.Kind, req.Animated)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"options": opts})
}

// handleArtworkCurrent dice que imagenes tiene puestas ya un juego.
func (s *Server) handleArtworkCurrent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID uint32 `json:"appId"`
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	current, err := c.CurrentArtwork(ctx, req.AppID)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"current": current, "live": c.CEFAvailable(ctx)})
}

// handleArtworkApply instala una imagen concreta elegida por el usuario.
func (s *Server) handleArtworkApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID uint32 `json:"appId"`
		Kind  string `json:"kind"`
		URL   string `json:"url"`
		Ext   string `json:"ext"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if req.AppID == 0 {
		writeErr(w, i18n.Errorf("falta el juego"), http.StatusBadRequest)
		return
	}
	if err := allowedArtworkURL(req.URL); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	data, err := s.metaClient().Download(ctx, req.URL)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}

	// Preferimos que lo aplique el propio Steam: se ve al momento y ademas
	// actualiza su registro interno de arte personalizado. Solo si no se puede
	// (icono, o Steam cerrado) escribimos el fichero a mano.
	// La miniatura de la biblioteca deja de valer en cuanto se cambia el arte.
	s.covers.drop(strconv.FormatUint(uint64(req.AppID), 10))

	if deck.SupportsLiveArtwork(req.Kind) {
		if err := c.ApplyArtworkLive(ctx, req.AppID, req.Kind, req.Ext, data); err == nil {
			name, _ := deck.ArtFileName(req.AppID, req.Kind, req.Ext)
			writeJSON(w, map[string]any{
				"ok": true, "file": name, "bytes": len(data),
				"live": true, "message": "aplicada al momento, sin reiniciar Steam",
			})
			return
		} else {
			s.logf("no se pudo aplicar en caliente (%v); escribo el fichero", err)
		}
	}

	name, err := c.WriteArtwork(ctx, req.AppID, req.Kind, req.Ext, data)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{
		"ok": true, "file": name, "bytes": len(data),
		"live": false, "message": "instalada; hay que reiniciar Steam para verla",
	})
}

// handleArtworkRemove quita una imagen y deja que Steam vuelva a la suya.
func (s *Server) handleArtworkRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID uint32 `json:"appId"`
		Kind  string `json:"kind"` // vacio = todas
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	s.covers.drop(strconv.FormatUint(uint64(req.AppID), 10))

	live := false
	if req.Kind != "" && deck.SupportsLiveArtwork(req.Kind) {
		if err := c.ClearArtworkLive(ctx, req.AppID, req.Kind); err == nil {
			live = true
		}
	}
	if !live {
		if req.Kind == "" {
			err = c.ClearArtwork(ctx, req.AppID)
		} else {
			err = c.RemoveArtworkKind(ctx, req.AppID, req.Kind)
		}
		if err != nil {
			writeErr(w, err, http.StatusBadGateway)
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "live": live})
}

// handleArtworkSize pregunta cuanto pesa una imagen sin bajarla. Las animadas
// llegan a 45 MB, asi que conviene decirlo antes de instalarla.
func (s *Server) handleArtworkSize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := allowedArtworkURL(req.URL); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	size, err := s.metaClient().HeadSize(ctx, req.URL)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"bytes": size})
}
