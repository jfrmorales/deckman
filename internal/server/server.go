package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jfrmorales/deckman/internal/config"
	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/i18n"
	"github.com/jfrmorales/deckman/internal/update"
)

//go:embed web
var webFS embed.FS

// Server mantiene la conexion con la Deck y sirve la interfaz.
type Server struct {
	// Version se muestra en la interfaz y sirve para que otro deckman
	// arrancado en el mismo puerto nos reconozca.
	Version string

	mu     sync.RWMutex
	client *deck.Client
	cfg    *config.Config

	// Resumen del ultimo scrapeo. Vive aparte del trabajo porque lo
	// interesante (que juegos se quedaron sin caratula) no cabe en una barra
	// de progreso y se consulta cuando el trabajo ya ha terminado.
	ultimoScrape *deck.ResultadoScrape

	quitOnce         sync.Once
	quit             chan struct{}
	restartRequested atomic.Bool

	updMu           sync.Mutex
	updater         *update.Updater
	updState        UpdateState
	updInfo         *update.Info
	updCancel       context.CancelFunc
	updRestartDelay time.Duration

	// Trabajo largo en curso (copia, movimiento...). Solo uno a la vez: dos
	// transferencias simultaneas por SSH se estorban mas que se ayudan.
	job       *job
	jobMu     sync.Mutex
	listeners map[chan []byte]bool
	listMu    sync.Mutex

	// Portadas de la biblioteca, con su cache. Ver cover.go.
	covers *coverStore

	// Ultima foto de la Deck, para que mover o borrar un juego no repita el
	// Scan completo (con su du -sb sobre todos los compatdata y shadercache)
	// que la interfaz acaba de pagar para pintar la biblioteca. Ver inventory.
	invMu     sync.Mutex
	inv       *deck.Inventory
	invAt     time.Time
	invClient *deck.Client
}

// inventarioTTL es cuanto vale la foto. Un minuto cubre el gesto tipico
// (cargar la biblioteca y operar sobre un juego) sin arriesgar mucho: lo unico
// que se decide con datos de la foto son rutas y tamanos, y las comprobaciones
// de seguridad (SteamRunning, CEFAvailable, safeToDelete) van siempre frescas.
const inventarioTTL = time.Minute

type job struct {
	ID     string        `json:"id"`
	Kind   string        `json:"kind"`
	Title  string        `json:"title"`
	Prog   deck.Progress `json:"progress"`
	cancel context.CancelFunc
}

// New crea el servidor.
func New() *Server {
	s := &Server{
		cfg:             config.Load(),
		listeners:       map[chan []byte]bool{},
		quit:            make(chan struct{}),
		covers:          newCoverStore(),
		updRestartDelay: 400 * time.Millisecond,
	}
	if s.cfg.RecoverInterrupted(time.Now().Unix()) {
		s.guardar(s.cfg.Snapshot())
	}
	return s
}

// QuitRequested se cierra cuando el usuario pide salir desde la interfaz.
func (s *Server) QuitRequested() <-chan struct{} {
	return s.quit
}

// RestartRequested indica si el cierre se pidio para relanzar una version nueva.
func (s *Server) RestartRequested() bool {
	return s.restartRequested.Load()
}

func (s *Server) requestRestart() {
	s.restartRequested.Store(true)
	s.quitOnce.Do(func() { close(s.quit) })
}

// Clientes cuenta las interfaces abiertas ahora mismo: cada pestana o ventana
// mantiene una conexion a /api/events mientras vive. Sirve para saber que el
// usuario ha cerrado la interfaz cuando el proceso del navegador no lo dice
// (en macOS Chrome sigue vivo sin ventanas; con una pestana normal no hay
// proceso propio que vigilar).
func (s *Server) Clientes() int {
	s.listMu.Lock()
	defer s.listMu.Unlock()
	return len(s.listeners)
}

func (s *Server) jobBusy() bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	return s.job != nil && !s.job.Prog.Done && s.job.Prog.Error == ""
}

// Busy dice si hay una operacion larga o una autoactualizacion en marcha. Un
// trabajo terminado se queda en s.job para conservar su resumen, asi que "hay
// job" no es "ocupado".
func (s *Server) Busy() bool {
	return s.jobBusy() || s.updateBusy()
}

