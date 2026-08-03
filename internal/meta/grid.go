package meta

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
)

// Artwork es una imagen de portada elegida para instalar.
//
// El nombre del fichero destino NO se decide aqui: esa convencion pertenece al
// paquete deck, que es quien conoce la estructura de carpetas de Steam.
type Artwork struct {
	Kind string `json:"kind"` // grid, gridh, hero, logo, icon
	URL  string `json:"url"`
	Ext  string `json:"ext"`
}

// HasGridKey indica si hay clave configurada.
func (c *Client) HasGridKey() bool { return strings.TrimSpace(c.GridKey) != "" }

func (c *Client) gridAuth() map[string]string {
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(c.GridKey)}
}

// GridGameID localiza el juego en SteamGridDB a partir de su appid de Steam.
func (c *Client) GridGameID(ctx context.Context, steamAppID string) (int, error) {
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Errors []string `json:"errors"`
	}
	u := "https://www.steamgriddb.com/api/v2/games/steam/" + url.PathEscape(steamAppID)
	if err := c.getJSON(ctx, u, c.gridAuth(), &out); err != nil {
		return 0, err
	}
	if !out.Success || out.Data.ID == 0 {
		return 0, fmt.Errorf("SteamGridDB no tiene ese juego: %s", strings.Join(out.Errors, ", "))
	}
	return out.Data.ID, nil
}

