package deck

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// claveDePrueba genera una clave publica de host cualquiera.
func claveDePrueba(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generando la clave: %v", err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("convirtiendo la clave: %v", err)
	}
	return k
}

func direccionDePrueba(t *testing.T) net.Addr {
	t.Helper()
	a, err := net.ResolveTCPAddr("tcp", "192.168.1.50:22")
	if err != nil {
		t.Fatalf("resolviendo: %v", err)
	}
	return a
}

// La primera conexion no pregunta nada y deja la clave apuntada: eso es el
// TOFU, y es lo que evita que verificar sea un incordio.
func TestPrimeraVezSeAceptaYSeRecuerda(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	cb, err := hostKeyCallback(ruta, false)
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	key := claveDePrueba(t)
	if err := cb("192.168.1.50:22", direccionDePrueba(t), key); err != nil {
		t.Fatalf("la primera conexion tenia que aceptarse: %v", err)
	}

	datos, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leyendo el fichero: %v", err)
	}
	if !strings.Contains(string(datos), "192.168.1.50") {
		t.Errorf("el host no quedo apuntado: %q", datos)
	}
	if strings.Count(strings.TrimSpace(string(datos)), "\n") != 0 {
		t.Errorf("se esperaba una sola linea, hay: %q", datos)
	}
}

// Segunda conexion con la MISMA clave: silencio absoluto, que es el caso
// normal de todos los dias.
func TestMismaClaveNoMolesta(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	key := claveDePrueba(t)

	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.50:22", direccionDePrueba(t), key); err != nil {
		t.Fatalf("primera: %v", err)
	}
	// Un callback nuevo, como en una ejecucion posterior de deckman.
	cb2, err := hostKeyCallback(ruta, false)
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := cb2("192.168.1.50:22", direccionDePrueba(t), key); err != nil {
		t.Errorf("la misma clave no deberia dar guerra: %v", err)
	}
}

// El caso que justifica todo esto: otra clave en la misma direccion se planta,
// y lo hace con un error que el servidor puede reconocer para poder preguntar.
func TestClaveDistintaSePlanta(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	buena, mala := claveDePrueba(t), claveDePrueba(t)

	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.50:22", direccionDePrueba(t), buena); err != nil {
		t.Fatalf("primera: %v", err)
	}

	cb2, _ := hostKeyCallback(ruta, false)
	err := cb2("192.168.1.50:22", direccionDePrueba(t), mala)
	if err == nil {
		t.Fatal("una clave distinta tenia que rechazarse")
	}
	var cambiada *ClaveDeHostCambiada
	if !errors.As(err, &cambiada) {
		t.Fatalf("el error no es reconocible como ClaveDeHostCambiada: %T (%v)", err, err)
	}
	if cambiada.Presentada != ssh.FingerprintSHA256(mala) {
		t.Errorf("huella presentada = %q", cambiada.Presentada)
	}
	if cambiada.Recordada != ssh.FingerprintSHA256(buena) {
		t.Errorf("huella recordada = %q", cambiada.Recordada)
	}
	// Y el fichero no se ha tocado: rechazar no puede tener efectos.
	datos, _ := os.ReadFile(ruta)
	if !strings.Contains(string(datos), strings.Fields(string(ssh.MarshalAuthorizedKey(buena)))[1]) {
		t.Errorf("la clave buena ya no esta en el fichero: %q", datos)
	}
}

// Volver a confiar (el usuario ha dicho que si tras reinstalar SteamOS)
// sustituye la linea, no añade una segunda: si quedaran las dos, la vieja
// seguiria valiendo para siempre y la verificacion no serviria de nada.
func TestConfiarSustituyeLaClave(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	vieja, nueva := claveDePrueba(t), claveDePrueba(t)

	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.50:22", direccionDePrueba(t), vieja); err != nil {
		t.Fatalf("primera: %v", err)
	}

	confiando, _ := hostKeyCallback(ruta, true)
	if err := confiando("192.168.1.50:22", direccionDePrueba(t), nueva); err != nil {
		t.Fatalf("confiando: %v", err)
	}

	datos, _ := os.ReadFile(ruta)
	if n := len(strings.Fields(strings.TrimSpace(string(datos)))) / 3; n != 1 {
		t.Errorf("se esperaba una sola entrada, el fichero es: %q", datos)
	}
	viejaB64 := strings.Fields(string(ssh.MarshalAuthorizedKey(vieja)))[1]
	if strings.Contains(string(datos), viejaB64) {
		t.Errorf("la clave vieja sigue valiendo: %q", datos)
	}

	// Y desde ahora la nueva es la buena, sin volver a preguntar.
	despues, _ := hostKeyCallback(ruta, false)
	if err := despues("192.168.1.50:22", direccionDePrueba(t), nueva); err != nil {
		t.Errorf("la clave nueva tenia que valer ya: %v", err)
	}
}

// Cada Deck con su clave: confiar en una no puede tocar la de la otra.
func TestVariasDecksConvivenEnElMismoFichero(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	unaKey, otraKey := claveDePrueba(t), claveDePrueba(t)

	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.50:22", direccionDePrueba(t), unaKey); err != nil {
		t.Fatalf("una: %v", err)
	}
	if err := cb("192.168.1.51:22", direccionDePrueba(t), otraKey); err != nil {
		t.Fatalf("otra: %v", err)
	}

	comprobar, _ := hostKeyCallback(ruta, false)
	if err := comprobar("192.168.1.50:22", direccionDePrueba(t), unaKey); err != nil {
		t.Errorf("la primera Deck deberia seguir valiendo: %v", err)
	}
	if err := comprobar("192.168.1.51:22", direccionDePrueba(t), otraKey); err != nil {
		t.Errorf("la segunda Deck deberia seguir valiendo: %v", err)
	}
	// Y no se han mezclado: la clave de una no vale para la otra.
	if err := comprobar("192.168.1.50:22", direccionDePrueba(t), otraKey); err == nil {
		t.Error("la clave de una Deck no puede valer para la otra")
	}
}

