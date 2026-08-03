package deck

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pruebas contra una Steam Deck de verdad. Se saltan salvo que se pida:
//
//	DECKMAN_TEST_HOST=192.168.1.50 DECKMAN_TEST_PASS=... go test ./internal/deck -run Integration -v
//
// No tocan la configuracion real: se monta un arbol de Steam de mentira en
// ~/deckman-selftest y se apunta el cliente ahi cambiando Home. Al terminar se
// borra entero.
func integrationClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	host := os.Getenv("DECKMAN_TEST_HOST")
	if host == "" {
		t.Skip("DECKMAN_TEST_HOST sin definir; se omite la prueba de integracion")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	c, err := Connect(ctx, Credentials{
		Host:     host,
		User:     envOr("DECKMAN_TEST_USER", "deck"),
		Password: os.Getenv("DECKMAN_TEST_PASS"),
		KeyPath:  os.Getenv("DECKMAN_TEST_KEY"),
	})
	if err != nil {
		t.Fatalf("no se pudo conectar: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, ctx
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// setupSandbox construye un arbol de Steam ficticio en la Deck y devuelve el
// cliente ya apuntando a el.
func setupSandbox(t *testing.T, c *Client, ctx context.Context) (*Client, string) {
	t.Helper()
	realHome := c.Home
	sandbox := path.Join(realHome, "deckman-selftest")

	// El sandbox imita un $HOME entero, porque de ahi cuelga SteamRoot().
	// Partimos de una copia de los ficheros reales para probar contra datos
	// autenticos, pero sin riesgo de estropear los originales.
	fake := path.Join(sandbox, ".local/share/Steam")
	script := fmt.Sprintf(`
set -e
rm -rf %[1]s
mkdir -p %[3]s/steamapps/common %[3]s/config %[3]s/userdata/33150357/config %[1]s/Games
cp %[2]s/userdata/*/config/shortcuts.vdf %[3]s/userdata/33150357/config/shortcuts.vdf 2>/dev/null || true
cp %[2]s/config/config.vdf %[3]s/config/config.vdf
cp %[2]s/steamapps/libraryfolders.vdf %[3]s/steamapps/libraryfolders.vdf
`, ShellQuote(sandbox), ShellQuote(path.Join(realHome, ".local/share/Steam")), ShellQuote(fake))

	if _, err := c.Run(ctx, script); err != nil {
		t.Fatalf("no se pudo preparar el arbol de pruebas: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		c.Run(cleanCtx, "rm -rf "+ShellQuote(sandbox))
	})

	// Desviamos Home al arbol de pruebas: SteamRoot() cuelga de Home, asi que
	// a partir de aqui todo el codigo escribe dentro del sandbox.
	return c.WithHome(sandbox), path.Join(sandbox, ".local/share/Steam")
}

func TestIntegrationSandboxSteamRoot(t *testing.T) {
	c, ctx := integrationClient(t)
	sc, steamRoot := setupSandbox(t, c, ctx)

	if got := sc.SteamRoot(); got != steamRoot {
		t.Fatalf("SteamRoot() = %s, se esperaba %s", got, steamRoot)
	}
	uid, err := sc.UserID(ctx, steamRoot)
	if err != nil {
		t.Fatalf("no se encontro el usuario en el sandbox: %v", err)
	}
	t.Logf("sandbox listo, userId %s", uid)
}

// La prueba clave: anadir un juego no-Steam escribe bien shortcuts.vdf y
// config.vdf, y ambos siguen siendo legibles despues.
func TestIntegrationAddNonSteamGame(t *testing.T) {
	c, ctx := integrationClient(t)
	sc, steamRoot := setupSandbox(t, c, ctx)

	scPath := path.Join(steamRoot, "userdata/33150357/config/shortcuts.vdf")
	before, err := sc.ReadFile(scPath)
	if err != nil {
		t.Fatalf("no se pudo leer el shortcuts.vdf de partida: %v", err)
	}
	sfBefore, err := ParseShortcuts(before)
	if err != nil {
		t.Fatal(err)
	}
	nBefore := len(sfBefore.Entries)

	exe := path.Join(sc.Home, "Games/Juego De Prueba/juego.exe")
	appID, err := sc.AddNonSteamGame(ctx, NonSteamOptions{
		Name:       "Juego De Prueba",
		Exe:        exe,
		CompatTool: "proton_experimental",
	})
	if err != nil {
		t.Fatalf("AddNonSteamGame fallo: %v", err)
	}
	t.Logf("appid asignado: %d", appID)

	// shortcuts.vdf: una entrada mas y legible.
	after, err := sc.ReadFile(scPath)
	if err != nil {
		t.Fatal(err)
	}
	sfAfter, err := ParseShortcuts(after)
	if err != nil {
		t.Fatalf("shortcuts.vdf quedo corrupto: %v", err)
	}
	if len(sfAfter.Entries) != nBefore+1 {
		t.Fatalf("se esperaban %d entradas, hay %d", nBefore+1, len(sfAfter.Entries))
	}
	var found *Shortcut
	for _, s := range sfAfter.Entries {
		if s.AppID() == appID {
			found = s
		}
	}
	if found == nil {
		t.Fatal("no se encontro el juego recien anadido")
	}
	if got := found.GetStr("AppName"); got != "Juego De Prueba" {
		t.Errorf("AppName = %q", got)
	}
	if got := Unquote(found.GetStr("Exe")); got != exe {
		t.Errorf("Exe = %q, se esperaba %q", got, exe)
	}

	// Las entradas que ya existian tienen que seguir intactas.
	for i := 0; i < nBefore; i++ {
		if a, b := sfBefore.Entries[i].GetStr("AppName"), sfAfter.Entries[i].GetStr("AppName"); a != b {
			t.Errorf("la entrada %d cambio: %q -> %q", i, a, b)
		}
	}

	// config.vdf: el mapeo de Proton, y el resto del fichero sin tocar.
	cfgRaw, err := sc.ReadFile(path.Join(steamRoot, "config/config.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseVDF(cfgRaw)
	if err != nil {
		t.Fatalf("config.vdf quedo corrupto: %v", err)
	}
	mapping := cfg.Get("InstallConfigStore", "Software", "Valve", "Steam", "CompatToolMapping")
	if mapping == nil {
		t.Fatal("desaparecio CompatToolMapping")
	}
	entry := mapping.Get(fmt.Sprint(appID))
	if entry == nil {
		t.Fatalf("no se escribio el mapeo para el appid %d", appID)
	}
	if got := entry.GetString("name"); got != "proton_experimental" {
		t.Errorf("Proton asignado = %q", got)
	}

	// Reenviar el mismo juego debe actualizar, no duplicar.
	appID2, err := sc.AddNonSteamGame(ctx, NonSteamOptions{Name: "Juego De Prueba", Exe: exe})
	if err != nil {
		t.Fatalf("el segundo envio fallo: %v", err)
	}
	if appID2 != appID {
		t.Errorf("cambio el appid al reenviar: %d -> %d", appID, appID2)
	}
	again, _ := sc.ReadFile(scPath)
	sfAgain, err := ParseShortcuts(again)
	if err != nil {
		t.Fatal(err)
	}
	if len(sfAgain.Entries) != nBefore+1 {
		t.Errorf("el reenvio duplico la entrada: %d entradas", len(sfAgain.Entries))
	}

	// Y quitarlo lo deja como estaba.
	if err := sc.RemoveShortcut(ctx, appID); err != nil {
		t.Fatalf("RemoveShortcut fallo: %v", err)
	}
	final, _ := sc.ReadFile(scPath)
	sfFinal, err := ParseShortcuts(final)
	if err != nil {
		t.Fatal(err)
	}
	if len(sfFinal.Entries) != nBefore {
		t.Errorf("tras borrar quedan %d entradas, se esperaban %d", len(sfFinal.Entries), nBefore)
	}
}

// Comprueba la subida de ficheros: arbol anidado, reanudacion y progreso.
func TestIntegrationUploadTree(t *testing.T) {
	c, ctx := integrationClient(t)
	sc, _ := setupSandbox(t, c, ctx)

	// Arbol local de prueba.
	local := t.TempDir()
	game := filepath.Join(local, "Juego De Prueba")
	if err := os.MkdirAll(filepath.Join(game, "bin", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]int{
		"juego.exe":            1024,
		"bin/lib.dll":          4096,
		"bin/data/recurso.pak": 256 * 1024,
		"leeme.txt":            64,
	}
	for rel, size := range files {
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = byte(i % 251)
		}
		if err := os.WriteFile(filepath.Join(game, filepath.FromSlash(rel)), buf, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dst := path.Join(sc.Home, "Games")
	var last Progress
	var updates int
	if err := sc.UploadTree(ctx, game, dst, func(p Progress) { last = p; updates++ }); err != nil {
		t.Fatalf("UploadTree fallo: %v", err)
	}
	if !last.Done {
		t.Error("no llego el aviso final")
	}
	if updates == 0 {
		t.Error("no se informo de ningun progreso")
	}
	t.Logf("subida: %d avisos, %d ficheros, %d bytes", updates, last.FilesTotal, last.BytesTotal)

	// Todos los ficheros, con su tamano exacto.
	remoteGame := path.Join(dst, "Juego De Prueba")
	for rel, size := range files {
		st, err := sc.SFTP().Stat(path.Join(remoteGame, rel))
		if err != nil {
			t.Errorf("falta %s: %v", rel, err)
			continue
		}
		if st.Size() != int64(size) {
			t.Errorf("%s: %d bytes en la Deck, %d en origen", rel, st.Size(), size)
		}
	}

	// Contenido byte a byte de uno de ellos.
	got, err := sc.ReadFile(path.Join(remoteGame, "bin/data/recurso.pak"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(game, "bin", "data", "recurso.pak"))
	if len(got) != len(want) {
		t.Fatalf("tamano distinto: %d vs %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("el contenido difiere en el byte %d", i)
		}
	}

	// Repetir la subida debe saltarse todo lo que ya esta.
	var second Progress
	if err := sc.UploadTree(ctx, game, dst, func(p Progress) { second = p }); err != nil {
		t.Fatalf("la segunda subida fallo: %v", err)
	}
	if !strings.Contains(second.Message, "saltado") {
		t.Errorf("se esperaba que reutilizara lo ya copiado, mensaje: %q", second.Message)
	}
	t.Logf("reanudacion: %s", second.Message)
}

// Mover un juego con Steam abierto tiene que negarse, no romper nada.
func TestIntegrationMoveRefusesWithSteamRunning(t *testing.T) {
	c, ctx := integrationClient(t)
	if !c.SteamRunning(ctx) {
		t.Skip("Steam no esta arrancado en la Deck; no se puede comprobar la negativa")
	}
	err := c.MoveGame(ctx, "252950", "/run/media/deck/USD00", nil)
	if err == nil {
		t.Fatal("MoveGame deberia haberse negado con Steam abierto")
	}
	if !strings.Contains(err.Error(), "Steam esta abierto") {
		t.Errorf("el error no explica el motivo real: %v", err)
	}
	t.Logf("se nego correctamente: %v", err)
}

// La red de seguridad contra borrados catastroficos.
func TestSafeToDelete(t *testing.T) {
	c := &Client{Home: "/home/deck"}
	nope := []string{
		"/", "/home", "/home/deck", "/usr", "/etc",
		"/home/deck/Games", "/home/deck/.local/share/Steam", "/home/deck/Emulation",
	}
	for _, p := range nope {
		if c.safeToDelete(p) {
			t.Errorf("safeToDelete(%q) deberia ser false", p)
		}
	}
	ok := []string{
		"/home/deck/Games/Mi Juego",
		"/run/media/deck/USD00/Emulation/roms/nes",
	}
	for _, p := range ok {
		if !c.safeToDelete(p) {
			t.Errorf("safeToDelete(%q) deberia ser true", p)
		}
	}
}

// Comprueba el lado de la Deck de las caratulas: carpeta correcta, nombres
// correctos y que ClearArtwork solo borra lo del appid indicado.
//
// La parte de SteamGridDB no se prueba aqui: hace falta una clave del usuario.
func TestIntegrationArtwork(t *testing.T) {
	c, ctx := integrationClient(t)
	sc, steamRoot := setupSandbox(t, c, ctx)

	dir, err := sc.GridDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := path.Join(steamRoot, "userdata/33150357/config/grid"); dir != want {
		t.Fatalf("GridDir() = %s, se esperaba %s", dir, want)
	}

	const appID = uint32(3465116546)
	const otro = uint32(1234567890)
	png := []byte("\x89PNG\r\n\x1a\n" + "contenido de prueba")

	for _, a := range []struct {
		id   uint32
		kind string
		ext  string
	}{
		{appID, "grid", ".png"},
		{appID, "gridh", ".png"},
		{appID, "hero", ".jpg"},
		{appID, "logo", ".png"},
		{otro, "grid", ".png"}, // de otro juego: no debe tocarse
	} {
		name, err := sc.WriteArtwork(ctx, a.id, a.kind, a.ext, png)
		if err != nil {
			t.Fatalf("WriteArtwork(%d, %s): %v", a.id, a.kind, err)
		}
		t.Logf("instalada %s", name)
	}

	// Y debe saber decir que hay puesto.
	current, err := sc.CurrentArtwork(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"grid", "gridh", "hero", "logo"} {
		if _, ok := current[kind]; !ok {
			t.Errorf("CurrentArtwork no detecto %s (devolvio %v)", kind, current)
		}
	}
	if current["hero"] != fmt.Sprint(appID)+"_hero.jpg" {
		t.Errorf("hero mal identificado: %q", current["hero"])
	}
	// La vertical y la horizontal solo se distinguen por una "p": comprobamos
	// que no se confunden.
	if current["grid"] == current["gridh"] {
		t.Errorf("portada vertical y horizontal identificadas igual: %q", current["grid"])
	}

	// Cambiar un tipo debe reemplazar, no acumular: la nueva es .png y la
	// anterior era .jpg.
	if _, err := sc.WriteArtwork(ctx, appID, "hero", ".png", png); err != nil {
		t.Fatal(err)
	}
	current, _ = sc.CurrentArtwork(ctx, appID)
	if current["hero"] != fmt.Sprint(appID)+"_hero.png" {
		t.Errorf("tras reemplazar, hero = %q", current["hero"])
	}

	// Quitar un solo tipo no debe llevarse los demas.
	if err := sc.RemoveArtworkKind(ctx, appID, "logo"); err != nil {
		t.Fatal(err)
	}
	current, _ = sc.CurrentArtwork(ctx, appID)
	if _, ok := current["logo"]; ok {
		t.Error("el logo deberia haber desaparecido")
	}
	if _, ok := current["grid"]; !ok {
		t.Error("la portada no deberia haberse borrado")
	}

	// El contenido tiene que llegar intacto.
	got, err := sc.ReadFile(path.Join(dir, fmt.Sprint(appID)+"p.png"))
	if err != nil || string(got) != string(png) {
		t.Errorf("la imagen no se guardo bien: %v", err)
	}

	// Limpiar debe llevarse solo las del appid indicado.
	if err := sc.ClearArtwork(ctx, appID); err != nil {
		t.Fatalf("ClearArtwork: %v", err)
	}
	entries, err := sc.SFTP().ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("deberia quedar solo la del otro juego, quedan: %v", names)
	}
	if entries[0].Name() != fmt.Sprint(otro)+"p.png" {
		t.Errorf("quedo el fichero equivocado: %s", entries[0].Name())
	}
}

// Comprueba que se detecta bien como reiniciar Steam. Solo lee: no reinicia
// nada, que eso interrumpiria lo que el usuario tenga abierto.
func TestIntegrationSteamRestartMode(t *testing.T) {
	c, ctx := integrationClient(t)

	mode := c.SteamRestartMode(ctx)
	if mode != "gamemode" && mode != "desktop" {
		t.Fatalf("modo desconocido: %q", mode)
	}
	t.Logf("modo detectado: %s", mode)

	// En modo juego la unidad tiene que existir de verdad, porque es la que
	// vamos a reiniciar.
	if mode == "gamemode" {
		out, err := c.Run(ctx, "systemctl --user is-active steam-launcher.service")
		if err != nil || strings.TrimSpace(out) != "active" {
			t.Errorf("se detecto modo juego pero steam-launcher.service dice %q (%v)", out, err)
		}
		// Y su ExecStop debe encargarse de matar al hijo: el envoltorio de
		// Steam no reenvia senales, y sin eso el reinicio se quedaria colgado.
		unit, err := c.Run(ctx, "systemctl --user cat steam-launcher.service")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(unit, "ExecStop=") {
			t.Error("la unidad no define ExecStop; reiniciarla podria dejar Steam a medias")
		}
	}
}

// Con Steam parado, reiniciar debe negarse con un mensaje claro en vez de
// intentarlo a ciegas.
func TestRestartSteamNeedsSteamRunning(t *testing.T) {
	c, ctx := integrationClient(t)
	if c.SteamRunning(ctx) {
		t.Skip("Steam esta arrancado; no se puede comprobar la negativa sin cerrarlo")
	}
	if _, err := c.RestartSteam(ctx); err == nil {
		t.Error("deberia negarse si Steam no esta arrancado")
	}
}
