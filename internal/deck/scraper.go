package deck

import (
	"context"
	"fmt"
	"github.com/jfrmorales/deckman/internal/i18n"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// El scraper tira de libretro-thumbnails, que es la unica base de caratulas
// retro que no pide registrarse: ni clave de API ni cuenta. Indexa por el
// nombre exacto del fichero en la nomenclatura No-Intro/Redump, que es la que
// usan los volcados que reconoce EmuDeck.
//
// Comprobado contra la Deck del usuario: las 7 ROMs que tenia (n64, psx, ps2,
// gc) acertaron las 7, incluidos los nombres con sufijos raros como «(EDC)» y
// «(Spain)».
//
// Lo que NO da libretro es texto: descripcion, año, genero, jugadores. Eso
// esta en ScreenScraper, que exige credenciales de desarrollador concedidas
// por ellos a cada aplicacion (comprobado contra su API: sin devid responde
// «Verifier vos identifiants developpeur»). Cuando deckman las tenga, esa
// mitad se añade aqui.
const libretroBase = "https://thumbnails.libretro.com"

// libretroSistemas traduce el nombre de carpeta de EmuDeck/ES-DE al de
// libretro, que usa el nombre largo con fabricante.
var libretroSistemas = map[string]string{
	"3do":             "The 3DO Company - 3DO",
	"amiga":           "Commodore - Amiga",
	"amstradcpc":      "Amstrad - CPC",
	"arcade":          "MAME",
	"atari2600":       "Atari - 2600",
	"atari5200":       "Atari - 5200",
	"atari7800":       "Atari - 7800",
	"atari800":        "Atari - 8-bit",
	"atarijaguar":     "Atari - Jaguar",
	"atarilynx":       "Atari - Lynx",
	"atarist":         "Atari - ST",
	"c64":             "Commodore - 64",
	"colecovision":    "Coleco - ColecoVision",
	"dreamcast":       "Sega - Dreamcast",
	"fbneo":           "FBNeo - Arcade Games",
	"fds":             "Nintendo - Family Computer Disk System",
	"gamegear":        "Sega - Game Gear",
	"gb":              "Nintendo - Game Boy",
	"gba":             "Nintendo - Game Boy Advance",
	"gbc":             "Nintendo - Game Boy Color",
	"gc":              "Nintendo - GameCube",
	"genesis":         "Sega - Mega Drive - Genesis",
	"intellivision":   "Mattel - Intellivision",
	"mame":            "MAME",
	"mastersystem":    "Sega - Master System - Mark III",
	"megacd":          "Sega - Mega-CD - Sega CD",
	"megadrive":       "Sega - Mega Drive - Genesis",
	"msx":             "Microsoft - MSX",
	"msx2":            "Microsoft - MSX2",
	"n3ds":            "Nintendo - Nintendo 3DS",
	"n64":             "Nintendo - Nintendo 64",
	"nds":             "Nintendo - Nintendo DS",
	"neogeo":          "SNK - Neo Geo",
	"nes":             "Nintendo - Nintendo Entertainment System",
	"ngp":             "SNK - Neo Geo Pocket",
	"ngpc":            "SNK - Neo Geo Pocket Color",
	"pcengine":        "NEC - PC Engine - TurboGrafx 16",
	"pcenginecd":      "NEC - PC Engine CD - TurboGrafx-CD",
	"pcfx":            "NEC - PC-FX",
	"ps2":             "Sony - PlayStation 2",
	"ps3":             "Sony - PlayStation 3",
	"psp":             "Sony - PlayStation Portable",
	"psvita":          "Sony - PlayStation Vita",
	"psx":             "Sony - PlayStation",
	"saturn":          "Sega - Saturn",
	"sega32x":         "Sega - 32X",
	"segacd":          "Sega - Mega-CD - Sega CD",
	"sg-1000":         "Sega - SG-1000",
	"snes":            "Nintendo - Super Nintendo Entertainment System",
	"supergrafx":      "NEC - PC Engine SuperGrafx",
	"tg16":            "NEC - PC Engine - TurboGrafx 16",
	"tg-cd":           "NEC - PC Engine CD - TurboGrafx-CD",
	"vectrex":         "GCE - Vectrex",
	"videopac":        "Philips - Videopac+",
	"virtualboy":      "Nintendo - Virtual Boy",
	"wii":             "Nintendo - Wii",
	"wiiu":            "Nintendo - Wii U",
	"wonderswan":      "Bandai - WonderSwan",
	"wonderswancolor": "Bandai - WonderSwan Color",
	"x68000":          "Sharp - X68000",
	"xbox":            "Microsoft - Xbox",
	"xbox360":         "Microsoft - Xbox 360",
	"zx81":            "Sinclair - ZX 81",
	"zxspectrum":      "Sinclair - ZX Spectrum",
}

// mediosLibretro empareja cada carpeta de libretro con la que ES-DE mira.
// ES-DE encuentra las imagenes SOLO por el nombre del fichero: no hace falta
// tocar el gamelist.xml, que es donde guarda playcount y lastplayed. Dejarlo
// en paz evita reescribir un XML del que ES-DE es dueño.
var mediosLibretro = []struct{ libretro, esde string }{
	{"Named_Boxarts", "covers"},
	{"Named_Titles", "titlescreens"},
	{"Named_Snaps", "screenshots"},
}

// ResultadoScrape resume que se consiguio.
type ResultadoScrape struct {
	Juegos     int      `json:"juegos"`
	ConImagen  int      `json:"conImagen"`
	Imagenes   int      `json:"imagenes"`
	Saltados   int      `json:"saltados"`
	SinImagen  []string `json:"sinImagen"`
	MediaDir   string   `json:"mediaDir"`
	SinSoporte bool     `json:"sinSoporte"`
}

var scrapeHTTP = &http.Client{Timeout: 60 * time.Second}

// scrapeConcurrencia: 4 descargas a la vez. Son PNG de unos cientos de KB y
// el cuello de botella es la latencia, no el ancho de banda.
const scrapeConcurrencia = 4

// SistemaScrapeable dice si sabemos buscar caratulas de ese sistema.
func SistemaScrapeable(system string) bool {
	_, ok := libretroSistemas[system]
	return ok
}

// mediaDirDe localiza donde guarda ES-DE las imagenes de un sistema.
func (c *Client) mediaDirDe(ctx context.Context, system string) (string, error) {
	romsDir, _ := c.romSystems(ctx)
	if romsDir == "" {
		return "", i18n.Errorf("no se encontro la carpeta de ROMs")
	}
	// EmuDeck deja <sistema>/media como enlace a tools/downloaded_media/<sistema>.
	// Seguir el enlace es mas fiable que rehacer la ruta a mano: si la
	// instalacion esta en la microSD o el usuario la movio, el enlace lo sabe
	// y nosotros no.
	if destino, err := c.sftp.ReadLink(path.Join(romsDir, system, "media")); err == nil {
		if destino = strings.TrimSpace(destino); destino != "" {
			return destino, nil
		}
	}
	return path.Join(path.Dir(romsDir), "tools", "downloaded_media", system), nil
}

// nombreBase quita la extension para buscar en libretro, que indexa por el
// nombre del juego sin ella.
func nombreBase(fichero string) string {
	return strings.TrimSuffix(fichero, path.Ext(fichero))
}

// esROM dice si el fichero parece una ROM y no un resto de descarga.
func esROM(nombre string) bool {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(nombre), "."))
	for _, e := range extROM {
		if e == ext {
			return true
		}
	}
	return false
}

