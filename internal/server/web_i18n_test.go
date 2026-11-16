package server

import (
	"html"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Pruebas del catalogo de la interfaz (web/i18n-catalogo.js).
//
// El de Go (internal/i18n) ya tenia las suyas; este no, y asi llegaron a
// juntarse medio centenar de frases que JavaScript pintaba sin pasar por t():
// en ingles y en frances salian en castellano y nada lo avisaba. No se
// interpreta JavaScript, se leen los literales con expresiones regulares, que
// basta porque el catalogo y las llamadas a t() se escriben siempre igual.

const reLiteral = `'(?:[^'\\]|\\.)*'`

// sinComillas devuelve el texto de un literal de JavaScript entre comillas
// simples, con sus escapes resueltos.
func sinComillas(lit string) string {
	var b strings.Builder
	s := lit[1 : len(lit)-1]
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			if s[i] == 'n' {
				b.WriteByte('\n')
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func leerWeb(t *testing.T, nombre string) string {
	t.Helper()
	data, err := webFS.ReadFile("web/" + nombre)
	if err != nil {
		t.Fatalf("no se pudo leer web/%s: %v", nombre, err)
	}
	return string(data)
}

// catalogoWeb devuelve, por idioma, clave -> traduccion.
func catalogoWeb(t *testing.T) map[string]map[string]string {
	t.Helper()
	src := leerWeb(t, "i18n-catalogo.js")
	reEntrada := regexp.MustCompile(`(?m)^\s*(` + reLiteral + `)\s*:\s*(` + reLiteral + `)\s*,`)
	out := map[string]map[string]string{}
	for _, lang := range []string{"en", "fr"} {
		ini := strings.Index(src, "\n  "+lang+": {")
		if ini < 0 {
			t.Fatalf("el catalogo no tiene bloque %q", lang)
		}
		fin := strings.Index(src[ini:], "\n  },")
		if fin < 0 {
			t.Fatalf("el bloque %q del catalogo no se cierra", lang)
		}
		cat := map[string]string{}
		for _, m := range reEntrada.FindAllStringSubmatch(src[ini:ini+fin], -1) {
			clave := sinComillas(m[1])
			if _, repetida := cat[clave]; repetida {
				t.Errorf("[%s] clave repetida en el catalogo: %q", lang, clave)
			}
			cat[clave] = sinComillas(m[2])
		}
		if len(cat) == 0 {
			t.Fatalf("no se ha leido ninguna entrada de %q: ¿ha cambiado el formato del catalogo?", lang)
		}
		out[lang] = cat
	}
	return out
}

// fuentesJS junta el JavaScript de la aplicacion, sin el traductor ni el
// catalogo.
func fuentesJS(t *testing.T) string {
	t.Helper()
	nombres, err := fs.Glob(webFS, "web/*.js")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, n := range nombres {
		base := strings.TrimPrefix(n, "web/")
		if strings.HasPrefix(base, "i18n") {
			continue
		}
		b.WriteString(leerWeb(t, base))
		b.WriteByte('\n')
	}
	return b.String()
}

// textosDeT saca los literales que el JavaScript pasa a t(): directos
// (`t('...')`), los dos brazos de un ternario (`t(x ? '...' : '...')`) y las
// etiquetas de ART_KINDS, que se traducen al pintarlas.
func textosDeT(js string) map[string]bool {
	usados := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bt\(\s*(`+reLiteral+`)`).FindAllStringSubmatch(js, -1) {
		usados[sinComillas(m[1])] = true
	}
	reTernario := regexp.MustCompile(`\bt\(\s*[^'"` + "`" + `()]*\?\s*(` + reLiteral + `)\s*:\s*(` + reLiteral + `)`)
	for _, m := range reTernario.FindAllStringSubmatch(js, -1) {
		usados[sinComillas(m[1])] = true
		usados[sinComillas(m[2])] = true
	}
	if ini := strings.Index(js, "const ART_KINDS = ["); ini >= 0 {
		fin := strings.Index(js[ini:], "];")
		for _, m := range regexp.MustCompile(`label: (`+reLiteral+`)`).FindAllStringSubmatch(js[ini:ini+fin], -1) {
			usados[sinComillas(m[1])] = true
		}
	}
	return usados
}

// textosDelHTML saca lo que el traductor encontrara al recorrer el DOM: cada
// nodo de texto y los atributos que traduce (ver ATTRS en i18n.js).
func textosDelHTML(doc string) map[string]bool {
	doc = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(doc, "")
	textos := map[string]bool{}
	espacios := regexp.MustCompile(`\s+`)
	for _, m := range regexp.MustCompile(`>([^<]+)<`).FindAllStringSubmatch(doc, -1) {
		if x := strings.TrimSpace(html.UnescapeString(m[1])); x != "" {
			textos[espacios.ReplaceAllString(x, " ")] = true
		}
	}
	for _, m := range regexp.MustCompile(`(?:placeholder|title|aria-label)="([^"]*)"`).FindAllStringSubmatch(doc, -1) {
		textos[html.UnescapeString(m[1])] = true
	}
	return textos
}

func ordenadas(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Ingles y frances tienen que cubrir exactamente lo mismo: una frase traducida
// solo a uno es un olvido.
func TestCatalogoWebMismaCobertura(t *testing.T) {
	cat := catalogoWeb(t)
	for clave := range cat["en"] {
		if _, ok := cat["fr"][clave]; !ok {
			t.Errorf("falta en frances: %q", clave)
		}
	}
	for clave := range cat["fr"] {
		if _, ok := cat["en"][clave]; !ok {
			t.Errorf("falta en ingles: %q", clave)
		}
	}
}

// Todo lo que el JavaScript pasa a t() tiene que estar en el catalogo. Si
// falta, sale en castellano: no rompe nada, y por eso mismo no se nota.
func TestCatalogoWebCubreElJavaScript(t *testing.T) {
	cat := catalogoWeb(t)
	usados := textosDeT(fuentesJS(t))
	if len(usados) < 100 {
		t.Fatalf("solo se han encontrado %d llamadas a t(): ¿ha cambiado la forma de llamarla?", len(usados))
	}
	for _, clave := range ordenadas(usados) {
		for _, lang := range []string{"en", "fr"} {
			if _, ok := cat[lang][clave]; !ok {
				t.Errorf("[%s] t(%q) no esta en web/i18n-catalogo.js", lang, clave)
			}
		}
	}
}

// Los huecos {0}, {1}... de la traduccion tienen que ser los del original. Uno
// de menos se come un dato; uno de mas deja «{2}» escrito en pantalla.
func TestCatalogoWebHuecosCuadran(t *testing.T) {
	reHueco := regexp.MustCompile(`\{\d+\}`)
	huecos := func(s string) string {
		h := reHueco.FindAllString(s, -1)
		sort.Strings(h)
		return strings.Join(h, ",")
	}
	for lang, cat := range catalogoWeb(t) {
		for clave, traduccion := range cat {
			if huecos(clave) != huecos(traduccion) {
				t.Errorf("[%s] los huecos no cuadran\n  es: %q\n  %s: %q", lang, clave, lang, traduccion)
			}
		}
	}
}

// textosSinTraducir son los textos del HTML que se dejan en castellano a
// proposito: nombres propios, ordenes y ejemplos, que son iguales en los tres
// idiomas.
var textosSinTraducir = map[string]bool{
	"Auto": true, "English": true, "Español": true, "Français": true,
	"Idioma / Language / Langue": true,
	"API":                        true, "SSH": true,
	"Konsole": true, "Proton": true, "Steam": true, "SteamGridDB": true,
	"deck": true, "deckman": true, "steamgriddb.com": true, "~/Games": true,
	"hostname -I": true, "passwd": true, "sudo systemctl enable --now sshd": true,
	"https://ejemplo.com/juego.zip": true,
}

// Todo texto del HTML tiene que estar en el catalogo o en la lista de arriba.
// El traductor busca cada nodo por su texto con los espacios juntados, asi que
// aqui se comprueba igual.
func TestCatalogoWebCubreElHTML(t *testing.T) {
	cat := catalogoWeb(t)
	conLetras := regexp.MustCompile(`\pL{3}`)
	for _, texto := range ordenadas(textosDelHTML(leerWeb(t, "index.html"))) {
		if textosSinTraducir[texto] || !conLetras.MatchString(texto) {
			continue
		}
		if _, ok := cat["en"][texto]; !ok {
			t.Errorf("texto de index.html sin traducir: %q", texto)
		}
	}
}

// clavesDelServidor son textos que no estan escritos en la interfaz porque
// llegan del servidor como dato y se traducen al pintarlos. Cada una con su
// motivo: una clave aqui sin explicar es una huerfana con permiso.
var clavesDelServidor = map[string]string{
	"Interno": "etiqueta de la biblioteca interna, viene en el inventario (lib.label)",
}

// Una clave que ya no aparece ni en el HTML ni en el JavaScript es una frase
// que se quito de la interfaz y se quedo en el catalogo. No hace dano, pero
// se acumulan y luego nadie sabe cuales siguen vivas.
func TestCatalogoWebSinHuerfanas(t *testing.T) {
	cat := catalogoWeb(t)
	usados := textosDeT(fuentesJS(t))
	enHTML := textosDelHTML(leerWeb(t, "index.html"))
	for clave := range cat["en"] {
		if usados[clave] || enHTML[clave] {
			continue
		}
		if _, ok := clavesDelServidor[clave]; ok {
			continue
		}
		t.Errorf("clave huerfana en web/i18n-catalogo.js: %q", clave)
	}
}
