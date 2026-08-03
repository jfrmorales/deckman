package deck

import (
	"bytes"
	"context"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/jfrmorales/deckman/internal/meta"
)

// Prueba la cadena completa de caratulas: SteamGridDB -> descarga -> instalada
// en la Deck. Es justo lo que hace el boton de la galeria.
//
// Necesita una clave, que no se versiona:
//
//	DECKMAN_TEST_HOST=... DECKMAN_TEST_PASS=... DECKMAN_TEST_GRIDKEY=... go test ./internal/deck -run ArtworkEndToEnd -v
//
// Escribe en el arbol de pruebas, nunca en la configuracion real de Steam.
func TestIntegrationArtworkEndToEnd(t *testing.T) {
	key := os.Getenv("DECKMAN_TEST_GRIDKEY")
	if key == "" {
		t.Skip("DECKMAN_TEST_GRIDKEY sin definir; se omite")
	}
	c, ctx := integrationClient(t)
	sc, _ := setupSandbox(t, c, ctx)

	mc := meta.New(key)
	if !mc.HasGridKey() {
		t.Fatal("la clave no se reconocio")
	}

	// Resident Evil 4, por su appid de Steam.
	gameID, err := mc.ResolveGridGame(ctx, "2050650", "Resident Evil 4")
	if err != nil {
		t.Fatalf("no se localizo el juego en SteamGridDB: %v", err)
	}
	t.Logf("SteamGridDB game id = %d", gameID)

	// Un tipo de cada, para cubrir los cinco nombres de fichero.
	for _, kind := range ArtKinds() {
		t.Run(kind, func(t *testing.T) {
			lctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()

			opts, err := mc.ListArtwork(lctx, gameID, kind, false)
			if err != nil {
				t.Fatalf("ListArtwork: %v", err)
			}
			if len(opts) == 0 {
				t.Skipf("no hay imagenes de tipo %s", kind)
			}
			o := opts[0]
			if o.URL == "" || o.Ext == "" {
				t.Fatalf("opcion incompleta: %+v", o)
			}

			data, err := mc.Download(lctx, o.URL)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if len(data) < 1000 {
				t.Fatalf("la imagen pesa solo %d bytes", len(data))
			}
			// Debe ser una imagen de verdad, no una pagina de error.
			if !isImage(data) {
				t.Fatalf("lo descargado no parece una imagen: %q", data[:min(16, len(data))])
			}

			name, err := sc.WriteArtwork(lctx, 999000111, kind, o.Ext, data)
			if err != nil {
				t.Fatalf("WriteArtwork: %v", err)
			}

			// Y tiene que llegar entera a la Deck.
			dir, _ := sc.GridDir(lctx)
			got, err := sc.ReadFile(path.Join(dir, name))
			if err != nil {
				t.Fatalf("no se pudo releer %s: %v", name, err)
			}
			if !bytes.Equal(got, data) {
				t.Errorf("%s: %d bytes en la Deck, %d en origen", name, len(got), len(data))
			}
			t.Logf("%-6s %s  %dx%d  %d KB  por %s", kind, name, o.Width, o.Height, len(data)/1024, o.Author)
		})
	}

	// Y el inventario de arte debe verlas todas.
	current, err := sc.CurrentArtwork(ctx, 999000111)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) < 4 {
		t.Errorf("CurrentArtwork solo ve %d imagenes: %v", len(current), current)
	}
	t.Logf("instaladas: %v", current)
}

