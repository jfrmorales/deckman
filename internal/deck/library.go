package deck

import (
	"context"
	"fmt"
	"github.com/jfrmorales/deckman/internal/i18n"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Library es una carpeta de biblioteca de Steam (el SSD interno, la microSD...).
type Library struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	IsDefault bool   `json:"isDefault"` // la interna, dentro del home
	Removable bool   `json:"removable"` // microSD o USB
	Total     uint64 `json:"total"`
	Free      uint64 `json:"free"`
	Used      uint64 `json:"used"`

	// GamesDir es donde van los juegos no-Steam de esta unidad. Steam no tiene
	// nada que decir aqui (sus accesos directos apuntan a una ruta cualquiera),
	// asi que lo fija deckman: ~/Games en el interno y <unidad>/Games fuera.
	GamesDir string `json:"gamesDir"`
}

// Game es una entrada de la biblioteca, sea de Steam o no.
type Game struct {
	AppID      string `json:"appId"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // "steam" o "nonsteam"
	Size       uint64 `json:"size"`
	LastPlayed int64  `json:"lastPlayed"`

	// Juegos de Steam
	LibraryPath string `json:"libraryPath,omitempty"`
	InstallDir  string `json:"installDir,omitempty"` // nombre de carpeta, no ruta
	FullPath    string `json:"fullPath,omitempty"`
	StateFlags  int    `json:"stateFlags,omitempty"`

	// Juegos no-Steam
	Exe           string `json:"exe,omitempty"`
	StartDir      string `json:"startDir,omitempty"`
	LaunchOptions string `json:"launchOptions,omitempty"`

	// GameRoot es la carpeta que ES el juego, que no siempre coincide con
	// StartDir: en el acceso directo se guarda la carpeta del ejecutable, y esa
	// puede estar tres niveles mas abajo (en Riddick, System/Win32_x86: 16 MB de
	// un juego de 11 GB). Solo se sabe cuando el juego cuelga de un Games
	// conocido; si no, va vacio y ni se mueve ni se mide por aqui.
	GameRoot string `json:"gameRoot,omitempty"`

	// Espacio accesorio, comun a ambos tipos
	CompatSize uint64 `json:"compatSize"` // prefijo de Proton
	ShaderSize uint64 `json:"shaderSize"` // cache de shaders
	CompatTool string `json:"compatTool"` // proton_experimental, GE-Proton9-20...
	HasCompat  bool   `json:"hasCompat"`
	HasShaders bool   `json:"hasShaders"`

	// HasCover dice si hay una portada que ensenar en la biblioteca. Lo rellena
	// AnnotateCovers, no Scan: es una consulta mas y solo la necesita la
	// interfaz.
	HasCover bool `json:"hasCover"`
}

// CompatTool es una version de Proton instalada.
type CompatTool struct {
	Name        string `json:"name"`        // identificador interno para config.vdf
	DisplayName string `json:"displayName"` // como se ve en la interfaz
}

// Inventory es la foto completa de la Deck.
type Inventory struct {
	SteamRoot    string       `json:"steamRoot"`
	UserID       string       `json:"userId"`
	Libraries    []Library    `json:"libraries"`
	Games        []Game       `json:"games"`
	CompatTools  []CompatTool `json:"compatTools"`
	SteamRunning bool         `json:"steamRunning"`
	GamesDir     string       `json:"gamesDir"`
	RomsDir      string       `json:"romsDir"`
	RomSystems   []string     `json:"romSystems"`
}

// nonGameAppIDs son entradas de steamapps que no son juegos ni llevan
// toolmanifest.vdf, asi que hay que descartarlas por id.
var nonGameAppIDs = map[string]bool{
	"228980": true, // Steamworks Common Redistributables
}

// mergeTools junta las listas de Proton quitando repetidos y dejando primero
// los oficiales, que es lo que casi siempre se quiere usar.
func mergeTools(lists ...[]CompatTool) []CompatTool {
	seen := map[string]bool{}
	var out []CompatTool
	for _, l := range lists {
		// Copia antes de ordenar: una funcion llamada "merge" que reordena los
		// argumentos del llamante es una sorpresa esperando su momento.
		list := append([]CompatTool(nil), l...)
		sort.Slice(list, func(i, j int) bool { return list[i].DisplayName < list[j].DisplayName })
		for _, t := range list {
			if t.Name == "" || seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			out = append(out, t)
		}
	}
	return out
}

// SteamRoot localiza la raiz de Steam en la Deck. El resultado se memoiza:
// instalar las cuatro caratulas de un juego lo preguntaba doce veces, y cada
// consulta son uno o dos Stat por SFTP. La cache se vacia en cada Scan, asi
// que un cambio raro (reinstalar SteamOS con la sesion abierta) se recoge en
// el siguiente escaneo.
func (c *Client) SteamRoot() string {
	if c.cache == nil {
		return c.findSteamRoot()
	}
	c.cache.mu.Lock()
	defer c.cache.mu.Unlock()
	if c.cache.steamRoot == "" {
		c.cache.steamRoot = c.findSteamRoot()
	}
	return c.cache.steamRoot
}

func (c *Client) findSteamRoot() string {
	for _, p := range []string{
		path.Join(c.Home, ".local/share/Steam"),
		path.Join(c.Home, ".steam/steam"),
	} {
		if c.Exists(path.Join(p, "steamapps")) {
			return p
		}
	}
	return path.Join(c.Home, ".local/share/Steam")
}

// UserID devuelve el id de usuario de Steam (la carpeta de userdata). Si hay
// varias cuentas nos quedamos con la modificada mas recientemente.
//
// Memoizado igual que SteamRoot, y con el mismo vaciado en Scan: cada consulta
// abria una sesion SSH entera para un ls. Si el usuario cambia de cuenta de
// Steam con deckman abierto, el siguiente escaneo lo recoge.
func (c *Client) UserID(ctx context.Context, steamRoot string) (string, error) {
	if c.cache != nil {
		c.cache.mu.Lock()
		uid := c.cache.userID
		c.cache.mu.Unlock()
		if uid != "" {
			return uid, nil
		}
	}
	out, err := c.Run(ctx, fmt.Sprintf(
		`ls -1t %s 2>/dev/null | grep -E '^[0-9]+$' | head -1`,
		ShellQuote(path.Join(steamRoot, "userdata"))))
	if err != nil || out == "" {
		return "", i18n.Errorf("no se encontro ninguna cuenta de Steam en userdata")
	}
	uid := strings.TrimSpace(out)
	if c.cache != nil {
		c.cache.mu.Lock()
		c.cache.userID = uid
		c.cache.mu.Unlock()
	}
	return uid, nil
}

// Scan recorre la Deck y devuelve el inventario completo.
func (c *Client) Scan(ctx context.Context) (*Inventory, error) {
	c.invalidateCache()
	inv := &Inventory{SteamRoot: c.SteamRoot()}
	inv.SteamRunning = c.SteamRunning(ctx)
	inv.GamesDir = path.Join(c.Home, "Games")

	if uid, err := c.UserID(ctx, inv.SteamRoot); err == nil {
		inv.UserID = uid
	}

	libs, err := c.libraries(ctx, inv.SteamRoot)
	if err != nil {
		return nil, err
	}
	inv.Libraries = libs

	// Proton y los Steam Linux Runtime aparecen en steamapps como si fueran
	// juegos. Los apartamos: los Proton pasan a la lista de compatibilidad y
	// el resto se descarta, para que la biblioteca muestre solo juegos.
	toolDirs := c.toolInstallDirs(ctx, libs)
	var protons []CompatTool

	for _, lib := range libs {
		entries, err := c.steamGames(ctx, lib)
		if err != nil {
			continue // una biblioteca ilegible (SD fuera) no debe tumbar el escaneo
		}
		for _, g := range entries {
			if toolDirs[g.InstallDir] {
				if internal := protonInternalName(g.Name); internal != "" {
					protons = append(protons, CompatTool{Name: internal, DisplayName: g.Name})
				}
				continue
			}
			if nonGameAppIDs[g.AppID] {
				continue
			}
			inv.Games = append(inv.Games, g)
		}
	}

	if inv.UserID != "" {
		nonSteam, err := c.nonSteamGames(ctx, inv.SteamRoot, inv.UserID)
		if err == nil {
			// Un juego no-Steam tambien esta en una unidad u otra, aunque Steam
			// no lo sepa: sin esto la interfaz no puede ofrecer moverlo.
			for i := range nonSteam {
				nonSteam[i].LibraryPath = libraryForPath(libs, nonSteam[i].StartDir)
				nonSteam[i].GameRoot = gameRootFor(libs, nonSteam[i].StartDir)
			}
			inv.Games = append(inv.Games, nonSteam...)
		}
	}

	inv.CompatTools = mergeTools(protons, c.customCompatTools(ctx, inv.SteamRoot, libs))
	c.annotateCompat(ctx, inv)
	c.annotateExtraSizes(ctx, inv)

	inv.RomsDir, inv.RomSystems = c.romSystems(ctx)

	sort.Slice(inv.Games, func(i, j int) bool {
		return strings.ToLower(inv.Games[i].Name) < strings.ToLower(inv.Games[j].Name)
	})
	return inv, nil
}

func (c *Client) libraries(ctx context.Context, steamRoot string) ([]Library, error) {
	p := path.Join(steamRoot, "steamapps", "libraryfolders.vdf")
	data, err := c.ReadFile(p)
	if err != nil {
		return nil, i18n.Errorf("no se pudo leer %s: %w", p, err)
	}
	root, err := ParseVDF(data)
	if err != nil {
		return nil, i18n.Errorf("libraryfolders.vdf ilegible: %w", err)
	}
	lf := root.Get("libraryfolders")
	if lf == nil {
		return nil, i18n.Errorf("libraryfolders.vdf no tiene la clave esperada")
	}

	var libs []Library
	for _, entry := range lf.Children {
		lp := entry.GetString("path")
		if lp == "" {
			continue
		}
		lib := Library{
			Path:      lp,
			Label:     entry.GetString("label"),
			IsDefault: strings.HasPrefix(lp, c.Home),
		}
		lib.Removable = strings.HasPrefix(lp, "/run/media/")
		if lib.Label == "" {
			if lib.IsDefault {
				lib.Label = "Interno"
			} else {
				lib.Label = path.Base(lp)
			}
		}
		lib.GamesDir = c.gamesDirFor(lib)
		libs = append(libs, lib)
	}

	// Espacio libre real de cada punto de montaje, todo en una sesion SSH.
	// Cada linea lleva su ruta delante en vez de fiarse del orden: si una
	// unidad se ha ido (SD expulsada), su df falla y desalinearia el resto.
	if len(libs) > 0 {
		var parts []string
		for i := range libs {
			q := ShellQuote(libs[i].Path)
			parts = append(parts, fmt.Sprintf(`echo "==="%s; df -B1 --output=size,used,avail %s 2>/dev/null | tail -1`, q, q))
		}
		out, err := c.Run(ctx, strings.Join(parts, "; ")+"; true")
		if err == nil {
			porRuta := map[string][]string{}
			for _, block := range strings.Split(out, "===") {
				ruta, resto, ok := strings.Cut(block, "\n")
				if !ok {
					continue
				}
				if f := strings.Fields(resto); len(f) >= 3 {
					porRuta[strings.TrimSpace(ruta)] = f
				}
			}
			for i := range libs {
				if f, ok := porRuta[libs[i].Path]; ok {
					libs[i].Total, _ = strconv.ParseUint(f[0], 10, 64)
					libs[i].Used, _ = strconv.ParseUint(f[1], 10, 64)
					libs[i].Free, _ = strconv.ParseUint(f[2], 10, 64)
				}
			}
		}
	}
	return libs, nil
}

// gamesDirFor dice donde viven los juegos no-Steam de una unidad. En el interno
// es ~/Games, que es donde deckman copia todo lo que se envia; en la microSD, un
// Games al lado de steamapps, para no mezclarlo con lo de Steam.
func (c *Client) gamesDirFor(lib Library) string {
	if lib.IsDefault {
		return path.Join(c.Home, "Games")
	}
	return path.Join(lib.Path, "Games")
}

// libraryForPath dice en que unidad vive una carpeta cualquiera.
//
// Los juegos no-Steam no pertenecen a ninguna biblioteca de Steam: son una
// carpeta suelta. Se mira por prefijo mas largo, y lo que no cuelgue de ninguna
// unidad externa se da por interno (~/Games, ~/Emulation, lo que sea).
func libraryForPath(libs []Library, p string) string {
	if p == "" {
		return ""
	}
	best, bestLen := "", -1
	var interna string
	for _, l := range libs {
		if l.IsDefault && interna == "" {
			interna = l.Path
		}
		if l.Path == "" {
			continue
		}
		if (p == l.Path || strings.HasPrefix(p, l.Path+"/")) && len(l.Path) > bestLen {
			best, bestLen = l.Path, len(l.Path)
		}
	}
	if best != "" {
		return best
	}
	return interna
}

// nonSteamDir devuelve la carpeta con la que hay que contar para un juego
// no-Steam: la del juego si se ha podido averiguar, y si no la del ejecutable,
// que es lo unico que hay.
func nonSteamDir(g *Game) string {
	if g.GameRoot != "" {
		return g.GameRoot
	}
	return g.StartDir
}

// gameRootFor localiza la carpeta del juego a partir de la del ejecutable.
//
// Un acceso directo guarda como StartDir la carpeta del .exe, que puede estar
// varios niveles por debajo de la del juego: en "The Chronicles of Riddick" es
// System/Win32_x86, 16 MB de un juego de 11 GB. Mover o borrar eso rompe el
// juego y no libera nada.
//
// La unica frontera fiable es el Games de cada unidad: lo que cuelga de el, el
// primer tramo ES el juego. Un acceso directo a un emulador o a un plugin no
// esta ahi, y entonces se devuelve vacio: mejor no ofrecer moverlo que mover lo
// que no es.
func gameRootFor(libs []Library, startDir string) string {
	for _, l := range libs {
		if l.GamesDir == "" {
			continue
		}
		rest, ok := strings.CutPrefix(startDir, l.GamesDir+"/")
		if !ok {
			continue
		}
		if first, _, _ := strings.Cut(rest, "/"); first != "" {
			return path.Join(l.GamesDir, first)
		}
	}
	return ""
}

func (c *Client) steamGames(ctx context.Context, lib Library) ([]Game, error) {
	dir := path.Join(lib.Path, "steamapps")
	// La biblioteca tiene que existir de verdad: con la orden agrupada de abajo
	// un directorio ilegible devolveria "cero juegos" en vez de un error, y el
	// llamante no sabria distinguir una SD expulsada de una SD vacia.
	if _, err := c.sftp.Stat(dir); err != nil {
		return nil, err
	}

	// Todos los .acf en UNA orden remota, con el mismo separador que usa
	// customCompatTools. Antes era un open/read/close SFTP por juego: con cien
	// juegos y wifi, varios cientos de idas y vueltas encadenadas que eran el
	// grueso del tiempo de escaneo.
	out, err := c.Run(ctx, fmt.Sprintf(
		`for f in %s/appmanifest_*.acf; do [ -f "$f" ] && { echo "===$f"; cat "$f"; }; done 2>/dev/null; true`,
		ShellQuote(dir)))
	if err != nil {
		return nil, err
	}

	var games []Game
	for _, block := range strings.Split(out, "===") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		nl := strings.Index(block, "\n")
		if nl < 0 {
			continue
		}
		body := block[nl+1:]
		root, err := ParseVDF([]byte(body))
		if err != nil {
			continue
		}
		st := root.Get("AppState")
		if st == nil {
			continue
		}
		appID := st.GetString("appid")
		if appID == "" {
			continue
		}
		size, _ := strconv.ParseUint(st.GetString("SizeOnDisk"), 10, 64)
		last, _ := strconv.ParseInt(st.GetString("LastPlayed"), 10, 64)
		flags, _ := strconv.Atoi(st.GetString("StateFlags"))
		installDir := st.GetString("installdir")

		games = append(games, Game{
			AppID:       appID,
			Name:        st.GetString("name"),
			Kind:        "steam",
			Size:        size,
			LastPlayed:  last,
			LibraryPath: lib.Path,
			InstallDir:  installDir,
			FullPath:    path.Join(dir, "common", installDir),
			StateFlags:  flags,
		})
	}
	return games, nil
}

func (c *Client) nonSteamGames(ctx context.Context, steamRoot, userID string) ([]Game, error) {
	// Las operaciones SFTP no miran el contexto: al menos no empezar una nueva
	// con el plazo ya vencido, que es como un escaneo se pasaba del timeout
	// prometido por el handler.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := path.Join(steamRoot, "userdata", userID, "config", "shortcuts.vdf")
	data, err := c.ReadFile(p)
	if err != nil {
		return nil, nil // no tener juegos no-Steam es perfectamente normal
	}
	sf, err := ParseShortcuts(data)
	if err != nil {
		return nil, err
	}
	var games []Game
	for _, s := range sf.Entries {
		exe := Unquote(s.GetStr("Exe"))
		startDir := Unquote(s.GetStr("StartDir"))
		if startDir == "" && exe != "" {
			startDir = path.Dir(exe)
		}
		games = append(games, Game{
			AppID:         strconv.FormatUint(uint64(s.AppID()), 10),
			Name:          s.GetStr("AppName"),
			Kind:          "nonsteam",
			LastPlayed:    int64(s.GetInt("LastPlayTime")),
			Exe:           exe,
			StartDir:      startDir,
			FullPath:      startDir,
			LaunchOptions: s.GetStr("LaunchOptions"),
		})
	}
	return games, nil
}

// toolInstallDirs devuelve los nombres de carpeta de steamapps/common que son
// herramientas y no juegos (las versiones de Proton y los Steam Linux Runtime).
// Se reconocen porque llevan un toolmanifest.vdf dentro.
func (c *Client) toolInstallDirs(ctx context.Context, libs []Library) map[string]bool {
	var quoted []string
	for _, l := range libs {
		quoted = append(quoted, ShellQuote(path.Join(l.Path, "steamapps", "common")))
	}
	// El `; true` final importa: si el ultimo [ -f ] falla, el bucle sale con
	// codigo 1 y perderiamos toda la salida por creerla un error.
	cmd := fmt.Sprintf(
		`for d in %s; do for t in "$d"/*/toolmanifest.vdf; do [ -f "$t" ] && basename "$(dirname "$t")"; done; done 2>/dev/null; true`,
		strings.Join(quoted, " "))
	out, _ := c.Run(ctx, cmd)

	dirs := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			dirs[l] = true
		}
	}
	return dirs
}

// customCompatTools lee las versiones de Proton instaladas a mano (GE-Proton,
// ULWGL...). Estas si traen un compatibilitytool.vdf con su nombre interno.
func (c *Client) customCompatTools(ctx context.Context, steamRoot string, libs []Library) []CompatTool {
	dirs := []string{path.Join(steamRoot, "compatibilitytools.d")}
	for _, l := range libs {
		dirs = append(dirs, path.Join(l.Path, "compatibilitytools.d"))
	}
	var quoted []string
	for _, d := range dirs {
		quoted = append(quoted, ShellQuote(d))
	}
	cmd := fmt.Sprintf(
		`for d in %s; do for t in "$d"/*/compatibilitytool.vdf; do [ -f "$t" ] && { echo "===$t"; cat "$t"; }; done; done 2>/dev/null; true`,
		strings.Join(quoted, " "))
	out, _ := c.Run(ctx, cmd)

	var tools []CompatTool
	for _, block := range strings.Split(out, "===") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		nl := strings.Index(block, "\n")
		if nl < 0 {
			continue
		}
		toolPath, body := block[:nl], block[nl+1:]
		root, err := ParseVDF([]byte(body))
		if err != nil {
			continue
		}
		compat := root.Get("compatibilitytools", "compat_tools")
		if compat == nil {
			continue
		}
		for _, entry := range compat.Children {
			if entry.Key == "" {
				continue
			}
			display := entry.GetString("display_name")
			if display == "" {
				display = path.Base(path.Dir(toolPath))
			}
			tools = append(tools, CompatTool{Name: entry.Key, DisplayName: display})
		}
	}
	return tools
}

// protonInternalName traduce el nombre visible de un Proton oficial al
// identificador que hay que escribir en config.vdf. Devuelve "" si no es un
// Proton seleccionable.
//
// La regla, verificada contra el config.vdf de una Deck real (donde aparecen
// proton_42, proton_8, proton_9 y proton_experimental): se quita el "Proton ",
// y de la version se elimina el punto, salvo que la minor sea 0, en cuyo caso
// se deja solo la major.
//
//	Proton Experimental -> proton_experimental    Proton 4.2  -> proton_42
//	Proton Hotfix       -> proton_hotfix          Proton 6.3  -> proton_63
//	Proton 9.0          -> proton_9               Proton 10.0 -> proton_10
func protonInternalName(appName string) string {
	name := strings.TrimSpace(appName)
	if !strings.HasPrefix(name, "Proton") {
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(name, "Proton"))
	// Sufijos de estado que Steam anade al nombre visible pero no al id.
	for _, suffix := range []string{"(Beta)", "(beta)"} {
		rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), suffix))
	}
	switch strings.ToLower(rest) {
	case "":
		return ""
	case "experimental":
		return "proton_experimental"
	case "hotfix":
		return "proton_hotfix"
	case "easyanticheat runtime", "battleye runtime":
		return "" // runtimes auxiliares, no son seleccionables
	}

	major, minor, ok := strings.Cut(rest, ".")
	if !ok {
		if isDigits(major) {
			return "proton_" + major
		}
		return ""
	}
	if !isDigits(major) || !isDigits(minor) {
		return ""
	}
	if minor == "0" {
		return "proton_" + major
	}
	return "proton_" + major + minor
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// annotateCompat rellena que Proton tiene asignado cada juego, leyendo
// CompatToolMapping de config.vdf.
func (c *Client) annotateCompat(ctx context.Context, inv *Inventory) {
	data, err := c.ReadFile(path.Join(inv.SteamRoot, "config", "config.vdf"))
	if err != nil {
		return
	}
	root, err := ParseVDF(data)
	if err != nil {
		return
	}
	mapping := root.Get("InstallConfigStore", "Software", "Valve", "Steam", "CompatToolMapping")
	if mapping == nil {
		return
	}
	byID := map[string]string{}
	for _, e := range mapping.Children {
		byID[e.Key] = e.GetString("name")
	}
	for i := range inv.Games {
		if name, ok := byID[inv.Games[i].AppID]; ok {
			inv.Games[i].CompatTool = name
		}
	}
}

// annotateExtraSizes mide en una sola orden remota el prefijo de Proton, la
// cache de shaders y, para los juegos no-Steam, el tamano de su carpeta.
func (c *Client) annotateExtraSizes(ctx context.Context, inv *Inventory) {
	var parts []string
	for _, lib := range inv.Libraries {
		parts = append(parts,
			ShellQuote(path.Join(lib.Path, "steamapps", "compatdata")),
			ShellQuote(path.Join(lib.Path, "steamapps", "shadercache")))
	}
	// Un du por cada appid de compatdata/shadercache de golpe.
	cmd := fmt.Sprintf(
		`for base in %s; do [ -d "$base" ] && du -sb "$base"/* 2>/dev/null | while read -r sz p; do echo "$(basename "$base")|$(basename "$p")|$sz"; done; done`,
		strings.Join(parts, " "))
	out, _ := c.Run(ctx, cmd)

	compat := map[string]uint64{}
	shader := map[string]uint64{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 3 {
			continue
		}
		sz, _ := strconv.ParseUint(f[2], 10, 64)
		switch f[0] {
		case "compatdata":
			compat[f[1]] += sz
		case "shadercache":
			shader[f[1]] += sz
		}
	}

	// Tamano en disco de los juegos no-Steam. Se mide la carpeta del juego, no
	// la del ejecutable: si no, un juego de 11 GB con el .exe en System/Win32
	// aparecia con 16 MB, y ese numero es el que sale luego al mover o borrar.
	var dirs []string
	vistos := map[string]bool{}
	for _, g := range inv.Games {
		d := nonSteamDir(&g)
		if g.Kind == "nonsteam" && d != "" && !vistos[d] {
			vistos[d] = true
			dirs = append(dirs, ShellQuote(d))
		}
	}
	sizes := map[string]uint64{}
	if len(dirs) > 0 {
		out, _ := c.Run(ctx, fmt.Sprintf(`du -sb %s 2>/dev/null`, strings.Join(dirs, " ")))
		for _, line := range strings.Split(out, "\n") {
			// du separa tamano y ruta con un TABULADOR. Partir por espacios y
			// volver a juntar destrozaba las rutas con dos espacios seguidos o
			// con tabuladores dentro, y el juego se quedaba sin tamano.
			f := strings.SplitN(strings.TrimRight(line, "\r\n"), "\t", 2)
			if len(f) != 2 {
				continue
			}
			sz, err := strconv.ParseUint(strings.TrimSpace(f[0]), 10, 64)
			if err != nil {
				continue
			}
			sizes[f[1]] = sz
		}
	}

	for i := range inv.Games {
		g := &inv.Games[i]
		if v, ok := compat[g.AppID]; ok {
			g.CompatSize, g.HasCompat = v, true
		}
		if v, ok := shader[g.AppID]; ok {
			g.ShaderSize, g.HasShaders = v, true
		}
		if g.Kind == "nonsteam" {
			if v, ok := sizes[nonSteamDir(g)]; ok {
				g.Size = v
			}
		}
	}
}

// romSystems localiza la carpeta de ROMs de EmuDeck y los sistemas que ya
// existen dentro, para poder ofrecerlos como destino.
func (c *Client) romSystems(ctx context.Context) (string, []string) {
	candidates := []string{
		path.Join(c.Home, "Emulation", "roms"),
	}
	out, _ := c.Run(ctx, `ls -d /run/media/deck/*/Emulation/roms /run/media/*/*/Emulation/roms 2>/dev/null`)
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			candidates = append(candidates, l)
		}
	}

	for _, dir := range candidates {
		if ctx.Err() != nil {
			return "", nil
		}
		if !c.Exists(dir) {
			continue
		}
		entries, err := c.sftp.ReadDir(dir)
		if err != nil {
			continue
		}
		var systems []string
		for _, e := range entries {
			if e.IsDir() {
				systems = append(systems, e.Name())
			}
		}
		if len(systems) > 0 {
			sort.Strings(systems)
			return dir, systems
		}
	}
	return "", nil
}
