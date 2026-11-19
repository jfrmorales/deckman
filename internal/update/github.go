package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jfrmorales/deckman/internal/i18n"
)

const (
	// DefaultReleaseURL apunta a la ultima release publicada en GitHub.
	DefaultReleaseURL = "https://api.github.com/repos/jfrmorales/deckman/releases/latest"
	// DefaultReleasesPage es la pagina publica de respaldo cuando no hay html_url fiable.
	DefaultReleasesPage = "https://github.com/jfrmorales/deckman/releases/latest"

	maxMetadataBytes = 1 << 20
	maxAssetBytes    = 200 << 20
	maxNotesBytes    = 2000
)

// Progress informa del avance mientras se verifica o descarga una actualizacion.
type Progress struct {
	Phase      string `json:"phase"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
	Message    string `json:"message,omitempty"`
}

// ProgressFunc recibe los partes de progreso durante Apply.
type ProgressFunc func(Progress)

// Info resume el estado de actualizacion tras consultar la ultima release.
type Info struct {
	Current      string `json:"current"`
	Latest       string `json:"latest,omitempty"`
	Available    bool   `json:"available"`
	CanApply     bool   `json:"canApply"`
	ManualReason string `json:"manualReason,omitempty"`
	ReleaseURL   string `json:"releaseUrl,omitempty"`
	AssetName    string `json:"assetName,omitempty"`
	AssetSize    int64  `json:"assetSize,omitempty"`
	AssetURL     string `json:"-"`
	SumsURL      string `json:"-"`
	BundleURL    string `json:"-"`
	Signed       bool   `json:"signed"`
	IsFlatpak    bool   `json:"isFlatpak"`
	Notes        string `json:"notes,omitempty"`
	CheckedAt    int64  `json:"checkedAt,omitempty"`
}

// Updater comprueba y aplica actualizaciones de deckman. Los campos opcionales
// permiten inyectar un servidor y rutas locales en las pruebas.
type Updater struct {
	CurrentVersion   string
	ReleaseURL       string
	CosignPubKeyPEM  []byte
	HTTP             *http.Client
	AllowURL         func(string) error
	GOOS             string
	GOARCH           string
	FlatpakID        string
	InFlatpak        func() bool
	ExecutablePath   func() (string, error)
	CheckFlatpakUser func(ctx context.Context, appID string) bool
	InstallFlatpak   func(ctx context.Context, bundlePath string) error
	CacheDir         func() (string, error)
}

// New crea un Updater listo para comprobar y aplicar actualizaciones del binario
// o Flatpak en ejecucion.
func New(currentVersion string) *Updater {
	u := &Updater{
		CurrentVersion:  currentVersion,
		ReleaseURL:      DefaultReleaseURL,
		CosignPubKeyPEM: []byte(DefaultCosignPubKeyPEM),
		AllowURL:        AllowedReleaseURL,
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		FlatpakID:       os.Getenv("FLATPAK_ID"),
		InFlatpak:       func() bool { return os.Getenv("FLATPAK_ID") != "" },
		ExecutablePath:  os.Executable,
		CacheDir:        os.UserCacheDir,
	}
	u.HTTP = &http.Client{CheckRedirect: u.checkRedirect}
	return u
}

func (u *Updater) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return i18n.Errorf("demasiadas redirecciones")
	}
	if err := u.allowURL(req.URL.String()); err != nil {
		return i18n.Errorf("la descarga redirige a un sitio no permitido: %w", err)
	}
	return nil
}

func (u *Updater) httpClient() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{CheckRedirect: u.checkRedirect}
}

func (u *Updater) allowURL(raw string) error {
	if u.AllowURL != nil {
		return u.AllowURL(raw)
	}
	return AllowedReleaseURL(raw)
}

func (u *Updater) releaseURL() string {
	if u.ReleaseURL != "" {
		return u.ReleaseURL
	}
	return DefaultReleaseURL
}

func (u *Updater) cosignKey() []byte {
	if len(u.CosignPubKeyPEM) > 0 {
		return u.CosignPubKeyPEM
	}
	return []byte(DefaultCosignPubKeyPEM)
}

func (u *Updater) goos() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u *Updater) goarch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

func (u *Updater) isFlatpak() bool {
	if u.InFlatpak != nil {
		return u.InFlatpak()
	}
	return os.Getenv("FLATPAK_ID") != ""
}

func (u *Updater) flatpakID() string {
	if u.FlatpakID != "" {
		return u.FlatpakID
	}
	return os.Getenv("FLATPAK_ID")
}

func (u *Updater) executablePath() (string, error) {
	if u.ExecutablePath != nil {
		return u.ExecutablePath()
	}
	return os.Executable()
}

func (u *Updater) checkFlatpakUser(ctx context.Context, appID string) bool {
	if u.CheckFlatpakUser != nil {
		return u.CheckFlatpakUser(ctx, appID)
	}
	return defaultCheckFlatpakUser(ctx, appID)
}

func (u *Updater) installFlatpak(ctx context.Context, bundlePath string) error {
	if u.InstallFlatpak != nil {
		return u.InstallFlatpak(ctx, bundlePath)
	}
	return defaultInstallFlatpak(ctx, bundlePath)
}

func (u *Updater) cacheDir() (string, error) {
	if u.CacheDir != nil {
		return u.CacheDir()
	}
	return os.UserCacheDir()
}

// AllowedReleaseURL restringe las descargas y redirecciones de actualizaciones a
// HTTPS en dominios oficiales de GitHub.
func AllowedReleaseURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return i18n.Errorf("url invalida")
	}
	if parsed.Scheme != "https" {
		return i18n.Errorf("solo se aceptan descargas por https")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return i18n.Errorf("url invalida")
	}
	if host == "api.github.com" || host == "github.com" {
		return nil
	}
	const ghUserContentSuffix = ".githubusercontent.com"
	if len(host) > len(ghUserContentSuffix) && strings.HasSuffix(host, ghUserContentSuffix) {
		return nil
	}
	return i18n.Errorf("el servidor de descarga %q no esta permitido", parsed.Host)
}

// ExpectedAssetName devuelve el nombre exacto del fichero publicado en GitHub
// Releases para el sistema, arquitectura y formato indicados.
func ExpectedAssetName(version, goos, goarch string, flatpak bool) (string, bool) {
	v, ok := ParseVersion(version)
	if !ok || v.Suffix != "" {
		return "", false
	}
	ver := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if flatpak {
		if goos == "linux" && goarch == "amd64" {
			return "deckman-" + ver + ".flatpak", true
		}
		return "", false
	}
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64":
		return fmt.Sprintf("deckman-%s-%s-%s", ver, goos, goarch), true
	case "windows/amd64", "windows/arm64":
		return fmt.Sprintf("deckman-%s-%s-%s.exe", ver, goos, goarch), true
	default:
		return "", false
	}
}

type githubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	HTMLURL    string        `json:"html_url"`
	Body       string        `json:"body"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

// Check consulta la ultima release publicada y evalua si hay una version mas
// reciente y si se puede instalar automaticamente en este equipo.
func (u *Updater) Check(ctx context.Context) (*Info, error) {
	relURL := u.releaseURL()
	if err := u.allowURL(relURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relURL, nil)
	if err != nil {
		return nil, i18n.Errorf("no se pudo comprobar si hay actualizaciones: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "deckman/"+u.CurrentVersion)

	resp, err := u.httpClient().Do(req)
	if err != nil {
		return nil, i18n.Errorf("no se pudo comprobar si hay actualizaciones: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, i18n.Errorf("GitHub respondio HTTP %d al buscar actualizaciones", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes))
	if err != nil {
		return nil, i18n.Errorf("no se pudo comprobar si hay actualizaciones: %w", err)
	}

	var rel githubRelease
	if err := json.Unmarshal(raw, &rel); err != nil {
		return nil, i18n.Errorf("respuesta inesperada al buscar actualizaciones: %w", err)
	}

	isFlatpak := u.isFlatpak()
	info := &Info{
		Current:    u.CurrentVersion,
		ReleaseURL: sanitizeReleasePage(rel.HTMLURL),
		IsFlatpak:  isFlatpak,
		Notes:      trimUTF8(strings.TrimSpace(rel.Body), maxNotesBytes),
		CheckedAt:  time.Now().Unix(),
	}

	if lat, ok := ParseVersion(rel.TagName); ok && lat.Suffix == "" && !rel.Draft && !rel.Prerelease {
		info.Latest = "v" + lat.String()
	}

	if IsDevVersion(u.CurrentVersion) {
		info.ManualReason = "dev"
		return info, nil
	}
	if rel.Draft || rel.Prerelease || info.Latest == "" || !IsNewer(u.CurrentVersion, rel.TagName) {
		return info, nil
	}

	info.Available = true

	expectedName, supported := ExpectedAssetName(info.Latest, u.goos(), u.goarch(), isFlatpak)
	var targetAsset, sumsAsset, bundleAsset *githubAsset
	for i := range rel.Assets {
		a := &rel.Assets[i]
		switch a.Name {
		case "SHA256SUMS":
			sumsAsset = a
		case "SHA256SUMS.bundle":
			bundleAsset = a
		default:
			if supported && a.Name == expectedName {
				targetAsset = a
			}
		}
	}

	if targetAsset != nil {
		if err := u.allowURL(targetAsset.BrowserDownloadURL); err != nil {
			return nil, err
		}
		info.AssetName = targetAsset.Name
		info.AssetSize = targetAsset.Size
		info.AssetURL = targetAsset.BrowserDownloadURL
	}
	if sumsAsset != nil {
		if err := u.allowURL(sumsAsset.BrowserDownloadURL); err != nil {
			return nil, err
		}
		info.SumsURL = sumsAsset.BrowserDownloadURL
	}
	if bundleAsset != nil {
		if err := u.allowURL(bundleAsset.BrowserDownloadURL); err != nil {
			return nil, err
		}
		info.BundleURL = bundleAsset.BrowserDownloadURL
		info.Signed = true
	}

	switch {
	case !supported:
		info.ManualReason = "unsupported_platform"
	case targetAsset == nil && isFlatpak:
		info.ManualReason = "flatpak_no_bundle"
	case targetAsset == nil:
		info.ManualReason = "no_asset"
	case sumsAsset == nil:
		info.ManualReason = "no_sums"
	case isFlatpak:
		appID := u.flatpakID()
		if appID == "" || !u.checkFlatpakUser(ctx, appID) {
			info.ManualReason = "flatpak_system"
			return info, nil
		}
		info.CanApply = true
	default:
		exePath, err := u.executablePath()
		if err != nil {
			info.ManualReason = "no_write_permission"
			return info, nil
		}
		realPath, err := filepath.EvalSymlinks(exePath)
		if err != nil {
			realPath = exePath
		}
		if err := checkWritableDir(filepath.Dir(realPath)); err != nil {
			info.ManualReason = "no_write_permission"
			return info, nil
		}
		info.CanApply = true
	}

	return info, nil
}

func sanitizeReleasePage(raw string) string {
	const prefix = "https://github.com/jfrmorales/deckman/"
	if strings.HasPrefix(raw, prefix) && AllowedReleaseURL(raw) == nil {
		return raw
	}
	return DefaultReleasesPage
}

func trimUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + "…"
}
