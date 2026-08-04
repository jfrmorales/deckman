package deck

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jfrmorales/deckman/internal/i18n"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Verificacion de la clave de host, al estilo TOFU (trust on first use).
//
// Antes aqui habia un ssh.InsecureIgnoreHostKey() con este razonamiento: es una
// LAN domestica, y exigir un known_hosts solo conseguiria que el usuario no
// pueda conectarse tras reinstalar SteamOS. La primera mitad era discutible y
// la segunda, cierta — pero llevaban a la conclusion equivocada: aceptar
// cualquier clave significa que la contrasena de la Deck viaja hacia lo que
// conteste en esa IP, sea la Deck o no. En una red domestica con un movil
// comprometido, o simplemente con un DHCP que ha reasignado la direccion, eso
// es entregar la contrasena a otro.
//
// TOFU da las dos cosas: la primera vez se acepta y se recuerda sin preguntar
// nada (nadie tiene que entender que es una clave de host), y a partir de ahi
// un cambio se planta y avisa. Reinstalar SteamOS cambia la clave de forma
// legitima, asi que hay una via para volver a confiar — pero es una decision
// consciente de quien mira, con la huella delante, en vez de algo que pasa
// solo. Es lo que hace ssh de toda la vida, sin el susto tipografico.
//
// El fichero es de deckman, no ~/.ssh/known_hosts del usuario: se escribe y se
// reescribe entero, y no queremos tocar el de nadie.

// ClaveDeHostCambiada es lo que sale cuando la Deck presenta una clave distinta
// de la recordada. Es un tipo propio y no un error cualquiera para que el
// servidor pueda reconocerlo con errors.As y ofrecer volver a confiar: cualquier
// otro fallo de conexion no debe llevar a esa pregunta.
type ClaveDeHostCambiada struct {
	Host       string // tal y como se escribio, con puerto si lo llevaba
	Recordada  string // huella SHA256 de la clave que deckman tenia guardada
	Presentada string // huella SHA256 de la que ha contestado ahora

	msg error
}

func (e *ClaveDeHostCambiada) Error() string { return e.msg.Error() }

// Unwrap deja el i18n.Mensaje a la vista para que Traducir lo encuentre: sin
// esto el aviso saldria siempre en castellano, justo en el unico momento en que
// hace falta que se entienda a la primera.
func (e *ClaveDeHostCambiada) Unwrap() error { return e.msg }

// escrituraKnown serializa los cambios del fichero: dos conexiones a la vez a
// dos Decks distintas son normales (olvidar una mientras se usa otra) y se
// escribe leyendo el fichero entero.
var escrituraKnown sync.Mutex

// hostKeyCallback construye la verificacion para una conexion.
//
// confiar=true acepta y GUARDA la clave que presente la Deck aunque no cuadre
// con la recordada. Es lo que se manda cuando el usuario ha dicho que si al
// aviso; nunca es el defecto.
func hostKeyCallback(ruta string, confiar bool) (ssh.HostKeyCallback, error) {
	if ruta == "" {
		// Fallar cerrado a proposito: un camino vacio que "acepte lo que sea"
		// seria InsecureIgnoreHostKey con otro nombre, y ademas invisible.
		return nil, i18n.Errorf("falta la ruta del fichero de claves de host conocidas")
	}
	if err := os.MkdirAll(filepath.Dir(ruta), 0o700); err != nil {
		return nil, err
	}
	// knownhosts.New falla si el fichero no existe, y la primera vez nunca
	// existe. Crearlo vacio es mas simple que tratar ese caso aparte.
	f, err := os.OpenFile(ruta, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, i18n.Errorf("no se pudo crear %s: %w", ruta, err)
	}
	f.Close()

	// Una sola linea ilegible tumba knownhosts.New entero, y con ella la
	// conexion a TODAS las Decks: el error que salia era «invalid curve point»
	// y desde la interfaz no habia forma de arreglarlo. Salio probando contra
	// una Deck de verdad.
	//
	// No es señal de ataque —este fichero solo lo escribe deckman con claves
	// que le ha dado un servidor—, sino de escritura a medias o de alguien
	// editandolo a mano. Asi que se tiran las lineas rotas y se sigue: las
	// Decks cuya linea esta bien conservan su verificacion, y la de la linea
	// rota vuelve a empezar por el principio (se acepta y se recuerda). Quien
	// pueda corromper este fichero ya tiene acceso local, y con el la clave
	// privada, asi que no se pierde nada que no estuviera perdido.
	if err := sanearKnownHosts(ruta); err != nil {
		return nil, err
	}

	base, err := knownhosts.New(ruta)
	if err != nil {
		return nil, i18n.Errorf("no se pudo leer %s: %w", ruta, err)
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}

		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		// Want vacio significa "de este host no sabiamos nada". Es la primera
		// vez: se acepta y se apunta, que es el TOFU.
		if len(ke.Want) == 0 {
			return recordarClave(ruta, hostname, key)
		}
		if confiar {
			return recordarClave(ruta, hostname, key)
		}
		recordada := ""
		if len(ke.Want) > 0 {
			recordada = ssh.FingerprintSHA256(ke.Want[0].Key)
		}
		presentada := ssh.FingerprintSHA256(key)
		return &ClaveDeHostCambiada{
			Host:       hostname,
			Recordada:  recordada,
			Presentada: presentada,
			// El formato va entero en una linea, sin partirlo con +, porque la
			// prueba de claves huerfanas del catalogo busca la cadena literal
			// en el codigo y no la encontraria troceada.
			msg: i18n.Errorf("la Deck de %s presenta una clave SSH distinta de la que deckman recuerda (ahora %s, antes %s). Si acabas de reinstalar SteamOS es lo normal y puedes volver a confiar en ella; si no, puede que quien conteste en esa direccion no sea tu Deck", hostname, presentada, recordada),
		}
	}, nil
}

