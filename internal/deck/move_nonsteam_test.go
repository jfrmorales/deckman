package deck

import (
	"strconv"
	"testing"
)

// Mover un juego no-Steam es reescribir las rutas de su acceso directo. Lo que
// NO puede pasar es que cambie su identidad: el appid es la clave con la que
// Steam tiene asociadas las caratulas y la version de Proton.
func TestRelocateShortcut(t *testing.T) {
	const viejo = "/home/deck/Games/Meltopia"
	const nuevo = "/run/media/deck/USD00/Games/Meltopia"

	t.Run("mueve exe, carpeta e icono", func(t *testing.T) {
		s := NewShortcut("Meltopia", viejo+"/bin/juego.exe", viejo, "")
		s.SetStr("icon", viejo+"/icono.png")
		id := s.AppID()

		relocateShortcut(s, id, viejo, nuevo)

		if got := Unquote(s.GetStr("Exe")); got != nuevo+"/bin/juego.exe" {
			t.Errorf("Exe = %q", got)
		}
		if got := Unquote(s.GetStr("StartDir")); got != nuevo {
			t.Errorf("StartDir = %q", got)
		}
		if got := Unquote(s.GetStr("icon")); got != nuevo+"/icono.png" {
			t.Errorf("icon = %q", got)
		}
		if s.AppID() != id {
			t.Errorf("el appid ha cambiado: %d -> %d; las caratulas y el Proton apuntarian a otro juego", id, s.AppID())
		}
	})

	t.Run("conserva la subcarpeta de arranque", func(t *testing.T) {
		// El caso de Riddick: el acceso directo arranca desde
		// System/Win32_x86, no desde la raiz del juego. Aplastar StartDir con
		// la carpeta nueva cambiaria por donde arranca.
		sub := viejo + "/System/Win32_x86"
		s := NewShortcut("Riddick", sub+"/DarkAthena.exe", sub, "")
		relocateShortcut(s, s.AppID(), viejo, nuevo)
		if got := Unquote(s.GetStr("StartDir")); got != nuevo+"/System/Win32_x86" {
			t.Errorf("StartDir = %q", got)
		}
		if got := Unquote(s.GetStr("Exe")); got != nuevo+"/System/Win32_x86/DarkAthena.exe" {
			t.Errorf("Exe = %q", got)
		}
	})

	t.Run("respeta el appid guardado", func(t *testing.T) {
		// Los accesos directos que crea Steam llevan un id aleatorio que no
		// coincide con el CRC que calcularia deckman. Mandan ellos.
		s := NewShortcut("Meltopia", viejo+"/juego.exe", viejo, "")
		s.SetInt("appid", 1234567890)

		relocateShortcut(s, 1234567890, viejo, nuevo)

		if s.AppID() != 1234567890 {
			t.Errorf("appid = %d, se esperaba el guardado", s.AppID())
		}
	})

	t.Run("una entrada sin appid se queda con el suyo", func(t *testing.T) {
		// Sin campo appid, AppID() lo calcula a partir de Exe+AppName: al
		// cambiar el Exe cambiaria el juego de identidad. Hay que fijarlo antes.
		s := NewShortcut("Meltopia", viejo+"/juego.exe", viejo, "")
		antes := s.AppID()
		for i, f := range s.Fields {
			if f.Key == "appid" {
				s.Fields = append(s.Fields[:i], s.Fields[i+1:]...)
				break
			}
		}
		if s.GetInt("appid") != 0 {
			t.Fatal("no se pudo quitar el campo appid de la muestra")
		}

		relocateShortcut(s, antes, viejo, nuevo)

		if s.AppID() != antes {
			t.Errorf("appid = %d, se esperaba %d", s.AppID(), antes)
		}
	})

	t.Run("las opciones de lanzamiento tambien mudan", func(t *testing.T) {
		s := NewShortcut("Meltopia", viejo+"/juego.exe", viejo, "--config "+viejo+"/cfg.ini %command%")
		relocateShortcut(s, s.AppID(), viejo, nuevo)
		if got := s.GetStr("LaunchOptions"); got != "--config "+nuevo+"/cfg.ini %command%" {
			t.Errorf("LaunchOptions = %q", got)
		}
	})

	t.Run("no toca lo que vive fuera de la carpeta", func(t *testing.T) {
		// El caso de Heroic: el acceso directo lanza flatpak, no un fichero de
		// la carpeta. El Exe no debe reescribirse.
		s := NewShortcut("Meltopia", "flatpak", viejo, "run com.heroicgameslauncher.hgl")
		relocateShortcut(s, s.AppID(), viejo, nuevo)
		if got := Unquote(s.GetStr("Exe")); got != "flatpak" {
			t.Errorf("Exe = %q, no deberia haberse tocado", got)
		}
	})

	t.Run("el fichero se puede releer", func(t *testing.T) {
		s := NewShortcut("Meltopia", viejo+"/juego.exe", viejo, "")
		sf := &ShortcutsFile{Entries: []*Shortcut{s}}
		antes := sf.Marshal()

		relocateShortcut(s, s.AppID(), viejo, nuevo)
		despues := sf.Marshal()

		vuelta, err := ParseShortcuts(despues)
		if err != nil {
			t.Fatalf("shortcuts.vdf reescrito ilegible: %v", err)
		}
		if got := Unquote(vuelta.Entries[0].GetStr("StartDir")); got != nuevo {
			t.Errorf("tras releer, StartDir = %q", got)
		}
		// Y la red de seguridad no debe saltar: no se pierde ningun juego.
		if err := checkNoShortcutsLost(antes, despues, nil); err != nil {
			t.Errorf("mover no deberia hacer saltar la guardia: %v", err)
		}
	})
}

