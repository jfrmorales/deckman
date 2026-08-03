// Package meta consulta informacion de un juego en servicios publicos: la
// tienda de Steam para el nombre oficial, ProtonDB para saber como funciona en
// Linux y SteamGridDB para las caratulas.
//
// Todo es opcional y tolerante a fallos: si un servicio no responde, deckman
// sigue funcionando con lo que haya deducido en local.
package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Client agrupa las consultas. GridKey es la clave de SteamGridDB; si va
// vacia, simplemente no se buscan caratulas.
type Client struct {
	HTTP    *http.Client
	GridKey string

	// AllowURL, si esta puesto, decide de que servidores se acepta descargar
	// una imagen. Lo pone el servidor (es el quien conoce la lista blanca) y se
	// aplica tambien a CADA salto de una redireccion: sin eso, un 302 desde
	// SteamGridDB convertiria a deckman en un proxy hacia cualquier sitio.
	AllowURL func(rawURL string) error
}

// artRequestHeader marca las peticiones de descarga de imagenes. Sirve para que
// el filtro de redirecciones se aplique solo a ellas: las consultas a las APIs
// (tienda de Steam, ProtonDB) van a otros dominios legitimos y romperlas seria
// un remedio peor que la enfermedad.
const artRequestHeader = "X-Deckman-Artwork"

// New crea un cliente.
//
// Sin Timeout global a proposito: ese plazo cubre TAMBIEN la lectura del
// cuerpo, y una caratula animada ronda los 45 MB, asi que cortaba las descargas
// grandes a media. Cada llamada trae su propio contexto con plazo.
func New(gridKey string) *Client {
	c := &Client{GridKey: gridKey}
	c.HTTP = &http.Client{CheckRedirect: c.checkRedirect}
	return c
}

// checkRedirect revalida el destino de cada salto de una descarga de imagen.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("demasiadas redirecciones")
	}
	if len(via) == 0 || via[0].Header.Get(artRequestHeader) == "" {
		return nil // no es una descarga de imagen
	}
	if c.AllowURL == nil {
		return nil
	}
	if err := c.AllowURL(req.URL.String()); err != nil {
		return fmt.Errorf("la descarga redirige a un sitio no permitido: %w", err)
	}
	return nil
}

// Match es un juego candidato de la busqueda en la tienda.
type Match struct {
	AppID string `json:"appId"`
	Name  string `json:"name"`
}

// GameInfo es lo que se ha averiguado en la red.
type GameInfo struct {
	AppID       string  `json:"appId"`
	Name        string  `json:"name"`
	Developer   string  `json:"developer,omitempty"`
	HeaderImage string  `json:"headerImage,omitempty"`
	NativeLinux bool    `json:"nativeLinux"`
	Matches     []Match `json:"matches,omitempty"` // alternativas, por si acierta mal
	Proton      *Proton `json:"proton,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// Proton resume el veredicto de ProtonDB.
type Proton struct {
	Tier       string `json:"tier"`
	BestTier   string `json:"bestTier"`
	Confidence string `json:"confidence"`
	Reports    int    `json:"reports"`
	Suggested  string `json:"suggested"` // familia sugerida: "experimental" o "ge"
	Advice     string `json:"advice"`
}

func (c *Client) getJSON(ctx context.Context, rawURL string, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "deckman/1.0 (+gestor local de Steam Deck)")
	req.Header.Set("Accept", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("%s devolvio HTTP %d: %s", hostOf(rawURL), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return raw
}

// --- tienda de Steam ---

// SearchStore busca un juego por nombre. Devuelve los candidatos en el orden
// que da la tienda, que ya prioriza por relevancia.
func (c *Client) SearchStore(ctx context.Context, term string) ([]Match, error) {
	if strings.TrimSpace(term) == "" {
		return nil, nil
	}
	u := "https://store.steampowered.com/api/storesearch/?term=" +
		url.QueryEscape(term) + "&cc=es&l=spanish"

	var out struct {
		Items []struct {
			ID   json.Number `json:"id"`
			Name string      `json:"name"`
			Type string      `json:"type"`
		} `json:"items"`
	}
	if err := c.getJSON(ctx, u, nil, &out); err != nil {
		return nil, err
	}
	var matches []Match
	for _, it := range out.Items {
		if it.Type != "" && it.Type != "app" && it.Type != "game" {
			continue
		}
		matches = append(matches, Match{AppID: it.ID.String(), Name: it.Name})
	}
	return matches, nil
}

// AppDetails pide la ficha de un appid concreto.
func (c *Client) AppDetails(ctx context.Context, appID string) (*GameInfo, error) {
	if _, err := strconv.Atoi(appID); err != nil {
		return nil, fmt.Errorf("appid invalido: %q", appID)
	}
	u := "https://store.steampowered.com/api/appdetails?appids=" + appID + "&l=spanish"

	var out map[string]struct {
		Success bool `json:"success"`
		Data    struct {
			Name        string   `json:"name"`
			Type        string   `json:"type"`
			Developers  []string `json:"developers"`
			HeaderImage string   `json:"header_image"`
			Platforms   struct {
				Windows bool `json:"windows"`
				Linux   bool `json:"linux"`
			} `json:"platforms"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, u, nil, &out); err != nil {
		return nil, err
	}
	entry, ok := out[appID]
	if !ok || !entry.Success {
		return nil, fmt.Errorf("Steam no reconoce el appid %s", appID)
	}
	info := &GameInfo{
		AppID:       appID,
		Name:        entry.Data.Name,
		HeaderImage: entry.Data.HeaderImage,
		NativeLinux: entry.Data.Platforms.Linux,
	}
	if len(entry.Data.Developers) > 0 {
		info.Developer = entry.Data.Developers[0]
	}
	return info, nil
}

