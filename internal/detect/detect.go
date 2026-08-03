// Package detect deduce que juego hay en una carpeta del PC: su identificador
// de Steam, un nombre presentable y cual de los ejecutables es el que arranca
// el juego. Todo con lo que hay en el disco, sin tocar la red.
package detect

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Result es lo que se ha podido averiguar de una carpeta.
type Result struct {
	Dir         string      `json:"dir"`
	CleanName   string      `json:"cleanName"`   // nombre de la carpeta, ya limpio
	SteamAppID  string      `json:"steamAppId"`  // appid encontrado dentro, si lo hay
	AppIDSource string      `json:"appIdSource"` // fichero del que salio
	Executables []Candidate `json:"executables"` // ordenados: el primero es la apuesta
	Skipped     []string    `json:"skipped"`     // ejecutables descartados, para poder explicarlo
}

// Candidate es un ejecutable con su puntuacion.
type Candidate struct {
	Rel   string `json:"rel"` // ruta relativa a la carpeta del juego
	Size  int64  `json:"size"`
	Score int    `json:"score"`
	Why   string `json:"why"`
}

// Detect examina la carpeta. Nunca falla del todo: si no encuentra nada,
// devuelve lo que haya podido deducir.
func Detect(dir string) *Result {
	r := &Result{Dir: dir, CleanName: CleanName(filepath.Base(filepath.Clean(dir)))}
	if id, src := FindEmbeddedAppID(dir); id != "" {
		r.SteamAppID, r.AppIDSource = id, src
	}
	r.Executables, r.Skipped = PickExecutable(dir, r.CleanName)
	return r
}

// --- nombre ---

// Etiquetas de grupos de release y de repack que no forman parte del titulo.
var tagWords = map[string]bool{
	"repack": true, "fitgirl": true, "dodi": true, "codex": true, "plaza": true,
	"skidrow": true, "reloaded": true, "empress": true, "rune": true, "tenoke": true,
	"elamigos": true, "gog": true, "razor1911": true, "hoodlum": true, "flt": true,
	"prophet": true, "steampunks": true, "cpy": true, "goldberg": true, "online": true,
	"multi": true, "proper": true, "crack": true, "cracked": true, "portable": true,
	"repacked": true, "nosteam": true, "unlocked": true, "denuvoless": true,
}

var (
	reVersionTag    = regexp.MustCompile(`(?i)^v\d+([._]\d+)*[a-z]?$`)
	reDottedVersion = regexp.MustCompile(`^\d+[._]\d+([._]\d+)*$`)
	reBuild         = regexp.MustCompile(`(?i)^(build|update|hotfix|patch|rev)$`)
	reMultiN        = regexp.MustCompile(`(?i)^multi\d+$`)
	reBrackets      = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
	reSpaces        = regexp.MustCompile(`\s+`)
)

// CleanName convierte el nombre de una carpeta en algo presentable y
// buscable: "Resident.Evil.4.REPACK-FitGirl" -> "Resident Evil 4".
func CleanName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	// Fuera lo que va entre corchetes o parentesis: casi siempre son etiquetas.
	s = reBrackets.ReplaceAllString(s, " ")

	// Los puntos y guiones bajos solo son separadores en nombres de scene, que
	// no llevan espacios. Si ya hay espacios, respetamos el nombre tal cual
	// para no destrozar titulos como "S.T.A.L.K.E.R." o "Mr. Robot".
	if !strings.Contains(s, " ") {
		s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	}
	s = strings.ReplaceAll(s, "+", " ")

	// El guion suele separar el titulo del grupo: "Juego-CODEX".
	words := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '-' })

	// En cuanto aparece una marca de version, de build o de grupo, lo que viene
	// detras ya no es el titulo. Cortamos ahi en vez de ir filtrando palabra a
	// palabra: partir "v1.12.3" por los puntos deja sueltos un "12" y un "3"
	// que un filtro por palabras no sabe distinguir de "Portal 2".
	var out []string
	for _, w := range words {
		lw := strings.ToLower(strings.Trim(w, ".,;"))
		if lw == "" {
			continue
		}
		if isStopWord(lw) {
			break
		}
		out = append(out, w)
	}

	// Si la primera palabra ya era una marca, nos quedamos sin nombre: mejor
	// devolver el original limpio de separadores que una cadena vacia.
	if len(out) == 0 {
		out = words
	}

	res := reSpaces.ReplaceAllString(strings.Join(out, " "), " ")
	return strings.TrimSpace(res)
}