// Olvidar una Deck se lleva su clave y solo la suya.
func TestOlvidarClaveDeHost(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	unaKey, otraKey := claveDePrueba(t), claveDePrueba(t)

	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.50:22", direccionDePrueba(t), unaKey); err != nil {
		t.Fatalf("una: %v", err)
	}
	if err := cb("192.168.1.51:22", direccionDePrueba(t), otraKey); err != nil {
		t.Fatalf("otra: %v", err)
	}

	if err := OlvidarClaveDeHost(ruta, "192.168.1.50", 22); err != nil {
		t.Fatalf("olvidando: %v", err)
	}

	comprobar, _ := hostKeyCallback(ruta, false)
	// La olvidada vuelve a ser desconocida: se acepta de nuevo sin aviso, que
	// es justo lo que se quiere al volver a añadir una Deck reinstalada.
	if err := comprobar("192.168.1.50:22", direccionDePrueba(t), claveDePrueba(t)); err != nil {
		t.Errorf("tras olvidarla tendria que aceptarse cualquier clave: %v", err)
	}
	// La otra sigue donde estaba.
	comprobar2, _ := hostKeyCallback(ruta, false)
	if err := comprobar2("192.168.1.51:22", direccionDePrueba(t), claveDePrueba(t)); err == nil {
		t.Error("olvidar una Deck no puede borrar la clave de la otra")
	}
}

// Sin fichero donde recordar, Connect no puede seguir: aceptar "lo que sea"
// seria volver al InsecureIgnoreHostKey de antes, pero sin que se note.
func TestSinRutaFallaEnVezDeAceptarTodo(t *testing.T) {
	if _, err := hostKeyCallback("", false); err == nil {
		t.Fatal("sin ruta tenia que fallar")
	}
}

// El puerto no separa identidades: la Deck es la misma en el 22 que en el
// 2222, y knownhosts normaliza el puerto por defecto quitandolo.
func TestPuertoNoPorDefectoSeGuardaAparte(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	key := claveDePrueba(t)

	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.50:2222", direccionDePrueba(t), key); err != nil {
		t.Fatalf("primera: %v", err)
	}
	if err := OlvidarClaveDeHost(ruta, "192.168.1.50", 2222); err != nil {
		t.Fatalf("olvidando: %v", err)
	}
	datos, _ := os.ReadFile(ruta)
	if strings.TrimSpace(string(datos)) != "" {
		t.Errorf("olvidar con el mismo puerto tenia que dejarlo vacio: %q", datos)
	}
}

// Una linea ilegible no puede dejar sin conexion a las demas Decks.
//
// Salio probando contra una Deck de verdad: knownhosts.New se cae entero con
// una sola linea mala («invalid curve point»), y desde la interfaz no habia
// forma de arreglarlo — el fichero no se ve desde ningun sitio.
func TestUnaLineaRotaNoTumbaElResto(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	buena := claveDePrueba(t)

	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.51:22", direccionDePrueba(t), buena); err != nil {
		t.Fatalf("primera: %v", err)
	}

	// Se le mete delante una linea que no se entiende, como la que dejaria una
	// escritura a medias.
	previo, _ := os.ReadFile(ruta)
	roto := "192.168.1.50 ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBBBB=\n"
	if err := os.WriteFile(ruta, append([]byte(roto), previo...), 0o600); err != nil {
		t.Fatal(err)
	}

	cb2, err := hostKeyCallback(ruta, false)
	if err != nil {
		t.Fatalf("con una linea rota tenia que poder seguir: %v", err)
	}
	// La Deck cuya linea estaba bien conserva su verificacion...
	if err := cb2("192.168.1.51:22", direccionDePrueba(t), buena); err != nil {
		t.Errorf("la Deck de la linea buena deberia seguir valiendo: %v", err)
	}
	if err := cb2("192.168.1.51:22", direccionDePrueba(t), claveDePrueba(t)); err == nil {
		t.Error("la Deck de la linea buena tenia que seguir rechazando otra clave")
	}
	// ...y la de la linea rota vuelve a empezar por el principio.
	if err := cb2("192.168.1.50:22", direccionDePrueba(t), claveDePrueba(t)); err != nil {
		t.Errorf("la Deck de la linea rota tenia que aceptarse de nuevo: %v", err)
	}

	// Y queda copia de lo que habia, por si hiciera falta mirarlo.
	if _, err := os.Stat(ruta + ".roto"); err != nil {
		t.Errorf("no se guardo copia del known_hosts roto: %v", err)
	}
}

// Un fichero sano no se toca: sanear no puede reescribir en cada conexion.
func TestFicheroSanoNoSeReescribe(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "known_hosts")
	cb, _ := hostKeyCallback(ruta, false)
	if err := cb("192.168.1.50:22", direccionDePrueba(t), claveDePrueba(t)); err != nil {
		t.Fatalf("primera: %v", err)
	}
	antes, _ := os.ReadFile(ruta)

	if _, err := hostKeyCallback(ruta, false); err != nil {
		t.Fatalf("segunda: %v", err)
	}
	despues, _ := os.ReadFile(ruta)
	if string(antes) != string(despues) {
		t.Errorf("el fichero ha cambiado sin motivo:\n antes: %q\n despues: %q", antes, despues)
	}
	if _, err := os.Stat(ruta + ".roto"); err == nil {
		t.Error("se guardo copia de un fichero que estaba bien")
	}
}
