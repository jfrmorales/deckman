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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfrmorales/deckman/internal/config"
	"github.com/jfrmorales/deckman/internal/deck"
	"github.com/jfrmorales/deckman/internal/detect"
	"github.com/jfrmorales/deckman/internal/meta"
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

	quitOnce sync.Once
	quit     chan struct{}

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
	return &Server{
		cfg:       config.Load(),
		listeners: map[chan []byte]bool{},
		quit:      make(chan struct{}),
		covers:    newCoverStore(),
	}
}

// QuitRequested se cierra cuando el usuario pide salir desde la interfaz.
func (s *Server) QuitRequested() <-chan struct{} {
	return s.quit
}

// Busy dice si hay una operacion larga en marcha. Un trabajo terminado se
// queda en s.job para conservar su resumen, asi que "hay job" no es "ocupado".
func (s *Server) Busy() bool {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	return s.job != nil && !s.job.Prog.Done && s.job.Prog.Error == ""
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
	mux.HandleFunc("/api/disconnect", s.guard(s.handleDisconnect))
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
	mux.HandleFunc("/api/delete", s.guard(s.handleDelete))
	mux.HandleFunc("/api/compat", s.guard(s.handleCompat))
	mux.HandleFunc("/api/restart-steam", s.guard(s.handleRestartSteam))
	mux.HandleFunc("/api/cancel", s.guard(s.handleCancel))
	mux.HandleFunc("/api/quit", s.guard(s.handleQuit))
	mux.HandleFunc("/api/roms/list", s.guard(s.handleRomsList))
	mux.HandleFunc("/api/roms/delete", s.guard(s.handleRomsDelete))
	mux.HandleFunc("/api/roms/rename", s.guard(s.handleRomsRename))
	mux.HandleFunc("/api/roms/download", s.guard(s.handleRomsDownload))
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
// de nadie. La interfaz manda siempre la cabecera (funcion api() de app.js).
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
		return fmt.Errorf("peticion sin cabecera Host")
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
	return fmt.Errorf("deckman solo atiende peticiones a localhost, no a %q", host)
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

func writeErr(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) conn() (*deck.Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client == nil {
		return nil, fmt.Errorf("no hay conexion con la Steam Deck")
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
		"app":        "deckman",
		"version":    s.Version,
		"connected":  connected,
		"host":       activa.Host,
		"port":       activa.Port,
		"user":       activa.User,
		"decks":      cfg.Decks,
		"active":     cfg.Active,
		"hasKey":     cfg.KeyPath != "",
		"lastLocal":  cfg.LastLocal,
		"job":        cur,
		"os":         runtime.GOOS,
		"hasGridKey": cfg.GridKey != "",
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
		writeErr(w, fmt.Errorf("hay una operacion en curso; cancelala o espera a que termine"), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	// Responder primero y cerrar despues: si no, el navegador ve la conexion
	// cortada y pinta un error en vez de la despedida.
	s.quitOnce.Do(func() { close(s.quit) })
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Name     string `json:"name"` // etiqueta opcional, para distinguir varias Decks
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if req.Host == "" {
		writeErr(w, fmt.Errorf("indica la IP de la Steam Deck"), http.StatusBadRequest)
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.User == "" {
		req.User = "deck"
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	creds := deck.Credentials{Host: req.Host, Port: req.Port, User: req.User, Password: req.Password}

	// Si ya tenemos clave instalada, la preferimos y no pedimos contrasena.
	s.mu.RLock()
	existingKey := s.cfg.KeyPath
	s.mu.RUnlock()
	if existingKey != "" {
		if _, err := os.Stat(existingKey); err == nil {
			creds.KeyPath = existingKey
		}
	}

	client, err := deck.Connect(ctx, creds)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}

	// Dejamos instalada nuestra clave publica para no depender de la contrasena
	// en adelante.
	var keyNote string
	keyPath, pubKey, kerr := config.EnsureKey()
	if kerr == nil {
		if err := client.InstallPublicKey(ctx, pubKey); err == nil {
			s.mu.Lock()
			s.cfg.KeyPath = keyPath
			s.mu.Unlock()
			keyNote = "clave SSH instalada: las proximas conexiones no pediran contrasena"
		} else {
			keyNote = "conectado, pero no se pudo instalar la clave SSH: " + err.Error()
		}
	}

	s.mu.Lock()
	if s.client != nil {
		s.client.Close()
	}
	s.client = client
	s.cfg.UpsertDeck(config.Deck{Host: req.Host, Port: req.Port, User: req.User, Name: req.Name})
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	cfg.Save()

	writeJSON(w, map[string]any{"ok": true, "home": client.Home, "note": keyNote})
}

// handleForgetDeck olvida una Deck guardada y, sobre todo, le retira la clave
// SSH que deckman le instalo.
//
// Borrarla solo de la configuracion local no valdria: la clave se quedaria en
// authorized_keys de la Deck dando acceso permanente desde este PC, y el
// usuario creeria que ha cortado el acceso. Si la Deck no se puede alcanzar
// para revocarla, se dice claramente y se explica como quitarla a mano, en vez
// de callarlo.
func (s *Server) handleForgetDeck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	if s.Busy() {
		writeErr(w, fmt.Errorf("hay una operacion en curso; espera a que termine antes de olvidar una Deck"), http.StatusConflict)
		return
	}
	var req struct {
		Index int `json:"index"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	if req.Index < 0 || req.Index >= len(s.cfg.Decks) {
		s.mu.RUnlock()
		writeErr(w, fmt.Errorf("esa Deck ya no esta en la lista"), http.StatusBadRequest)
		return
	}
	objetivo := s.cfg.Decks[req.Index]
	activa := s.cfg.ActiveDeck()
	conectado := s.client != nil
	keyPath := s.cfg.KeyPath
	s.mu.RUnlock()

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// La clave publica que hay que retirar es la nuestra. Si no la tenemos, no
	// hay nada que revocar (nunca se llego a instalar).
	var pubKey string
	if keyPath != "" {
		if _, pub, err := config.EnsureKey(); err == nil {
			pubKey = pub
		}
	}

	aviso := ""
	revocada := false
	if pubKey != "" {
		// Con la Deck delante se usa la sesion abierta; si no, se abre una con
		// la propia clave que vamos a retirar. Para eso esta.
		var c *deck.Client
		if conectado && activa.Same(objetivo) {
			s.mu.RLock()
			c = s.client
			s.mu.RUnlock()
		} else {
			nuevo, err := deck.Connect(ctx, deck.Credentials{
				Host: objetivo.Host, Port: objetivo.Port, User: objetivo.User, KeyPath: keyPath,
			})
			if err == nil {
				defer nuevo.Close()
				c = nuevo
			} else {
				aviso = fmt.Sprintf("no se pudo conectar con %s para retirar la clave (%v). "+
					"La quito de la lista igualmente, pero la clave sigue en esa Deck: "+
					"para retirarla, borra la linea que acaba en %q de ~/.ssh/authorized_keys",
					objetivo.Label(), err, comentarioClave(pubKey))
			}
		}
		if c != nil {
			n, err := c.RemovePublicKey(ctx, pubKey)
			switch {
			case err != nil:
				aviso = "no se pudo retirar la clave de la Deck: " + err.Error()
			case n > 0:
				revocada = true
			default:
				aviso = "la clave ya no estaba en esa Deck"
			}
		}
	}

	s.mu.Lock()
	// La lista ha podido cambiar mientras hablabamos con la Deck.
	if req.Index >= len(s.cfg.Decks) || !s.cfg.Decks[req.Index].Same(objetivo) {
		s.mu.Unlock()
		writeErr(w, fmt.Errorf("esa Deck ya no esta en la lista"), http.StatusConflict)
		return
	}
	s.cfg.RemoveDeck(req.Index)

	// Si era la que estaba conectada, se cierra la sesion: seguir usandola con
	// la clave ya revocada solo lleva a errores raros al primer reintento.
	if conectado && activa.Same(objetivo) && s.client != nil {
		s.client.Close()
		s.client = nil
	}

	// La clave local es una sola para todas las Decks. Solo se borra al
	// olvidar la ultima; hacerlo antes dejaria a las demas con una clave
	// instalada que este PC ya no tiene.
	if len(s.cfg.Decks) == 0 && s.cfg.KeyPath != "" {
		os.Remove(s.cfg.KeyPath)
		os.Remove(s.cfg.KeyPath + ".pub")
		s.cfg.KeyPath = ""
	}
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	cfg.Save()

	writeJSON(w, map[string]any{
		"ok":       true,
		"revocada": revocada,
		"aviso":    aviso,
		"decks":    cfg.Decks,
		"active":   cfg.Active,
	})
}

// comentarioClave saca el comentario final de una clave publica (deckman@pc),
// que es por lo que el usuario la reconoce en authorized_keys.
func comentarioClave(pubKey string) string {
	campos := strings.Fields(strings.TrimSpace(pubKey))
	if len(campos) < 3 {
		return "deckman@"
	}
	return campos[len(campos)-1]
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	inv, err := c.Scan(ctx)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	s.storeInventory(c, inv)
	// Las portadas se resuelven aqui y no dentro de Scan: es una consulta mas a
	// la Deck que solo le hace falta a la biblioteca, no a mover ni a borrar.
	s.covers.setIndex(c.AnnotateCovers(ctx, inv))
	writeJSON(w, inv)
}

// storeInventory guarda una copia de la foto para mover/borrar. Copia y no el
// puntero: AnnotateCovers sigue escribiendo HasCover sobre el original
// mientras un trabajo podria estar leyendo la foto guardada.
func (s *Server) storeInventory(c *deck.Client, inv *deck.Inventory) {
	copia := *inv
	copia.Games = append([]deck.Game(nil), inv.Games...)
	copia.Libraries = append([]deck.Library(nil), inv.Libraries...)
	s.invMu.Lock()
	s.inv, s.invAt, s.invClient = &copia, time.Now(), c
	s.invMu.Unlock()
}

func (s *Server) dropInventory() {
	s.invMu.Lock()
	s.inv, s.invClient = nil, nil
	s.invMu.Unlock()
}

// inventory devuelve la foto reciente de esta misma conexion, o escanea. Los
// datos que salen de aqui solo se usan para localizar rutas y tamanos; toda
// decision peligrosa (escribir shortcuts.vdf, borrar) se comprueba fresca en
// deck.MoveGame/Delete.
func (s *Server) inventory(ctx context.Context, c *deck.Client) (*deck.Inventory, error) {
	s.invMu.Lock()
	if s.inv != nil && s.invClient == c && time.Since(s.invAt) < inventarioTTL {
		inv := s.inv
		s.invMu.Unlock()
		return inv, nil
	}
	s.invMu.Unlock()

	inv, err := c.Scan(ctx)
	if err != nil {
		return nil, err
	}
	s.storeInventory(c, inv)
	return inv, nil
}

// --- explorador local ---

type browseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// handleBrowse lista el sistema de ficheros del PC. Hace falta porque el
// selector del navegador nunca entrega la ruta real de una carpeta, y para
// copiar un juego necesitamos exactamente eso.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")

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
	for _, base := range []string{"/run/media", "/media", "/mnt"} {
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
	for _, m := range []string{"/mnt", "/media", "/run/media"} {
		if _, err := os.Stat(m); err == nil {
			roots = append(roots, browseEntry{Name: m, Path: m, IsDir: true})
		}
	}
	return roots
}

// --- deteccion del juego ---

// handleSettings guarda ajustes sueltos. De momento solo la clave de
// SteamGridDB, que el usuario se saca gratis en su web.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GridKey *string `json:"gridKey"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if req.GridKey != nil {
		s.cfg.GridKey = strings.TrimSpace(*req.GridKey)
	}
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	if err := cfg.Save(); err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "hasGridKey": cfg.GridKey != ""})
}

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

// handleDetect examina una carpeta local y consulta la red para proponer
// nombre, ejecutable y version de Proton.
func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalPath string `json:"localPath"`
		Online    *bool  `json:"online"`
		Override  string `json:"overrideAppId"` // para reintentar con otro candidato
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if req.LocalPath == "" {
		writeErr(w, fmt.Errorf("indica la carpeta del juego"), http.StatusBadRequest)
		return
	}
	if fi, err := os.Stat(req.LocalPath); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	} else if !fi.IsDir() {
		// Un ejecutable suelto: no hay nada que rastrear mas alla del nombre.
		writeJSON(w, map[string]any{
			"local": &detect.Result{
				Dir:       req.LocalPath,
				CleanName: detect.CleanName(strings.TrimSuffix(filepath.Base(req.LocalPath), filepath.Ext(req.LocalPath))),
			},
		})
		return
	}

	local := detect.Detect(req.LocalPath)

	online := true
	if req.Online != nil {
		online = *req.Online
	}
	resp := map[string]any{"local": local}

	if online {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()

		appID := local.SteamAppID
		if req.Override != "" {
			appID = req.Override
		}
		resp["online"] = s.metaClient().Lookup(ctx, appID, local.CleanName)
	}
	writeJSON(w, resp)
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
		return fmt.Errorf("url invalida")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("solo se aceptan descargas por https")
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range artworkHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return nil
		}
	}
	return fmt.Errorf("origen no permitido: %s", host)
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
		writeErr(w, fmt.Errorf("falta la clave de SteamGridDB: ponla en Enviar juego > Ajustes de caratulas"), http.StatusPreconditionFailed)
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
		writeErr(w, fmt.Errorf("falta la clave de SteamGridDB"), http.StatusPreconditionFailed)
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
		writeErr(w, fmt.Errorf("falta el juego"), http.StatusBadRequest)
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

