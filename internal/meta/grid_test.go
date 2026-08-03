package meta

import "testing"

func TestProtonAdvice(t *testing.T) {
	cases := []struct{ tier, wantFamily string }{
		{"platinum", "experimental"},
		{"gold", "experimental"},
		{"silver", "ge"},
		{"bronze", "ge"},
		{"borked", "ge"},
		{"native", ""},
		{"", "experimental"}, // sin datos: la apuesta razonable
	}
	for _, c := range cases {
		got, advice := protonAdvice(c.tier, "", 10)
		if got != c.wantFamily {
			t.Errorf("tier %q -> %q, se esperaba %q", c.tier, got, c.wantFamily)
		}
		if advice == "" {
			t.Errorf("tier %q: sin explicacion", c.tier)
		}
	}
}

// Sin clave no se debe intentar nada contra SteamGridDB.
func TestHasGridKey(t *testing.T) {
	if New("").HasGridKey() {
		t.Error("una clave vacia no cuenta")
	}
	if New("   ").HasGridKey() {
		t.Error("solo espacios no cuenta")
	}
	if !New("abc123").HasGridKey() {
		t.Error("una clave normal deberia contar")
	}
}
