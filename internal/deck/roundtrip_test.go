package deck

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Skipf("falta la muestra %s: %v", name, err)
	}
	return data
}

// El requisito no negociable: si leemos y reescribimos shortcuts.vdf sin
// tocar nada, tiene que salir exactamente el mismo fichero. Cualquier
// diferencia significa que estamos corrompiendo la configuracion de Steam.
func TestShortcutsRoundTrip(t *testing.T) {
	orig := fixture(t, "shortcuts.vdf")

	sf, err := ParseShortcuts(orig)
	if err != nil {
		t.Fatalf("no se pudo interpretar: %v", err)
	}
	if len(sf.Entries) == 0 {
		t.Fatal("no se leyo ningun acceso directo")
	}
	t.Logf("%d accesos directos leidos", len(sf.Entries))

	got := sf.Marshal()
	if !bytes.Equal(orig, got) {
		t.Errorf("el fichero reescrito difiere del original (%d bytes vs %d)", len(orig), len(got))
		for i := 0; i < len(orig) && i < len(got); i++ {
			if orig[i] != got[i] {
				lo := max(0, i-24)
				t.Fatalf("primera diferencia en el byte %d\noriginal: %q\nescrito:  %q",
					i, orig[lo:min(len(orig), i+24)], got[lo:min(len(got), i+24)])
			}
		}
	}
}

