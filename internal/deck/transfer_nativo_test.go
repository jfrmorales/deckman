package deck

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pkg/sftp"
)

// clienteSFTPLocal monta un servidor SFTP de pkg/sftp sobre este mismo sistema
// de ficheros, unido al cliente por dos tuberias. Sirve para probar UploadTree
// de verdad (permisos y enlaces incluidos) sin SSH y sin la Deck.
func clienteSFTPLocal(t *testing.T) *Client {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	srv, err := sftp.NewServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, sw})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	cli, err := sftp.NewClientPipe(cr, cw)
	if err != nil {
		t.Fatal(err)
	}
	// Las tuberias se cierran primero y a mano: cli.Close() espera a que su
	// lector termine, y el servidor no cierra su lado hasta que se lo piden.
	// En el otro orden la prueba pasa y luego se queda colgada al limpiar.
	t.Cleanup(func() { cw.Close(); sw.Close(); cli.Close(); srv.Close() })
	return &Client{sftp: cli}
}

func escribir(t *testing.T, ruta, contenido string, modo os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(ruta), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ruta, []byte(contenido), modo); err != nil {
		t.Fatal(err)
	}
}

// elfFalso: lo justo para que la cabecera se reconozca como ELF de x86-64.
func elfFalso() string {
	cab := make([]byte, 64)
	copy(cab, "\x7fELF")
	cab[4], cab[5], cab[16], cab[18] = 2, 1, 3, 62
	return string(cab)
}

// El caso que no funcionaba: un juego nativo de Linux que llega SIN permisos
// (descomprimido en Windows, o pasado por un NTFS). Todo esta a 0644 en el
// origen y aun asi el binario y el script tienen que quedar ejecutables.
func TestUploadTreeDaPermisoDeEjecucion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("el destino de la prueba necesita permisos de Unix")
	}
	c := clienteSFTPLocal(t)
	origen := filepath.Join(t.TempDir(), "Juego")
	destino := t.TempDir()

	escribir(t, filepath.Join(origen, "juego"), elfFalso(), 0o644)
	escribir(t, filepath.Join(origen, "start.sh"), "#!/bin/sh\nexec ./juego\n", 0o644)
	escribir(t, filepath.Join(origen, "lib", "libfoo.so.1"), elfFalso(), 0o644)
	escribir(t, filepath.Join(origen, "juego.exe"), "MZ no soy de aqui", 0o644)
	escribir(t, filepath.Join(origen, "datos.pak"), "datos y mas datos", 0o644)
	// Sin cabecera reconocible, pero con el bit puesto en el origen: se respeta.
	escribir(t, filepath.Join(origen, "raro"), "sin cabecera", 0o755)

	if err := c.UploadTree(context.Background(), origen, destino, nil); err != nil {
		t.Fatal(err)
	}

	esperado := map[string]bool{
		"juego": true, "start.sh": true, "lib/libfoo.so.1": true, "raro": true,
		"juego.exe": false, "datos.pak": false,
	}
	for rel, quiere := range esperado {
		fi, err := os.Stat(filepath.Join(destino, "Juego", rel))
		if err != nil {
			t.Fatalf("%s no se copio: %v", rel, err)
		}
		if tiene := fi.Mode()&0o100 != 0; tiene != quiere {
			t.Errorf("%s quedo con modo %v; ejecutable=%v, se esperaba %v", rel, fi.Mode(), tiene, quiere)
		}
	}
}

// Reenviar el juego arregla una copia hecha por una version que no ponia el
// permiso: el fichero ya esta (mismo tamano, se salta) pero sin el bit.
func TestUploadTreeReparaPermisoEnLoYaCopiado(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("el destino de la prueba necesita permisos de Unix")
	}
	c := clienteSFTPLocal(t)
	origen := filepath.Join(t.TempDir(), "Juego")
	destino := t.TempDir()
	escribir(t, filepath.Join(origen, "juego"), elfFalso(), 0o644)
	// La copia vieja: mismo contenido, sin permiso de ejecucion.
	escribir(t, filepath.Join(destino, "Juego", "juego"), elfFalso(), 0o644)

	if err := c.UploadTree(context.Background(), origen, destino, nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(destino, "Juego", "juego"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o100 == 0 {
		t.Errorf("el binario ya copiado sigue sin permiso de ejecucion: %v", fi.Mode())
	}
}

// Los enlaces de las bibliotecas se recrean; los que apuntan fuera de la
// carpeta, no. Y repetir el envio no falla porque el enlace ya exista.
func TestUploadTreeRecreaEnlaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("crear enlaces simbolicos en Windows pide privilegios")
	}
	c := clienteSFTPLocal(t)
	origen := filepath.Join(t.TempDir(), "Juego")
	destino := t.TempDir()
	escribir(t, filepath.Join(origen, "lib", "libSDL2-2.0.so.0.18.2"), elfFalso(), 0o755)
	escribir(t, filepath.Join(origen, "juego"), elfFalso(), 0o755)
	enlazar := func(apunta, nombre string) {
		t.Helper()
		if err := os.Symlink(apunta, filepath.Join(origen, nombre)); err != nil {
			t.Fatal(err)
		}
	}
	enlazar("libSDL2-2.0.so.0.18.2", "lib/libSDL2-2.0.so.0")
	enlazar("lib", "lib64") // a una carpeta
	enlazar("/etc/passwd", "absoluto")
	enlazar("../../fuera", "lib/escapa")

	for intento := 1; intento <= 2; intento++ {
		if err := c.UploadTree(context.Background(), origen, destino, nil); err != nil {
			t.Fatalf("envio %d: %v", intento, err)
		}
	}

	raiz := filepath.Join(destino, "Juego")
	for nombre, quiere := range map[string]string{
		"lib/libSDL2-2.0.so.0": "libSDL2-2.0.so.0.18.2",
		"lib64":                "lib",
	} {
		got, err := os.Readlink(filepath.Join(raiz, nombre))
		if err != nil || got != quiere {
			t.Errorf("enlace %s -> %q (%v); se esperaba %q", nombre, got, err, quiere)
		}
	}
	for _, nombre := range []string{"absoluto", "lib/escapa"} {
		if _, err := os.Lstat(filepath.Join(raiz, nombre)); err == nil {
			t.Errorf("el enlace %s apunta fuera de la carpeta y no deberia haberse copiado", nombre)
		}
	}
}

func TestEnlaceSeguro(t *testing.T) {
	casos := []struct {
		rel, destino string
		quiere       bool
	}{
		{"lib/libfoo.so.1", "libfoo.so.1.2", true},
		{"lib64", "lib", true},
		{"bin/juego", "../lib/juego.real", true},
		{"juego", "./bin/juego", true},
		{"absoluto", "/usr/lib/libfoo.so", false},
		{"escapa", "../otro", false},
		{"lib/escapa", "../../otro", false},
		{"raiz", "..", false},
		{"vacio", "", false},
		{"windows", `..\otro`, false},
	}
	for _, c := range casos {
		if got := enlaceSeguro(c.rel, c.destino); got != c.quiere {
			t.Errorf("enlaceSeguro(%q, %q) = %v, se esperaba %v", c.rel, c.destino, got, c.quiere)
		}
	}
}
