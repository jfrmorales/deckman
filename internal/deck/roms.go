package deck

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

// ROM es un fichero suelto dentro de <romsDir>/<sistema>.
//
// Deliberadamente NO lleva la ruta completa. La interfaz manda de vuelta lo
// que le damos, y una ruta que viaja al navegador y vuelve es una ruta que el
// navegador puede cambiar: al otro lado de DeleteROM hay un sftp.Remove. Con
// sistema y nombre basta, y la ruta la arma siempre este lado.
type ROM struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// nombreSuelto exige que el nombre sea un nombre y no un trozo de ruta.
//
// Es la unica barrera entre lo que teclea el navegador y sftp.Remove: sin
// ella, "borra Sonic.zip" y "borra ../../../.ssh/authorized_keys" son la misma
// peticion. Prohibidos los separadores y los dos nombres que suben de
// directorio; el resto de nombres raros (espacios, acentos, guiones) son ROMs
// perfectamente legitimas y no se tocan.
func nombreSuelto(n string) error {
	if strings.TrimSpace(n) == "" {
		return fmt.Errorf("hace falta el nombre del fichero")
	}
	if strings.ContainsAny(n, "/\\") || strings.ContainsRune(n, 0) {
		return fmt.Errorf("un nombre de ROM no puede llevar barras: %q", n)
	}
	if n == "." || n == ".." {
		return fmt.Errorf("nombre de ROM invalido: %q", n)
	}
	return nil
}

// romSystemDir devuelve <romsDir>/<sistema> y de paso comprueba que el sistema
// existe de verdad. El nombre del sistema tampoco se concatena a ciegas: solo
// vale si romSystems lo ha listado como carpeta.
func (c *Client) romSystemDir(ctx context.Context, system string) (string, error) {
	romsDir, systems := c.romSystems(ctx)
	if romsDir == "" {
		return "", fmt.Errorf("no se encontro la carpeta de ROMs: instala EmuDeck en la Deck o crea ~/Emulation/roms")
	}
	for _, s := range systems {
		if s == system {
			return path.Join(romsDir, system), nil
		}
	}
	return "", fmt.Errorf("el sistema %q no existe dentro de %s", system, romsDir)
}

// mueblesEmuDeck son los ficheros que EmuDeck deja en TODAS las carpetas de
// sistema, tenga ROMs o no. Comprobado en una Deck real: junto a ellos hay un
// enlace «media» a tools/downloaded_media/<sistema>. No son ROMs y listarlos
// con un boton de Eliminar al lado es pedir un accidente.
var mueblesEmuDeck = map[string]bool{
	"systeminfo.txt": true,
	"metadata.txt":   true,
}

// ListROMs lista los ficheros de un sistema. Las subcarpetas se saltan: ahi
// dentro cada emulador se monta lo suyo (BIOS, saves, texturas) y no son ROMs
// que el usuario quiera renombrar o borrar desde aqui.
//
// Lo que no encaje en extROM SI se lista: una descarga a medias o un fichero
// con extension rara son justo lo que uno viene a limpiar. Solo se esconden
// los muebles de EmuDeck, que no son del usuario.
func (c *Client) ListROMs(ctx context.Context, system string) ([]ROM, error) {
	dir, err := c.romSystemDir(ctx, system)
	if err != nil {
		return nil, err
	}
	entries, err := c.sftp.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer %s: %w", dir, err)
	}

	roms := make([]ROM, 0, len(entries))
	for _, e := range entries {
		// Solo ficheros de verdad. El enlace «media» que EmuDeck pone en cada
		// sistema apunta a una carpeta, pero ReadDir lo describe como enlace,
		// no como directorio: sin esta comprobacion se colaba en la lista con
		// su boton de Eliminar al lado, y borrarlo deja al sistema sin
		// caratulas.
		if !e.Mode().IsRegular() || mueblesEmuDeck[e.Name()] {
			continue
		}
		roms = append(roms, ROM{Name: e.Name(), Size: e.Size()})
	}
	sort.Slice(roms, func(i, j int) bool { return roms[i].Name < roms[j].Name })
	return roms, nil
}

// DeleteROM borra una ROM de un sistema. Sistema y nombre, nunca una ruta.
func (c *Client) DeleteROM(ctx context.Context, system, name string) error {
	if err := nombreSuelto(name); err != nil {
		return err
	}
	dir, err := c.romSystemDir(ctx, system)
	if err != nil {
		return err
	}
	if err := c.sftp.Remove(path.Join(dir, name)); err != nil {
		return fmt.Errorf("no se pudo borrar %q de %s: %w", name, system, err)
	}
	return nil
}

