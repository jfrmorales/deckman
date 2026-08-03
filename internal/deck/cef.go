package deck

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/crypto/ssh"
)

// Steam arranca su interfaz (que es Chromium empotrado) con
// --remote-debugging-port=8080 escuchando en el localhost de la Deck. Por ahi
// se le puede pedir que aplique una caratula EN CALIENTE, sin reiniciarlo.
//
// Es el mismo mecanismo que usa Decky. Ademas de evitar el reinicio es mas
// correcto: Steam lleva su propio registro del arte personalizado en
// userdata/<id>/config/librarycache/<appid>.json, y escribir el fichero por
// detras se lo salta.
const cefPort = 8080

// ArtAssetType es el codigo que usa Steam para cada tipo de arte.
//
// Averiguado a mano contra una Deck real, llamando uno por uno y mirando que
// fichero aparecia: no esta documentado en ningun sitio.
//
//	0 -> <appid>p.png      portada vertical
//	1 -> <appid>_hero.png  fondo
//	2 -> <appid>_logo.png  logo
//	3 -> <appid>.png       portada horizontal
//	4 -> no hace nada      (los iconos no pasan por esta API)
var artAssetType = map[string]int{
	"grid":  0,
	"hero":  1,
	"logo":  2,
	"gridh": 3,
}

// SupportsLiveArtwork indica si un tipo de arte se puede aplicar en caliente.
func SupportsLiveArtwork(kind string) bool {
	_, ok := artAssetType[kind]
	return ok
}

// cefTarget es una pestana del depurador de Steam.
type cefTarget struct {
	Title   string `json:"title"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	WSDebug string `json:"webSocketDebuggerUrl"`
}

// cefHTTPClient devuelve el cliente HTTP que sale por el tunel SSH hasta el
// localhost de la Deck. El puerto 8080 solo escucha ahi, no en la red.
//
// Es UNO por conexion y se reutiliza a proposito. Antes se construia un
// http.Client con su Transport en cada llamada: cada Transport se queda con sus
// conexiones ociosas y nadie las cerraba nunca, y aqui cada conexion es un
// canal SSH abierto. Con el uso normal (galeria de caratulas) se iban acumulando
// hasta agotar los canales que admite el sshd de la Deck.
func (c *Client) cefHTTPClient() *http.Client {
	if c.cefHTTP != nil {
		return c.cefHTTP
	}
	// Un Client armado sin Connect (pruebas): uno suelto antes que nada.
	return newCEFClient(c.ssh)
}

// newCEFClient arma el cliente HTTP tunelizado de una conexion.
func newCEFClient(sshc *ssh.Client) *http.Client {
	return &http.Client{
		// coder/websocket usa este plazo solo para el saludo inicial y luego se
		// queda con una copia del cliente sin Timeout, asi que no corta la
		// subida de una caratula animada de 45 MB.
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// ssh.Client.Dial no admite contexto, asi que lo respetamos por
				// fuera: si no, cancelar deja la peticion esperando a un canal
				// SSH que quiza no llegue nunca.
				type result struct {
					conn net.Conn
					err  error
				}
				ch := make(chan result, 1)
				go func() {
					conn, err := sshc.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", cefPort))
					ch <- result{conn, err}
				}()
				select {
				case <-ctx.Done():
					// El Dial sigue su curso: cerramos lo que llegue tarde para
					// no dejar el canal abierto.
					go func() {
						if r := <-ch; r.conn != nil {
							r.conn.Close()
						}
					}()
					return nil, ctx.Err()
				case r := <-ch:
					return r.conn, r.err
				}
			},
		},
	}
}

// cefTargets lista las pestanas del depurador.
func (c *Client) cefTargets(ctx context.Context) ([]cefTarget, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/json", cefPort), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.cefHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("el depurador de Steam no responde: %w", err)
	}
	defer resp.Body.Close()

	var targets []cefTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("respuesta inesperada del depurador: %w", err)
	}
	return targets, nil
}

// sharedJSContext localiza la pestana que tiene la API de Steam. Es la unica
// con SteamClient.Apps: las demas (menus, avisos) solo llevan un SteamClient
// recortado.
func (c *Client) sharedJSContext(ctx context.Context) (cefTarget, error) {
	targets, err := c.cefTargets(ctx)
	if err != nil {
		return cefTarget{}, err
	}
	for _, t := range targets {
		if t.Title == "SharedJSContext" && t.WSDebug != "" {
			return t, nil
		}
	}
	return cefTarget{}, fmt.Errorf("no se encontro SharedJSContext; puede que Steam aun este arrancando")
}

var cefMsgID atomic.Int64

// cefEval ejecuta JavaScript dentro de Steam y espera al resultado.
func (c *Client) cefEval(ctx context.Context, expr string) (json.RawMessage, error) {
	target, err := c.sharedJSContext(ctx)
	if err != nil {
		return nil, err
	}

	conn, _, err := websocket.Dial(ctx, target.WSDebug, &websocket.DialOptions{
		HTTPClient: c.cefHTTPClient(),
	})
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir el canal con Steam: %w", err)
	}
	defer conn.CloseNow()

	// Las caratulas animadas rondan los 40 MB, que en base64 son unos 55.
	conn.SetReadLimit(96 << 20)

	id := cefMsgID.Add(1)
	req := map[string]any{
		"id":     id,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    expr,
			"awaitPromise":  true,
			"returnByValue": true,
		},
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		return nil, fmt.Errorf("no se pudo enviar la orden a Steam: %w", err)
	}

	// Steam manda tambien eventos por el mismo canal: hay que ir leyendo hasta
	// dar con la respuesta a nuestra peticion.
	for {
		var resp struct {
			ID     int64 `json:"id"`
			Result struct {
				Result struct {
					Value json.RawMessage `json:"value"`
				} `json:"result"`
				ExceptionDetails *struct {
					Text      string `json:"text"`
					Exception struct {
						Description string `json:"description"`
					} `json:"exception"`
				} `json:"exceptionDetails"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := wsjson.Read(ctx, conn, &resp); err != nil {
			return nil, fmt.Errorf("Steam corto la comunicacion: %w", err)
		}
		if resp.ID != id {
			continue // un evento suyo, no es lo nuestro
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("Steam rechazo la orden: %s", resp.Error.Message)
		}
		if d := resp.Result.ExceptionDetails; d != nil {
			msg := d.Exception.Description
			if msg == "" {
				msg = d.Text
			}
			return nil, fmt.Errorf("error dentro de Steam: %s", msg)
		}
		return resp.Result.Result.Value, nil
	}
}

