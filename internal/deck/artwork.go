package deck

import (
	"context"
	"fmt"
	"github.com/jfrmorales/deckman/internal/i18n"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// La convencion de nombres de las caratulas vive SOLO aqui. Steam busca
// exactamente estos sufijos dentro de userdata/<usuario>/config/grid, y con
// cualquier otro la imagen no aparece:
//
//	<appid>p.png      portada vertical (la de la cuadricula de la biblioteca)
//	<appid>.png       portada horizontal (la de "reproducidos recientemente")
//	<appid>_hero.png  fondo de la ficha del juego
//	<appid>_logo.png  logo sobre el fondo
//	<appid>_icon.png  icono pequeno
var artSuffix = map[string]string{
	"grid":  "p",
	"gridh": "",
	"hero":  "_hero",
	"logo":  "_logo",
	"icon":  "_icon",
}

// ArtKinds devuelve los tipos de arte admitidos, en el orden en que conviene
// mostrarlos.
func ArtKinds() []string { return []string{"grid", "gridh", "hero", "logo", "icon"} }

// ArtFileName compone el nombre que debe tener la imagen en la carpeta grid.
func ArtFileName(appID uint32, kind, ext string) (string, error) {
	suffix, ok := artSuffix[kind]
	if !ok {
		return "", i18n.Errorf("tipo de arte desconocido: %q", kind)
	}
	return fmt.Sprintf("%d%s%s", appID, suffix, steamExt(ext)), nil
}

// steamExt normaliza la extension a una de las que Steam reconoce.
//
// Steam decide si un fichero es arte por la EXTENSION del nombre, no por su
// contenido. Un .webp lo ignora, aunque luego sepa decodificarlo de sobra:
// comprobado en una Deck real, el mismo fichero animado guardado como .webp no
// aparece y renombrado a .png si. Es lo que hace tambien el plugin de
// SteamGridDB para Decky, que guarda los webp animados con nombre .png.
func steamExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	switch ext {
	case ".png", ".jpg", ".ico":
		return ext
	case ".jpeg":
		return ".jpg"
	}
	// .webp, .apng y cualquier cosa rara: se guardan como .png, que Steam si
	// mira. El contenido se deja intacto.
	return ".png"
}

// isArtExt indica si un fichero de la carpeta grid es una imagen nuestra.
//
// Importa mas de lo que parece: el plugin de SteamGridDB para Decky guarda
// ficheros "<appid>.json" con sus metadatos, y sin este filtro se tomarian por
// la portada horizontal (mismo nombre, sin sufijo). Al cambiar esa portada le
// borrariamos los datos a otra herramienta.
func isArtExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".ico", ".webp", ".apng", ".gif", ".bmp", ".tga":
		return true
	}
	return false
}

// GridDir devuelve la carpeta donde Steam guarda las caratulas del usuario.
func (c *Client) GridDir(ctx context.Context) (string, error) {
	steamRoot := c.SteamRoot()
	userID, err := c.UserID(ctx, steamRoot)
	if err != nil {
		return "", err
	}
	return path.Join(steamRoot, "userdata", userID, "config", "grid"), nil
}

var reNumeric = regexp.MustCompile(`^\d+$`)

// artworkFiles agrupa por tipo TODOS los ficheros de arte de un juego.
//
// Puede haber varios del mismo tipo con distinta extension (un .jpg viejo
// junto a un .png nuevo). Steam se queda con el que encuentre primero, asi que
// hay que conocerlos todos para poder limpiarlos.
func (c *Client) artworkFiles(ctx context.Context, appID uint32) (map[string][]string, error) {
	dir, err := c.GridDir(ctx)
	if err != nil {
		return nil, err
	}
	return c.artworkFilesIn(dir, appID)
}

