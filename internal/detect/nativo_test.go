package detect

import (
	"path/filepath"
	"testing"
)

// cabeceraELF fabrica los 20 primeros bytes de un ELF de 64 bits little-endian
// con el tipo y la maquina que se pidan. Es todo lo que mira Olfatear.
func cabeceraELF(tipo, maquina byte) string {
	cab := make([]byte, 20)
	copy(cab, "\x7fELF")
	cab[4], cab[5], cab[6] = 2, 1, 1
	cab[16], cab[18] = tipo, maquina
	return string(cab)
}

var (
	elfJuego   = cabeceraELF(elfDyn, elfX86_64) // PIE: lo que saca cualquier compilador moderno
	elfAarch64 = cabeceraELF(elfDyn, 183)
)

func TestOlfatear(t *testing.T) {
	casos := []struct {
		nombre, contenido    string
		ejecutable, lanzable bool
	}{
		{"juego", elfJuego, true, true},
		{"juego.x86_64", cabeceraELF(elfExec, elfX86_64), true, true},
		{"juego.x86", cabeceraELF(elfExec, elfX86), true, true},
		{"Juego.AppImage", elfJuego, true, true},
		{"start.sh", "#!/bin/sh\nexec ./juego\n", true, true},
		// Una biblioteca lleva el mismo e_type que un PIE: se aparta por el nombre.
		{"libSDL2-2.0.so.0", elfJuego, true, false},
		{"libfoo.so", elfJuego, true, false},
		// Otra arquitectura: se le pone el permiso (no estorba) pero no se propone.
		{"juego-arm64", elfAarch64, true, false},
		{"objeto.o", cabeceraELF(1, elfX86_64), true, false},
		{"juego.exe", "MZ\x90\x00 esto es un PE de Windows", false, false},
		{"datos.bin", "\x00\x01\x02\x03 nada reconocible aqui", false, false},
		{"vacio", "", false, false},
		{"corto", "\x7fEL", false, false},
	}
	dir := t.TempDir()
	for _, c := range casos {
		p := filepath.Join(dir, c.nombre)
		writeFile(t, p, 0, c.contenido)
		n := Olfatear(p)
		if n.Ejecutable() != c.ejecutable {
			t.Errorf("%s: Ejecutable() = %v, se esperaba %v", c.nombre, n.Ejecutable(), c.ejecutable)
		}
		if n.Lanzable(c.nombre) != c.lanzable {
			t.Errorf("%s: Lanzable() = %v, se esperaba %v", c.nombre, n.Lanzable(c.nombre), c.lanzable)
		}
	}
	if Olfatear(filepath.Join(dir, "no-existe")).Ejecutable() {
		t.Error("un fichero que no existe no puede salir como ejecutable")
	}
}

// Un ELF big-endian lleva el tipo y la maquina con los bytes al reves.
func TestOlfatearBigEndian(t *testing.T) {
	cab := make([]byte, 20)
	copy(cab, "\x7fELF")
	cab[5] = 2
	cab[17], cab[19] = elfExec, 20 // PowerPC
	n := olfatearCabecera(cab)
	if !n.ELF || n.Tipo != elfExec || n.Maquina != 20 {
		t.Errorf("cabecera big-endian mal leida: %+v", n)
	}
}

// La forma de una recompilacion estatica con build de Linux: un binario sin
// extension, sus bibliotecas y los datos. Antes de esto la deteccion decia
// "No se encontro ningun .exe".
func TestDetectNativoDeLinux(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Zelda64Recompiled-v1.2.0-Linux")
	writeFile(t, filepath.Join(dir, "Zelda64Recompiled"), 30<<20, elfJuego)
	writeFile(t, filepath.Join(dir, "libSDL2-2.0.so.0"), 3<<20, elfJuego)
	writeFile(t, filepath.Join(dir, "assets", "recomp.pak"), 50<<20, "")
	writeFile(t, filepath.Join(dir, "README.md"), 0, "#! esto no es un script, es un readme\n")

	r := Detect(dir)
	if len(r.Executables) != 1 {
		t.Fatalf("se esperaba un unico candidato, hay %+v", r.Executables)
	}
	c := r.Executables[0]
	if c.Rel != "Zelda64Recompiled" || !c.Native {
		t.Errorf("candidato = %+v; se esperaba Zelda64Recompiled marcado como nativo", c)
	}
}