// CEFAvailable indica si se puede hablar con Steam en caliente.
func (c *Client) CEFAvailable(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	_, err := c.sharedJSContext(checkCtx)
	return err == nil
}

// ApplyArtworkLive instala una caratula usando la propia API de Steam, que la
// guarda, actualiza su registro interno y refresca la biblioteca al momento.
//
// Devuelve un error si el tipo de arte no lo admite (los iconos) o si Steam no
// esta accesible; en ambos casos hay que recurrir a escribir el fichero.
func (c *Client) ApplyArtworkLive(ctx context.Context, appID uint32, kind, ext string, data []byte) error {
	assetType, ok := artAssetType[kind]
	if !ok {
		return fmt.Errorf("Steam no admite cambiar %q en caliente", kind)
	}

	// Steam quiere la extension sin punto, y solo entiende png o jpg.
	imgType := strings.TrimPrefix(steamExt(ext), ".")
	if imgType != "jpg" {
		imgType = "png"
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	// El base64 va como argumento de una funcion, no concatenado en el codigo:
	// asi no hay que escapar nada ni cabe una inyeccion.
	expr := fmt.Sprintf(
		`(async () => { await SteamClient.Apps.SetCustomArtworkForApp(%d, %s, %s, %d); return "ok"; })()`,
		appID, jsString(b64), jsString(imgType), assetType)

	got, err := c.cefEval(ctx, expr)
	if err != nil {
		return err
	}
	var s string
	if err := json.Unmarshal(got, &s); err != nil || s != "ok" {
		return fmt.Errorf("Steam no confirmo el cambio (respondio %s)", string(got))
	}
	return nil
}

// ClearArtworkLive quita una caratula por la via de Steam.
func (c *Client) ClearArtworkLive(ctx context.Context, appID uint32, kind string) error {
	assetType, ok := artAssetType[kind]
	if !ok {
		return fmt.Errorf("Steam no admite quitar %q en caliente", kind)
	}
	expr := fmt.Sprintf(
		`(async () => { await SteamClient.Apps.ClearCustomArtworkForApp(%d, %d); return "ok"; })()`,
		appID, assetType)
	_, err := c.cefEval(ctx, expr)
	return err
}

// jsString convierte un texto en un literal de JavaScript seguro.
func jsString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// AddShortcutLive registra un juego no-Steam usando la API del propio Steam.
//
// Es la unica forma segura de hacerlo con Steam abierto. Steam mantiene la
// lista de accesos directos EN MEMORIA y reescribe shortcuts.vdf al salir, asi
// que editar el fichero por detras se pierde en el mejor caso y, si Steam
// vuelca un estado a medias, se lleva por delante los demas accesos directos.
// Pasado de verdad en una Deck: se anadio un juego, Steam se reinicio y el
// fichero quedo con una sola entrada de siete.
//
// Devuelve el appid que asigna Steam, que es aleatorio.
func (c *Client) AddShortcutLive(ctx context.Context, name, exe, startDir, launchOptions, compatTool string) (uint32, error) {
	if name == "" || exe == "" {
		return 0, fmt.Errorf("hacen falta el nombre y el ejecutable")
	}
	if startDir == "" {
		startDir = path.Dir(exe)
	}

	expr := fmt.Sprintf(`(async () => {
		const appid = await SteamClient.Apps.AddShortcut(%s, %s, %s, %s);
		if (!appid) throw new Error("Steam no devolvio un identificador");
		await SteamClient.Apps.SetShortcutName(appid, %s);
		await SteamClient.Apps.SetShortcutStartDir(appid, %s);
		await SteamClient.Apps.SetShortcutLaunchOptions(appid, %s);
		%s
		return appid;
	})()`,
		jsString(name), jsString(exe), jsString(startDir), jsString(launchOptions),
		jsString(name), jsString(startDir), jsString(launchOptions),
		compatToolCall(compatTool))

	got, err := c.cefEval(ctx, expr)
	if err != nil {
		return 0, err
	}
	var appID uint32
	if err := json.Unmarshal(got, &appID); err != nil || appID == 0 {
		return 0, fmt.Errorf("Steam devolvio un identificador raro: %s", string(got))
	}
	return appID, nil
}

func compatToolCall(tool string) string {
	if tool == "" {
		return ""
	}
	return fmt.Sprintf("await SteamClient.Apps.SpecifyCompatTool(appid, %s);", jsString(tool))
}

// RemoveShortcutLive quita un acceso directo por la via de Steam.
func (c *Client) RemoveShortcutLive(ctx context.Context, appID uint32) error {
	expr := fmt.Sprintf(`(async () => { await SteamClient.Apps.RemoveShortcut(%d); return "ok"; })()`, appID)
	_, err := c.cefEval(ctx, expr)
	return err
}

// SetShortcutPathLive cambia el ejecutable y la carpeta de trabajo de un acceso
// directo por la via de Steam. Es lo que hace falta tras mover un juego no-Steam
// a otra unidad con Steam abierto: tocar shortcuts.vdf por detras se perderia al
// cerrar Steam, y de paso puede llevarse los demas accesos directos.
//
// El icono no se toca: no siempre existe un SetShortcutIcon utilizable y es
// puramente estetico. Si apuntaba dentro de la carpeta movida, se arregla solo
// la proxima vez que se cambie el arte.
func (c *Client) SetShortcutPathLive(ctx context.Context, appID uint32, exe, startDir string) error {
	if exe == "" || startDir == "" {
		return fmt.Errorf("hacen falta el ejecutable y la carpeta")
	}
	expr := fmt.Sprintf(`(async () => {
		await SteamClient.Apps.SetShortcutExe(%d, %s);
		await SteamClient.Apps.SetShortcutStartDir(%d, %s);
		return "ok";
	})()`, appID, jsString(exe), appID, jsString(startDir))

	got, err := c.cefEval(ctx, expr)
	if err != nil {
		return err
	}
	var s string
	if err := json.Unmarshal(got, &s); err != nil || s != "ok" {
		return fmt.Errorf("Steam no confirmo el cambio (respondio %s)", string(got))
	}
	return nil
}

// SetCompatToolLive asigna la version de Proton por la via de Steam.
func (c *Client) SetCompatToolLive(ctx context.Context, appID uint32, tool string) error {
	expr := fmt.Sprintf(`(async () => { await SteamClient.Apps.SpecifyCompatTool(%d, %s); return "ok"; })()`,
		appID, jsString(tool))
	_, err := c.cefEval(ctx, expr)
	return err
}
