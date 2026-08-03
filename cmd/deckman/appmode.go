package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/jfrmorales/deckman/internal/config"
)

// Ventana "modo app": los navegadores Chromium aceptan --app=<url>, que abre
// una ventana propia sin pestanas ni barra de direcciones. Es la manera de
// parecer una aplicacion de escritorio sin cargar con un motor web propio:
// en Windows solo es el respaldo (alli hay ventana WebView2 nativa), en Linux
// es la via principal.

func inFlatpak() bool { return os.Getenv("FLATPAK_ID") != "" }

// hostCmd prepara un comando que corre en el host aunque deckman este dentro
// del sandbox de Flatpak. Necesita el permiso --talk-name=org.freedesktop.Flatpak
// del manifiesto: sin el, flatpak-spawn no puede salir del sandbox.
func hostCmd(name string, args ...string) *exec.Cmd {
	if inFlatpak() {
		return exec.Command("flatpak-spawn", append([]string{"--host", name}, args...)...)
	}
	return exec.Command(name, args...)
}

func hostHasBin(name string) bool {
	if inFlatpak() {
		return hostCmd("which", name).Run() == nil
	}
	_, err := exec.LookPath(name)
	return err == nil
}

func hostHasFlatpak(id string) bool {
	return hostCmd("flatpak", "info", id).Run() == nil
}

func chromiumBins() []string {
	switch runtime.GOOS {
	case "windows":
		// Primero el PATH y despues las rutas tipicas: Edge y Chrome no suelen
		// estar en el PATH de Windows.
		pf := os.Getenv("ProgramFiles")
		pf86 := os.Getenv("ProgramFiles(x86)")
		local := os.Getenv("LocalAppData")
		return []string{
			"msedge", "chrome", "brave", "vivaldi",
			filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(pf, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pf86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
		}
	default:
		return []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"brave-browser", "brave", "microsoft-edge", "microsoft-edge-stable", "vivaldi",
		}
	}
}

func chromiumFlatpaks() []string {
	return []string{
		"com.brave.Browser", "org.chromium.Chromium", "com.google.Chrome",
		"com.microsoft.Edge", "com.vivaldi.Vivaldi",
	}
}

// appArgs monta los argumentos del modo app. El perfil aparte es obligatorio:
// sin el, el navegador delega la ventana en la instancia que el usuario ya
// tenga abierta y el proceso lanzado muere al momento, con lo que no
// sabriamos cuando se cierra la ventana para apagar deckman.
func appArgs(url, profile string) []string {
	args := []string{"--app=" + url, "--no-first-run", "--no-default-browser-check"}
	if profile != "" {
		args = append(args, "--user-data-dir="+profile)
	}
	return args
}

// openAppWindow intenta abrir la interfaz como ventana de aplicacion.
// Devuelve el proceso arrancado para poder atar la vida de deckman a la de la
// ventana, o nil si no hay ningun navegador compatible.
func openAppWindow(url string) *exec.Cmd {
	profile := ""
	if dir, err := config.Dir(); err == nil {
		profile = filepath.Join(dir, "ventana")
	}
	for _, bin := range chromiumBins() {
		if !hostHasBin(bin) {
			continue
		}
		cmd := hostCmd(bin, appArgs(url, profile)...)
		if cmd.Start() == nil {
			return cmd
		}
	}
	if runtime.GOOS == "linux" {
		// Navegadores instalados como Flatpak (Brave, Chromium...). El perfil
		// tiene que vivir donde el sandbox DEL NAVEGADOR pueda escribir, es
		// decir, en la carpeta privada de ese flatpak: la nuestra le es
		// invisible y Chromium se queda clavado en un dialogo de error.
		home, err := os.UserHomeDir()
		for _, id := range chromiumFlatpaks() {
			if !hostHasFlatpak(id) {
				continue
			}
			profile := ""
			if err == nil {
				profile = filepath.Join(home, ".var", "app", id, "data", "deckman-ventana")
			}
			cmd := hostCmd("flatpak", append([]string{"run", id}, appArgs(url, profile)...)...)
			if cmd.Start() == nil {
				return cmd
			}
		}
	}
	return nil
}