// --- trabajos largos ---

// startJob arranca una operacion larga si no hay otra en curso.
func (s *Server) startJob(kind, title string, fn func(ctx context.Context, report deck.ProgressFunc) error) (*job, error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()
	if s.job != nil && !s.job.Prog.Done && s.job.Prog.Error == "" {
		return nil, fmt.Errorf("ya hay una operacion en curso: %s", s.job.Title)
	}

	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		ID:     strconv.FormatInt(time.Now().UnixNano(), 36),
		Kind:   kind,
		Title:  title,
		Prog:   deck.Progress{Phase: "empezando"},
		cancel: cancel,
	}
	s.job = j

	report := func(p deck.Progress) {
		s.jobMu.Lock()
		j.Prog = p
		s.jobMu.Unlock()
		s.broadcast(map[string]any{"type": "progress", "job": j.ID, "title": j.Title, "kind": j.Kind, "progress": p})
	}

	go func() {
		defer cancel()
		err := fn(ctx, report)
		s.jobMu.Lock()
		final := j.Prog
		final.Done = true
		if err != nil {
			final.Error = err.Error()
			final.Phase = "error"
		} else if final.Phase != "listo" {
			final.Phase = "listo"
		}
		j.Prog = final
		s.jobMu.Unlock()
		s.broadcast(map[string]any{"type": "progress", "job": j.ID, "title": j.Title, "kind": j.Kind, "progress": final})
	}()

	return j, nil
}