// isStopWord indica si una palabra marca el final del titulo.
//
// Los numeros pelados NO cuentan: "Portal 2" y "Tomb Raider 2013" tienen que
// sobrevivir enteros.
func isStopWord(lw string) bool {
	switch {
	case tagWords[lw]:
		return true
	case reMultiN.MatchString(lw), reBuild.MatchString(lw):
		return true
	case reVersionTag.MatchString(lw): // v1, v4.04, v1.12.3b
		return true
	case reDottedVersion.MatchString(lw): // 1.12.3, 4_04
		return true
	}
	return false
}

// --- appid embebido ---

// Ficheros que los repacks y los emuladores de Steam dejan con el appid real.
var appIDFiles = []string{
	"steam_appid.txt", "steam_api.ini", "steam_api64.ini", "steam_emu.ini",
	"ColdClientLoader.ini", "SteamConfig.ini", "hlm.ini", "steamclient_loader.ini",
}

var reAppIDLine = regexp.MustCompile(`(?i)^\s*app_?id\s*=\s*(\d+)`)
var reDigits = regexp.MustCompile(`^\d{2,8}$`)

// FindEmbeddedAppID busca el identificador de Steam dentro de la carpeta.
// Devuelve el appid y el fichero del que salio.
//
// Es la via mas fiable con diferencia: no depende de acertar con el nombre.
func FindEmbeddedAppID(dir string) (appID, source string) {
	// Solo la raiz y un nivel por debajo: mas hondo suele ser de un DLC o de
	// una herramienta incluida, y daria un appid equivocado.
	roots := []string{dir}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() && !isNoiseDir(e.Name()) {
				roots = append(roots, filepath.Join(dir, e.Name()))
			}
		}
	}

	for _, root := range roots {
		for _, name := range appIDFiles {
			p := filepath.Join(root, name)
			f, err := os.Open(p)
			if err != nil {
				continue
			}
			id := scanAppID(f, strings.EqualFold(name, "steam_appid.txt"))
			f.Close()
			if id != "" {
				rel, err := filepath.Rel(dir, p)
				if err != nil {
					rel = name
				}
				return id, filepath.ToSlash(rel)
			}
		}
	}
	return "", ""
}

// scanAppID lee el appid. steam_appid.txt es un fichero con el numero pelado;
// los .ini llevan una linea "AppId=...".
func scanAppID(f *os.File, plain bool) string {
	sc := bufio.NewScanner(f)
	lines := 0
	for sc.Scan() && lines < 200 {
		lines++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if plain {
			if reDigits.MatchString(line) {
				return line
			}
			continue
		}
		if m := reAppIDLine.FindStringSubmatch(line); m != nil {
			if m[1] != "0" {
				return m[1]
			}
		}
	}
	return ""
}

// --- ejecutable ---

// Ejecutables que nunca son el juego. Comprobado contra repacks reales: en
// Resident Evil 4 el segundo .exe mas grande es CrashReport.exe, con 151 MB,
// asi que quedarse con el mayor sin filtrar da la respuesta equivocada.
//
// Se comparan por PALABRA, no como subcadena suelta: ver isDeniedExe.
var exeDenyContains = []string{
	"unins", "uninstall", "setup", "install",
	"crashreport", "crashhandler", "crashpad", "unitycrashhandler", "crashsender",
	"vc_redist", "vcredist", "dxwebsetup", "dxsetup", "directx", "oalinst",
	"prereqsetup", "ue4prereq", "ue5prereq", "physx", "dotnet", "quicksfv",
	"trainer", "cheat", "activation", "keygen", "patch", "update",
	"language selector", "languageselector", "config", "settings", "benchmark",
	"editor", "server", "dedicated", "notification_helper", "eossetup",
	"anticheat", "easyanticheat", "battleye", "touchup", "helper",
}

// Carpetas cuyo contenido no es el juego.
var noiseDirs = []string{
	"_redist", "redist", "_commonredist", "commonredist", "directx", "vcredist",
	"uninstall", "_bonus content", "bonus", "extras", "soundtrack", "ost",
	"docs", "manual", "_enable cheats", "dotnet", "_cheats",
}

