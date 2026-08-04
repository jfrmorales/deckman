package meta

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Prueba contra SteamGridDB de verdad. Necesita una clave, que no se versiona:
//
//	DECKMAN_TEST_GRIDKEY=... go test ./internal/meta -run Integration -v
func gridClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	key := os.Getenv("DECKMAN_TEST_GRIDKEY")
	if key == "" {
		t.Skip("DECKMAN_TEST_GRIDKEY sin definir; se omite")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return New(key), ctx
}

// Un appid de Steam tiene que resolver a un juego concreto, sin ambiguedad.
func TestIntegrationGridGameByAppID(t *testing.T) {
	c, ctx := gridClient(t)
	id, err := c.GridGameID(ctx, "2050650") // Resident Evil 4 (2023)
	if err != nil {
		t.Fatalf("GridGameID: %v", err)
	}
	if id == 0 {
		t.Fatal("id vacio")
	}
	t.Logf("appid 2050650 -> SteamGridDB %d", id)

	// Un appid inventado no debe colar.
	if _, err := c.GridGameID(ctx, "999999999"); err == nil {
		t.Error("un appid inexistente deberia dar error")
	}
}

func TestIntegrationGridSearch(t *testing.T) {
	c, ctx := gridClient(t)
	games, err := c.SearchGames(ctx, "Devil May Cry 5")
	if err != nil {
		t.Fatalf("SearchGames: %v", err)
	}
	if len(games) == 0 {
		t.Fatal("sin resultados")
	}
	for _, g := range games[:min(3, len(games))] {
		t.Logf("  %d | %s", g.ID, g.Name)
	}
	if games[0].Name == "" || games[0].ID == 0 {
		t.Errorf("resultado incompleto: %+v", games[0])
	}
}

// El contrato completo de la galeria: los cinco tipos devuelven opciones con
// todos los campos que la interfaz necesita, y la imagen se descarga entera.
func TestIntegrationArtworkOptions(t *testing.T) {
	c, ctx := gridClient(t)
	gameID, err := c.ResolveGridGame(ctx, "2050650", "Resident Evil 4")
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"grid", "gridh", "hero", "logo", "icon"} {
		t.Run(kind, func(t *testing.T) {
			opts, err := c.ListArtwork(ctx, gameID, kind, false)
			if err != nil {
				t.Fatalf("ListArtwork: %v", err)
			}
			if len(opts) == 0 {
				t.Skipf("no hay imagenes de tipo %s", kind)
			}
			o := opts[0]
			// Cada campo que consume la interfaz.
			if o.URL == "" {
				t.Error("sin URL")
			}
			if o.Thumb == "" {
				t.Error("sin miniatura: la galeria se veria vacia")
			}
			if o.Ext == "" {
				t.Error("sin extension: no sabriamos como nombrar el fichero")
			}
			if o.Width == 0 || o.Height == 0 {
				t.Error("sin dimensiones")
			}
			t.Logf("%d opciones | 1a: %dx%d %s %s por %q",
				len(opts), o.Width, o.Height, o.Ext, o.Style, o.Author)

			// La descarga tiene que traer una imagen de verdad.
			data, err := c.Download(ctx, o.URL)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if !looksLikeImage(data) {
				t.Fatalf("lo descargado no es una imagen: %q", data[:min(16, len(data))])
			}
			t.Logf("   descargada: %d KB", len(data)/1024)

			// Y la miniatura debe cargarse sin clave, porque la pide el navegador.
			thumb, err := c.Download(ctx, o.Thumb)
			if err != nil {
				t.Errorf("la miniatura no se puede cargar: %v", err)
			} else if !looksLikeImage(thumb) {
				t.Error("la miniatura no es una imagen")
			}
		})
	}
}

