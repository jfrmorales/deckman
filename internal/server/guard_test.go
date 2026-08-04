package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jfrmorales/deckman/internal/i18n"
)

// Estas pruebas cubren el borde HTTP, que es la unica parte de deckman
// expuesta a algo que no ha escrito el usuario: cualquier pagina abierta en
// otra pestaña del navegador puede intentar hablar con 127.0.0.1:8777.
//
// La regresion que las justifica ya paso: la cabecera propia solo se exigia en
// POST, y rutas con efecto (reiniciar Steam, cancelar, desconectar) contestaban
// igual a un GET. Con eso, un <img src="http://127.0.0.1:8777/api/restart-steam">
// en cualquier web reiniciaba Steam en la Deck. Por eso la prueba principal no
// lleva una lista de rutas escrita a mano: las saca del propio codigo, para que
// una ruta nueva quede cubierta sin que nadie se acuerde de venir aqui.

// nuevoServidorDePrueba monta un Server sin tocar la configuracion real.
func nuevoServidorDePrueba(t *testing.T) *Server {
	t.Helper()
	// config.Load() lee la carpeta de configuracion del usuario; apuntandola a
	// un temporal, la prueba ni la lee ni la ensucia.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return New()
}

// rutasAPI saca del codigo las rutas registradas, para no mantener la lista a
// mano. Si mañana se añade /api/loquesea, esta prueba la exige igual.
func rutasAPI(t *testing.T) []string {
	t.Helper()
	fuente, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("leyendo server.go: %v", err)
	}
	re := regexp.MustCompile(`mux\.HandleFunc\("(/api/[^"]+)"`)
	var rutas []string
	for _, m := range re.FindAllStringSubmatch(string(fuente), -1) {
		rutas = append(rutas, m[1])
	}
	if len(rutas) < 20 {
		t.Fatalf("se han encontrado solo %d rutas; la extraccion esta rota", len(rutas))
	}
	return rutas
}

// Sin la cabecera propia no se atiende nada, ni con GET ni con POST. Es lo que
// impide que otra pestaña dispare ordenes: no puede ponerla sin un preflight
// CORS que no se concede.
func TestSinCabeceraNoSeAtiendeNingunaRuta(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()

	for _, ruta := range rutasAPI(t) {
		if ruta == "/api/events" {
			// Fuera aposta: EventSource no puede mandar cabeceras. Es solo
			// lectura y desde otro origen el navegador no entrega los datos.
			continue
		}
		for _, metodo := range []string{http.MethodGet, http.MethodPost} {
			req := httptest.NewRequest(metodo, ruta, strings.NewReader("{}"))
			req.Host = "127.0.0.1:8777"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s sin cabecera = %d, se esperaba 403", metodo, ruta, w.Code)
			}
		}
	}
}

// Con la cabecera puesta, la ruta existe y contesta algo que no es 403. No se
// mira el que: sin Deck conectada casi todas responden un error, y lo que se
// esta comprobando es que la puerta se abre con la llave correcta.
func TestConCabeceraLaRutaResponde(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.Host = "127.0.0.1:8777"
	req.Header.Set("X-Deckman", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/state = %d, se esperaba 200", w.Code)
	}
	var estado map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &estado); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (%q)", err, w.Body.String())
	}
	if estado["connected"] != false {
		t.Errorf("sin Deck, connected = %v", estado["connected"])
	}
}

// La cabecera Host es lo que corta el DNS rebinding: un dominio del atacante
// que resuelve a 127.0.0.1 pasa el filtro del navegador (mismo origen) y SI
// puede mandar la cabecera propia. Lo que no puede es cambiar el Host.
func TestHostQueNoEsLocalhostSeRechaza(t *testing.T) {
	casos := map[string]bool{ // host -> se acepta
		"127.0.0.1:8777":            true,
		"localhost:8777":            true,
		"LOCALHOST:8777":            true, // el navegador puede mandarlo asi
		"[::1]:8777":                true,
		"127.0.0.1":                 true, // sin puerto
		"malo.example.com:8777":     false,
		"127.0.0.1.malo.example":    false, // el clasico: acaba pareciendose
		"localhost.malo.example":    false,
		"192.168.1.50:8777":         false, // la IP del PC en la LAN tampoco
		"":                          false,
		"xn--localhost-1234.evil":   false,
		"evil.com:8777@127.0.0.1":   false,
		"0.0.0.0:8777":              false,
		"deckman.internal.lan:8777": false,
	}
	for host, seAcepta := range casos {
		err := checkHost(host)
		if seAcepta && err != nil {
			t.Errorf("checkHost(%q) = %v; se esperaba que valiera", host, err)
		}
		if !seAcepta && err == nil {
			t.Errorf("checkHost(%q) = nil; tenia que rechazarse", host)
		}
	}
}

// Y el rechazo llega de verdad al handler, no solo a la funcion suelta.
func TestPeticionConHostAjenoDaForbidden(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.Host = "malo.example.com"
	req.Header.Set("X-Deckman", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Host ajeno = %d, se esperaba 403", w.Code)
	}
}

// La interfaz se sirve desde el binario: si el embed se rompe, deckman arranca
// y no se ve nada, que es de los fallos que no da la cara hasta el final.
func TestLaInterfazSeSirve(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:8777"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/ = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Errorf("/ no devuelve el HTML de la interfaz")
	}
}