// Handler monta las rutas.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/state", s.guard(s.handleState))
	mux.HandleFunc("/api/connect", s.guard(s.handleConnect))
	mux.HandleFunc("/api/decks/forget", s.guard(s.handleForgetDeck))
	mux.HandleFunc("/api/inventory", s.guard(s.handleInventory))
	mux.HandleFunc("/api/browse", s.guard(s.handleBrowse))
	mux.HandleFunc("/api/places", s.guard(s.handlePlaces))
	mux.HandleFunc("/api/detect", s.guard(s.handleDetect))
	mux.HandleFunc("/api/settings", s.guard(s.handleSettings))
	mux.HandleFunc("/api/artwork/games", s.guard(s.handleArtworkGames))
	mux.HandleFunc("/api/artwork/list", s.guard(s.handleArtworkList))
	mux.HandleFunc("/api/artwork/current", s.guard(s.handleArtworkCurrent))
	mux.HandleFunc("/api/artwork/apply", s.guard(s.handleArtworkApply))
	mux.HandleFunc("/api/artwork/remove", s.guard(s.handleArtworkRemove))
	mux.HandleFunc("/api/artwork/size", s.guard(s.handleArtworkSize))
	mux.HandleFunc("/api/cover", s.guard(s.handleCover))
	mux.HandleFunc("/api/send-game", s.guard(s.handleSendGame))
	mux.HandleFunc("/api/send-rom", s.guard(s.handleSendROM))
	mux.HandleFunc("/api/move", s.guard(s.handleMove))
	mux.HandleFunc("/api/operations/clear", s.guard(s.handleClearOperations))
	mux.HandleFunc("/api/delete", s.guard(s.handleDelete))
	mux.HandleFunc("/api/compat", s.guard(s.handleCompat))
	mux.HandleFunc("/api/restart-steam", s.guard(s.handleRestartSteam))
	mux.HandleFunc("/api/cancel", s.guard(s.handleCancel))
	mux.HandleFunc("/api/quit", s.guard(s.handleQuit))
	mux.HandleFunc("/api/update/check", s.guard(s.handleUpdateCheck))
	mux.HandleFunc("/api/update/apply", s.guard(s.handleUpdateApply))
	mux.HandleFunc("/api/update/restart", s.guard(s.handleUpdateRestart))
	mux.HandleFunc("/api/update/cancel", s.guard(s.handleUpdateCancel))
	mux.HandleFunc("/api/roms/systems", s.guard(s.handleRomsSystems))
	mux.HandleFunc("/api/roms/list", s.guard(s.handleRomsList))
	mux.HandleFunc("/api/roms/scrape", s.guard(s.handleRomsScrape))
	mux.HandleFunc("/api/roms/scrape-resumen", s.guard(s.handleRomsScrapeResumen))
	mux.HandleFunc("/api/roms/delete", s.guard(s.handleRomsDelete))
	mux.HandleFunc("/api/roms/rename", s.guard(s.handleRomsRename))
	mux.HandleFunc("/api/roms/download", s.guard(s.handleRomsDownload))
	mux.HandleFunc("/api/roms/search", s.guard(s.handleRomsSearch))
	mux.HandleFunc("/api/events", s.handleEvents)

	return mux
}

// guard exige una cabecera propia en TODAS las peticiones a la API, sea cual
// sea el metodo. Como el servidor solo escucha en localhost, esto basta para que
// una web abierta en otra pestana no pueda dispararle ordenes: no puede mandar
// la cabecera sin un preflight CORS que no concedemos.
//
// Antes solo se exigia en POST, y varias rutas con efecto (reiniciar Steam,
// cancelar, desconectar) respondian igual a un GET. Con eso, cualquier pagina
// podia reiniciar Steam en la Deck con un simple
// <img src="http://127.0.0.1:8777/api/restart-steam">, que no necesita permiso
// de nadie. La interfaz manda siempre la cabecera (funcion api() de web/nucleo.js).
//
// /api/events se queda fuera aposta: EventSource no puede mandar cabeceras. Es
// solo lectura, y desde otro origen el navegador no entrega los datos sin CORS.
func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := checkHost(r.Host); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Deckman") == "" {
			http.Error(w, "peticion no autorizada", http.StatusForbidden)
			return
		}
		// El idioma se fija en cada peticion, no al arrancar: asi cambiarlo en
		// la interfaz surte efecto en los errores desde la siguiente orden, sin
		// reiniciar nada.
		ponerIdioma(s.idiomaEfectivo(r))
		h(w, r)
	}
}