// GridSearchByName busca el juego por nombre, para cuando no hay appid.
func (c *Client) GridSearchByName(ctx context.Context, name string) (int, error) {
	var out struct {
		Success bool `json:"success"`
		Data    []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	u := "https://www.steamgriddb.com/api/v2/search/autocomplete/" + url.PathEscape(name)
	if err := c.getJSON(ctx, u, c.gridAuth(), &out); err != nil {
		return 0, err
	}
	if !out.Success || len(out.Data) == 0 {
		return 0, fmt.Errorf("SteamGridDB no encuentra %q", name)
	}
	return out.Data[0].ID, nil
}

// GridGame es un juego del catalogo de SteamGridDB.
type GridGame struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// SearchGames busca juegos por nombre y devuelve varios candidatos, para que
// sea el usuario quien decida cual es el suyo.
func (c *Client) SearchGames(ctx context.Context, name string) ([]GridGame, error) {
	var out struct {
		Success bool       `json:"success"`
		Data    []GridGame `json:"data"`
	}
	u := "https://www.steamgriddb.com/api/v2/search/autocomplete/" + url.PathEscape(name)
	if err := c.getJSON(ctx, u, c.gridAuth(), &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("SteamGridDB rechazo la busqueda")
	}
	return out.Data, nil
}

// ArtOption es una imagen concreta que se puede elegir.
type ArtOption struct {
	ID       int    `json:"id"`
	URL      string `json:"url"`   // la imagen a instalar
	Thumb    string `json:"thumb"` // miniatura para la galeria
	Style    string `json:"style"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Mime     string `json:"mime"`
	Ext      string `json:"ext"` // extension con la que guardarla
	Animated bool   `json:"animated"`
	Author   string `json:"author,omitempty"`
	Upvotes  int    `json:"upvotes"`
}

// extFor decide la extension con la que guardar la imagen.
//
// Los webp y los apng se guardan como .png a proposito: Steam decide si un
// fichero es arte por la extension, y .webp lo ignora. El contenido se deja
// tal cual, que Steam lo decodifica sin problema.
func (a ArtOption) extFor() string {
	switch a.Mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png", "image/webp", "image/apng":
		return ".png"
	}
	ext := strings.ToLower(path.Ext(a.URL))
	if i := strings.IndexByte(ext, '?'); i >= 0 {
		ext = ext[:i]
	}
	switch ext {
	case ".jpg", ".jpeg":
		return ".jpg"
	case ".ico":
		return ".ico"
	}
	return ".png"
}

// isAnimated distingue las imagenes animadas.
//
// SteamGridDB sirve las animadas como .webp y su miniatura como un VIDEO .webm,
// no como una imagen. Eso obliga a pintarlas con <video> en vez de <img>, asi
// que hay que marcarlas bien o la galeria muestra huecos en blanco.
//
// La extension de la miniatura es la senal fiable: el mime image/webp tambien
// lo llevan algunas estaticas.
func isAnimated(mime, thumb string) bool {
	t := strings.ToLower(thumb)
	if i := strings.IndexByte(t, '?'); i >= 0 {
		t = t[:i]
	}
	if strings.HasSuffix(t, ".webm") || strings.HasSuffix(t, ".mp4") {
		return true
	}
	return mime == "image/apng"
}

// artKinds describe cada tipo de arte: a que endpoint de SteamGridDB
// corresponde y con que medidas lo quiere Steam.
var artKinds = map[string]struct {
	Endpoint   string
	Dimensions string
	Label      string
}{
	"grid":  {"grids", "600x900,342x482,660x930", "Portada vertical"},
	"gridh": {"grids", "920x430,460x215", "Portada horizontal"},
	"hero":  {"heroes", "1920x620,3840x1240,1600x650", "Fondo"},
	"logo":  {"logos", "", "Logo"},
	"icon":  {"icons", "", "Icono"},
}

// ArtKinds devuelve los tipos de arte admitidos y su nombre para la interfaz.
func ArtKinds() map[string]string {
	out := map[string]string{}
	for k, v := range artKinds {
		out[k] = v.Label
	}
	return out
}

// ListArtwork trae las opciones disponibles de un tipo de arte.
func (c *Client) ListArtwork(ctx context.Context, gameID int, kind string, animated bool) ([]ArtOption, error) {
	spec, ok := artKinds[kind]
	if !ok {
		return nil, fmt.Errorf("tipo de arte desconocido: %q", kind)
	}

	q := url.Values{}
	if spec.Dimensions != "" {
		q.Set("dimensions", spec.Dimensions)
	}
	// Los animados solo los pedimos si se piden: Steam los admite, pero pesan
	// mucho mas y no todo el mundo los quiere.
	if animated {
		q.Set("types", "static,animated")
	} else {
		q.Set("types", "static")
	}
	q.Set("nsfw", "false")

	u := fmt.Sprintf("https://www.steamgriddb.com/api/v2/%s/game/%d?%s", spec.Endpoint, gameID, q.Encode())

	var out struct {
		Success bool `json:"success"`
		Data    []struct {
			ID      int    `json:"id"`
			Score   int    `json:"score"`
			Style   string `json:"style"`
			Width   int    `json:"width"`
			Height  int    `json:"height"`
			Mime    string `json:"mime"`
			URL     string `json:"url"`
			Thumb   string `json:"thumb"`
			Upvotes int    `json:"upvotes"`
			Author  struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"data"`
		Errors []string `json:"errors"`
	}
	if err := c.getJSON(ctx, u, c.gridAuth(), &out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("SteamGridDB: %s", strings.Join(out.Errors, ", "))
	}

	opts := make([]ArtOption, 0, len(out.Data))
	for _, d := range out.Data {
		o := ArtOption{
			ID: d.ID, URL: d.URL, Thumb: d.Thumb, Style: d.Style,
			Width: d.Width, Height: d.Height, Mime: d.Mime,
			Author: d.Author.Name, Upvotes: d.Upvotes,
		}
		o.Animated = isAnimated(d.Mime, d.Thumb)
		o.Ext = o.extFor()
		opts = append(opts, o)
	}

	// Si se han pedido animadas, van delante: son pocas entre las 50 que
	// devuelve la API, y quien marca esa casilla es justo lo que busca.
	if animated {
		sort.SliceStable(opts, func(i, j int) bool {
			return opts[i].Animated && !opts[j].Animated
		})
	}
	return opts, nil
}

type gridListResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		URL   string `json:"url"`
		Style string `json:"style"`
	} `json:"data"`
}

// first devuelve la primera imagen de un endpoint de SteamGridDB.
func (c *Client) first(ctx context.Context, endpoint string, gameID int, query, kind string) *Artwork {
	u := fmt.Sprintf("https://www.steamgriddb.com/api/v2/%s/game/%d", endpoint, gameID)
	if query != "" {
		u += "?" + query
	}
	var out gridListResponse
	if err := c.getJSON(ctx, u, c.gridAuth(), &out); err != nil || !out.Success || len(out.Data) == 0 {
		return nil
	}
	raw := out.Data[0].URL
	ext := strings.ToLower(path.Ext(raw))
	if i := strings.IndexByte(ext, '?'); i >= 0 {
		ext = ext[:i]
	}
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
		ext = ".png"
	}
	return &Artwork{Kind: kind, URL: raw, Ext: ext}
}