// artworkFilesIn es artworkFiles con la carpeta ya resuelta: quien la tiene
// (removeArtwork) no vuelve a preguntarla.
func (c *Client) artworkFilesIn(dir string, appID uint32) (map[string][]string, error) {
	entries, err := c.sftp.ReadDir(dir)
	if err != nil {
		return map[string][]string{}, nil // que no exista la carpeta es normal
	}

	id := fmt.Sprint(appID)
	out := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := path.Ext(name)
		if !isArtExt(ext) {
			continue
		}
		stem := strings.TrimSuffix(name, ext)
		if !strings.HasPrefix(stem, id) {
			continue
		}
		// Lo que queda tras el numero identifica el tipo, y hay que compararlo
		// exacto: "123p" y "123" son dos artes distintas del mismo juego.
		rest := stem[len(id):]
		for kind, suffix := range artSuffix {
			if rest == suffix {
				out[kind] = append(out[kind], name)
				break
			}
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out, nil
}

// CurrentArtwork indica que imagenes hay ya instaladas para un juego.
// Devuelve tipo -> nombre de fichero.
func (c *Client) CurrentArtwork(ctx context.Context, appID uint32) (map[string]string, error) {
	files, err := c.artworkFiles(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for kind, names := range files {
		if len(names) > 0 {
			out[kind] = names[0]
		}
	}
	return out, nil
}

// ClearArtwork borra todas las caratulas de un juego. Hace falta al cambiar
// una imagen porque la anterior puede tener otra extension (.jpg frente a
// .png) y Steam se quedaria con la que encuentre primero.
func (c *Client) ClearArtwork(ctx context.Context, appID uint32) error {
	return c.removeArtwork(ctx, appID, ArtKinds())
}

// RemoveArtworkKind borra solo un tipo de arte.
func (c *Client) RemoveArtworkKind(ctx context.Context, appID uint32, kind string) error {
	if _, ok := artSuffix[kind]; !ok {
		return i18n.Errorf("tipo de arte desconocido: %q", kind)
	}
	return c.removeArtwork(ctx, appID, []string{kind})
}

func (c *Client) removeArtwork(ctx context.Context, appID uint32, kinds []string) error {
	dir, err := c.GridDir(ctx)
	if err != nil {
		return err
	}
	files, err := c.artworkFilesIn(dir, appID)
	if err != nil {
		return err
	}
	// TODOS los de cada tipo, no solo uno: si queda un .jpg viejo junto al .png
	// nuevo, Steam puede seguir mostrando el viejo.
	for _, k := range kinds {
		for _, n := range files[k] {
			if err := c.sftp.Remove(path.Join(dir, n)); err != nil {
				return i18n.Errorf("no se pudo borrar %s: %w", n, err)
			}
		}
	}
	return nil
}

// WriteArtwork instala una imagen para un tipo de arte, quitando la que hubiera
// de ese mismo tipo.
//
// El orden importa: primero se sube a un temporal y solo cuando esta entero se
// borran las anteriores y se renombra encima. Al reves (borrar y luego escribir)
// un fallo de red a mitad dejaba el juego SIN caratula, habiendo perdido la que
// tenia. El temporal no lleva extension de imagen, asi que la limpieza de
// artworkFiles no lo confunde con arte.
func (c *Client) WriteArtwork(ctx context.Context, appID uint32, kind, ext string, data []byte) (string, error) {
	name, err := ArtFileName(appID, kind, ext)
	if err != nil {
		return "", err
	}
	dir, err := c.GridDir(ctx)
	if err != nil {
		return "", err
	}
	if err := c.MkdirAll(dir); err != nil {
		return "", err
	}

	dst := path.Join(dir, name)
	tmp := dst + ".deckman.tmp"
	c.sftp.Remove(tmp) // restos de un intento anterior

	f, err := c.sftp.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		c.sftp.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		c.sftp.Remove(tmp)
		return "", err
	}

	// Ahora si: fuera las anteriores de este tipo (pueden tener otra extension,
	// y Steam se quedaria con la que encuentre primero).
	if err := c.RemoveArtworkKind(ctx, appID, kind); err != nil {
		c.sftp.Remove(tmp)
		return "", err
	}
	if err := c.sftp.PosixRename(tmp, dst); err != nil {
		// Sin la extension POSIX hay que apartar el destino antes de renombrar.
		if rmErr := c.sftp.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
			c.sftp.Remove(tmp)
			return "", i18n.Errorf("no se pudo dejar %s en su sitio: %w", name, err)
		}
		if err2 := c.sftp.Rename(tmp, dst); err2 != nil {
			return "", i18n.Errorf("no se pudo dejar %s en su sitio: %w", name, err2)
		}
	}
	return name, nil
}