// checkHost rechaza las peticiones que no vengan dirigidas a localhost.
//
// Es la defensa contra el DNS rebinding: un dominio del atacante que resuelve a
// 127.0.0.1 hace que el navegador considere su pagina del MISMO origen que
// deckman, y entonces si puede mandar la cabecera X-Deckman y leer las
// respuestas. Lo que no puede falsear es la cabecera Host, que sigue siendo su
// dominio, asi que comprobarla corta el ataque de raiz.
func checkHost(host string) error {
	if host == "" {
		return i18n.Errorf("peticion sin cabecera Host")
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.ToLower(strings.Trim(name, "[]"))
	switch name {
	case "127.0.0.1", "localhost", "::1":
		return nil
	}
	return i18n.Errorf("deckman solo atiende peticiones a localhost, no a %q", host)
}

// guardar persiste la configuracion y deja dicho si no ha podido.
//
// Ninguna de las cuatro llamadas debe tumbar la peticion en curso: enviar un
// juego funciona igual aunque no se recuerde la ultima carpeta. Pero fallaban
// en silencio, y el sintoma que veia el usuario era que deckman se olvidaba de
// su Deck entre arranques sin que nada lo explicara.
func (s *Server) guardar(cfg config.Config) {
	if err := cfg.Save(); err != nil {
		s.logf("no se pudo guardar la configuracion: %v", err)
	}
}

// logf deja constancia en la consola de deckman de las cosas que no son
// errores para el usuario pero conviene poder mirar.
func (s *Server) logf(format string, args ...any) {
	log.Printf(format, args...)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// idiomaActual: en que idioma salen los errores por HTTP.
//
// Es una variable de paquete y no un campo de Server a proposito. writeErr se
// llama desde 91 sitios; pasarle el idioma a todos, solo para que lo reenvien
// aqui, ensuciaria 91 firmas sin aportar nada. Y deckman es un proceso para un
// usuario delante de una pantalla: «el idioma» es un dato global de verdad, no
// un atajo. Lo pone guard en cada peticion, asi que va siempre a la par de lo
// que muestra la interfaz.
var idiomaActual atomic.Value // string

func ponerIdioma(lang string) {
	if !i18n.Soportado(lang) {
		lang = i18n.ES
	}
	idiomaActual.Store(lang)
}

func idioma() string {
	if v, ok := idiomaActual.Load().(string); ok && v != "" {
		return v
	}
	return i18n.ES
}

// writeErr traduce el error antes de mandarlo. El castellano sigue siendo el
// que va al registro (err.Error()); lo que se traduce es lo que ve la persona.
func writeErr(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": i18n.Traducir(err, idioma())})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) conn() (*deck.Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client == nil {
		return nil, i18n.Errorf("no hay conexion con SteamOS")
	}
	return s.client, nil
}

// --- estado y conexion ---

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	connected := s.client != nil
	cfg := s.cfg.Snapshot()
	s.mu.RUnlock()

	s.jobMu.Lock()
	var cur *job
	if s.job != nil {
		j := *s.job
		cur = &j
	}
	s.jobMu.Unlock()

	// host/port/user son los de la Deck activa. Se siguen mandando sueltos
	// porque es lo que rellena el formulario de conexion; "decks" es la lista
	// completa para poder elegir entre varias.
	activa := cfg.ActiveDeck()
	writeJSON(w, map[string]any{
		"app":              "deckman",
		"version":          s.Version,
		"connected":        connected,
		"host":             activa.Host,
		"port":             activa.Port,
		"user":             activa.User,
		"decks":            cfg.Decks,
		"active":           cfg.Active,
		"hasKey":           cfg.KeyPath != "",
		"lastLocal":        cfg.LastLocal,
		"job":              cur,
		"operations":       cfg.Operations,
		"os":               runtime.GOOS,
		"hasGridKey":       cfg.GridKey != "",
		"autoCheckUpdates": !cfg.DisableUpdateCheck,
		"update":           s.updateSnapshot(),
		// "idioma" es lo elegido a mano ("" = automatico) y "idiomaEfectivo" el
		// que se va a usar de verdad. La interfaz necesita los dos: uno para
		// marcar el desplegable y otro para pintarse.
		"idioma":         cfg.Idioma,
		"idiomaEfectivo": s.idiomaEfectivo(r),
		"idiomas":        i18n.Idiomas,
	})
}

