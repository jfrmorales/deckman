package deck

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Las portadas de la biblioteca salen de dos sitios distintos de la Deck, y
// ninguno de los dos vale por si solo:
//
//	userdata/<id>/config/grid/     el arte que ha elegido el usuario (o deckman)
//	appcache/librarycache/<appid>/ lo que Steam se baja de la tienda
//
// Comprobado en una Deck real: de 428 juegos con cache, 173 tenian
// library_600x900.jpg y 328 solo header.jpg (la horizontal de la tienda). Asi
// que hay que combinar ambas fuentes y aceptar una horizontal recortada antes
// que dejar el hueco vacio.
// Steam ha cambiado el nombre y la profundidad de estos ficheros entre
// versiones, y en una misma Deck conviven las dos formas:
//
//	librarycache/<appid>/library_600x900.jpg          la de siempre (173 juegos)
//	librarycache/<appid>/<hash>/library_capsule.jpg   la nueva (21 juegos)
//
// Por eso se buscan los cuatro nombres a dos profundidades. Sin la variante
// nueva, juegos como Rocket League o Tomb Raider salian sin portada aunque
// Steam si se la ensena.
var (
	coverVerticales   = []string{"library_600x900.jpg", "library_capsule.jpg"}
	coverHorizontales = []string{"header.jpg", "library_header.jpg"}
)

// AnnotateCovers marca en el inventario que juegos tienen portada y devuelve el
// indice con la ruta de cada una. Si algo falla se devuelve nil y la biblioteca
// se pinta igual, solo que con los recuadros vacios: una portada no vale un
// error en la cara del usuario.
func (c *Client) AnnotateCovers(ctx context.Context, inv *Inventory) map[string]string {
	index, err := c.CoverIndex(ctx)
	if err != nil {
		return nil
	}
	for i := range inv.Games {
		_, ok := index[inv.Games[i].AppID]
		inv.Games[i].HasCover = ok
	}
	return index
}

// reGridArt separa el appid del sufijo que identifica el tipo de arte en la
// carpeta grid: "2154132344p.png" -> "2154132344" + "p".
var reGridArt = regexp.MustCompile(`^(\d+)([a-z_]*)$`)

// CoverIndex devuelve, para cada juego, la ruta remota de la imagen que mejor
// hace de portada. La clave es el appid tal cual aparece en el inventario.
//
// Se resuelve de una vez para toda la biblioteca, con un unico comando remoto:
// preguntar juego a juego eran cuatro o cinco stat por fila y con cuarenta
// juegos se notaba al abrir la pestana.
//
// El orden de preferencia imita lo que ensena la propia cuadricula de Steam:
//
//  1. <appid>p.png    portada vertical elegida por el usuario
//  2. library_600x900.jpg  la vertical que Steam se bajo de la tienda
//  3. <appid>.png     portada horizontal elegida por el usuario
//  4. header.jpg      la cabecera de la tienda, recortada
func (c *Client) CoverIndex(ctx context.Context) (map[string]string, error) {
	gridDir, err := c.GridDir(ctx)
	if err != nil {
		return nil, err
	}
	// Steam ha movido esta cache de sitio entre versiones: en SteamOS 3.7 vive
	// en appcache/, pero las versiones nuevas la escriben dentro de userdata.
	// Miramos las dos, que solo cuesta un glob mas.
	cacheDirs := []string{
		path.Join(c.SteamRoot(), "appcache", "librarycache"),
		path.Join(path.Dir(gridDir), "librarycache"),
	}

	// Se entrecomilla la CARPETA, no el patron: ShellQuote mete comillas
	// simples y dentro de ellas el shell no expande el asterisco, asi que
	// citarlo entero devolvia siempre cero resultados y los juegos de Steam se
	// quedaban sin portada. Lo que va suelto son constantes de aqui al lado,
	// sin nada que el shell pueda interpretar.
	var globs []string
	for _, d := range cacheDirs {
		for _, name := range append(append([]string{}, coverVerticales...), coverHorizontales...) {
			globs = append(globs, ShellQuote(d)+"/*/"+name, ShellQuote(d)+"/*/*/"+name)
		}
	}
	// Las dos listas van en la misma orden y separadas por una marca: dos
	// conexiones para esto seria tirar el tiempo. El `true` final evita que un
	// glob sin resultados tumbe toda la salida.
	out, err := c.Run(ctx, fmt.Sprintf("echo '#grid'; ls -1 %s 2>/dev/null; echo '#cache'; ls -1d %s 2>/dev/null; true",
		ShellQuote(gridDir), strings.Join(globs, " ")))
	if err != nil {
		return nil, err
	}

	// Se guarda la mejor candidata de cada juego junto con su prioridad, para no
	// depender del orden en que lleguen las lineas.
	type candidate struct {
		path string
		prio int
	}
	best := map[string]candidate{}
	keep := func(appID, p string, prio int) {
		if cur, ok := best[appID]; ok && cur.prio <= prio {
			return
		}
		best[appID] = candidate{p, prio}
	}

	section := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#"):
			section = line
			continue
		}
		if section == "#grid" {
			ext := path.Ext(line)
			// Los <appid>.json del plugin de Decky comparten nombre con la
			// portada horizontal: sin filtrar por extension de imagen se
			// colarian aqui como si fueran arte.
			if !isArtExt(ext) {
				continue
			}
			m := reGridArt.FindStringSubmatch(strings.TrimSuffix(line, ext))
			if m == nil {
				continue
			}
			switch m[2] {
			case "p":
				keep(m[1], path.Join(gridDir, line), 1)
			case "":
				keep(m[1], path.Join(gridDir, line), 3)
			}
			continue
		}
		// #cache: .../librarycache/<appid>/[<hash>/]<fichero>. El appid es
		// siempre el primer tramo tras la carpeta de la cache, este el fichero
		// colgando de el o de una carpeta con su hash.
		appID := cacheAppID(cacheDirs, line)
		if appID == "" {
			continue
		}
		name := path.Base(line)
		if contiene(coverVerticales, name) {
			keep(appID, line, 2)
		} else if contiene(coverHorizontales, name) {
			keep(appID, line, 4)
		}
	}

	index := make(map[string]string, len(best))
	for appID, cand := range best {
		index[appID] = cand.path
	}
	return index, nil
}

// cacheAppID saca el appid de una ruta de la cache de Steam, sirva el fichero
// de <appid>/x.jpg o de <appid>/<hash>/x.jpg.
func cacheAppID(cacheDirs []string, p string) string {
	for _, d := range cacheDirs {
		rest, ok := strings.CutPrefix(p, d+"/")
		if !ok {
			continue
		}
		id, _, _ := strings.Cut(rest, "/")
		if id != "" && strings.Trim(id, "0123456789") == "" {
			return id
		}
	}
	return ""
}

func contiene(lista []string, s string) bool {
	for _, x := range lista {
		if x == s {
			return true
		}
	}
	return false
}