// isDeniedExe decide si el nombre de un ejecutable esta en la lista negra.
//
// La comparacion es por principio de palabra, no por subcadena: buscar "server"
// dentro del nombre entero descartaba en silencio "Observer.exe", y "patch"
// se llevaba por delante "Dispatch.exe". Juegos reales, los dos.
//
// El nombre se parte por todo lo que no sea letra o numero, y una entrada de la
// lista casa si aparece al PRINCIPIO de una palabra (o abarcando varias
// seguidas). Asi siguen cayendo "unins000.exe" (empieza por "unins"),
// "UnityCrashHandler64.exe", "vc_redist.x64.exe" o "Language Selector.exe",
// pero no lo que solo termina igual.
func isDeniedExe(stem string) bool {
	name := " " + strings.Join(splitWords(stem), " ")
	for _, bad := range exeDenyContains {
		if strings.Contains(name, " "+strings.Join(splitWords(bad), " ")) {
			return true
		}
	}
	return false
}

// splitWords parte por cualquier cosa que no sea letra o numero y pasa a
// minusculas.
func splitWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

func isNoiseDir(name string) bool {
	l := strings.ToLower(name)
	for _, d := range noiseDirs {
		if l == d {
			return true
		}
	}
	return false
}

// Carpetas donde suele vivir el ejecutable bueno.
var goodDirHints = []string{"bin", "binaries", "win64", "win32", "x64", "game"}

// PickExecutable ordena los .exe de la carpeta de mas a menos probable.
// Devuelve tambien los descartados, para poder justificar la eleccion.
func PickExecutable(dir, gameName string) (picked []Candidate, skipped []string) {
	base := filepath.Clean(dir)
	nameKey := normalizeKey(gameName)

	filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && isNoiseDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".exe") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		lower := strings.ToLower(d.Name())
		if isDeniedExe(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))) {
			skipped = append(skipped, rel)
			return nil
		}

		c := Candidate{Rel: rel, Size: fi.Size()}
		var why []string

		// El tamano es la senal mas fuerte: el ejecutable del juego suele ser
		// de decenas o cientos de MB, y los accesorios de unos pocos.
		switch {
		case fi.Size() > 100<<20:
			c.Score += 50
			why = append(why, "muy grande")
		case fi.Size() > 20<<20:
			c.Score += 35
			why = append(why, "grande")
		case fi.Size() > 2<<20:
			c.Score += 15
		case fi.Size() < 200<<10:
			c.Score -= 15
			why = append(why, "muy pequeno")
		}

		// Parecido con el nombre del juego.
		stem := normalizeKey(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())))
		if stem != "" && nameKey != "" {
			if stem == nameKey {
				c.Score += 45
				why = append(why, "coincide con el nombre")
			} else if strings.Contains(nameKey, stem) || strings.Contains(stem, nameKey) {
				c.Score += 30
				why = append(why, "parecido al nombre")
			}
		}

		// Profundidad: la raiz manda, y bin/ o Binaries/Win64 son sitios tipicos.
		depth := strings.Count(rel, "/")
		switch depth {
		case 0:
			c.Score += 20
			why = append(why, "en la raiz")
		default:
			c.Score -= depth * 4
			for _, hint := range goodDirHints {
				if strings.Contains(strings.ToLower(rel), hint+"/") {
					c.Score += 12
					why = append(why, "en carpeta de binarios")
					break
				}
			}
		}

		if strings.Contains(lower, "launcher") {
			c.Score -= 10 // a veces es el arranque bueno, pero casi nunca
			why = append(why, "parece un lanzador")
		}

		c.Why = strings.Join(why, ", ")
		picked = append(picked, c)
		return nil
	})

	sort.SliceStable(picked, func(i, j int) bool {
		if picked[i].Score != picked[j].Score {
			return picked[i].Score > picked[j].Score
		}
		return picked[i].Size > picked[j].Size
	})
	sort.Strings(skipped)
	return picked, skipped
}

var reNonAlnum = regexp.MustCompile(`[^a-z0-9]`)

// normalizeKey deja solo letras y numeros en minuscula, para comparar
// "DevilMayCry5.exe" con "Devil May Cry 5".
func normalizeKey(s string) string {
	return reNonAlnum.ReplaceAllString(strings.ToLower(s), "")
}
