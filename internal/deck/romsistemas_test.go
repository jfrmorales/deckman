package deck

import "testing"

// La salida real de la Deck del usuario, tal cual: 4 sistemas con juegos de
// las 181 carpetas que EmuDeck crea al instalarse.
func TestParseSistemas(t *testing.T) {
	out := "1\tgc\n3\tn64\n4\tps2\n2\tpsx\n"
	sis := parseSistemas(out)
	if len(sis) != 4 {
		t.Fatalf("parseSistemas devolvio %d sistemas: %+v", len(sis), sis)
	}
	// Ordenados por nombre, para que el desplegable no baile entre cargas.
	esperado := []SistemaROM{{"gc", 1}, {"n64", 3}, {"ps2", 4}, {"psx", 2}}
	for i, e := range esperado {
		if sis[i] != e {
			t.Errorf("sistema %d = %+v, se esperaba %+v", i, sis[i], e)
		}
	}
}

func TestParseSistemasBasura(t *testing.T) {
	// Lineas sueltas del shell, avisos por stderr mezclados, cuentas a cero:
	// nada de eso puede convertirse en un sistema de la lista.
	out := "\nfind: no se puede acceder\n0\tvacio\n-3\tnegativo\nsintab\n" +
		"x\tnoesnumero\n5\t\n  2  \t  snes  \n"
	sis := parseSistemas(out)
	if len(sis) != 1 {
		t.Fatalf("parseSistemas colo basura: %+v", sis)
	}
	if sis[0] != (SistemaROM{"snes", 2}) {
		t.Errorf("sistema = %+v, se esperaba {snes 2}", sis[0])
	}
}

func TestParseSistemasVacio(t *testing.T) {
	if sis := parseSistemas(""); len(sis) != 0 {
		t.Errorf("parseSistemas(\"\") = %+v", sis)
	}
}