// sanearKnownHosts quita del fichero las lineas que no se entienden, dejando
// copia si ha tenido que tocar algo. Ver el porque en hostKeyCallback.
func sanearKnownHosts(ruta string) error {
	escrituraKnown.Lock()
	defer escrituraKnown.Unlock()

	datos, err := os.ReadFile(ruta)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return i18n.Errorf("no se pudo leer %s: %w", ruta, err)
	}

	var buenas []string
	rotas := 0
	for _, linea := range strings.Split(string(datos), "\n") {
		if strings.TrimSpace(linea) == "" {
			continue
		}
		// ParseKnownHosts es el mismo analizador que usa knownhosts.New, asi
		// que lo que pase por aqui pasa por alli. Comprobarlo con otra cosa
		// dejaria justo el hueco que se quiere tapar.
		if _, _, _, _, _, err := ssh.ParseKnownHosts([]byte(linea + "\n")); err != nil {
			rotas++
			continue
		}
		buenas = append(buenas, linea)
	}
	if rotas == 0 {
		return nil
	}

	log.Printf("known_hosts: %d linea(s) ilegible(s) en %s; se apartan en %s.roto y se sigue con las demas",
		rotas, ruta, ruta)
	if err := os.WriteFile(ruta+".roto", datos, 0o600); err != nil {
		log.Printf("known_hosts: no se pudo guardar la copia: %v", err)
	}
	contenido := ""
	if len(buenas) > 0 {
		contenido = strings.Join(buenas, "\n") + "\n"
	}
	if err := os.WriteFile(ruta, []byte(contenido), 0o600); err != nil {
		return i18n.Errorf("no se pudo escribir %s: %w", ruta, err)
	}
	return nil
}

// recordarClave deja en el fichero la clave de este host, quitando antes lo que
// hubiera suyo. Reescribe el fichero entero en vez de añadir al final: dos
// lineas para el mismo host con claves distintas dejarian la vieja valida para
// siempre, que es justo lo que se quiere evitar.
func recordarClave(ruta, hostname string, key ssh.PublicKey) error {
	escrituraKnown.Lock()
	defer escrituraKnown.Unlock()

	normalizado := knownhosts.Normalize(hostname)
	previo, err := os.ReadFile(ruta)
	if err != nil && !os.IsNotExist(err) {
		return i18n.Errorf("no se pudo leer %s: %w", ruta, err)
	}

	var lineas []string
	for _, l := range strings.Split(string(previo), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if esDeEsteHost(l, normalizado) {
			continue
		}
		lineas = append(lineas, l)
	}
	lineas = append(lineas, knownhosts.Line([]string{normalizado}, key))

	nuevo := strings.Join(lineas, "\n") + "\n"
	// Fichero pequeño y propio: escribir a un temporal al lado y renombrar
	// evita que un corte lo deje a medias y obligue a reconfiar a mano.
	tmp := ruta + ".tmp"
	if err := os.WriteFile(tmp, []byte(nuevo), 0o600); err != nil {
		return i18n.Errorf("no se pudo escribir %s: %w", ruta, err)
	}
	if err := os.Rename(tmp, ruta); err != nil {
		os.Remove(tmp)
		return i18n.Errorf("no se pudo escribir %s: %w", ruta, err)
	}
	return nil
}

// OlvidarClaveDeHost borra lo que se recordara de un host. Se llama al olvidar
// una Deck: si la lista la pierde de vista, su clave tampoco pinta nada, y
// dejarla haria que volver a añadirla mas tarde (con SteamOS reinstalado entre
// medias) saliera con el aviso de clave cambiada sin motivo.
func OlvidarClaveDeHost(ruta, host string, puerto int) error {
	if ruta == "" {
		return nil
	}
	escrituraKnown.Lock()
	defer escrituraKnown.Unlock()

	previo, err := os.ReadFile(ruta)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return i18n.Errorf("no se pudo leer %s: %w", ruta, err)
	}
	normalizado := knownhosts.Normalize(direccion(host, puerto))

	var lineas []string
	for _, l := range strings.Split(string(previo), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if esDeEsteHost(l, normalizado) {
			continue
		}
		lineas = append(lineas, l)
	}
	contenido := ""
	if len(lineas) > 0 {
		contenido = strings.Join(lineas, "\n") + "\n"
	}
	if err := os.WriteFile(ruta, []byte(contenido), 0o600); err != nil {
		return i18n.Errorf("no se pudo escribir %s: %w", ruta, err)
	}
	return nil
}

// esDeEsteHost dice si una linea del fichero habla del host indicado.
//
// Compara el primer campo tal cual porque este fichero lo escribe solo
// deckman, siempre con un unico host ya normalizado y sin hashear. Con el
// known_hosts de un usuario cualquiera no valdria (listas separadas por comas,
// comodines, entradas |1| con el nombre cifrado), y por eso no se usa el suyo.
func esDeEsteHost(linea, normalizado string) bool {
	campos := strings.Fields(linea)
	if len(campos) == 0 {
		return false
	}
	return campos[0] == normalizado
}

// direccion junta host y puerto como los espera knownhosts.Normalize.
func direccion(host string, puerto int) string {
	if puerto == 0 {
		puerto = 22
	}
	return net.JoinHostPort(host, fmt.Sprint(puerto))
}
