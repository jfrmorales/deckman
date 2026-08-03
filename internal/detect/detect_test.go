package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"Resident.Evil.4.REPACK-FitGirl":          "Resident Evil 4",
		"Devil May Cry 5":                         "Devil May Cry 5",
		"Elden.Ring.v1.12.3-CODEX":                "Elden Ring",
		"Cyberpunk 2077 [FitGirl Repack]":         "Cyberpunk 2077",
		"Hollow.Knight.Silksong.MULTi12-ElAmigos": "Hollow Knight Silksong",
		"DOOM.Eternal.Build.7407769-EMPRESS":      "DOOM Eternal",
		"Portal 2":                                "Portal 2",
		"The.Witcher.3.Wild.Hunt.GOTY.v4.04-GOG":  "The Witcher 3 Wild Hunt GOTY",
		"Baldurs Gate 3 (v4.1.1.3937785)":         "Baldurs Gate 3",
		"Half-Life 2":                             "Half Life 2",
		"Tomb_Raider_2013_MULTi9-PLAZA":           "Tomb Raider 2013",
		"":                                        "",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

// Los titulos con puntos de verdad no deben destrozarse: solo tratamos los
// puntos como separadores cuando el nombre no tiene ningun espacio.
func TestCleanNamePreservesRealTitles(t *testing.T) {
	for _, in := range []string{"S.T.A.L.K.E.R. 2", "Mr. Robot"} {
		if got := CleanName(in); got != in {
			t.Errorf("CleanName(%q) = %q; no deberia tocarlo", in, got)
		}
	}
}

func writeFile(t *testing.T, path string, size int, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(content)
	if size > len(data) {
		data = append(data, make([]byte, size-len(data))...)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Reproduce la estructura real de la carpeta de Resident Evil 4 de la Deck,
// incluido el CrashReport.exe de 151 MB que es la trampa principal.
func TestDetectResidentEvil4(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Resident.Evil.4.REPACK-FitGirl")
	writeFile(t, filepath.Join(dir, "steam_appid.txt"), 0, "2050650\n")
	writeFile(t, filepath.Join(dir, "re4.exe"), 12<<20, "")
	writeFile(t, filepath.Join(dir, "CrashReport.exe"), 8<<20, "")
	writeFile(t, filepath.Join(dir, "InstallerMessage.exe"), 1<<20, "")
	writeFile(t, filepath.Join(dir, "Resident Evil 4 v1.0 Plus 36 Trainer.exe"), 1<<20, "")
	writeFile(t, filepath.Join(dir, "Uninstall", "unins000.exe"), 1<<20, "")
	writeFile(t, filepath.Join(dir, "_Bonus Content", "extra.exe"), 20<<20, "")

	r := Detect(dir)

	if r.SteamAppID != "2050650" {
		t.Errorf("appid = %q, se esperaba 2050650", r.SteamAppID)
	}
	if r.AppIDSource != "steam_appid.txt" {
		t.Errorf("origen del appid = %q", r.AppIDSource)
	}
	if r.CleanName != "Resident Evil 4" {
		t.Errorf("nombre = %q", r.CleanName)
	}
	if len(r.Executables) == 0 {
		t.Fatal("no se propuso ningun ejecutable")
	}
	if r.Executables[0].Rel != "re4.exe" {
		t.Errorf("ejecutable elegido = %q, se esperaba re4.exe (candidatos: %+v)", r.Executables[0].Rel, r.Executables)
	}
	// El de la carpeta de extras no debe ni aparecer, aunque sea mas grande.
	for _, c := range r.Executables {
		if c.Rel == "_Bonus Content/extra.exe" {
			t.Error("se coló un ejecutable de la carpeta de extras")
		}
	}
	t.Logf("descartados: %v", r.Skipped)
}

// Devil May Cry 5: el appid va en un .ini y el nombre del .exe coincide.
func TestDetectDevilMayCry5(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Devil May Cry 5")
	writeFile(t, filepath.Join(dir, "steam_emu.ini"), 0,
		"[Settings]\nAppId=601150\nUserName=Player\n")
	writeFile(t, filepath.Join(dir, "DevilMayCry5.exe"), 12<<20, "")
	writeFile(t, filepath.Join(dir, "Language Selector.exe"), 40<<10, "")
	writeFile(t, filepath.Join(dir, "unins000.exe"), 1<<20, "")
	writeFile(t, filepath.Join(dir, "_Redist", "vc_redist.x64.exe"), 15<<20, "")
	writeFile(t, filepath.Join(dir, "_Redist", "QuickSFV.EXE"), 100<<10, "")

	r := Detect(dir)

	if r.SteamAppID != "601150" {
		t.Errorf("appid = %q, se esperaba 601150", r.SteamAppID)
	}
	if r.CleanName != "Devil May Cry 5" {
		t.Errorf("nombre = %q", r.CleanName)
	}
	if len(r.Executables) == 0 || r.Executables[0].Rel != "DevilMayCry5.exe" {
		t.Fatalf("ejecutable elegido mal: %+v", r.Executables)
	}
	if len(r.Executables) != 1 {
		t.Errorf("solo deberia quedar un candidato, hay %d: %+v", len(r.Executables), r.Executables)
	}
}

// Un juego de Unreal con el binario enterrado en Binaries/Win64.
func TestDetectUnrealLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Some.Game-CODEX")
	writeFile(t, filepath.Join(dir, "SomeGame.exe"), 300<<10, "") // lanzadera pequena
	writeFile(t, filepath.Join(dir, "Engine", "Binaries", "Win64", "UE4PrereqSetup_x64.exe"), 60<<20, "")
	writeFile(t, filepath.Join(dir, "SomeGame", "Binaries", "Win64", "SomeGame-Win64-Shipping.exe"), 90<<20, "")

	r := Detect(dir)
	if len(r.Executables) == 0 {
		t.Fatal("sin candidatos")
	}
	got := r.Executables[0].Rel
	if got != "SomeGame/Binaries/Win64/SomeGame-Win64-Shipping.exe" {
		t.Errorf("ejecutable elegido = %q; candidatos: %+v", got, r.Executables)
	}
}

// Sin marcador de appid: no debe inventarse nada.
func TestDetectNoAppID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Juego Viejo")
	writeFile(t, filepath.Join(dir, "juego.exe"), 5<<20, "")

	r := Detect(dir)
	if r.SteamAppID != "" {
		t.Errorf("no deberia haber appid, salio %q", r.SteamAppID)
	}
	if r.CleanName != "Juego Viejo" {
		t.Errorf("nombre = %q", r.CleanName)
	}
}

