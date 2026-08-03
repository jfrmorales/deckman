package deck

import (
	"strings"
	"testing"
)

// Quitar una clave de authorized_keys es la operacion que, mal hecha, deja al
// usuario fuera de su propia Deck. Estas pruebas cubren lo que hay que
// conservar, que es todo lo demas.
func TestStripKeyLines(t *testing.T) {
	const nuestra = "AAAAC3NzaC1lZDI1NTE5AAAAINWr1D91ycXN1nuestraclave"
	const ajena = "AAAAC3NzaC1lZDI1NTE5AAAAIotracosadistinta"

	casos := []struct {
		nombre    string
		entrada   string
		esperado  string
		quitadas  int
		conservar []string
	}{
		{
			nombre:   "solo estaba la nuestra",
			entrada:  "ssh-ed25519 " + nuestra + " deckman@pc\n",
			esperado: "",
			quitadas: 1,
		},
		{
			nombre: "se respetan las claves ajenas",
			entrada: "ssh-ed25519 " + ajena + " jose@portatil\n" +
				"ssh-ed25519 " + nuestra + " deckman@pc\n",
			esperado:  "ssh-ed25519 " + ajena + " jose@portatil\n",
			quitadas:  1,
			conservar: []string{ajena},
		},
		{
			nombre: "se respetan comentarios del usuario",
			entrada: "# la del portatil, no borrar\n" +
				"ssh-ed25519 " + ajena + " jose@portatil\n" +
				"ssh-ed25519 " + nuestra + " deckman@pc\n",
			quitadas:  1,
			conservar: []string{"# la del portatil, no borrar", ajena},
		},
		{
			// El comentario lo escribimos con el nombre del PC. Si el usuario
			// renombra la maquina, la linea deja de coincidir entera pero el
			// material sigue siendo el mismo: hay que quitarla igual.
			nombre:   "cambio de nombre del PC",
			entrada:  "ssh-ed25519 " + nuestra + " deckman@nombre-viejo\n",
			esperado: "",
			quitadas: 1,
		},
		{
			// Si alguien le puso restricciones delante a mano, sigue siendo
			// nuestra clave y hay que retirarla.
			nombre:   "con opciones delante",
			entrada:  `from="192.168.1.0/24" ssh-ed25519 ` + nuestra + " deckman@pc\n",
			esperado: "",
			quitadas: 1,
		},
		{
			nombre:    "la clave no esta: no se toca nada",
			entrada:   "ssh-ed25519 " + ajena + " jose@portatil\n",
			esperado:  "ssh-ed25519 " + ajena + " jose@portatil\n",
			quitadas:  0,
			conservar: []string{ajena},
		},
		{
			nombre:   "fichero vacio",
			entrada:  "",
			esperado: "",
			quitadas: 0,
		},
		{
			nombre: "duplicada de instalaciones anteriores",
			entrada: "ssh-ed25519 " + nuestra + " deckman@pc\n" +
				"ssh-ed25519 " + nuestra + " deckman@pc\n",
			esperado: "",
			quitadas: 2,
		},
		{
			nombre: "sin salto final",
			entrada: "ssh-ed25519 " + ajena + " jose@portatil\n" +
				"ssh-ed25519 " + nuestra + " deckman@pc",
			esperado:  "ssh-ed25519 " + ajena + " jose@portatil\n",
			quitadas:  1,
			conservar: []string{ajena},
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got, n := stripKeyLines([]byte(c.entrada), []byte(nuestra))
			if n != c.quitadas {
				t.Errorf("quitadas = %d, esperaba %d", n, c.quitadas)
			}
			if strings.Contains(string(got), nuestra) {
				t.Error("nuestra clave sigue en el fichero")
			}
			for _, debe := range c.conservar {
				if !strings.Contains(string(got), debe) {
					t.Errorf("se ha perdido una linea que no era nuestra: %q", debe)
				}
			}
			if c.esperado != "" && string(got) != c.esperado {
				t.Errorf("resultado:\n%q\nesperaba:\n%q", got, c.esperado)
			}
			// Nunca devolvemos un fichero de solo blancos: confunde al leerlo.
			if s := string(got); s != "" && strings.TrimSpace(s) == "" {
				t.Errorf("ha quedado un fichero de solo blancos: %q", s)
			}
		})
	}
}

// Si no se reconoce el material de la clave, no se puede tocar el fichero:
// mas vale dejar una clave de mas que borrar las de otro.
func TestStripKeyLinesSinMaterialNoBorraNada(t *testing.T) {
	entrada := "ssh-ed25519 AAAAtal jose@portatil\n"
	got, n := stripKeyLines([]byte(entrada), nil)
	if n != 0 || string(got) != entrada {
		t.Fatalf("con material vacio no deberia tocar nada: n=%d, got=%q", n, got)
	}
}
