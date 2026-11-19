//go:build windows

package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jfrmorales/deckman/internal/i18n"
)

func replaceBinary(tmpPath, targetPath string, _ os.FileMode) error {
	oldPath := targetPath + ".old"
	if err := swapBinaryWithBackup(tmpPath, targetPath, oldPath); err != nil {
		return err
	}
	// En Windows el proceso viejo sigue bloqueando el fichero hasta salir;
	// CleanupOld lo retira en el siguiente arranque.
	_ = os.Remove(oldPath)
	return nil
}

// CleanupOld retira el ejecutable .old que deja una autoactualizacion en
// Windows cuando el proceso anterior aun no habia soltado el bloqueo al salir.
func CleanupOld() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	realPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		realPath = exe
	}
	oldPath := realPath + ".old"
	if _, err := os.Stat(oldPath); err != nil {
		return
	}
	if os.Remove(oldPath) == nil {
		return
	}
	go func() {
		for range 10 {
			time.Sleep(300 * time.Millisecond)
			if os.Remove(oldPath) == nil {
				return
			}
		}
	}()
}

func defaultCheckFlatpakUser(context.Context, string) bool { return false }

func defaultInstallFlatpak(context.Context, string) error {
	return i18n.Errorf("no se pudo instalar la actualizacion de Flatpak")
}

// Relaunch lanza la nueva version de deckman conservando los argumentos
// originales.
func Relaunch(origArgs []string) error {
	exe, err := os.Executable()
	if err != nil {
		return i18n.Errorf("no se pudo reiniciar deckman: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		realPath = exe
	}
	cmd := exec.Command(realPath, origArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return i18n.Errorf("no se pudo reiniciar deckman: %w", err)
	}
	return nil
}