// RenameROM cambia el nombre de una ROM dentro de su propio sistema. No sirve
// para moverla a otro sitio, y por eso el nombre nuevo se valida igual que el
// viejo.
func (c *Client) RenameROM(ctx context.Context, system, oldName, newName string) error {
	if err := nombreSuelto(oldName); err != nil {
		return err
	}
	if err := nombreSuelto(newName); err != nil {
		return err
	}
	if oldName == newName {
		return nil
	}
	dir, err := c.romSystemDir(ctx, system)
	if err != nil {
		return err
	}
	destino := path.Join(dir, newName)
	// sftp.Rename pisa el destino sin avisar en algunos servidores: mejor
	// negarse que perder la otra ROM.
	if c.Exists(destino) {
		return fmt.Errorf("ya hay un %q en %s", newName, system)
	}
	if err := c.sftp.Rename(path.Join(dir, oldName), destino); err != nil {
		return fmt.Errorf("no se pudo renombrar %q: %w", oldName, err)
	}
	return nil
}

// NombreDesdeURL saca el nombre de fichero de una URL de descarga y comprueba
// que la URL es descargable. Separada de DownloadROM para poder probarla sin
// una Deck delante.
func NombreDesdeURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("esa URL no se entiende: %v", err)
	}
	// Solo http(s): un file:// o un scheme raro acabaria en la linea de
	// ordenes de la Deck pidiendole cosas que nadie ha pedido.
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("la URL tiene que empezar por http:// o https://")
	}
	// Una URL acabada en barra apunta a una carpeta, y path.Base devolveria el
	// nombre de esa carpeta: curl guardaria el indice HTML con nombre de ROM.
	if u.Path == "" || strings.HasSuffix(u.Path, "/") {
		return "", fmt.Errorf("esa URL no apunta a un fichero; pon el enlace directo a la ROM")
	}
	nombre := path.Base(u.Path)
	// El nombre viaja escapado en la URL (%20, %5B...); en el disco va tal cual.
	if suelto, err := url.PathUnescape(nombre); err == nil {
		nombre = suelto
	}
	if err := nombreSuelto(nombre); err != nil {
		return "", fmt.Errorf("de esa URL no sale un nombre de fichero; pon el enlace directo a la ROM")
	}
	return nombre, nil
}

// DownloadROM baja una ROM a la Deck. La descarga la hace la propia Deck, no
// este PC: asi el fichero no cruza dos veces la red.
func (c *Client) DownloadROM(ctx context.Context, rawURL, system string, report ProgressFunc) error {
	rawURL = strings.TrimSpace(rawURL)
	nombre, err := NombreDesdeURL(rawURL)
	if err != nil {
		return err
	}
	dir, err := c.romSystemDir(ctx, system)
	if err != nil {
		return err
	}
	destino := path.Join(dir, nombre)
	if c.Exists(destino) {
		return fmt.Errorf("ya hay un %q en %s: borralo antes o cambiale el nombre", nombre, system)
	}
	if report != nil {
		report(Progress{Phase: "descargando en la Deck", File: nombre, Message: "la descarga la hace la Deck; aqui no hay barra de avance"})
	}

	// Todo en una sola orden remota, y con red de seguridad:
	//   - se baja a un .parcial y solo se renombra si curl termina bien. Una
	//     ROM truncada es peor que ninguna: el emulador la lista como si
	//     sirviera y el fallo aparece a mitad de partida.
	//   - `mv -n` no pisa nada, y el `[ ! -e ]` detecta que se haya negado
	//     (si alguien creo el fichero mientras bajabamos).
	//   - si algo falla, el parcial se borra ahi mismo: no dejamos basura en
	//     la carpeta de ROMs.
	parcial := destino + ".deckman-parcial"
	cmd := fmt.Sprintf(
		"command -v curl >/dev/null 2>&1 || { echo 'la Deck no tiene curl' >&2; exit 127; }; "+
			"curl -fsSL --proto '=http,https' --connect-timeout 20 -o %s %s && mv -n %s %s && [ ! -e %s ] || { rm -f %s; exit 1; }",
		ShellQuote(parcial), ShellQuote(rawURL),
		ShellQuote(parcial), ShellQuote(destino),
		ShellQuote(parcial), ShellQuote(parcial))

	out, err := c.Run(ctx, cmd)
	if err != nil {
		if detalle := strings.TrimSpace(out); detalle != "" {
			return fmt.Errorf("no se pudo descargar %q: %s", nombre, detalle)
		}
		return fmt.Errorf("no se pudo descargar %q: comprueba el enlace y que la Deck tenga internet", nombre)
	}
	if report != nil {
		report(Progress{Phase: "listo", File: nombre, Done: true})
	}
	return nil
}
