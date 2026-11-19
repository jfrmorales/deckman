// Package update gestiona la comprobacion, descarga, verificacion e
// instalacion de nuevas versiones de deckman desde GitHub Releases, tanto para
// binarios sueltos como para el paquete Flatpak de usuario.
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// SemVer representa una version semantica X.Y.Z con un sufijo opcional tras
// guion (por ejemplo el que anade git describe entre tags: 0.8.1-3-gabc123).
type SemVer struct {
	Major  int
	Minor  int
	Patch  int
	Suffix string
}

// ParseVersion analiza una cadena de version tipo "v0.8.1" o "0.8.1-3-gabc".
// Rechaza "dev", cadenas vacias o versiones que no tengan exactamente tres
// componentes numericos no negativos.
func ParseVersion(s string) (SemVer, bool) {
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	if s == "" {
		return SemVer{}, false
	}

	core := s
	suffix := ""
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		core = s[:idx]
		suffix = s[idx+1:]
		if suffix == "" {
			return SemVer{}, false
		}
	}

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return SemVer{}, false
	}

	var nums [3]int
	for i, p := range parts {
		if !soloDigitosASCII(p) {
			return SemVer{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return SemVer{}, false
		}
		nums[i] = n
	}

	return SemVer{
		Major:  nums[0],
		Minor:  nums[1],
		Patch:  nums[2],
		Suffix: suffix,
	}, true
}

func soloDigitosASCII(s string) bool {
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

// String devuelve la version canonica sin prefijo "v".
func (v SemVer) String() string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Suffix != "" {
		return base + "-" + v.Suffix
	}
	return base
}

// IsDevVersion indica si la cadena corresponde a una compilacion de desarrollo
// o sin etiqueta numerica reconocible.
func IsDevVersion(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || s == "dev" {
		return true
	}
	_, ok := ParseVersion(s)
	return !ok
}

// IsNewer devuelve true si latest representa una release estable mas reciente
// que current. Las versiones de desarrollo ("dev", "") o las releases con
// sufijo (pre-releases) nunca se consideran actualizables automaticamente.
func IsNewer(current, latest string) bool {
	cur, okCur := ParseVersion(current)
	lat, okLat := ParseVersion(latest)
	if !okCur || !okLat || lat.Suffix != "" {
		return false
	}
	if lat.Major != cur.Major {
		return lat.Major > cur.Major
	}
	if lat.Minor != cur.Minor {
		return lat.Minor > cur.Minor
	}
	return lat.Patch > cur.Patch
}
