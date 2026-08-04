package deck

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SistemaROM es una carpeta de sistema con cuantas ROMs tiene dentro.
type SistemaROM struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// extROM: extensiones que cuentan como ROM o imagen de disco.
//
// Sirve para decidir que carpetas de EmuDeck tienen juegos de verdad, y por
// que hace falta: EmuDeck crea 181 carpetas de sistema al instalarse (contadas
// en una Deck real) y solo cuatro tenian ROMs. Un desplegable con 181 entradas
// para elegir entre cuatro no es una lista, es un obstaculo.
//
// Filtrar por extension resuelve de paso tres falsos positivos que una simple
// cuenta de ficheros daba por buenos, sin necesidad de lista negra:
// «emulators» (guiones .sh de EmuDeck), «model2» y «xbox360» (los ficheros del
// propio emulador: .exe, .toml, .lua).
var extROM = []string{
	// empaquetados: casi cualquier sistema los admite
	"zip", "7z", "rar",
	// imagenes de disco
	"iso", "chd", "cue", "bin", "img", "mdf", "nrg", "ccd", "cso", "cdi", "gdi",
	"rvz", "wbfs", "wia", "gcm", "gcz", "wud", "wux", "nsp", "xci", "pbp", "m3u",
	// cartuchos
	"sfc", "smc", "fig", "nes", "fds", "unf", "gba", "gb", "gbc", "gg", "sms",
	"md", "gen", "smd", "32x", "sg", "n64", "z64", "v64", "ndd", "nds", "dsi",
	"3ds", "cia", "pce", "sgx", "a26", "a52", "a78", "j64", "jag", "lnx", "ws",
	"wsc", "col", "int", "vec", "vb", "min", "vpk",
	// ordenadores de 8 y 16 bits
	"d64", "t64", "tap", "tzx", "dsk", "adf", "st", "z80", "cdt", "prg", "cas",
}

// ROMSystems devuelve la carpeta de ROMs y los sistemas que tienen juegos,
// con la cuenta de cada uno.
//
// Todo en una sola orden remota. La leccion de la 0.2.4 vale igual aqui: un
// viaje SFTP por cada una de las 181 carpetas es medio minuto por wifi.
func (c *Client) ROMSystems(ctx context.Context) (string, []SistemaROM, error) {
	romsDir, _ := c.romSystems(ctx)
	if romsDir == "" {
		return "", nil, fmt.Errorf("no se encontro la carpeta de ROMs: instala EmuDeck en la Deck o crea ~/Emulation/roms")
	}

	// Los enlaces simbolicos se saltan porque EmuDeck crea alias: «gamecube»
	// apunta a «gc» y son el mismo sitio (comprobado: mismo inodo). Sin esto,
	// la misma carpeta sale dos veces con nombres distintos.
	// Se cuentan juegos, no ficheros: un juego de PlayStation son un .cue y un
	// .bin, y decir «4 ROMs» donde hay dos juegos confunde. Quitar la
	// extension y contar nombres distintos da la misma cifra que luego
	// reporta el scraper.
	//
	// El `exit 0` del final no sobra: la ultima orden del bucle es un
	// `[ ... ] && printf`, que devuelve 1 cuando el ultimo sistema no tiene
	// ROMs — y esa se convierte en la salida del script entero. Sin esto, la
	// lista llegaba completa pero el cliente la tiraba por «status 1».
	cmd := fmt.Sprintf(`cd %s 2>/dev/null || exit 0
for d in */; do
  d=${d%%/}
  [ -L "$d" ] && continue
  n=$(find "$d/" -maxdepth 1 -type f -printf '%%f\n' 2>/dev/null \
      | grep -iE '\.(%s)$' | sed 's/\.[^.]*$//' | sort -u | wc -l) || n=0
  [ "$n" -gt 0 ] && printf '%%s\t%%s\n' "$n" "$d"
done
exit 0`, ShellQuote(romsDir), strings.Join(extROM, "|"))

	out, err := c.Run(ctx, cmd)
	if err != nil {
		return "", nil, fmt.Errorf("no se pudieron contar las ROMs de %s: %w", romsDir, err)
	}

	return romsDir, parseSistemas(out), nil
}

// parseSistemas lee la salida «cuenta<TAB>sistema» del script. Aparte para
// poder probarla: lo que se cuela por aqui es lo que decide que ve el usuario.
func parseSistemas(out string) []SistemaROM {
	var sistemas []SistemaROM
	for _, linea := range strings.Split(out, "\n") {
		campos := strings.SplitN(strings.TrimRight(linea, "\r\n"), "\t", 2)
		if len(campos) != 2 {
			continue
		}
		nombre := strings.TrimSpace(campos[1])
		n, err := strconv.Atoi(strings.TrimSpace(campos[0]))
		if err != nil || n <= 0 || nombre == "" {
			continue
		}
		sistemas = append(sistemas, SistemaROM{Name: nombre, Count: n})
	}
	sort.Slice(sistemas, func(i, j int) bool { return sistemas[i].Name < sistemas[j].Name })
	return sistemas
}
