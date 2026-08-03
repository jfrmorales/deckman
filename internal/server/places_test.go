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
