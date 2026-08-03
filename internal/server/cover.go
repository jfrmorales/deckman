package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Las portadas de la biblioteca se sirven desde aqui. La interfaz no puede
// pedirlas directamente a la Deck (viven al otro lado del SSH), asi que deckman
// hace de intermediario y se las guarda en memoria: la tabla se vuelve a pintar
// con cada tecla del filtro y no tiene sentido volver a bajar las mismas
// imagenes por SSH cada vez.
const (
	// Una portada normal ronda los 50-500 KB. Por encima de esto es un arte
	// animado (los de SteamGridDB llegan a 45 MB) y no compensa bajarlo por
	// SSH para pintarlo a 44 pixeles de ancho.
	maxCoverBytes = 8 << 20
	// Techo de la cache. Con portadas de 200 KB salen unas 150 en memoria, de
	// sobra para una biblioteca grande.
	maxCoverCache = 32 << 20
)

type coverBlob struct {
	data  []byte
	ctype string
}

// coverStore guarda el indice de portadas (appid -> ruta remota) y las que ya
// se han traido.
type coverStore struct {
	mu    sync.Mutex
	index map[string]string
	blobs map[string]coverBlob
	order []string // orden de llegada: se sueltan las mas viejas primero
	bytes int

	// buildMu serializa la construccion del indice a demanda: si llegan varias
	// portadas antes del primer inventario, solo la primera peticion paga el
	// CoverIndex (decenas de globs remotos) y las demas lo encuentran hecho.
	buildMu sync.Mutex
}

func newCoverStore() *coverStore {
	return &coverStore{index: map[string]string{}, blobs: map[string]coverBlob{}}
}

// setIndex sustituye el indice tras un escaneo. Las imagenes ya cacheadas cuya
// ruta haya cambiado se sueltan: es lo que pasa al pasar de la portada de la
// tienda a una elegida a mano.
func (s *coverStore) setIndex(index map[string]string) {
	if index == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for appID, blob := range s.blobs {
		if index[appID] != s.index[appID] {
			s.bytes -= len(blob.data)
			delete(s.blobs, appID)
		}
	}
	s.index = index
}

// path devuelve la ruta remota de la portada de un juego.
func (s *coverStore) path(appID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.index[appID]
	return p, ok
}

func (s *coverStore) get(appID string) (coverBlob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[appID]
	return b, ok
}

func (s *coverStore) put(appID string, b coverBlob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ya := s.blobs[appID]; !ya {
		s.order = append(s.order, appID)
	}
	s.bytes += len(b.data)
	s.blobs[appID] = b
	for s.bytes > maxCoverCache && len(s.order) > 0 {
		viejo := s.order[0]
		// Desplazar en vez de reslicear: order[1:] arrastra el array de respaldo
		// entero, que ya no encogeria nunca.
		copy(s.order, s.order[1:])
		s.order = s.order[:len(s.order)-1]
		if old, ok := s.blobs[viejo]; ok {
			s.bytes -= len(old.data)
			delete(s.blobs, viejo)
		}
	}
}

// drop olvida la portada de un juego. Hay que llamarlo en cuanto se cambia el
// arte: si no, la tabla seguiria ensenando la imagen anterior.
func (s *coverStore) drop(appID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.blobs[appID]; ok {
		s.bytes -= len(b.data)
		delete(s.blobs, appID)
	}
	delete(s.index, appID)
}

// handleCover sirve la portada de un juego. La interfaz la pide con fetch (no
// con <img src>), asi que pasa por el mismo guard que el resto de la API.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("appId")
	if appID == "" || len(appID) > 10 || strings.Trim(appID, "0123456789") != "" {
		writeErr(w, fmt.Errorf("appId invalido"), http.StatusBadRequest)
		return
	}

	if blob, ok := s.covers.get(appID); ok {
		writeCover(w, blob)
		return
	}

	c, err := s.conn()
	if err != nil {
		writeErr(w, err, http.StatusConflict)
		return
	}

	p, ok := s.covers.path(appID)
	if !ok {
		// Sin inventario cargado todavia no hay indice: se construye a demanda,
		// de uno en uno. La interfaz pide varias portadas a la vez y sin el
		// cerrojo cada una lanzaba su propio CoverIndex contra la Deck.
		s.covers.buildMu.Lock()
		if p, ok = s.covers.path(appID); !ok {
			if index, err := c.CoverIndex(r.Context()); err == nil {
				s.covers.setIndex(index)
				p, ok = s.covers.path(appID)
			}
		}
		s.covers.buildMu.Unlock()
	}
	if !ok {
		writeErr(w, fmt.Errorf("ese juego no tiene portada"), http.StatusNotFound)
		return
	}

	fi, err := c.SFTP().Stat(p)
	if err != nil {
		writeErr(w, fmt.Errorf("no se pudo leer la portada: %w", err), http.StatusBadGateway)
		return
	}
	if fi.Size() > maxCoverBytes {
		s.logf("portada %s descartada: pesa %d bytes", p, fi.Size())
		writeErr(w, fmt.Errorf("la portada pesa demasiado para la miniatura"), http.StatusNotFound)
		return
	}
	data, err := c.ReadFile(p)
	if err != nil {
		writeErr(w, fmt.Errorf("no se pudo leer la portada: %w", err), http.StatusBadGateway)
		return
	}

	// El tipo se deduce del contenido, no del nombre: Steam solo reconoce el
	// arte por la extension del fichero, asi que deckman guarda los webp
	// animados con nombre .png (ver docs/HALLAZGOS-STEAM.md).
	blob := coverBlob{data: data, ctype: detectImageType(data)}
	s.covers.put(appID, blob)
	writeCover(w, blob)
}

func writeCover(w http.ResponseWriter, b coverBlob) {
	w.Header().Set("Content-Type", b.ctype)
	// La interfaz se queda con la imagen en un blob mientras dura la sesion; el
	// navegador no necesita cachearla y asi un cambio de arte se ve al momento.
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b.data)
}

// detectImageType olfatea el contenido. Reconoce png, jpeg, gif, webp e ico,
// que es todo lo que puede acabar en la carpeta grid.
func detectImageType(data []byte) string {
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return "application/octet-stream"
}
