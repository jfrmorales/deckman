package i18n

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// verbos saca los verbos de formato de una plantilla, ignorando los %% que son
// un porcentaje literal.
var reVerbo = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`)

func verbos(f string) []string {
	sinDobles := strings.ReplaceAll(f, "%%", "")
	v := reVerbo.FindAllString(sinDobles, -1)
	// %w y %v se pintan igual y se usan indistintamente al traducir: lo que
	// importa es que haya el mismo numero de huecos y en el mismo orden.
	for i, x := range v {
		if strings.HasSuffix(x, "w") {
			v[i] = x[:len(x)-1] + "v"
		}
	}
	return v
}

// Un verbo de menos no revienta: Sprintf pinta «%!(EXTRA string=...)» en la
// cara del usuario, y solo en el idioma traducido. Por eso se comprueba aqui.
func TestCatalogoVerbosCuadran(t *testing.T) {
	for lang, cat := range catalogo {
		for original, traducido := range cat {
			vo, vt := verbos(original), verbos(traducido)
			if strings.Join(vo, ",") != strings.Join(vt, ",") {
				t.Errorf("[%s] los verbos no cuadran\n  es: %q -> %v\n  %s: %q -> %v",
					lang, original, vo, lang, traducido, vt)
			}
		}
	}
}

func TestCatalogoSinTraduccionesVacias(t *testing.T) {
	for lang, cat := range catalogo {
		for original, traducido := range cat {
			if strings.TrimSpace(traducido) == "" {
				t.Errorf("[%s] traduccion vacia para %q", lang, original)
			}
			if traducido == original && !soloEstructura(original) {
				t.Errorf("[%s] traduccion identica al original: %q", lang, original)
			}
		}
	}
}

// soloEstructura: plantillas sin palabras, que no hay que traducir.
func soloEstructura(f string) bool {
	return strings.TrimSpace(reVerbo.ReplaceAllString(f, "")) == "" ||
		strings.TrimSpace(reVerbo.ReplaceAllString(f, "")) == ":"
}

// Los dos idiomas tienen que cubrir lo mismo: si se añade una entrada al
// ingles y se olvida el frances, el usuario frances ve castellano sin que
// nadie se entere.
func TestCatalogoCubreLosMismosTextos(t *testing.T) {
	en, fr := catalogo[EN], catalogo[FR]
	for k := range en {
		if _, ok := fr[k]; !ok {
			t.Errorf("falta la traduccion al frances de %q", k)
		}
	}
	for k := range fr {
		if _, ok := en[k]; !ok {
			t.Errorf("falta la traduccion al ingles de %q", k)
		}
	}
}

// El catalogo tiene que seguir al codigo: una clave que ya no existe en
// ningun fmt.Errorf es texto muerto que nadie va a volver a mirar.
func TestCatalogoSinClavesHuerfanas(t *testing.T) {
	fuentes := leerFuentes(t)
	for lang, cat := range catalogo {
		for original := range cat {
			if !strings.Contains(fuentes, strconv.Quote(original)) {
				t.Errorf("[%s] la clave ya no esta en el codigo: %q", lang, original)
			}
		}
	}
}

// leerFuentes junta el codigo del proyecto (sin pruebas) para buscar en el.
func leerFuentes(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, raiz := range []string{"..", "../../cmd"} {
		err := filepath.Walk(raiz, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".go") ||
				strings.HasSuffix(p, "_test.go") || strings.Contains(p, "i18n/catalogo.go") {
				return nil
			}
			datos, err := os.ReadFile(p)
			if err == nil {
				b.Write(datos)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("recorriendo %s: %v", raiz, err)
		}
	}
	return b.String()
}

func TestTraducir(t *testing.T) {
	err := Errorf("no se pudo borrar %q de %s: %w", "Sonic.zip", "snes", errors.New("permission denied"))

	// El castellano es el original y sale tal cual.
	if got, want := err.Error(), `no se pudo borrar "Sonic.zip" de snes: permission denied`; got != want {
		t.Errorf("Error() = %q, se esperaba %q", got, want)
	}
	if got, want := Traducir(err, EN), `"Sonic.zip" could not be deleted from snes: permission denied`; got != want {
		t.Errorf("EN = %q, se esperaba %q", got, want)
	}
	if got, want := Traducir(err, FR), `"Sonic.zip" n'a pas pu etre supprime de snes : permission denied`; got != want {
		t.Errorf("FR = %q, se esperaba %q", got, want)
	}
	// Un idioma que no hablamos no puede dejar la interfaz en blanco.
	if got := Traducir(err, "de"); got != err.Error() {
		t.Errorf("idioma desconocido = %q, se esperaba el castellano", got)
	}
}

// Envolver con %w no puede romper errors.Is: ese fallo es silencioso.
func TestErroresEnvueltosSiguenSiendoComparables(t *testing.T) {
	raiz := errors.New("la raiz")
	err := Errorf("no se pudo leer %s: %w", "config.vdf", raiz)
	if !errors.Is(err, raiz) {
		t.Error("errors.Is no atraviesa el Mensaje")
	}
	// Y el error de dentro tambien se traduce, para no mezclar idiomas.
	dentro := Errorf("no hay conexion con la Steam Deck")
	fuera := Errorf("no se pudo leer %s: %w", "shortcuts.vdf", dentro)
	if got, want := Traducir(fuera, EN), "shortcuts.vdf could not be read: there is no connection to the Steam Deck"; got != want {
		t.Errorf("anidado EN = %q, se esperaba %q", got, want)
	}
}

func TestTraducirNoTraducibles(t *testing.T) {
	if got := Traducir(nil, EN); got != "" {
		t.Errorf("Traducir(nil) = %q", got)
	}
	// Un error ajeno (SSH, sistema de ficheros) sale como viene.
	ajeno := fmt.Errorf("connection refused")
	if got := Traducir(ajeno, FR); got != "connection refused" {
		t.Errorf("error ajeno = %q", got)
	}
}

func TestNegociar(t *testing.T) {
	casos := map[string]string{
		"es-ES,es;q=0.9,en;q=0.8":    ES,
		"en-GB,en;q=0.9":             EN,
		"fr-FR,fr;q=0.9,en-US;q=0.8": FR,
		"de-DE,de;q=0.9,en;q=0.5":    EN, // aleman no, pero ingles si
		"de,ja":                      "", // ninguno: decide el llamante
		"":                           "",
		"  EN-us  ":                  EN, // mayusculas y espacios
	}
	for cabecera, want := range casos {
		if got := Negociar(cabecera); got != want {
			t.Errorf("Negociar(%q) = %q, se esperaba %q", cabecera, got, want)
		}
	}
}

func TestSoportado(t *testing.T) {
	for _, l := range []string{ES, EN, FR} {
		if !Soportado(l) {
			t.Errorf("Soportado(%q) = false", l)
		}
	}
	for _, l := range []string{"", "de", "ES", "es-ES"} {
		if Soportado(l) {
			t.Errorf("Soportado(%q) = true", l)
		}
	}
}