// progressTracker recuerda el ultimo parte emitido para que el aviso de
// finalizacion conserve los contadores. Sin esto, el mensaje final llega con
// los bytes y los ficheros a cero y la interfaz pierde el resumen.
type progressTracker struct {
	report deck.ProgressFunc
	mu     sync.Mutex
	last   deck.Progress
}

func (t *progressTracker) Report(p deck.Progress) {
	t.mu.Lock()
	t.last = p
	t.mu.Unlock()
	t.report(p)
}

// snapshot devuelve el ultimo parte con su cerrojo puesto. Los llamantes de
// hoy leen cuando las gorutinas de la subida ya han terminado, pero last tiene
// dueno declarado (t.mu) y leerlo sin el es invitar a la proxima carrera.
func (t *progressTracker) snapshot() deck.Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

// Finish emite el parte final reutilizando los contadores acumulados.
func (t *progressTracker) Finish(msg string) {
	t.mu.Lock()
	p := t.last
	t.mu.Unlock()
	p.Phase = "listo"
	p.Done = true
	p.Speed = 0
	p.File = ""
	p.Message = msg
	t.report(p)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.jobMu.Lock()
	if s.job != nil && s.job.cancel != nil {
		s.job.cancel()
	}
	s.jobMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

// registerShortcut anade un juego no-Steam por la via que toque.
//
// Con Steam abierto SIEMPRE se le pide a Steam: editar shortcuts.vdf por
// detras mientras Steam corre no solo se pierde (Steam reescribe el fichero al
// salir desde su copia en memoria) sino que puede dejar el fichero con menos
// juegos de los que habia. Paso de verdad en una Deck: se anadio un juego,
// Steam se reinicio y quedo una sola entrada de siete.
//
// Devuelve el appid y si se hizo por la via de Steam.
func (s *Server) registerShortcut(ctx context.Context, c *deck.Client, opts deck.NonSteamOptions) (uint32, bool, error) {
	steamCorriendo := c.SteamRunning(ctx)

	if steamCorriendo {
		if !c.CEFAvailable(ctx) {
			return 0, false, fmt.Errorf(
				"Steam esta abierto pero no responde: cierralo en la Deck y vuelve a intentarlo, " +
					"o reinicialo y espera a que cargue del todo. Anadir el juego con Steam abierto " +
					"le haria perder los accesos directos que ya tienes")
		}
		appID, err := c.AddShortcutLive(ctx, opts.Name, opts.Exe, opts.StartDir, opts.LaunchOptions, opts.CompatTool)
		if err != nil {
			return 0, false, fmt.Errorf("no se pudo anadir el juego a traves de Steam: %w", err)
		}
		return appID, true, nil
	}

	// Con Steam cerrado el fichero es nuestro y se puede editar sin riesgo.
	appID, err := c.AddNonSteamGame(ctx, opts)
	return appID, false, err
}

// safeRelExe valida la ruta del ejecutable dentro de la carpeta del juego y la
// devuelve normalizada con barras.
//
// No es paranoia: de esta ruta sale el StartDir que se guarda en el acceso
// directo, y StartDir es exactamente lo que "Eliminar" le pasa a un rm -rf en
// la Deck. Un "../.." colocaria ese borrado fuera de ~/Games.
func safeRelExe(exe string) (string, error) {
	// El navegador puede estar en Windows y mandar "bin\juego.exe".
	clean := strings.ReplaceAll(strings.TrimSpace(exe), `\`, "/")
	malo := fmt.Errorf("la ruta del ejecutable tiene que ser relativa a la carpeta del juego, sin \"..\": %q", exe)
	if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, ":") {
		return "", malo
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", malo
		}
	}
	// IsLocal remata: descarta rutas absolutas, escapes y los nombres
	// reservados de Windows.
	if !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", malo
	}
	return clean, nil
}

func (s *Server) handleSendGame(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalPath     string `json:"localPath"`
		Name          string `json:"name"`
		Exe           string `json:"exe"` // relativa dentro de la carpeta
		CompatTool    string `json:"compatTool"`
		LaunchOptions string `json:"launchOptions"`
		AddShortcut   bool   `json:"addShortcut"`
		SteamAppID    string `json:"steamAppId"` // para buscar las caratulas
		Artwork       bool   `json:"artwork"`
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
	if req.LocalPath == "" {
		writeErr(w, fmt.Errorf("elige la carpeta del juego"), http.StatusBadRequest)
		return
	}
	info, err := os.Stat(req.LocalPath)
	if err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = filepath.Base(req.LocalPath)
	}
	if req.AddShortcut && req.Exe == "" {
		writeErr(w, fmt.Errorf("elige el ejecutable del juego para poder anadirlo a Steam"), http.StatusBadRequest)
		return
	}
	exeRel := ""
	if req.Exe != "" {
		exeRel, err = safeRelExe(req.Exe)
		if err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
	}

	s.mu.Lock()
	s.cfg.LastLocal = filepath.Dir(req.LocalPath)
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	cfg.Save()

	gamesDir := c.Home + "/Games"
	folderName := filepath.Base(filepath.Clean(req.LocalPath))
	remoteBase := gamesDir + "/" + folderName

	j, err := s.startJob("send-game", "Enviando "+req.Name, func(ctx context.Context, report deck.ProgressFunc) error {
		tr := &progressTracker{report: report}
		if err := c.MkdirAll(gamesDir); err != nil {
			return err
		}
		if !info.IsDir() {
			// Un ejecutable suelto va a su propia carpeta.
			remoteBase = gamesDir + "/" + strings.TrimSuffix(folderName, filepath.Ext(folderName))
			if err := c.MkdirAll(remoteBase); err != nil {
				return err
			}
			if err := c.UploadTree(ctx, req.LocalPath, remoteBase, tr.Report); err != nil {
				return err
			}
		} else if err := c.UploadTree(ctx, req.LocalPath, gamesDir, tr.Report); err != nil {
			return err
		}

		if !req.AddShortcut {
			tr.Finish("Ficheros copiados a " + remoteBase)
			return nil
		}

		reg := tr.snapshot()
		reg.Phase, reg.Done, reg.File = "registrando en Steam", false, ""
		reg.Message = "escribiendo shortcuts.vdf"
		tr.Report(reg)
		exeRemote := remoteBase + "/" + exeRel
		if !info.IsDir() {
			exeRemote = remoteBase + "/" + folderName
		}
		startDir := exeRemote[:strings.LastIndex(exeRemote, "/")]

		appID, viaSteam, err := s.registerShortcut(ctx, c, deck.NonSteamOptions{
			Name:          req.Name,
			Exe:           exeRemote,
			StartDir:      startDir,
			LaunchOptions: req.LaunchOptions,
			CompatTool:    req.CompatTool,
		})
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("%s anadido a Steam (id %d).", req.Name, appID)
		if viaSteam {
			msg += " Ya aparece en la biblioteca, sin reiniciar."
		}
		if req.Artwork {
			reg := tr.snapshot()
			reg.Phase, reg.Done, reg.File = "descargando caratulas", false, ""
			reg.Message = "consultando SteamGridDB"
			tr.Report(reg)
			msg += " " + s.installArtwork(ctx, c, appID, req.SteamAppID, req.Name)
		}
		if !viaSteam {
			msg += " Reinicia Steam en la Deck para verlo en la biblioteca."
		}
		tr.Finish(msg)
		return nil
	})
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID})
}

func (s *Server) handleSendROM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LocalPath string `json:"localPath"`
		RomsDir   string `json:"romsDir"`
		System    string `json:"system"`
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
	if req.LocalPath == "" || req.System == "" {
		writeErr(w, fmt.Errorf("elige la ROM y el sistema de destino"), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.cfg.LastLocal = filepath.Dir(req.LocalPath)
	cfg := s.cfg.Snapshot()
	s.mu.Unlock()
	cfg.Save()

	j, err := s.startJob("send-rom", "Enviando ROM a "+req.System, func(ctx context.Context, report deck.ProgressFunc) error {
		return c.UploadROM(ctx, req.LocalPath, req.RomsDir, req.System, report)
	})
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID})
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID  string `json:"appId"`
		Target string `json:"target"`
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
	j, err := s.startJob("move", "Moviendo juego", func(ctx context.Context, report deck.ProgressFunc) error {
		inv, err := s.inventory(ctx, c)
		if err != nil {
			return err
		}
		// Tras mover, la foto guardada miente (rutas y espacio libre): fuera.
		defer s.dropInventory()
		return c.MoveGame(ctx, inv, req.AppID, req.Target, report)
	})
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "job": j.ID})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID   string             `json:"appId"`
		Targets deck.DeleteTargets `json:"targets"`
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	inv, err := s.inventory(ctx, c)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	// Tras borrar, la foto guardada miente (juego y tamanos): fuera.
	defer s.dropInventory()
	freed, err := c.Delete(ctx, inv, req.AppID, req.Targets)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "freed": freed})
}

func (s *Server) handleCompat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID string `json:"appId"`
		Tool  string `json:"tool"`
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
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Con Steam abierto se le pide a el: escribir config.vdf por detras se
	// pierde en cuanto Steam se cierra, que era justo lo que la interfaz pedia
	// hacer para ver el cambio. Ver deck.ApplyCompatTool.
	live, err := c.ApplyCompatTool(ctx, req.AppID, req.Tool)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "live": live})
}

func (s *Server) handleRestartSteam(w http.ResponseWriter, r *http.Request) {
	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}
	// Sin timeout del request: reiniciar Steam puede pasar del minuto y no
	// queremos abortarlo a medias.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	msg, err := c.RestartSteam(ctx)
	if err != nil {
		writeErr(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": msg})
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