// --- ProtonDB ---

// ProtonDB consulta como se comporta el juego bajo Proton.
func (c *Client) ProtonDB(ctx context.Context, appID string) (*Proton, error) {
	if _, err := strconv.Atoi(appID); err != nil {
		return nil, fmt.Errorf("appid invalido: %q", appID)
	}
	u := "https://www.protondb.com/api/v1/reports/summaries/" + appID + ".json"

	var out struct {
		Tier             string `json:"tier"`
		BestReportedTier string `json:"bestReportedTier"`
		Confidence       string `json:"confidence"`
		Total            int    `json:"total"`
	}
	if err := c.getJSON(ctx, u, nil, &out); err != nil {
		return nil, err
	}
	p := &Proton{
		Tier:       out.Tier,
		BestTier:   out.BestReportedTier,
		Confidence: out.Confidence,
		Reports:    out.Total,
	}
	p.Suggested, p.Advice = protonAdvice(out.Tier, out.BestReportedTier, out.Total)
	return p, nil
}

// protonAdvice traduce el veredicto de ProtonDB a una recomendacion.
//
// Nunca es una certeza: ProtonDB agrega reportes de hardware muy variado. Por
// eso devolvemos tambien un texto que explique en que nos basamos, para que la
// decision final sea del usuario.
func protonAdvice(tier, best string, reports int) (suggested, advice string) {
	switch strings.ToLower(tier) {
	case "platinum":
		return "experimental", fmt.Sprintf("funciona de fabrica segun %d reportes; Proton Experimental deberia bastar", reports)
	case "gold":
		return "experimental", fmt.Sprintf("funciona bien con algun ajuste menor (%d reportes); empieza por Proton Experimental", reports)
	case "silver":
		return "ge", fmt.Sprintf("funciona con fallos segun %d reportes; GE-Proton suele ir mejor en estos casos", reports)
	case "bronze":
		return "ge", fmt.Sprintf("da bastantes problemas (%d reportes); prueba GE-Proton y espera tener que trastear", reports)
	case "borked":
		advice = fmt.Sprintf("marcado como no funcional en Linux (%d reportes)", reports)
		if strings.EqualFold(best, "platinum") || strings.EqualFold(best, "gold") {
			advice += ", aunque alguien lo ha conseguido: prueba GE-Proton"
		}
		return "ge", advice
	case "native":
		return "", "tiene version nativa de Linux: no necesita Proton"
	}
	return "experimental", "sin datos suficientes en ProtonDB; Proton Experimental es la apuesta razonable"
}

// --- orquestacion ---

// Lookup resuelve un juego. Si se conoce el appid (por venir embebido en la
// carpeta) va directo a por la ficha; si no, busca por nombre.
//
// Los errores de red no se propagan como fallo: se devuelven dentro de Note,
// porque enviar el juego tiene que poder seguir adelante igualmente.
func (c *Client) Lookup(ctx context.Context, appID, name string) *GameInfo {
	// Name se deja vacio a proposito: solo se rellena si la tienda responde.
	// Asi quien lo consume distingue un nombre oficial de nuestra conjetura.
	info := &GameInfo{AppID: appID}

	if appID == "" {
		matches, err := c.SearchStore(ctx, name)
		if err != nil {
			info.Note = "no se pudo buscar en Steam: " + err.Error()
			return info
		}
		if len(matches) == 0 {
			info.Note = fmt.Sprintf("Steam no encuentra ningun juego llamado %q (puede estar retirado de la tienda)", name)
			return info
		}
		info.Matches = matches
		appID = matches[0].AppID
	}

	details, err := c.AppDetails(ctx, appID)
	if err != nil {
		info.AppID = appID
		info.Note = err.Error()
	} else {
		matches := info.Matches
		note := info.Note
		*info = *details
		info.Matches = matches
		info.Note = note
	}

	if p, err := c.ProtonDB(ctx, info.AppID); err == nil {
		info.Proton = p
	} else if info.Note == "" {
		info.Note = "sin datos de ProtonDB"
	}
	if info.NativeLinux && info.Proton != nil {
		info.Proton.Suggested = ""
		info.Proton.Advice = "tiene version nativa de Linux, pero esta carpeta es la de Windows: usa Proton igualmente"
	}
	return info
}