// FetchArtwork reune las cuatro imagenes que usa la biblioteca de Steam.
// Las que no existan simplemente no vienen; nunca es un error.
//
// Las cuatro consultas son independientes y van a la vez: en serie, con la
// latencia tipica hasta SteamGridDB, eran ~600 ms solo de metadatos antes de
// empezar a descargar nada. El http.Client comparte su pool de conexiones.
func (c *Client) FetchArtwork(ctx context.Context, gameID int) []Artwork {
	// Los tamanos son los que espera Steam. "static" descarta los animados,
	// que la Deck no muestra en la vista de biblioteca.
	specs := []struct{ endpoint, query, kind string }{
		{"grids", "dimensions=600x900&types=static", "grid"},
		{"grids", "dimensions=920x430,460x215&types=static", "gridh"},
		{"heroes", "types=static", "hero"},
		{"logos", "types=static", "logo"},
	}
	results := make([]*Artwork, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.first(ctx, s.endpoint, gameID, s.query, s.kind)
		}()
	}
	wg.Wait()

	// El orden de specs se conserva: la interfaz y los mensajes lo esperan.
	var out []Artwork
	for _, a := range results {
		if a != nil {
			out = append(out, *a)
		}
	}
	return out
}

// maxArtworkBytes es el techo de una descarga. Las animadas son enormes: un
// fondo animado de 3840x1240 ronda los 45 MB, asi que el limite tiene que
// dejarlas pasar con holgura.
const maxArtworkBytes = 128 << 20

// Download baja una imagen.
//
// Comprueba que llega COMPLETA. Antes se recortaba en seco al llegar al limite
// y se instalaba un fichero a medias que Steam no podia mostrar; el fallo no
// se veia porque la cabecera del webp seguia siendo valida.
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "deckman/1.0")
	// Marca para que checkRedirect revise cada salto contra la lista blanca.
	req.Header.Set(artRequestHeader, "1")
	if c.AllowURL != nil {
		if err := c.AllowURL(rawURL); err != nil {
			return nil, err
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("la descarga devolvio HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArtworkBytes {
		return nil, fmt.Errorf("la imagen pesa %d MB, por encima del limite de %d MB",
			resp.ContentLength>>20, maxArtworkBytes>>20)
	}

	// Un byte mas que el limite: si lo alcanzamos, es que no cabia.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtworkBytes+1))
	if err != nil {
		return nil, fmt.Errorf("descarga interrumpida tras %d bytes: %w", len(data), err)
	}
	if len(data) > maxArtworkBytes {
		return nil, fmt.Errorf("la imagen supera el limite de %d MB", maxArtworkBytes>>20)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("imagen vacia")
	}
	// Si el servidor dijo cuanto pesaba, tiene que cuadrar. Cubre tambien un
	// corte de red a mitad de descarga.
	if resp.ContentLength > 0 && int64(len(data)) != resp.ContentLength {
		return nil, fmt.Errorf("descarga incompleta: %d de %d bytes", len(data), resp.ContentLength)
	}
	return data, nil
}

// ResolveGridGame localiza el juego en SteamGridDB por appid y, si falla, por
// nombre.
func (c *Client) ResolveGridGame(ctx context.Context, steamAppID, name string) (int, error) {
	if steamAppID != "" {
		if id, err := c.GridGameID(ctx, steamAppID); err == nil {
			return id, nil
		}
	}
	if name != "" {
		return c.GridSearchByName(ctx, name)
	}
	return 0, fmt.Errorf("hacen falta el appid o el nombre")
}

// HeadSize consulta cuanto pesa un fichero sin descargarlo. Devuelve 0 si el
// servidor no lo dice.
func (c *Client) HeadSize(ctx context.Context, rawURL string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "deckman/1.0")
	req.Header.Set(artRequestHeader, "1")
	if c.AllowURL != nil {
		if err := c.AllowURL(rawURL); err != nil {
			return 0, err
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d al consultar el tamano", resp.StatusCode)
	}
	if resp.ContentLength < 0 {
		return 0, nil
	}
	return resp.ContentLength, nil
}