// Las medidas que pedimos tienen que ser las que Steam espera.
func TestIntegrationArtworkDimensions(t *testing.T) {
	c, ctx := gridClient(t)
	gameID, err := c.ResolveGridGame(ctx, "2050650", "Resident Evil 4")
	if err != nil {
		t.Fatal(err)
	}
	// La portada vertical y la horizontal salen del mismo endpoint y solo se
	// distinguen por el filtro de dimensiones: si se cruzaran, Steam mostraria
	// una imagen deformada.
	vert, err := c.ListArtwork(ctx, gameID, "grid", false)
	if err != nil || len(vert) == 0 {
		t.Fatalf("grids verticales: %v", err)
	}
	horiz, err := c.ListArtwork(ctx, gameID, "gridh", false)
	if err != nil || len(horiz) == 0 {
		t.Fatalf("grids horizontales: %v", err)
	}
	for _, o := range vert[:min(5, len(vert))] {
		if o.Height <= o.Width {
			t.Errorf("una 'vertical' mide %dx%d", o.Width, o.Height)
		}
	}
	for _, o := range horiz[:min(5, len(horiz))] {
		if o.Width <= o.Height {
			t.Errorf("una 'horizontal' mide %dx%d", o.Width, o.Height)
		}
	}
	t.Logf("verticales: %d, horizontales: %d", len(vert), len(horiz))
}

// Una clave mal formada debe fallar con un mensaje comprensible.
func TestIntegrationBadKey(t *testing.T) {
	if os.Getenv("DECKMAN_TEST_GRIDKEY") == "" {
		t.Skip("sin clave configurada")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := New("clave-invalida").GridGameID(ctx, "2050650")
	if err == nil {
		t.Fatal("una clave invalida deberia fallar")
	}
	t.Logf("mensaje: %v", err)
}

// headSize pregunta al servidor cuanto pesa el fichero, sin bajarlo.
func headSize(t *testing.T, u string) int64 {
	t.Helper()
	req, err := http.NewRequest(http.MethodHead, u, nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.ContentLength
}

func looksLikeImage(b []byte) bool {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return true
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}): // JPEG
		return true
	case len(b) > 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return true
	}
	return false
}

// Las animadas son el caso que se rompio: SteamGridDB las sirve como .webp
// pero su MINIATURA es un video .webm, y un <img> no puede pintarla. Si esto
// deja de marcarse bien, la galeria vuelve a mostrar huecos en blanco.
func TestIntegrationAnimatedArtwork(t *testing.T) {
	c, ctx := gridClient(t)
	gameID, err := c.ResolveGridGame(ctx, "2050650", "Resident Evil 4")
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"grid", "gridh", "hero"} {
		t.Run(kind, func(t *testing.T) {
			opts, err := c.ListArtwork(ctx, gameID, kind, true)
			if err != nil {
				t.Fatalf("ListArtwork: %v", err)
			}
			var animated []ArtOption
			for _, o := range opts {
				if o.Animated {
					animated = append(animated, o)
				}
			}
			if len(animated) == 0 {
				t.Skipf("este juego no tiene %s animadas", kind)
			}
			t.Logf("%d de %d opciones son animadas", len(animated), len(opts))

			o := animated[0]
			// La miniatura es un video: la interfaz tiene que saberlo.
			if !strings.HasSuffix(strings.ToLower(o.Thumb), ".webm") {
				t.Errorf("miniatura de una animada = %s; se esperaba .webm", o.Thumb)
			}
			// Y tiene que guardarse como .png aunque el original sea webp:
			// Steam solo reconoce el arte por la extension del nombre.
			if o.Ext != ".png" {
				t.Errorf("extension = %q; una animada debe guardarse como .png", o.Ext)
			}

			data, err := c.Download(ctx, o.URL)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if !looksLikeImage(data) {
				t.Fatalf("lo descargado no es un webp valido: %q", data[:min(16, len(data))])
			}
			// Lo que hundio esto la primera vez: el fichero llegaba recortado y
			// la cabecera seguia siendo valida, asi que parecia correcto.
			// Comparar con el tamano que declara el servidor es lo unico que lo
			// caza.
			declared := headSize(t, o.URL)
			if declared > 0 && int64(len(data)) != declared {
				t.Fatalf("descarga incompleta: %d bytes de %d", len(data), declared)
			}
			t.Logf("   %dx%d  %.1f MB  %s (completa)", o.Width, o.Height, float64(len(data))/(1<<20), o.Mime)

			// Sin pedir animadas no debe colarse ninguna.
			static, err := c.ListArtwork(ctx, gameID, kind, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range static {
				if s.Animated {
					t.Errorf("se colo una animada pidiendo solo estaticas: %s", s.Thumb)
				}
			}
		})
	}
}

