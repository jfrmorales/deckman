package deck

import "testing"

// Los nombres no son decorativos: Steam busca exactamente estos sufijos, y con
// cualquier otro la portada simplemente no aparece en la biblioteca.
func TestArtFileName(t *testing.T) {
	const appID = 3465116546
	cases := map[string]string{
		"grid":  "3465116546p.png",
		"gridh": "3465116546.png",
		"hero":  "3465116546_hero.png",
		"logo":  "3465116546_logo.png",
		"icon":  "3465116546_icon.png",
	}
	for kind, want := range cases {
		got, err := ArtFileName(appID, kind, ".png")
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if got != want {
			t.Errorf("%s: %q, se esperaba %q", kind, got, want)
		}
	}

	if got, _ := ArtFileName(appID, "hero", ".jpg"); got != "3465116546_hero.jpg" {
		t.Errorf("con .jpg salio %q", got)
	}
	// Sin punto y sin extension debe apanarselo igual.
	if got, _ := ArtFileName(appID, "grid", "png"); got != "3465116546p.png" {
		t.Errorf("sin punto salio %q", got)
	}
	if got, _ := ArtFileName(appID, "grid", ""); got != "3465116546p.png" {
		t.Errorf("sin extension salio %q", got)
	}
	if _, err := ArtFileName(appID, "inventado", ".png"); err == nil {
		t.Error("un tipo desconocido deberia dar error")
	}
}

// La portada vertical y la horizontal solo se distinguen por una "p", asi que
// hay que asegurarse de que no se confunden nunca.
func TestArtSuffixesUnambiguous(t *testing.T) {
	seen := map[string]string{}
	for kind, suffix := range artSuffix {
		if other, dup := seen[suffix]; dup {
			t.Errorf("%s y %s comparten el sufijo %q", kind, other, suffix)
		}
		seen[suffix] = kind
	}
	if len(ArtKinds()) != len(artSuffix) {
		t.Errorf("ArtKinds() lista %d tipos y hay %d definidos", len(ArtKinds()), len(artSuffix))
	}
	for _, k := range ArtKinds() {
		if _, ok := artSuffix[k]; !ok {
			t.Errorf("ArtKinds() incluye %q, que no tiene sufijo", k)
		}
	}
}

// Steam decide si un fichero es arte por la EXTENSION, no por su contenido.
// Comprobado en una Deck real: un webp animado guardado como .webp no aparece,
// y el MISMO fichero renombrado a .png si. De 175 ficheros de arte de esa
// Deck, ninguno escrito por Steam o por el plugin de Decky era .webp.
func TestArtFileNameNormalizesExtension(t *testing.T) {
	const appID = 3454118403
	cases := map[string]string{
		".png":   "3454118403_hero.png",
		".jpg":   "3454118403_hero.jpg",
		".jpeg":  "3454118403_hero.jpg",
		".ico":   "3454118403_hero.ico",
		".webp":  "3454118403_hero.png", // el caso que fallaba
		".apng":  "3454118403_hero.png",
		"":       "3454118403_hero.png",
		"png":    "3454118403_hero.png",
		".WEBP":  "3454118403_hero.png",
		".rarod": "3454118403_hero.png",
	}
	for ext, want := range cases {
		got, err := ArtFileName(appID, "hero", ext)
		if err != nil {
			t.Fatalf("%q: %v", ext, err)
		}
		if got != want {
			t.Errorf("ArtFileName(%q) = %q, se esperaba %q", ext, got, want)
		}
	}
}

// La carpeta grid no solo tiene imagenes: el plugin de Decky deja ahi sus
// "<appid>.json". Si los tomasemos por arte, cambiar la portada horizontal
// borraria los metadatos de otra herramienta.
func TestIsArtExt(t *testing.T) {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".ico", ".webp", ".PNG"} {
		if !isArtExt(ext) {
			t.Errorf("%q deberia contar como imagen", ext)
		}
	}
	for _, ext := range []string{".json", ".txt", ".vdf", "", ".db"} {
		if isArtExt(ext) {
			t.Errorf("%q NO deberia contar como imagen", ext)
		}
	}
}