// ScrapeSystem baja caratula, pantalla de titulo y captura de cada ROM de un
// sistema y las deja donde ES-DE las busca.
//
// rehacer=false salta las que ya estan: repetir el scrapeo de una coleccion
// entera para añadir dos juegos no deberia costar mil descargas.
func (c *Client) ScrapeSystem(ctx context.Context, system string, rehacer bool, report ProgressFunc) (ResultadoScrape, error) {
	var res ResultadoScrape

	carpeta, ok := libretroSistemas[system]
	if !ok {
		res.SinSoporte = true
		return res, i18n.Errorf("no se buscan caratulas de %q: libretro no tiene ese sistema", system)
	}

	roms, err := c.ListROMs(ctx, system)
	if err != nil {
		return res, err
	}

	// Un juego puede ser varios ficheros (.cue y su .bin, discos sueltos):
	// libretro indexa por el nombre sin extension, asi que se busca una vez
	// por nombre, no una por fichero.
	vistos := map[string]bool{}
	var juegos []string
	for _, r := range roms {
		if !esROM(r.Name) {
			continue
		}
		base := nombreBase(r.Name)
		if base == "" || vistos[base] {
			continue
		}
		vistos[base] = true
		juegos = append(juegos, base)
	}
	sort.Strings(juegos)
	res.Juegos = len(juegos)
	if len(juegos) == 0 {
		return res, i18n.Errorf("no hay ROMs que scrapear en %s", system)
	}

	mediaDir, err := c.mediaDirDe(ctx, system)
	if err != nil {
		return res, err
	}
	res.MediaDir = mediaDir
	for _, m := range mediosLibretro {
		if err := c.MkdirAll(path.Join(mediaDir, m.esde)); err != nil {
			return res, i18n.Errorf("no se pudo crear %s: %w", path.Join(mediaDir, m.esde), err)
		}
	}

	var (
		mu       sync.Mutex
		hechos   int
		sem      = make(chan struct{}, scrapeConcurrencia)
		wg       sync.WaitGroup
		primeraE error
	)

	for _, juego := range juegos {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(juego string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var bajadas, saltadas int
			for _, m := range mediosLibretro {
				if ctx.Err() != nil {
					return
				}
				destino := path.Join(mediaDir, m.esde, juego+".png")
				if !rehacer && c.Exists(destino) {
					saltadas++
					continue
				}
				datos, err := descargarMiniatura(ctx, carpeta, m.libretro, juego)
				if err != nil {
					mu.Lock()
					if primeraE == nil && !esNoEncontrado(err) {
						primeraE = err
					}
					mu.Unlock()
					continue
				}
				if err := c.WriteFileAtomic(destino, datos); err != nil {
					mu.Lock()
					if primeraE == nil {
						primeraE = err
					}
					mu.Unlock()
					continue
				}
				bajadas++
			}

			mu.Lock()
			hechos++
			res.Imagenes += bajadas
			res.Saltados += saltadas
			switch {
			case bajadas > 0 || saltadas > 0:
				res.ConImagen++
			default:
				res.SinImagen = append(res.SinImagen, juego)
			}
			if report != nil {
				report(Progress{
					Phase:      "buscando caratulas",
					File:       juego,
					FilesDone:  hechos,
					FilesTotal: len(juegos),
				})
			}
			mu.Unlock()
		}(juego)
	}
	wg.Wait()

	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	sort.Strings(res.SinImagen)
	// Un fallo de red suelto no invalida el resto: se avisa solo si no se
	// consiguio nada, que es cuando el usuario tiene algo que arreglar.
	if res.Imagenes == 0 && res.Saltados == 0 && primeraE != nil {
		return res, i18n.Errorf("no se pudo bajar ninguna caratula: %w", primeraE)
	}
	if report != nil {
		report(Progress{Phase: "listo", FilesDone: hechos, FilesTotal: len(juegos), Done: true})
	}
	return res, nil
}

// errNoEncontrado: libretro no tiene ese juego. No es un fallo que arreglar,
// es lo normal en volcados con nombre propio.
var errNoEncontrado = i18n.Errorf("sin caratula en libretro")

func esNoEncontrado(err error) bool { return strings.Contains(err.Error(), errNoEncontrado.Error()) }

func descargarMiniatura(ctx context.Context, sistema, tipo, juego string) ([]byte, error) {
	u := fmt.Sprintf("%s/%s/%s/%s.png",
		libretroBase, url.PathEscape(sistema), tipo, url.PathEscape(juego))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "deckman")
	resp, err := scrapeHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, i18n.Errorf("%s: %w", juego, errNoEncontrado)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, i18n.Errorf("libretro respondio %s al pedir %s", resp.Status, juego)
	}
	// 16 MiB de tope: una caratula son cientos de KB, y asi una respuesta
	// inesperada no se come la memoria.
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}
