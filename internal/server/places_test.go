package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Una carpeta de montaje vacia NO es una unidad: en Linux /media, /mnt y
// /run/media/<usuario> existen siempre, haya discos conectados o no.
func TestMountedUnderIgnoresEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("solo aplica a Linux")
	}
	base := t.TempDir()

	// Vacia: no debe salir.
	if err := os.MkdirAll(filepath.Join(base, "vacia"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Con contenido: si.
	conDatos := filepath.Join(base, "DISCO")
	if err := os.MkdirAll(filepath.Join(conDatos, "algo"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := mountedUnder(base)
	if len(got) != 1 || filepath.Base(got[0]) != "DISCO" {
		t.Errorf("mountedUnder = %v; se esperaba solo DISCO", got)
	}
}

// Un directorio que no existe no debe reventar.
func TestMountedUnderMissing(t *testing.T) {
	if got := mountedUnder(filepath.Join(t.TempDir(), "no-existe")); got != nil {
		t.Errorf("se esperaba nada, salio %v", got)
	}
}

// En macOS el disco de arranque sale en /Volumes como enlace a /. No es una
// unidad externa y ya esta listado como "Sistema de ficheros": mountedUnder no
// tiene que seguirlo, o apareceria la raiz dos veces con otro nombre.
func TestMountedUnderSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("los enlaces simbolicos en Windows piden permisos")
	}
	base := t.TempDir()
	if err := os.Symlink("/", filepath.Join(base, "Macintosh HD")); err != nil {
		t.Fatal(err)
	}
	pincho := filepath.Join(base, "PINCHO")
	if err := os.MkdirAll(filepath.Join(pincho, "juegos"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := mountedUnder(base)
	if len(got) != 1 || filepath.Base(got[0]) != "PINCHO" {
		t.Errorf("mountedUnder = %v; se esperaba solo PINCHO", got)
	}
}
