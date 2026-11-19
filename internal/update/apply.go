package update

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jfrmorales/deckman/internal/i18n"
)

// Apply descarga, verifica (SHA-256 y, si esta presente, firma cosign) e
// instala la actualizacion descrita por info.
func (u *Updater) Apply(ctx context.Context, info *Info, report ProgressFunc) error {
	if info == nil || !info.Available || !info.CanApply {
		return i18n.Errorf("no hay ninguna actualizacion lista para instalar")
	}
	if err := u.allowURL(info.SumsURL); err != nil {
		return err
	}
	if err := u.allowURL(info.AssetURL); err != nil {
		return err
	}
	if info.BundleURL != "" {
		if err := u.allowURL(info.BundleURL); err != nil {
			return err
		}
	}

	emit := func(p Progress) {
		if report != nil {
			report(p)
		}
	}

	emit(Progress{Phase: "verificando"})

	sumsBytes, err := u.fetchMetadata(ctx, info.SumsURL)
	if err != nil {
		return err
	}
	if info.BundleURL != "" {
		bundleBytes, err := u.fetchMetadata(ctx, info.BundleURL)
		if err != nil {
			return err
		}
		if err := VerifyCosignBundle(u.cosignKey(), sumsBytes, bundleBytes); err != nil {
			return err
		}
	}
	expectedHash, err := LookupSHA256(sumsBytes, info.AssetName)
	if err != nil {
		return err
	}

	var (
		destDir    string
		realExe    string
		exeMode    os.FileMode
		bundlePath string
	)

	if info.IsFlatpak {
		cacheBase, err := u.cacheDir()
		if err != nil {
			return i18n.Errorf("no se pudo crear la carpeta temporal de actualizacion: %w", err)
		}
		destDir = filepath.Join(cacheBase, "deckman-update")
		if err := os.MkdirAll(destDir, 0o700); err != nil {
			return i18n.Errorf("no se pudo crear la carpeta temporal de actualizacion: %w", err)
		}
		bundlePath = filepath.Join(destDir, filepath.Base(info.AssetName))
	} else {
		exePath, err := u.executablePath()
		if err != nil {
			return i18n.Errorf("no se pudo sustituir el ejecutable: %w", err)
		}
		realExe, err = filepath.EvalSymlinks(exePath)
		if err != nil {
			return i18n.Errorf("no se pudo sustituir el ejecutable: %w", err)
		}
		fi, err := os.Stat(realExe)
		if err != nil {
			return i18n.Errorf("no se pudo sustituir el ejecutable: %w", err)
		}
		exeMode = fi.Mode().Perm() | 0o700
		// El temporal vive junto al ejecutable para que os.Rename sea atomico
		// dentro del mismo sistema de ficheros.
		destDir = filepath.Dir(realExe)
	}

	tmpPath, actualHash, err := u.downloadAsset(ctx, info.AssetURL, info.AssetSize, destDir, emit)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()

	if err := VerifySHA256(actualHash, expectedHash); err != nil {
		return err
	}

	emit(Progress{
		Phase:      "instalando",
		BytesDone:  info.AssetSize,
		BytesTotal: info.AssetSize,
	})

	if info.IsFlatpak {
		if err := os.Rename(tmpPath, bundlePath); err != nil {
			return i18n.Errorf("no se pudo preparar el paquete Flatpak: %w", err)
		}
		defer func() { _ = os.Remove(bundlePath) }()
		return u.installFlatpak(ctx, bundlePath)
	}

	if err := replaceBinary(tmpPath, realExe, exeMode); err != nil {
		return i18n.Errorf("no se pudo sustituir el ejecutable: %w", err)
	}
	return nil
}

func (u *Updater) fetchMetadata(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, i18n.Errorf("no se pudo descargar la actualizacion: %w", err)
	}
	req.Header.Set("User-Agent", "deckman/"+u.CurrentVersion)

	resp, err := u.httpClient().Do(req)
	if err != nil {
		return nil, i18n.Errorf("no se pudo descargar la actualizacion: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, i18n.Errorf("la descarga devolvio HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes+1))
	if err != nil {
		return nil, i18n.Errorf("no se pudo descargar la actualizacion: %w", err)
	}
	if len(data) > maxMetadataBytes {
		return nil, i18n.Errorf("el fichero descargado supera el tamano maximo permitido")
	}
	return data, nil
}

func (u *Updater) downloadAsset(ctx context.Context, rawURL string, knownSize int64, destDir string, emit ProgressFunc) (string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", nil, i18n.Errorf("no se pudo descargar la actualizacion: %w", err)
	}
	req.Header.Set("User-Agent", "deckman/"+u.CurrentVersion)

	resp, err := u.httpClient().Do(req)
	if err != nil {
		return "", nil, i18n.Errorf("no se pudo descargar la actualizacion: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, i18n.Errorf("la descarga devolvio HTTP %d", resp.StatusCode)
	}

	total := knownSize
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	if total > maxAssetBytes {
		return "", nil, i18n.Errorf("el fichero descargado supera el tamano maximo permitido")
	}

	f, err := os.CreateTemp(destDir, ".deckman-update-*")
	if err != nil {
		return "", nil, i18n.Errorf("no se pudo guardar la actualizacion en disco: %w", err)
	}
	tmpPath := f.Name()
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(resp.Body, maxAssetBytes+1)
	buf := make([]byte, 32*1024)
	var written int64
	lastEmit := time.Time{}

	emit(Progress{Phase: "descargando", BytesDone: 0, BytesTotal: total})

	for {
		n, readErr := limited.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > maxAssetBytes {
				return "", nil, i18n.Errorf("el fichero descargado supera el tamano maximo permitido")
			}
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return "", nil, i18n.Errorf("no se pudo guardar la actualizacion en disco: %w", wErr)
			}
			_, _ = hasher.Write(buf[:n])
			now := time.Now()
			if lastEmit.IsZero() || now.Sub(lastEmit) >= 120*time.Millisecond {
				emit(Progress{Phase: "descargando", BytesDone: written, BytesTotal: total})
				lastEmit = now
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", nil, i18n.Errorf("descarga interrumpida tras %d bytes: %w", written, readErr)
		}
	}

	if total > 0 && written != total {
		return "", nil, i18n.Errorf("descarga incompleta: %d de %d bytes", written, total)
	}

	emit(Progress{Phase: "descargando", BytesDone: written, BytesTotal: total})

	if err := f.Sync(); err != nil {
		return "", nil, i18n.Errorf("no se pudo guardar la actualizacion en disco: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", nil, i18n.Errorf("no se pudo guardar la actualizacion en disco: %w", err)
	}

	keep = true
	return tmpPath, hasher.Sum(nil), nil
}

// swapBinaryWithBackup aparta el binario en ejecucion en oldPath antes de
// colocar tmpPath en targetPath. Windows no deja sobrescribir un .exe abierto,
// pero si renombrarlo dentro de la misma carpeta.
func swapBinaryWithBackup(tmpPath, targetPath, oldPath string) error {
	_ = os.Remove(oldPath)
	if err := os.Rename(targetPath, oldPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Rename(oldPath, targetPath)
		return err
	}
	return nil
}

func checkWritableDir(dir string) error {
	f, err := os.CreateTemp(dir, ".deckman-perm-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}
