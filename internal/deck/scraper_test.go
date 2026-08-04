package deck

import "testing"

// libretro indexa por el nombre sin extension, y un juego de PlayStation son
// dos ficheros (.cue y .bin) que tienen que dar una sola busqueda.
func TestNombreBase(t *testing.T) {
	casos := map[string]string{
		"CTR - Crash Team Racing (Europe) (En,Fr,De,Es,It,Nl) (EDC).cue": "CTR - Crash Team Racing (Europe) (En,Fr,De,Es,It,Nl) (EDC)",
		"CTR - Crash Team Racing (Europe) (En,Fr,De,Es,It,Nl) (EDC).bin": "CTR - Crash Team Racing (Europe) (En,Fr,De,Es,It,Nl) (EDC)",
		"Mario Kart 64 (Europe) (Rev 1).z64":                             "Mario Kart 64 (Europe) (Rev 1)",
		"sin-extension":                                                  "sin-extension",
	}
	for in, want := range casos {
		if got := nombreBase(in); got != want {
			t.Errorf("nombreBase(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

// Los restos de descarga y los muebles de EmuDeck no se scrapean: buscarles
// caratula es garantia de no encontrar nada.
func TestEsROM(t *testing.T) {
	roms := []string{
		"Mario Kart 64 (Europe) (Rev 1).z64",
		"Metal Gear Solid 3 - Snake Eater (Spain).iso",
		"Legend of Zelda, The - The Wind Waker (Europe).rvz",
		"juego.CUE", // la extension puede venir en mayusculas
		"algo.zip",
	}
	for _, n := range roms {
		if !esROM(n) {
			t.Errorf("esROM(%q) = false", n)
		}
	}

	noRoms := []string{
		".xdp-Sin confirmar 173902.crdownload-RPuzhV", // resto real de una descarga
		"systeminfo.txt",
		"metadata.txt",
		"xenia-canary.config.toml",
		"dolphin-emu.sh",
		"EMULATOR.EXE",
		"sin-extension",
		"",
	}
	for _, n := range noRoms {
		if esROM(n) {
			t.Errorf("esROM(%q) = true", n)
		}
	}
}

// El boton de buscar caratulas no debe ofrecerse donde no haria nada.
func TestSistemaScrapeable(t *testing.T) {
	// Los cuatro que tenia la Deck de pruebas.
	for _, s := range []string{"psx", "ps2", "n64", "gc"} {
		if !SistemaScrapeable(s) {
			t.Errorf("SistemaScrapeable(%q) = false", s)
		}
	}
	// «emulators» es una carpeta de EmuDeck, no una consola.
	for _, s := range []string{"emulators", "", "loquesea"} {
		if SistemaScrapeable(s) {
			t.Errorf("SistemaScrapeable(%q) = true", s)
		}
	}
}

// Cada sistema que sabemos scrapear tiene que tener tambien nombre en
// libretro; si no, el boton se ofrece y luego falla.
func TestLibretroSistemasNoVacios(t *testing.T) {
	for sistema, carpeta := range libretroSistemas {
		if carpeta == "" {
			t.Errorf("el sistema %q no tiene carpeta de libretro", sistema)
		}
	}
}