// Los errores salen en el idioma de quien mira, y se decide en cada peticion
// (no al arrancar) para que cambiar el idioma surta efecto sin reiniciar.
func TestElErrorSaleEnElIdiomaDeLaPeticion(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()

	casos := map[string]string{
		"en-GB,en;q=0.9": "there is no connection to the Steam Deck",
		"fr-FR,fr;q=0.9": "il n", // "il n'y a pas de connexion..."
		"es-ES":          "no hay conexion",
		"de-DE":          "no hay conexion", // idioma que no hablamos: castellano
	}
	for accept, esperado := range casos {
		req := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
		req.Host = "127.0.0.1:8777"
		req.Header.Set("X-Deckman", "1")
		req.Header.Set("Accept-Language", accept)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		var cuerpo struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &cuerpo); err != nil {
			t.Fatalf("[%s] respuesta no JSON: %q", accept, w.Body.String())
		}
		if !strings.Contains(strings.ToLower(cuerpo.Error), strings.ToLower(esperado)) {
			t.Errorf("[%s] error = %q, se esperaba que contuviera %q", accept, cuerpo.Error, esperado)
		}
	}
}

// El idioma guardado en la configuracion manda sobre el del navegador: es una
// eleccion explicita del usuario.
func TestElIdiomaGuardadoGanaAlDelNavegador(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	s.cfg.Idioma = i18n.FR

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	if got := s.idiomaEfectivo(req); got != i18n.FR {
		t.Errorf("idiomaEfectivo = %q, se esperaba %q", got, i18n.FR)
	}
}

// safeRelExe es la red que impide que un ".." acabe en el rm -rf de la Deck:
// de esta ruta sale el StartDir que borra "Eliminar".
func TestSafeRelExeRechazaLoQueSeSaleDeLaCarpeta(t *testing.T) {
	malos := []string{
		"",
		"..",
		"../fuera.exe",
		"bin/../../fuera.exe",
		"/etc/passwd",
		`C:\Windows\system32\cmd.exe`,
		`..\..\fuera.exe`,
		"  ",
		"sub/../../../home/deck/.ssh/authorized_keys",
	}
	for _, m := range malos {
		if got, err := safeRelExe(m); err == nil {
			t.Errorf("safeRelExe(%q) = %q, tenia que rechazarse", m, got)
		}
	}

	buenos := map[string]string{
		"juego.exe":                      "juego.exe",
		"bin/juego.exe":                  "bin/juego.exe",
		`bin\juego.exe`:                  "bin/juego.exe", // el navegador puede ir en Windows
		"  bin/juego.exe  ":              "bin/juego.exe",
		"carpeta con espacios/juego.exe": "carpeta con espacios/juego.exe",
	}
	for entrada, esperado := range buenos {
		got, err := safeRelExe(entrada)
		if err != nil {
			t.Errorf("safeRelExe(%q) = error %v", entrada, err)
			continue
		}
		if got != esperado {
			t.Errorf("safeRelExe(%q) = %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

// Las caratulas se bajan de sitios conocidos y solo por https: la URL viene de
// una respuesta de red, y seguirla a ciegas convierte a deckman en el ariete
// de quien conteste (peticiones a la red local desde el PC del usuario).
func TestSoloSeDescarganCaratulasDeSitiosConocidos(t *testing.T) {
	malas := []string{
		"http://cdn2.steamgriddb.com/x.png", // http pelado
		"https://evil.example.com/x.png",
		"https://steamgriddb.com.evil.example/x.png",
		"https://127.0.0.1/x.png",
		"https://192.168.1.1/admin",
		"file:///etc/passwd",
		"://roto",
	}
	for _, u := range malas {
		if err := allowedArtworkURL(u); err == nil {
			t.Errorf("allowedArtworkURL(%q) = nil; tenia que rechazarse", u)
		}
	}
	for _, u := range []string{
		"https://cdn2.steamgriddb.com/grid/x.png",
		"https://www.steamgriddb.com/x.png",
	} {
		if err := allowedArtworkURL(u); err != nil {
			t.Errorf("allowedArtworkURL(%q) = %v; tenia que valer", u, err)
		}
	}
}

// Sin Deck conectada, las rutas que la necesitan tienen que contestar un error
// claro y no reventar el servidor: son la mitad de la API y el usuario las
// toca antes de conectar mas veces de las que parece.
func TestSinConexionLasRutasContestanErrorYNoPanican(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()

	for _, ruta := range rutasAPI(t) {
		switch ruta {
		case "/api/events", "/api/quit": // uno se queda abierto, el otro apaga
			continue
		}
		req := httptest.NewRequest(http.MethodPost, ruta, strings.NewReader("{}"))
		req.Host = "127.0.0.1:8777"
		req.Header.Set("X-Deckman", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req) // si algo panica, la prueba falla aqui
		if w.Code >= 500 {
			t.Errorf("%s sin conexion = %d (%s)", ruta, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// Conectar sin decir a donde es un error del usuario, no del servidor.
func TestConectarSinHostEs400(t *testing.T) {
	s := nuevoServidorDePrueba(t)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/connect", strings.NewReader(`{"host":""}`))
	req.Host = "127.0.0.1:8777"
	req.Header.Set("X-Deckman", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("connect sin host = %d, se esperaba 400", w.Code)
	}
}
