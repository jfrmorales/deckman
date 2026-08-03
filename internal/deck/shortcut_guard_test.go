package deck

import (
	"strings"
	"testing"
)

// La red de seguridad que faltaba. Un shortcuts.vdf perdido borra de la
// biblioteca TODOS los juegos no-Steam de golpe, y sin copia no se recupera.
func TestCheckNoShortcutsLost(t *testing.T) {
	base := fixture(t, "shortcuts.vdf")
	sf, err := ParseShortcuts(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Entries) < 3 {
		t.Skip("la muestra tiene muy pocos accesos directos")
	}

	t.Run("sin cambios pasa", func(t *testing.T) {
		if err := checkNoShortcutsLost(base, sf.Marshal(), nil); err != nil {
			t.Errorf("no deberia quejarse: %v", err)
		}
	})

	t.Run("anadir uno pasa", func(t *testing.T) {
		mas := &ShortcutsFile{Entries: append([]*Shortcut{}, sf.Entries...)}
		mas.Entries = append(mas.Entries, NewShortcut("Nuevo", "/home/deck/Games/x/x.exe", "", ""))
		if err := checkNoShortcutsLost(base, mas.Marshal(), nil); err != nil {
			t.Errorf("anadir no deberia quejarse: %v", err)
		}
	})

	t.Run("perder uno se bloquea", func(t *testing.T) {
		menos := &ShortcutsFile{Entries: sf.Entries[1:]}
		err := checkNoShortcutsLost(base, menos.Marshal(), nil)
		if err == nil {
			t.Fatal("deberia negarse: se pierde un acceso directo")
		}
		if !strings.Contains(err.Error(), sf.Entries[0].GetStr("AppName")) {
			t.Errorf("el error deberia decir cual se pierde: %v", err)
		}
		t.Logf("bloqueado correctamente: %v", err)
	})

	t.Run("borrado intencionado pasa", func(t *testing.T) {
		quitar := sf.Entries[0].AppID()
		menos := &ShortcutsFile{Entries: sf.Entries[1:]}
		if err := checkNoShortcutsLost(base, menos.Marshal(), &quitar); err != nil {
			t.Errorf("quitar el que se pidio no deberia quejarse: %v", err)
		}
	})

	t.Run("quedarse en uno se bloquea", func(t *testing.T) {
		// Exactamente lo que paso en la Deck: de 7 entradas a 1.
		uno := &ShortcutsFile{Entries: sf.Entries[:1]}
		if err := checkNoShortcutsLost(base, uno.Marshal(), nil); err == nil {
			t.Fatal("pasar de varios accesos directos a uno deberia bloquearse")
		}
	})

	t.Run("fichero vacio de partida pasa", func(t *testing.T) {
		if err := checkNoShortcutsLost(nil, sf.Marshal(), nil); err != nil {
			t.Errorf("sin fichero previo no hay nada que perder: %v", err)
		}
	})

	t.Run("resultado ilegible se bloquea", func(t *testing.T) {
		if err := checkNoShortcutsLost(base, []byte("basura que no es VDF"), nil); err == nil {
			t.Error("deberia negarse a escribir algo que no se puede releer")
		}
	})
}

// Reenviar un juego que ya estaba tiene que ACTUALIZAR su entrada, no
// sustituirla: si se rehace desde cero se pierden las etiquetas, el icono, las
// horas jugadas y los campos que Steam anada en el futuro, que es justo lo que
// el parser conserva a proposito.
func TestUpdateShortcutConservaLosDemasCampos(t *testing.T) {
	e := NewShortcut("Juego Viejo", "/home/deck/Games/viejo/juego.exe", "/home/deck/Games/viejo", "-viejo")
	// id aleatorio, como los que crea Steam (no cabe en int32 sin convertir)
	var idGuardado uint32 = 0x87654321
	e.SetInt("appid", int32(idGuardado))
	e.SetStr("icon", "/home/deck/Games/viejo/icono.png")
	e.SetInt("LastPlayTime", 1750000000)
	e.SetStr("CampoQueNoConocemos", "no lo toques")
	if f := e.find("tags"); f != nil {
		f.Children = append(f.Children, &ShortcutField{Type: binString, Key: "0", Str: "favoritos"})
	}

	got := updateShortcut(e, NonSteamOptions{
		Name:          "Juego Nuevo",
		Exe:           "/home/deck/Games/nuevo/juego.exe",
		StartDir:      "/home/deck/Games/nuevo",
		LaunchOptions: "-nuevo",
	}, 0x81111111)

	if got != idGuardado {
		t.Errorf("se perdio el appid guardado: %#x", got)
	}
	if v := Unquote(e.GetStr("Exe")); v != "/home/deck/Games/nuevo/juego.exe" {
		t.Errorf("Exe = %q", v)
	}
	if v := Unquote(e.GetStr("StartDir")); v != "/home/deck/Games/nuevo" {
		t.Errorf("StartDir = %q", v)
	}
	if v := e.GetStr("AppName"); v != "Juego Nuevo" {
		t.Errorf("AppName = %q", v)
	}
	if v := e.GetStr("LaunchOptions"); v != "-nuevo" {
		t.Errorf("LaunchOptions = %q", v)
	}

	// Y lo que no se pidio cambiar sigue tal cual.
	if v := e.GetStr("icon"); v != "/home/deck/Games/viejo/icono.png" {
		t.Errorf("se perdio el icono: %q", v)
	}
	if v := e.GetInt("LastPlayTime"); v != 1750000000 {
		t.Errorf("se perdieron las horas jugadas: %d", v)
	}
	if v := e.GetStr("CampoQueNoConocemos"); v != "no lo toques" {
		t.Errorf("se perdio un campo desconocido: %q", v)
	}
	tags := e.find("tags")
	if tags == nil || len(tags.Children) != 1 || tags.Children[0].Str != "favoritos" {
		t.Errorf("se perdieron las etiquetas: %+v", tags)
	}
}

// Un acceso directo sin appid escrito se queda con el que calculamos nosotros.
func TestUpdateShortcutSinAppIDUsaElCalculado(t *testing.T) {
	e := &Shortcut{}
	e.SetStr("AppName", "Sin id")
	e.SetStr("Exe", `"/home/deck/Games/x/x.exe"`)

	got := updateShortcut(e, NonSteamOptions{
		Name: "Sin id", Exe: "/home/deck/Games/x/x.exe", StartDir: "/home/deck/Games/x",
	}, 0x81234567)

	if got != 0x81234567 {
		t.Errorf("appid = %#x, se esperaba el calculado", got)
	}
	if v := uint32(e.GetInt("appid")); v != 0x81234567 {
		t.Errorf("no se escribio el appid en la entrada: %#x", v)
	}
}