// Unity y Godot: "Juego.x86_64" junto a su crash handler, que cae por la misma
// lista negra que los .exe.
func TestDetectNativoDescartaAccesorios(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Mi Juego")
	writeFile(t, filepath.Join(dir, "MiJuego.x86_64"), 25<<20, elfJuego)
	writeFile(t, filepath.Join(dir, "UnityCrashHandler64"), 1<<20, elfJuego)
	writeFile(t, filepath.Join(dir, "MiJuego_Data", "Plugins", "libsteam_api.so"), 1<<20, elfJuego)

	r := Detect(dir)
	if len(r.Executables) != 1 || r.Executables[0].Rel != "MiJuego.x86_64" {
		t.Fatalf("candidatos = %+v; solo deberia quedar MiJuego.x86_64", r.Executables)
	}
	if len(r.Skipped) != 1 || r.Skipped[0] != "UnityCrashHandler64" {
		t.Errorf("descartados = %v", r.Skipped)
	}
}

// Un script de arranque no pierde puntos por pequeno, y de los scripts solo
// cuentan los .sh y los que no llevan extension.
func TestDetectScriptDeArranque(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Aventura")
	writeFile(t, filepath.Join(dir, "Aventura.sh"), 0, "#!/bin/sh\nexec python3 game.py\n")
	writeFile(t, filepath.Join(dir, "game.rb"), 0, "#!/usr/bin/env ruby\n")
	writeFile(t, filepath.Join(dir, "lib", "herramienta.pl"), 0, "#!/usr/bin/perl\n")

	r := Detect(dir)
	if len(r.Executables) != 1 || r.Executables[0].Rel != "Aventura.sh" || !r.Executables[0].Native {
		t.Fatalf("candidatos = %+v; se esperaba solo Aventura.sh, nativo", r.Executables)
	}
	if r.Executables[0].Score < 60 {
		t.Errorf("el script de arranque puntua %d: no deberia penalizar por tamano", r.Executables[0].Score)
	}
}

// Carpeta con las dos versiones: se propone la de Windows (con Proton no
// depende de las bibliotecas de SteamOS) y la nativa queda de alternativa.
func TestDetectDosVersionesProponeWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Mi Juego")
	writeFile(t, filepath.Join(dir, "MiJuego.exe"), 25<<20, "MZ")
	writeFile(t, filepath.Join(dir, "MiJuego.x86_64"), 25<<20, elfJuego)

	r := Detect(dir)
	if len(r.Executables) != 2 {
		t.Fatalf("se esperaban los dos candidatos: %+v", r.Executables)
	}
	if r.Executables[0].Rel != "MiJuego.exe" || r.Executables[0].Native {
		t.Errorf("primero = %+v; se esperaba el .exe", r.Executables[0])
	}
	if !r.Executables[1].Native {
		t.Errorf("segundo = %+v; se esperaba el nativo", r.Executables[1])
	}
}

// ...salvo que el .exe sea claramente un accesorio: un juego nativo de 80 MB
// que trae un .exe de 100 KB no es un juego de Windows.
func TestDetectNativoGanaAUnExeAccesorio(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Mi Juego")
	writeFile(t, filepath.Join(dir, "MiJuego"), 80<<20, elfJuego)
	writeFile(t, filepath.Join(dir, "tools", "convertir.exe"), 100<<10, "MZ")

	r := Detect(dir)
	if len(r.Executables) == 0 || r.Executables[0].Rel != "MiJuego" {
		t.Fatalf("candidatos = %+v; se esperaba MiJuego el primero", r.Executables)
	}
}

func TestLanzableEn(t *testing.T) {
	dir := t.TempDir()
	// Con extension de datos ni se abre, aunque por dentro sea un ELF.
	writeFile(t, filepath.Join(dir, "raro.png"), 0, elfJuego)
	writeFile(t, filepath.Join(dir, "juego"), 0, elfJuego)
	if LanzableEn(filepath.Join(dir, "raro.png")) {
		t.Error("un .png no deberia proponerse como ejecutable")
	}
	if !LanzableEn(filepath.Join(dir, "juego")) {
		t.Error("un ELF de x86-64 sin extension deberia proponerse")
	}
}