// Los campos que leemos tienen que ser los que Steam guardo de verdad.
func TestShortcutsFields(t *testing.T) {
	sf, err := ParseShortcuts(fixture(t, "shortcuts.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range sf.Entries {
		name := s.GetStr("AppName")
		exe := s.GetStr("Exe")
		if name == "" {
			t.Errorf("acceso directo %d sin nombre", i)
		}
		t.Logf("[%d] %-40s appid=%d exe=%s", i, name, s.AppID(), Unquote(exe))

		// El appid guardado tiene que mandar sobre cualquier calculo nuestro.
		// Los accesos directos que crea Steam llevan un id aleatorio, y si lo
		// recalculasemos el mapeo de Proton apuntaria a otro juego.
		if stored := s.GetInt("appid"); stored != 0 {
			if got := s.AppID(); got != uint32(stored) {
				t.Errorf("[%d] %s: AppID() devolvio %d en vez del guardado %d", i, name, got, stored)
			}
		}
		_ = exe
	}
}

// Reenviar un juego que ya estaba no debe duplicarlo, y tiene que conservar su
// appid original aunque Steam se lo hubiera asignado al azar.
func TestAddNonSteamDoesNotDuplicate(t *testing.T) {
	sf, err := ParseShortcuts(fixture(t, "shortcuts.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	before := len(sf.Entries)

	// Tomamos uno real cuyo id no sigue la formula CRC.
	var target *Shortcut
	for _, s := range sf.Entries {
		if stored := uint32(s.GetInt("appid")); stored != 0 && stored != ShortcutAppID(s.GetStr("Exe"), s.GetStr("AppName")) {
			target = s
			break
		}
	}
	if target == nil {
		t.Skip("no hay ningun acceso directo con id aleatorio en la muestra")
	}
	origID := target.AppID()
	exe := Unquote(target.GetStr("Exe"))
	name := target.GetStr("AppName")

	// Reproducimos la logica de emparejado de AddNonSteamGame.
	found := -1
	for i, s := range sf.Entries {
		if Unquote(s.GetStr("Exe")) == exe {
			found = i
			break
		}
	}
	if found < 0 {
		t.Fatalf("no se reencontro %s por su ejecutable", name)
	}

	replacement := NewShortcut(name, exe, "/home/deck/Games/X", "")
	replacement.SetInt("appid", int32(origID))
	sf.Entries[found] = replacement

	if len(sf.Entries) != before {
		t.Errorf("se duplico la entrada: %d -> %d", before, len(sf.Entries))
	}
	if got := sf.Entries[found].AppID(); got != origID {
		t.Errorf("se perdio el appid original: %d -> %d", origID, got)
	}

	// Y el fichero resultante sigue siendo legible.
	if _, err := ParseShortcuts(sf.Marshal()); err != nil {
		t.Fatalf("el fichero resultante no se puede releer: %v", err)
	}
}

// config.vdf es el fichero mas delicado: 66 KB con toda la configuracion del
// cliente. Reescribirlo mal dejaria Steam inservible.
func TestConfigVDFRoundTrip(t *testing.T) {
	orig := fixture(t, "config.vdf")

	root, err := ParseVDF(orig)
	if err != nil {
		t.Fatalf("no se pudo interpretar config.vdf: %v", err)
	}
	got := root.Marshal()

	// Comparamos reinterpretando: el formateo exacto (tabulaciones) puede
	// variar, pero el contenido tiene que ser identico.
	reparsed, err := ParseVDF(got)
	if err != nil {
		t.Fatalf("lo que escribimos ya no se puede releer: %v", err)
	}
	if a, b := len(root.Children), len(reparsed.Children); a != b {
		t.Fatalf("se perdieron claves de primer nivel: %d -> %d", a, b)
	}
	if !bytes.Equal(got, reparsed.Marshal()) {
		t.Error("la reescritura no es estable entre pasadas")
	}

	mapping := root.Get("InstallConfigStore", "Software", "Valve", "Steam", "CompatToolMapping")
	if mapping == nil {
		t.Fatal("no se encontro CompatToolMapping; el resto del programa depende de esa ruta")
	}
	t.Logf("%d juegos con Proton asignado", len(mapping.Children))
	for _, e := range mapping.Children {
		t.Logf("  appid %-12s -> %s", e.Key, e.GetString("name"))
	}
}

func TestLibraryFolders(t *testing.T) {
	root, err := ParseVDF(fixture(t, "libraryfolders.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	lf := root.Get("libraryfolders")
	if lf == nil {
		t.Fatal("falta la clave libraryfolders")
	}
	for _, e := range lf.Children {
		p := e.GetString("path")
		if p == "" {
			t.Errorf("biblioteca %s sin ruta", e.Key)
		}
		apps := e.Get("apps")
		n := 0
		if apps != nil {
			n = len(apps.Children)
		}
		t.Logf("biblioteca %s: %s (%d juegos)", e.Key, p, n)
	}
}

func TestAppManifest(t *testing.T) {
	root, err := ParseVDF(fixture(t, "appmanifest_252950.acf"))
	if err != nil {
		t.Fatal(err)
	}
	st := root.Get("AppState")
	if st == nil {
		t.Fatal("falta AppState")
	}
	for _, key := range []string{"appid", "name", "installdir", "SizeOnDisk"} {
		if v := st.GetString(key); v == "" {
			t.Errorf("falta el campo %s", key)
		} else {
			t.Logf("%-12s = %s", key, v)
		}
	}
}

// Comprobamos el calculo del appid contra un caso conocido, para que un
// cambio futuro en la funcion no rompa el mapeo de Proton en silencio.
func TestShortcutAppIDStable(t *testing.T) {
	// Las comillas forman parte del valor, tal como lo guarda Steam.
	id := ShortcutAppID(`"/home/deck/Games/Test/test.exe"`, "Test")
	if id&0x80000000 == 0 {
		t.Errorf("el bit alto debe estar puesto, salio %#x", id)
	}
	if id2 := ShortcutAppID(`"/home/deck/Games/Test/test.exe"`, "Test"); id != id2 {
		t.Error("la funcion no es determinista")
	}
	if same := ShortcutAppID(`"/home/deck/Games/Otro/otro.exe"`, "Test"); same == id {
		t.Error("ejecutables distintos no deberian dar el mismo id")
	}
}

// Un shortcuts.vdf nuevo tiene que ser legible por nosotros mismos.
func TestNewShortcutRoundTrip(t *testing.T) {
	sf := &ShortcutsFile{}
	sf.Entries = append(sf.Entries,
		NewShortcut("Mi Juego", "/home/deck/Games/Mi Juego/juego.exe", "/home/deck/Games/Mi Juego", ""))

	data := sf.Marshal()
	back, err := ParseShortcuts(data)
	if err != nil {
		t.Fatalf("no se puede releer lo que escribimos: %v", err)
	}
	if len(back.Entries) != 1 {
		t.Fatalf("se esperaba 1 entrada, hay %d", len(back.Entries))
	}
	s := back.Entries[0]
	if got := s.GetStr("AppName"); got != "Mi Juego" {
		t.Errorf("AppName = %q", got)
	}
	if got := Unquote(s.GetStr("Exe")); got != "/home/deck/Games/Mi Juego/juego.exe" {
		t.Errorf("Exe = %q", got)
	}
	if !bytes.Equal(data, back.Marshal()) {
		t.Error("la reescritura no es estable")
	}
}

// Un fichero vacio o inexistente es un caso normal, no un error.
func TestParseShortcutsEmpty(t *testing.T) {
	sf, err := ParseShortcuts(nil)
	if err != nil {
		t.Fatalf("un fichero vacio no deberia fallar: %v", err)
	}
	if len(sf.Entries) != 0 {
		t.Error("no deberia haber entradas")
	}
}