// handleQuit apaga deckman a peticion de la interfaz. Lanzado desde el
// escritorio (Flatpak, acceso directo) no hay consola con Ctrl+C: sin esto el
// proceso quedaria corriendo hasta cerrar sesion.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	if s.Busy() {
		writeErr(w, i18n.Errorf("hay una operacion en curso; cancelala o espera a que termine"), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	// Responder primero y cerrar despues: si no, el navegador ve la conexion
	// cortada y pinta un error en vez de la despedida.
	s.quitOnce.Do(func() { close(s.quit) })
}

// handleSettings guarda ajustes sueltos. De momento solo la clave de
// SteamGridDB, que el usuario se saca gratis en su web.
// idiomaEfectivo decide en que idioma hablarle a quien pregunta: lo que haya
// elegido, y si no ha elegido, lo que pida su navegador. El castellano es el
// ultimo recurso porque es el original.
func (s *Server) idiomaEfectivo(r *http.Request) string {
	s.mu.RLock()
	elegido := s.cfg.Idioma
	s.mu.RUnlock()
	if i18n.Soportado(elegido) {
		return elegido
	}
	if r != nil {
		if lang := i18n.Negociar(r.Header.Get("Accept-Language")); lang != "" {
			return lang
		}
	}
	return i18n.ES
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GridKey          *string `json:"gridKey"`
		Idioma           *string `json:"idioma"`
		AutoCheckUpdates *bool   `json:"autoCheckUpdates"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if req.GridKey != nil {
		s.cfg.GridKey = strings.TrimSpace(*req.GridKey)
	}
	if req.AutoCheckUpdates != nil {
		s.cfg.DisableUpdateCheck = !*req.AutoCheckUpdates
	}
	if req.Idioma != nil {
		// "" es valido y significa «el que diga el navegador»: es la unica
		// forma de volver a automatico despues de haber elegido uno.
		lang := strings.TrimSpace(*req.Idioma)
		if lang != "" && !i18n.Soportado(lang) {
			s.mu.Unlock()
			writeErr(w, i18n.Errorf("idioma no soportado: %q", lang), http.StatusBadRequest)
			return
		}
		s.cfg.Idioma = lang
	}
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	if err := cfg.Save(); err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	// El cambio vale ya para los errores de esta misma respuesta en adelante.
	ponerIdioma(s.idiomaEfectivo(r))
	writeJSON(w, map[string]any{
		"ok": true, "hasGridKey": cfg.GridKey != "",
		"autoCheckUpdates": !cfg.DisableUpdateCheck,
		"idioma":           cfg.Idioma, "idiomaEfectivo": idioma(),
	})
}

// --- eventos en vivo (SSE) ---

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming no soportado", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan []byte, 32)
	s.listMu.Lock()
	s.listeners[ch] = true
	s.listMu.Unlock()

	defer func() {
		s.listMu.Lock()
		delete(s.listeners, ch)
		close(ch)
		s.listMu.Unlock()
	}()

	fmt.Fprint(w, ": conectado\n\n")
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) broadcast(v any) {
	msg, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.listMu.Lock()
	defer s.listMu.Unlock()
	for ch := range s.listeners {
		select {
		case ch <- msg:
		default: // cliente atascado: nos saltamos el evento antes que bloquear
		}
	}
}

// Close suelta la conexion con la Deck.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
}
