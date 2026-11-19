//go:build !windows

package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jfrmorales/deckman/internal/i18n"
)

func replaceBinary(tmpPath, targetPath string, mode os.FileMode) error {
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, targetPath)
}

// CleanupOld limpia binarios residuales tras reiniciar; en Unix os.Rename
// reemplaza en sitio y no deja ningun fichero .old.
func CleanupOld() {}

func defaultCheckFlatpakUser(ctx context.Context, appID string) bool {
	if appID == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, "flatpak-spawn", "--host", "flatpak", "info", "--user", appID)
	return cmd.Run() == nil
}

func defaultInstallFlatpak(ctx context.Context, bundlePath string) error {
	cmd := exec.CommandContext(
		ctx,
		"flatpak-spawn", "--host",
		"flatpak", "install", "--user", "--bundle", "--reinstall", "--noninteractive", "-y", bundlePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return i18n.Errorf("no se pudo instalar la actualizacion de Flatpak (%s): %w", msg, err)
		}
		return i18n.Errorf("no se pudo instalar la actualizacion de Flatpak: %w", err)
	}
	return nil
}

// Relaunch arranca la version recien instalada conservando los argumentos
// originales. En binario suelto sustituye el proceso actual via exec; en
// Flatpak lanza una nueva instancia en el host antes de que esta termine.
func Relaunch(origArgs []string) error {
	if os.Getenv("FLATPAK_ID") != "" {
		const flatpakAppID = "io.github.jfrmorales.deckman"
		args := append([]string{"--host", "sh", "-c", `nohup flatpak run --user "$@" >/dev/null 2>&1 </dev/null &`, "_", flatpakAppID}, origArgs...)
		cmd := exec.Command("flatpak-spawn", args...)
		if err := cmd.Run(); err != nil {
			return i18n.Errorf("no se pudo reiniciar deckman: %w", err)
		}
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return i18n.Errorf("no se pudo reiniciar deckman: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		realPath = exe
	}
	argv := append([]string{realPath}, origArgs...)
	if err := syscall.Exec(realPath, argv, os.Environ()); err != nil {
		return i18n.Errorf("no se pudo reiniciar deckman: %w", err)
	}
	return nil
}