// Un AppId=0 en el .ini no vale: es el valor por defecto de los emuladores.
func TestDetectIgnoresZeroAppID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Juego")
	writeFile(t, filepath.Join(dir, "steam_emu.ini"), 0, "[Settings]\nAppId=0\n")
	writeFile(t, filepath.Join(dir, "juego.exe"), 5<<20, "")

	if r := Detect(dir); r.SteamAppID != "" {
		t.Errorf("AppId=0 no deberia aceptarse, salio %q", r.SteamAppID)
	}
}

// La lista negra se compara por palabra, no como subcadena suelta: "Observer"
// contiene "server" y "Dispatch" contiene "patch", y ambos son juegos de
// verdad que desaparecian sin decir nada.
func TestDetectNoDescartaJuegosPorSubcadena(t *testing.T) {
	casos := []struct {
		carpeta string
		exe     string
	}{
		{"Observer", "Observer.exe"},
		{"Dispatch", "Dispatch.exe"},
		{"Observer System Redux", "ObserverSystemRedux.exe"},
	}
	for _, c := range casos {
		dir := filepath.Join(t.TempDir(), c.carpeta)
		writeFile(t, filepath.Join(dir, c.exe), 30<<20, "")

		r := Detect(dir)
		if len(r.Skipped) > 0 {
			t.Errorf("%s: se descarto %v y no deberia", c.exe, r.Skipped)
		}
		if len(r.Executables) != 1 || r.Executables[0].Rel != c.exe {
			t.Errorf("%s: candidatos = %+v", c.exe, r.Executables)
		}
	}
}

// Y lo que si tiene que caer sigue cayendo, aunque el nombre venga pegado.
func TestDetectSigueDescartandoAccesorios(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Juego")
	writeFile(t, filepath.Join(dir, "Juego.exe"), 30<<20, "")
	malos := []string{
		"unins000.exe", "UnityCrashHandler64.exe", "vc_redist.x64.exe",
		"Language Selector.exe", "UE4PrereqSetup_x64.exe", "EasyAntiCheat_Setup.exe",
		"CrashReport.exe", "dxwebsetup.exe",
	}
	for _, m := range malos {
		writeFile(t, filepath.Join(dir, m), 60<<20, "") // mas grandes que el juego
	}

	r := Detect(dir)
	if len(r.Executables) != 1 || r.Executables[0].Rel != "Juego.exe" {
		t.Fatalf("candidatos = %+v (descartados: %v)", r.Executables, r.Skipped)
	}
	if len(r.Skipped) != len(malos) {
		t.Errorf("descartados %d de %d: %v", len(r.Skipped), len(malos), r.Skipped)
	}
}

// Una carpeta sin ejecutables no debe reventar.
func TestDetectEmptyDir(t *testing.T) {
	dir := t.TempDir()
	r := Detect(dir)
	if len(r.Executables) != 0 {
		t.Errorf("no deberia haber candidatos: %+v", r.Executables)
	}
}