// isImage reconoce las cabeceras de los formatos que sirve SteamGridDB.
func isImage(b []byte) bool {
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

// Reproduce el fallo que aparecio con Devil May Cry 5: una caratula animada se
// instalaba como .webp y Steam no la mostraba, porque decide si un fichero es
// arte por la extension del nombre.
func TestIntegrationAnimatedInstallsAsPNG(t *testing.T) {
	key := os.Getenv("DECKMAN_TEST_GRIDKEY")
	if key == "" {
		t.Skip("DECKMAN_TEST_GRIDKEY sin definir; se omite")
	}
	c, ctx := integrationClient(t)
	sc, _ := setupSandbox(t, c, ctx)

	mc := meta.New(key)
	gameID, err := mc.ResolveGridGame(ctx, "601150", "Devil May Cry 5")
	if err != nil {
		t.Fatalf("no se localizo el juego: %v", err)
	}

	opts, err := mc.ListArtwork(ctx, gameID, "hero", true)
	if err != nil {
		t.Fatal(err)
	}
	var anim *meta.ArtOption
	for i := range opts {
		if opts[i].Animated {
			anim = &opts[i]
			break
		}
	}
	if anim == nil {
		t.Skip("Devil May Cry 5 no tiene fondos animados ahora mismo")
	}
	if anim.Ext != ".png" {
		t.Fatalf("la extension propuesta es %q; Steam ignora todo lo que no sea .png/.jpg/.ico", anim.Ext)
	}

	data, err := mc.Download(ctx, anim.URL)
	if err != nil {
		t.Fatal(err)
	}
	const appID = uint32(3454118403) // el de Devil May Cry 5 en la Deck real
	name, err := sc.WriteArtwork(ctx, appID, "hero", anim.Ext, data)
	if err != nil {
		t.Fatal(err)
	}
	if name != "3454118403_hero.png" {
		t.Errorf("se instalo como %q; deberia ser 3454118403_hero.png", name)
	}
	t.Logf("instalada %s (%.1f MB, contenido %s)", name, float64(len(data))/(1<<20), anim.Mime)

	// Y el contenido tiene que seguir siendo el webp original, intacto.
	dir, _ := sc.GridDir(ctx)
	got, err := sc.ReadFile(path.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Error("el contenido cambio al instalarlo")
	}
	if !bytes.HasPrefix(got, []byte("RIFF")) {
		t.Error("el fichero deberia seguir siendo un webp por dentro")
	}
}

// Si ya hay dos ficheros del mismo tipo con distinta extension, instalar uno
// nuevo debe dejar solo el nuevo: con ambos presentes Steam elige a ciegas.
func TestIntegrationArtworkRemovesDuplicateExtensions(t *testing.T) {
	c, ctx := integrationClient(t)
	sc, _ := setupSandbox(t, c, ctx)

	const appID = uint32(555000222)
	png := []byte("\x89PNG\r\n\x1a\n de prueba")

	// Simulamos el estado que dejo el fallo: un .webp y un .png a la vez.
	dir, err := sc.GridDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"555000222_hero.webp", "555000222_hero.jpg"} {
		f, err := sc.SFTP().Create(path.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		f.Write(png)
		f.Close()
	}

	name, err := sc.WriteArtwork(ctx, appID, "hero", ".png", png)
	if err != nil {
		t.Fatal(err)
	}
	if name != "555000222_hero.png" {
		t.Fatalf("nombre = %q", name)
	}

	entries, err := sc.SFTP().ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var heroes []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "555000222_hero") {
			heroes = append(heroes, e.Name())
		}
	}
	if len(heroes) != 1 || heroes[0] != "555000222_hero.png" {
		t.Errorf("deberia quedar solo el .png, quedan: %v", heroes)
	}
}