// withinDir decide si un juego no-Steam se puede mover: su ejecutable tiene que
// estar dentro de su propia carpeta.
func TestWithinDir(t *testing.T) {
	casos := []struct {
		dir, p string
		quiere bool
	}{
		{"/home/deck/Games/X", "/home/deck/Games/X/juego.exe", true},
		{"/home/deck/Games/X", "/home/deck/Games/X", true},
		{"/home/deck/Games/X", "/home/deck/Games/X/bin/juego.exe", true},
		// El vecino que empieza igual: el clasico fallo de comparar prefijos.
		{"/home/deck/Games/X", "/home/deck/Games/XY/juego.exe", false},
		{"/home/deck/Games/X", "flatpak", false},
		{"/home/deck/Games/X", "/usr/bin/retroarch", false},
	}
	for _, c := range casos {
		if got := withinDir(c.dir, c.p); got != c.quiere {
			t.Errorf("withinDir(%q, %q) = %v", c.dir, c.p, got)
		}
	}
}

// La unidad de un juego no-Steam no la dice Steam: hay que deducirla de donde
// esta su carpeta, o la interfaz no sabe a donde ofrecer moverlo.
func TestLibraryForPath(t *testing.T) {
	libs := []Library{
		{Path: "/home/deck/.local/share/Steam", Label: "Interno", IsDefault: true},
		{Path: "/run/media/deck/USD00", Label: "USD00", Removable: true},
	}
	casos := []struct{ p, quiere string }{
		// ~/Games no cuelga de la biblioteca de Steam, pero es el disco interno.
		{"/home/deck/Games/X", "/home/deck/.local/share/Steam"},
		{"/home/deck/Emulation/roms", "/home/deck/.local/share/Steam"},
		{"/run/media/deck/USD00/Games/X", "/run/media/deck/USD00"},
		{"/run/media/deck/USD00", "/run/media/deck/USD00"},
		{"", ""},
	}
	for _, c := range casos {
		if got := libraryForPath(libs, c.p); got != c.quiere {
			t.Errorf("libraryForPath(%q) = %q, se esperaba %q", c.p, got, c.quiere)
		}
	}
}

// La carpeta del juego no es la del ejecutable, y confundirlas cuesta caro:
// mover o borrar System/Win32_x86 deja 11 GB tirados y el juego roto.
func TestGameRootFor(t *testing.T) {
	libs := []Library{
		{Path: "/home/deck/.local/share/Steam", IsDefault: true, GamesDir: "/home/deck/Games"},
		{Path: "/run/media/deck/USD00", Removable: true, GamesDir: "/run/media/deck/USD00/Games"},
	}
	casos := []struct{ startDir, quiere string }{
		// El .exe tres niveles por debajo: la raiz sigue siendo el juego.
		{"/home/deck/Games/Riddick/System/Win32_x86", "/home/deck/Games/Riddick"},
		{"/home/deck/Games/Meltopia", "/home/deck/Games/Meltopia"},
		{"/run/media/deck/USD00/Games/X/bin", "/run/media/deck/USD00/Games/X"},
		// Fuera de un Games no se sabe donde empieza el juego: mejor no tocarlo.
		{"/home/deck/homebrew/plugins/moondeck/python", ""},
		{"/run/media/deck/USD00/Emulation/tools/launchers", ""},
		{"/home/deck/Games", ""},
		{"", ""},
	}
	for _, c := range casos {
		if got := gameRootFor(libs, c.startDir); got != c.quiere {
			t.Errorf("gameRootFor(%q) = %q, se esperaba %q", c.startDir, got, c.quiere)
		}
	}
}

// El nombre del fichero de portada lleva el appid del acceso directo, que es un
// uint32: comprobamos que el id que maneja la interfaz cabe donde toca.
func TestAppIDCabeEnUint32(t *testing.T) {
	s := NewShortcut("Juego", "/home/deck/Games/J/j.exe", "/home/deck/Games/J", "")
	txt := strconv.FormatUint(uint64(s.AppID()), 10)
	vuelta, err := strconv.ParseUint(txt, 10, 32)
	if err != nil || uint32(vuelta) != s.AppID() {
		t.Errorf("el appid %s no sobrevive la ida y vuelta por texto: %v", txt, err)
	}
}