// La deteccion no debe depender solo del mime: hay estaticas en webp.
func TestIsAnimated(t *testing.T) {
	cases := []struct {
		mime, thumb string
		want        bool
	}{
		{"image/webp", "https://cdn2.steamgriddb.com/thumb/abc.webm", true},
		{"image/apng", "https://cdn2.steamgriddb.com/thumb/abc.png", true},
		{"image/png", "https://cdn2.steamgriddb.com/thumb/abc.jpg", false},
		{"image/jpeg", "https://cdn2.steamgriddb.com/thumb/abc.jpg", false},
		{"image/webp", "https://cdn2.steamgriddb.com/thumb/abc.jpg", false}, // webp estatico
		{"image/webp", "https://cdn2.steamgriddb.com/thumb/abc.webm?v=2", true},
	}
	for _, c := range cases {
		if got := isAnimated(c.mime, c.thumb); got != c.want {
			t.Errorf("isAnimated(%q, %q) = %v, se esperaba %v", c.mime, c.thumb, got, c.want)
		}
	}
}

// El fondo animado mas grande de la biblioteca ronda los 45 MB. Esta prueba
// fija que el limite de descarga los admite enteros.
func TestIntegrationLargeAnimatedDownload(t *testing.T) {
	c, ctx := gridClient(t)
	gameID, err := c.ResolveGridGame(ctx, "2050650", "Resident Evil 4")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := c.ListArtwork(ctx, gameID, "hero", true)
	if err != nil {
		t.Fatal(err)
	}

	var biggest ArtOption
	var biggestSize int64
	for _, o := range opts {
		if !o.Animated {
			continue
		}
		if sz := headSize(t, o.URL); sz > biggestSize {
			biggest, biggestSize = o, sz
		}
	}
	if biggestSize == 0 {
		t.Skip("sin fondos animados")
	}
	t.Logf("el mayor pesa %.1f MB", float64(biggestSize)/(1<<20))

	data, err := c.Download(ctx, biggest.URL)
	if err != nil {
		t.Fatalf("no se pudo bajar entero: %v", err)
	}
	if int64(len(data)) != biggestSize {
		t.Fatalf("llegaron %d bytes de %d", len(data), biggestSize)
	}
}

// Al pedir animadas deben salir primero: son minoria entre los 50 resultados
// y si no, quedan enterradas justo cuando se buscan.
func TestIntegrationAnimatedSortedFirst(t *testing.T) {
	c, ctx := gridClient(t)
	gameID, err := c.ResolveGridGame(ctx, "2050650", "Resident Evil 4")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := c.ListArtwork(ctx, gameID, "hero", true)
	if err != nil {
		t.Fatal(err)
	}
	var nAnim, firstStatic = 0, -1
	for i, o := range opts {
		if o.Animated {
			nAnim++
			if firstStatic >= 0 {
				t.Errorf("una animada en la posicion %d, despues de una estatica en la %d", i, firstStatic)
			}
		} else if firstStatic < 0 {
			firstStatic = i
		}
	}
	if nAnim == 0 {
		t.Skip("sin animadas")
	}
	t.Logf("%d animadas, todas antes de las %d estaticas", nAnim, len(opts)-nAnim)
}