// El .json del plugin de Decky comparte nombre con la portada horizontal y no
// debe tocarse nunca.
func TestIntegrationArtworkLeavesJSONAlone(t *testing.T) {
	c, ctx := integrationClient(t)
	sc, _ := setupSandbox(t, c, ctx)

	const appID = uint32(777000333)
	dir, err := sc.GridDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	// Como lo deja el plugin: 777000333.json junto a las imagenes.
	jsonPath := path.Join(dir, "777000333.json")
	f, err := sc.SFTP().Create(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(`{"grid":"123","hero":"456"}`))
	f.Close()

	// No debe aparecer como arte.
	current, err := sc.CurrentArtwork(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := current["gridh"]; ok {
		t.Errorf("el .json se tomo por la portada horizontal: %q", v)
	}

	// Ni desaparecer al instalar o limpiar.
	if _, err := sc.WriteArtwork(ctx, appID, "gridh", ".png", []byte("\x89PNG\r\n\x1a\n x")); err != nil {
		t.Fatal(err)
	}
	if err := sc.ClearArtwork(ctx, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.SFTP().Stat(jsonPath); err != nil {
		t.Fatalf("se ha borrado el .json del plugin de Decky: %v", err)
	}
}

// La via en caliente: Steam aplica la caratula al momento por su propia API,
// sin reiniciarse. Toca la biblioteca REAL (no hay sandbox posible: es el
// Steam en marcha), asi que restaura al final lo que hubiera.
func TestIntegrationLiveArtwork(t *testing.T) {
	key := os.Getenv("DECKMAN_TEST_GRIDKEY")
	if key == "" {
		t.Skip("DECKMAN_TEST_GRIDKEY sin definir; se omite")
	}
	c, ctx := integrationClient(t)
	if !c.CEFAvailable(ctx) {
		t.Skip("Steam no expone su depurador; puede estar cerrado")
	}

	const appID = uint32(3454118403) // Devil May Cry 5

	// Copia de lo que haya ahora, para dejarlo igual.
	before, err := c.CurrentArtwork(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := c.GridDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var original []byte
	if name, ok := before["logo"]; ok {
		if original, err = c.ReadFile(path.Join(dir, name)); err != nil {
			t.Fatalf("no se pudo respaldar el logo: %v", err)
		}
		t.Cleanup(func() {
			restoreCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := c.ApplyArtworkLive(restoreCtx, appID, "logo", ".png", original); err != nil {
				t.Errorf("NO SE PUDO RESTAURAR el logo original: %v", err)
			}
		})
	}

	mc := meta.New(key)
	gameID, err := mc.ResolveGridGame(ctx, "601150", "Devil May Cry 5")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := mc.ListArtwork(ctx, gameID, "logo", false)
	if err != nil || len(opts) < 2 {
		t.Skipf("no hay logos suficientes para probar: %v", err)
	}
	// Uno distinto del que estuviera puesto.
	pick := opts[1]
	data, err := mc.Download(ctx, pick.URL)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(data, original) {
		pick = opts[0]
		if data, err = mc.Download(ctx, pick.URL); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now()
	if err := c.ApplyArtworkLive(ctx, appID, "logo", pick.Ext, data); err != nil {
		t.Fatalf("ApplyArtworkLive: %v", err)
	}
	t.Logf("aplicada en caliente en %.1fs (%d KB)", time.Since(start).Seconds(), len(data)/1024)

	// Lo escribe el propio Steam, asi que el fichero debe tener el contenido nuevo.
	current, err := c.CurrentArtwork(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	name, ok := current["logo"]
	if !ok {
		t.Fatal("tras aplicarla no hay logo")
	}
	got, err := c.ReadFile(path.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("el fichero tiene %d bytes y se enviaron %d", len(got), len(data))
	}
	if original != nil && bytes.Equal(got, original) {
		t.Error("el logo no ha cambiado")
	}
}

// Los iconos no pasan por la API de Steam: hay que decirlo, no fallar raro.
func TestLiveArtworkKinds(t *testing.T) {
	for _, k := range []string{"grid", "gridh", "hero", "logo"} {
		if !SupportsLiveArtwork(k) {
			t.Errorf("%s deberia poder aplicarse en caliente", k)
		}
	}
	if SupportsLiveArtwork("icon") {
		t.Error("el icono NO pasa por la API de Steam; comprobado en una Deck real")
	}
	if SupportsLiveArtwork("inventado") {
		t.Error("un tipo desconocido no deberia admitirse")
	}
}

// Con Steam abierto, anadir un juego tiene que ir por la API de Steam.
// Editar shortcuts.vdf en ese momento es lo que hizo perder seis accesos
// directos en una Deck real.
func TestIntegrationAddShortcutLive(t *testing.T) {
	c, ctx := integrationClient(t)
	if !c.CEFAvailable(ctx) {
		t.Skip("Steam no responde en caliente")
	}

	steamRoot := c.SteamRoot()
	userID, err := c.UserID(ctx, steamRoot)
	if err != nil {
		t.Fatal(err)
	}
	scPath := path.Join(steamRoot, "userdata", userID, "config", "shortcuts.vdf")

	antes, err := c.ReadFile(scPath)
	if err != nil {
		t.Fatal(err)
	}
	sfAntes, err := ParseShortcuts(antes)
	if err != nil {
		t.Fatal(err)
	}
	n := len(sfAntes.Entries)
	t.Logf("accesos directos antes: %d", n)

	appID, err := c.AddShortcutLive(ctx, "deckman prueba borrable",
		"/home/deck/deckman-prueba/juego.exe", "/home/deck/deckman-prueba", "", "")
	if err != nil {
		t.Fatalf("AddShortcutLive: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := c.RemoveShortcutLive(cleanCtx, appID); err != nil {
			t.Errorf("NO SE PUDO QUITAR el acceso directo de prueba (%d): %v", appID, err)
		}
	})

	// Steam lo persiste solo, sin reiniciar.
	time.Sleep(3 * time.Second)
	despues, err := c.ReadFile(scPath)
	if err != nil {
		t.Fatal(err)
	}
	sfDespues, err := ParseShortcuts(despues)
	if err != nil {
		t.Fatalf("shortcuts.vdf quedo ilegible: %v", err)
	}
	if len(sfDespues.Entries) != n+1 {
		t.Fatalf("se esperaban %d accesos directos, hay %d", n+1, len(sfDespues.Entries))
	}

	// Y lo que ya estaba sigue estando: es justo lo que se perdio aquella vez.
	presentes := map[uint32]bool{}
	for _, s := range sfDespues.Entries {
		presentes[s.AppID()] = true
	}
	for _, s := range sfAntes.Entries {
		if !presentes[s.AppID()] {
			t.Errorf("desaparecio %q", s.GetStr("AppName"))
		}
	}
	t.Logf("añadido id %d; los %d anteriores intactos", appID, n)
}
