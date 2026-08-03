package deck

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// MoveGame traslada un juego de una unidad a otra (del SSD interno a la microSD
// o al reves) sin volver a descargarlo.
//
// La copia la hace rsync dentro de la propia Deck, asi que los datos no pasan
// por la red: nosotros solo lanzamos la orden y seguimos su progreso.
//
// Los juegos de Steam y los no-Steam se mueven de forma distinta (unos tienen
// manifiesto y biblioteca, los otros un acceso directo con rutas escritas
// dentro), asi que aqui solo se localizan el juego y el destino.
//
// inv puede venir de una foto reciente del servidor (evita repetir el Scan
// completo, con su du -sb de todos los compatdata) o ser nil para escanear
// aqui. De la foto solo salen rutas y tamanos: todo lo que decide si es seguro
// tocar algo (SteamRunning, CEFAvailable, Exists) se comprueba fresco abajo.
func (c *Client) MoveGame(ctx context.Context, inv *Inventory, appID, targetLibPath string, onProgress ProgressFunc) error {
	report := throttle(onProgress)

	if inv == nil {
		var err error
		inv, err = c.Scan(ctx)
		if err != nil {
			return err
		}
	}

	var game *Game
	for i := range inv.Games {
		if inv.Games[i].AppID == appID {
			game = &inv.Games[i]
			break
		}
	}
	if game == nil {
		return fmt.Errorf("no se encontro el juego con id %s", appID)
	}

	var target *Library
	for i := range inv.Libraries {
		if inv.Libraries[i].Path == targetLibPath {
			target = &inv.Libraries[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("la unidad de destino %s no esta registrada en Steam", targetLibPath)
	}
	if game.LibraryPath == targetLibPath {
		return fmt.Errorf("%s ya esta en %s", game.Name, target.Label)
	}

	if game.Kind == "nonsteam" {
		return c.moveNonSteamGame(ctx, game, target, report)
	}
	return c.moveSteamGame(ctx, game, target, report)
}

// moveSteamGame mueve un juego de Steam: la carpeta, el prefijo de Proton, la
// cache de shaders y, al final, el manifiesto.
//
// Steam tiene que estar cerrado. Guarda su estado en memoria y al salir
// reescribe los .acf, de modo que si movemos los ficheros con Steam abierto
// deshace el cambio y deja el juego marcado como no instalado.
func (c *Client) moveSteamGame(ctx context.Context, game *Game, target *Library, report ProgressFunc) error {
	appID := game.AppID
	targetLibPath := target.Path

	if c.SteamRunning(ctx) {
		return fmt.Errorf("Steam esta abierto en la Deck: cierralo antes de mover un juego, o deshara el cambio al salir")
	}

	needed := game.Size + game.CompatSize + game.ShaderSize
	if target.Free > 0 && needed > 0 && needed+512<<20 > target.Free {
		return fmt.Errorf("no cabe: %s ocupa %s y en %s quedan %s",
			game.Name, humanBytes(needed), target.Label, humanBytes(target.Free))
	}

	srcApps := path.Join(game.LibraryPath, "steamapps")
	dstApps := path.Join(targetLibPath, "steamapps")

	for _, d := range []string{
		path.Join(dstApps, "common"),
		path.Join(dstApps, "compatdata"),
		path.Join(dstApps, "shadercache"),
	} {
		if err := c.MkdirAll(d); err != nil {
			return fmt.Errorf("no se pudo preparar %s: %w", d, err)
		}
	}

	// Carpeta del juego, prefijo de Proton y cache de shaders.
	type part struct {
		label string
		src   string
		dst   string
		size  uint64
	}
	parts := []part{{
		label: "juego",
		src:   path.Join(srcApps, "common", game.InstallDir),
		dst:   path.Join(dstApps, "common"),
		size:  game.Size,
	}}
	if game.HasCompat {
		parts = append(parts, part{"prefijo de Proton",
			path.Join(srcApps, "compatdata", appID), path.Join(dstApps, "compatdata"), game.CompatSize})
	}
	if game.HasShaders {
		parts = append(parts, part{"cache de shaders",
			path.Join(srcApps, "shadercache", appID), path.Join(dstApps, "shadercache"), game.ShaderSize})
	}

	var movedBytes uint64
	for _, p := range parts {
		if !c.Exists(p.src) {
			continue
		}
		base := movedBytes
		if err := c.copyVerified(ctx, p.src, p.dst, p.label, base, needed, report); err != nil {
			return err
		}

		report(Progress{Phase: "borrando el original de " + p.label, BytesDone: base + p.size, BytesTotal: needed})
		if _, err := c.Run(ctx, fmt.Sprintf("rm -rf %s", ShellQuote(p.src))); err != nil {
			return fmt.Errorf("copiado, pero no se pudo borrar el original %s: %w", p.src, err)
		}
		movedBytes += p.size
	}

	// El manifiesto va al final: mientras esta en el origen, Steam considera
	// que el juego vive alli. Moverlo el ultimo hace la operacion reanudable.
	acf := "appmanifest_" + appID + ".acf"
	manifest, err := c.ReadFile(path.Join(srcApps, acf))
	if err != nil {
		return fmt.Errorf("no se pudo leer %s: %w", acf, err)
	}
	if err := c.WriteFileAtomic(path.Join(dstApps, acf), manifest); err != nil {
		return fmt.Errorf("no se pudo escribir el manifiesto en el destino: %w", err)
	}
	if _, err := c.Run(ctx, fmt.Sprintf("rm -f %s %s",
		ShellQuote(path.Join(srcApps, acf)),
		ShellQuote(path.Join(dstApps, acf+".deckman.bak")))); err != nil {
		return fmt.Errorf("no se pudo retirar el manifiesto original: %w", err)
	}

	report(Progress{
		Phase: "listo", Done: true, BytesDone: needed, BytesTotal: needed,
		Message: fmt.Sprintf("%s movido a %s. Arranca Steam y, si el juego apareciera como no instalado, usa Propiedades > Archivos locales > Verificar.", game.Name, target.Label),
	})
	return nil
}

// copyVerified copia src dentro de dstDir con rsync y comprueba el resultado.
// No borra nada: eso lo decide quien llama, y solo despues de que esto vuelva
// sin error.
//
// base y total son para la barra de progreso, que cuenta el conjunto de la
// operacion y no cada parte por separado.
func (c *Client) copyVerified(ctx context.Context, src, dstDir, label string, base, total uint64, report ProgressFunc) error {
	phase := "copiando " + label
	report(Progress{Phase: phase, File: path.Base(src), BytesDone: base, BytesTotal: total})

	// --info=progress2 da bytes acumulados; -aHAX conserva permisos y
	// atributos, que Proton necesita para los prefijos.
	cmd := fmt.Sprintf("rsync -aHAX --info=progress2 --no-inc-recursive %s %s",
		ShellQuote(src), ShellQuote(dstDir+"/"))
	err := c.RunStream(ctx, cmd, func(line string) {
		if n, ok := parseRsyncBytes(line); ok {
			report(Progress{Phase: phase, File: path.Base(src), BytesDone: base + n, BytesTotal: total})
		}
	})
	if err != nil {
		return fmt.Errorf("fallo copiando %s: %w", label, err)
	}

	// Verificamos tamano antes de que nadie borre el origen. Si no cuadra,
	// paramos y lo dejamos intacto. Y si NO se puede verificar tampoco vale:
	// dar por buena una copia que no hemos podido comprobar es la forma mas
	// facil de perder un juego de 60 GB.
	srcSize, err1 := c.dirSize(ctx, src)
	dstSize, err2 := c.dirSize(ctx, path.Join(dstDir, path.Base(src)))
	if err1 != nil || err2 != nil {
		err := err1
		if err == nil {
			err = err2
		}
		return fmt.Errorf("no pude verificar la copia de %s; no borro nada, revisa %s a mano: %w",
			label, dstDir, err)
	}
	if srcSize != dstSize {
		return fmt.Errorf("la copia de %s no coincide (origen %s, destino %s); no borro nada",
			label, humanBytes(srcSize), humanBytes(dstSize))
	}
	return nil
}

// moveNonSteamGame lleva la carpeta de un juego no-Steam a la otra unidad y
// deja el acceso directo apuntando al sitio nuevo.
//
// Un juego no-Steam no tiene manifiesto ni pertenece a ninguna biblioteca: es
// una carpeta suelta y una entrada en shortcuts.vdf con la ruta escrita dentro.
// Por eso mover uno es copiar la carpeta y reescribir esa ruta.
//
// El prefijo de Proton y la cache de shaders NO se mueven. Steam los crea para
// los accesos directos en la biblioteca principal (comprobado en la Deck del
// usuario: los prefijos de los siete juegos no-Steam estaban en
// ~/.local/share/Steam/steamapps/compatdata aunque la microSD tambien es
// biblioteca), y llevarselos a la tarjeta dejaria las partidas guardadas donde
// Steam no las busca.
func (c *Client) moveNonSteamGame(ctx context.Context, game *Game, target *Library, report ProgressFunc) error {
	// Se mueve la carpeta del JUEGO, no la del ejecutable: en Riddick el acceso
	// directo apunta a System/Win32_x86, y llevarse solo eso dejaria 11 GB
	// tirados y el juego roto. GameRoot solo esta cuando el juego cuelga de un
	// Games conocido, que es justo cuando sabemos donde empieza y donde acaba.
	if game.GameRoot == "" {
		return fmt.Errorf("no se sabe que carpeta es exactamente %s: solo se pueden mover los juegos que estan "+
			"en una carpeta Games (los que envia deckman). El acceso directo apunta a %s", game.Name, game.StartDir)
	}
	src := path.Clean(game.GameRoot)
	exe := path.Clean(game.Exe)

	// Y solo si el juego se lanza desde dentro de su propia carpeta. Un acceso
	// directo puede arrancar otra cosa (Heroic lanza "flatpak", los de EmuDeck
	// un emulador que vive fuera): ahi mover la carpeta no arregla nada y
	// reescribir el Exe lo romperia.
	if exe == "" || exe == "." || !withinDir(src, exe) {
		return fmt.Errorf("%s se lanza desde fuera de su carpeta (%s), asi que moverlo no serviria de nada",
			game.Name, game.Exe)
	}
	if !c.safeToDelete(src) {
		return fmt.Errorf("me niego a mover %s: no parece la carpeta de un juego", src)
	}

	dstDir := target.GamesDir
	if dstDir == "" {
		dstDir = c.gamesDirFor(*target)
	}
	if path.Dir(src) == path.Clean(dstDir) {
		return fmt.Errorf("%s ya esta en %s", game.Name, dstDir)
	}
	dst := path.Join(dstDir, path.Base(src))
	if c.Exists(dst) {
		return fmt.Errorf("en el destino ya hay una carpeta %s; muevela o borrala antes", dst)
	}

	needed := game.Size
	if target.Free > 0 && needed > 0 && needed+512<<20 > target.Free {
		return fmt.Errorf("no cabe: %s ocupa %s y en %s quedan %s",
			game.Name, humanBytes(needed), target.Label, humanBytes(target.Free))
	}

	// Como se va a actualizar el acceso directo se decide ANTES de tocar un
	// solo fichero. Con Steam abierto, shortcuts.vdf no se puede escribir (lo
	// tiene en memoria y al salir lo reescribe, llevandose por delante lo que
	// haya): hay que pedirselo a el. Si no responde, mejor no empezar siquiera,
	// que quedarse con el juego movido y el acceso directo apuntando al vacio.
	appID64, err := strconv.ParseUint(game.AppID, 10, 32)
	if err != nil {
		return fmt.Errorf("id de acceso directo invalido: %s", game.AppID)
	}
	appID := uint32(appID64)
	// pgrep fresco, no el del inventario: la foto puede venir de una cache del
	// servidor, y de si Steam corre AHORA depende por que via se reescribe el
	// acceso directo. Equivocarse aqui es editar shortcuts.vdf con Steam
	// abierto, que ya borro seis juegos una vez.
	enCaliente := c.SteamRunning(ctx)
	if enCaliente && !c.CEFAvailable(ctx) {
		return fmt.Errorf(
			"Steam esta abierto pero no responde: cierralo en la Deck y vuelve a intentarlo, " +
				"o reinicialo y espera a que cargue del todo. Cambiar la ruta del juego con Steam " +
				"abierto le haria perder los accesos directos que ya tienes")
	}

	if err := c.MkdirAll(dstDir); err != nil {
		return fmt.Errorf("no se pudo preparar %s: %w", dstDir, err)
	}
	if err := c.copyVerified(ctx, src, dstDir, "juego", 0, needed, report); err != nil {
		return err
	}

	// El acceso directo se actualiza con las dos copias en su sitio: si algo
	// falla aqui, el juego sigue entero en el origen y basta con borrar la
	// copia nueva.
	report(Progress{Phase: "actualizando el acceso directo", BytesDone: needed, BytesTotal: needed})
	newExe := dst + strings.TrimPrefix(exe, src)
	// La carpeta de trabajo se muda igual que el resto: puede ser el juego
	// entero o una subcarpeta suya, y hay que respetar cual era.
	newStartDir := dst + strings.TrimPrefix(path.Clean(game.StartDir), src)
	if enCaliente {
		if err := c.SetShortcutPathLive(ctx, appID, newExe, newStartDir); err != nil {
			return fmt.Errorf("copiado a %s, pero Steam no acepto la ruta nueva; borra esa copia y vuelve a intentarlo: %w", dst, err)
		}
	} else if err := c.RelocateShortcut(ctx, appID, src, dst); err != nil {
		return fmt.Errorf("copiado a %s, pero no se pudo actualizar el acceso directo; borra esa copia y vuelve a intentarlo: %w", dst, err)
	}

	report(Progress{Phase: "borrando el original", BytesDone: needed, BytesTotal: needed})
	if _, err := c.Run(ctx, fmt.Sprintf("rm -rf %s", ShellQuote(src))); err != nil {
		return fmt.Errorf("%s ya esta en %s y el acceso directo apunta alli, pero no se pudo borrar la copia vieja de %s: %w",
			game.Name, dst, src, err)
	}

	msg := fmt.Sprintf("%s movido a %s.", game.Name, target.Label)
	if enCaliente {
		msg += " Steam ya lo sabe, sin reiniciar."
	} else {
		msg += " Arranca Steam y compruebalo."
	}
	// El prefijo de Proton no viaja, y conviene decirlo: es donde estan las
	// partidas guardadas de los juegos de Windows.
	if game.HasCompat {
		msg += " El prefijo de Proton (partidas guardadas) se queda donde estaba, que es donde Steam lo busca."
	}
	report(Progress{Phase: "listo", Done: true, BytesDone: needed, BytesTotal: needed, Message: msg})
	return nil
}

// withinDir dice si p esta dentro de dir (o es el propio dir).
func withinDir(dir, p string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// dirSize devuelve el tamano en bytes de un arbol remoto.
func (c *Client) dirSize(ctx context.Context, p string) (uint64, error) {
	out, err := c.Run(ctx, fmt.Sprintf("du -sb %s 2>/dev/null | cut -f1", ShellQuote(p)))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(out), 10, 64)
}

// parseRsyncBytes saca el contador de bytes de una linea de --info=progress2,
// del estilo "  1,234,567  45%  12.34MB/s  0:01:23".
func parseRsyncBytes(line string) (uint64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasSuffix(fields[1], "%") {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.ReplaceAll(fields[0], ",", ""), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// DeleteTargets marca que partes borrar de un juego.
type DeleteTargets struct {
	Game        bool `json:"game"`        // los ficheros del juego
	CompatData  bool `json:"compatData"`  // prefijo de Proton (partidas guardadas incluidas)
	ShaderCache bool `json:"shaderCache"` // cache de shaders, se regenera sola
	Shortcut    bool `json:"shortcut"`    // acceso directo, solo juegos no-Steam
}

// Delete borra las partes indicadas de un juego y devuelve cuanto se ha liberado.
//
// inv admite una foto reciente del servidor, igual que en MoveGame: de ella
// solo salen rutas y tamanos, y los chequeos peligrosos (SteamRunning,
// CEFAvailable, safeToDelete) se hacen frescos aqui debajo.
func (c *Client) Delete(ctx context.Context, inv *Inventory, appID string, t DeleteTargets) (uint64, error) {
	if inv == nil {
		var err error
		inv, err = c.Scan(ctx)
		if err != nil {
			return 0, err
		}
	}
	var game *Game
	for i := range inv.Games {
		if inv.Games[i].AppID == appID {
			game = &inv.Games[i]
			break
		}
	}
	if game == nil {
		return 0, fmt.Errorf("no se encontro el juego con id %s", appID)
	}
	// Un solo pgrep para toda la operacion, y fresco a proposito (nunca el del
	// inventario, que puede venir de una foto): de esto depende que no se toque
	// shortcuts.vdf con Steam abierto.
	steamCorriendo := c.SteamRunning(ctx)
	if t.Game && game.Kind == "steam" && steamCorriendo {
		return 0, fmt.Errorf("Steam esta abierto: cierralo antes de desinstalar, o volvera a escribir el manifiesto")
	}

	// Quitar el acceso directo toca shortcuts.vdf, que con Steam abierto NO se
	// puede escribir: Steam lo tiene en memoria y al salir lo reescribe con su
	// copia, que puede llevarse por delante los demas juegos no-Steam (paso de
	// verdad: quedo 1 entrada de 7). Con Steam abierto hay que pedirselo a el
	// por su depurador; si no responde, no se borra nada.
	//
	// La decision se toma AQUI, antes del rm -rf, para no dejar el juego sin
	// ficheros y con el acceso directo colgando.
	quitarEnCaliente := false
	var shortcutID uint32
	if t.Shortcut && game.Kind == "nonsteam" {
		id, err := strconv.ParseUint(appID, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("id de acceso directo invalido: %s", appID)
		}
		shortcutID = uint32(id)
		if steamCorriendo {
			if !c.CEFAvailable(ctx) {
				return 0, fmt.Errorf(
					"Steam esta abierto pero no responde: cierralo en la Deck y vuelve a intentarlo, " +
						"o reinicialo y espera a que cargue del todo. Quitar el acceso directo con Steam " +
						"abierto le haria perder los que ya tienes")
			}
			quitarEnCaliente = true
		}
	}

	var freed uint64
	var paths []string

	if t.Game {
		switch game.Kind {
		case "steam":
			paths = append(paths,
				path.Join(game.LibraryPath, "steamapps", "common", game.InstallDir),
				path.Join(game.LibraryPath, "steamapps", "appmanifest_"+appID+".acf"))
		case "nonsteam":
			// La carpeta del juego, no la del ejecutable: borrar el
			// System/Win32_x86 de Riddick decia "liberados 16 MB" y dejaba los
			// otros 11 GB en el disco. Es el mismo tamano que se ensena en la
			// biblioteca y en el dialogo, asi que lo que se borra y lo que se
			// promete coinciden.
			dir := nonSteamDir(game)
			if dir == "" {
				return 0, fmt.Errorf("el acceso directo no apunta a ninguna carpeta; borra los ficheros a mano")
			}
			if !c.safeToDelete(dir) {
				return 0, fmt.Errorf("me niego a borrar %s: no parece una carpeta de juego", dir)
			}
			paths = append(paths, dir)
		}
		freed += game.Size
	}
	if t.CompatData && game.HasCompat {
		for _, lib := range inv.Libraries {
			paths = append(paths, path.Join(lib.Path, "steamapps", "compatdata", appID))
		}
		freed += game.CompatSize
	}
	if t.ShaderCache && game.HasShaders {
		for _, lib := range inv.Libraries {
			paths = append(paths, path.Join(lib.Path, "steamapps", "shadercache", appID))
		}
		freed += game.ShaderSize
	}

	if len(paths) > 0 {
		var quoted []string
		for _, p := range paths {
			quoted = append(quoted, ShellQuote(p))
		}
		if _, err := c.Run(ctx, "rm -rf "+strings.Join(quoted, " ")); err != nil {
			return 0, err
		}
	}

	if t.Shortcut && game.Kind == "nonsteam" {
		if quitarEnCaliente {
			if err := c.RemoveShortcutLive(ctx, shortcutID); err != nil {
				return freed, fmt.Errorf("no se pudo quitar el acceso directo a traves de Steam: %w", err)
			}
			// El mapeo de Proton huerfano se queda: limpiarlo obligaria a
			// escribir config.vdf, y con Steam abierto eso tambien se pierde al
			// salir. Steam se encarga de sus propios accesos directos.
		} else {
			if err := c.RemoveShortcut(ctx, shortcutID); err != nil {
				return freed, err
			}
			c.SetCompatTool(ctx, appID, "") // limpia el mapeo de Proton huerfano
		}
	}
	return freed, nil
}

// safeToDelete es una red de seguridad contra un `rm -rf` sobre algo critico:
// un shortcuts.vdf corrupto podria apuntar a /home/deck o a /.
func (c *Client) safeToDelete(p string) bool {
	p = path.Clean(p)
	if p == "/" || p == "" || p == "." {
		return false
	}
	forbidden := []string{
		c.Home, "/home", "/usr", "/etc", "/var", "/run", "/boot",
		path.Join(c.Home, ".steam"), path.Join(c.Home, ".local"),
		path.Join(c.Home, ".local/share/Steam"),
		path.Join(c.Home, "Games"), path.Join(c.Home, "Emulation"),
	}
	for _, f := range forbidden {
		if p == path.Clean(f) {
			return false
		}
	}
	// Al menos dos niveles por debajo de la raiz.
	return strings.Count(strings.Trim(p, "/"), "/") >= 1
}

// RestartSteam reinicia Steam en la Deck para que recargue shortcuts.vdf,
// config.vdf y las caratulas, que solo se leen al arrancar.
//
// En modo juego Steam corre bajo la unidad de usuario steam-launcher.service,
// que ya sabe pararlo bien (su ExecStop mata al hijo del envoltorio, que no
// reenvia senales). Reiniciar esa unidad es la via limpia y ademas lo vuelve a
// levantar solo. En modo escritorio no existe esa unidad y hay que recurrir a
// "steam -shutdown".
//
// Devuelve una descripcion de lo que ha hecho, para poder decirselo al usuario.
func (c *Client) RestartSteam(ctx context.Context) (string, error) {
	if !c.SteamRunning(ctx) {
		return "", fmt.Errorf("Steam no parece estar arrancado en la Deck")
	}

	state, _ := c.Run(ctx, "systemctl --user is-active steam-launcher.service 2>/dev/null || true")
	if strings.TrimSpace(state) == "active" {
		// Puede tardar: la unidad da hasta 60 s a Steam para cerrarse.
		runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if _, err := c.Run(runCtx, "systemctl --user restart steam-launcher.service"); err != nil {
			return "", fmt.Errorf("no se pudo reiniciar el servicio de Steam: %w", err)
		}
		return "Steam reiniciado en modo juego. Tarda unos segundos en volver a aparecer.", nil
	}

	// Modo escritorio: lo cerramos y lo tiene que abrir el usuario.
	if _, err := c.Run(ctx, "steam -shutdown"); err != nil {
		return "", fmt.Errorf("no se pudo cerrar Steam: %w", err)
	}
	return "Steam cerrado. En modo escritorio hay que volver a abrirlo a mano desde la Deck.", nil
}

// SteamRestartMode indica como se reiniciaria Steam, para avisar antes.
func (c *Client) SteamRestartMode(ctx context.Context) string {
	state, _ := c.Run(ctx, "systemctl --user is-active steam-launcher.service 2>/dev/null || true")
	if strings.TrimSpace(state) == "active" {
		return "gamemode"
	}
	return "desktop"
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}
