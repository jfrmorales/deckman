package deck

import "testing"

// Al otro lado de nombreSuelto hay un sftp.Remove y el nombre llega del
// navegador: lo que se cuela aqui se borra en la Deck. Los casos malos son
// justo los que convierten un nombre en una ruta.
func TestNombreSuelto(t *testing.T) {
	buenos := []string{
		"Sonic.zip",
		"Super Mario World (USA).sfc",
		"juego-con-guion.7z",
		"Título con acentos y ñ.chd",
		".oculto.nes",
		"raro..zip", // dos puntos en medio no suben de directorio
		"-empieza-por-guion.iso",
	}
	for _, n := range buenos {
		if err := nombreSuelto(n); err != nil {
			t.Errorf("nombreSuelto(%q) = %v, se esperaba que valiera", n, err)
		}
	}

	malos := []string{
		"",
		"   ",
		"..",
		".",
		"../../../home/deck/.ssh/authorized_keys",
		"..\\..\\windows",
		"sub/carpeta.zip",
		"con\x00nulo.zip",
	}
	for _, n := range malos {
		if err := nombreSuelto(n); err == nil {
			t.Errorf("nombreSuelto(%q) = nil, tendria que haberlo rechazado", n)
		}
	}
}

func TestNombreDesdeURL(t *testing.T) {
	buenos := map[string]string{
		"https://archive.org/download/item/Sonic%20The%20Hedgehog.zip": "Sonic The Hedgehog.zip",
		"http://ejemplo.com/roms/juego.7z":                             "juego.7z",
		"  https://ejemplo.com/juego.iso  ":                            "juego.iso",
		"https://ejemplo.com/a/b/c/mario.sfc?token=xyz":                "mario.sfc",
	}
	for in, want := range buenos {
		got, err := NombreDesdeURL(in)
		if err != nil {
			t.Errorf("NombreDesdeURL(%q) = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NombreDesdeURL(%q) = %q, se esperaba %q", in, got, want)
		}
	}

	// Lo que no es una descarga http(s) de un fichero con nombre no pasa: esa
	// cadena acaba en la linea de ordenes de la Deck.
	malos := []string{
		"",
		"no-es-una-url",
		"file:///etc/passwd",
		"ftp://ejemplo.com/juego.zip",
		"https://ejemplo.com/",     // sin nombre de fichero
		"https:///juego.zip",       // sin host
		"https://ejemplo.com/..",   // el nombre subiria de directorio
		"-o /home/deck/.bashrc",    // parece una opcion de curl
		"javascript:alert(1)",      //
		"https://ejemplo.com/a/b/", // termina en carpeta
	}
	for _, in := range malos {
		if got, err := NombreDesdeURL(in); err == nil {
			t.Errorf("NombreDesdeURL(%q) = %q, tendria que haber fallado", in, got)
		}
	}
}
