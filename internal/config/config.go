package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/jfrmorales/deckman/internal/i18n"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Deck es una Steam Deck recordada. Se identifica por host y puerto: cambiar
// el usuario de una Deck ya guardada la actualiza en vez de duplicarla.
type Deck struct {
	Name string `json:"name,omitempty"` // etiqueta que pone el usuario; si esta vacia se muestra el host
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
}

// Label es como se llama a esta Deck en la interfaz.
func (d Deck) Label() string {
	if d.Name != "" {
		return d.Name
	}
	return d.Host
}

// Same dice si dos entradas son la misma Deck. El usuario no cuenta: si
// alguien conecta al mismo sitio con otra cuenta, es la misma maquina y lo que
// quiere es corregir el usuario, no acabar con dos fichas iguales.
func (d Deck) Same(o Deck) bool {
	return d.Host == o.Host && d.Port == o.Port
}

// Config es lo que deckman recuerda entre ejecuciones.
//
// A proposito no guardamos la contrasena: en cuanto la conexion funciona
// instalamos una clave SSH y a partir de ahi se usa esa. La clave es una sola
// para todas las Decks: identifica a "deckman en este PC", no a una maquina
// concreta, y asi olvidar una Deck no deja a las demas fuera.
type Config struct {
	Decks  []Deck `json:"decks"`
	Active int    `json:"active"` // indice dentro de Decks; -1 si no hay ninguna

	KeyPath   string `json:"keyPath,omitempty"`
	LastLocal string `json:"lastLocal,omitempty"` // ultima carpeta usada al enviar
	GridKey   string `json:"gridKey,omitempty"`   // clave de SteamGridDB (caratulas)

	// Idioma de la interfaz: "es", "en", "fr" o "" para seguir al navegador.
	// Vive aqui y no en el navegador porque tambien decide en que idioma
	// traduce el servidor sus mensajes de error: si lo guardara solo el
	// navegador, la interfaz saldria en frances y los errores en castellano.
	Idioma string `json:"idioma,omitempty"`

	// De cuando solo se podia guardar una Deck. Solo se leen, para migrar la
	// configuracion existente a Decks; al primer Save desaparecen del fichero.
	// No los uses en codigo nuevo: la Deck en uso es ActiveDeck().
	LegacyHost string `json:"host,omitempty"`
	LegacyPort int    `json:"port,omitempty"`
	LegacyUser string `json:"user,omitempty"`
}

// Snapshot devuelve una copia independiente para serializar fuera del cerrojo.
//
// Copiar con `cfg := *c` a secas comparte el array de respaldo de Decks, y
// UpsertDeck escribe en sitio (c.Decks[i] = d): un /api/state codificando la
// copia mientras otro handler conectaba era una carrera de datos de verdad.
func (c *Config) Snapshot() Config {
	cp := *c
	cp.Decks = append([]Deck(nil), c.Decks...)
	return cp
}

// ActiveDeck devuelve la Deck en uso. Si no hay ninguna guardada devuelve una
// vacia con los valores por defecto, que es justo lo que la interfaz necesita
// para pintar el formulario de conexion la primera vez.
func (c *Config) ActiveDeck() Deck {
	if c.Active < 0 || c.Active >= len(c.Decks) {
		return Deck{Port: 22, User: "deck"}
	}
	return c.Decks[c.Active]
}

// UpsertDeck guarda una Deck y la deja como activa. Si ya estaba, la actualiza.
// Devuelve su indice.
func (c *Config) UpsertDeck(d Deck) int {
	if d.Port == 0 {
		d.Port = 22
	}
	if d.User == "" {
		d.User = "deck"
	}
	for i, existing := range c.Decks {
		if existing.Same(d) {
			// El nombre lo pone el usuario aparte: conectar no debe borrarlo.
			if d.Name == "" {
				d.Name = existing.Name
			}
			c.Decks[i] = d
			c.Active = i
			return i
		}
	}
	c.Decks = append(c.Decks, d)
	c.Active = len(c.Decks) - 1
	return c.Active
}

// RemoveDeck quita una Deck de la lista. Devuelve la que quito.
//
// Mantener Active apuntando a la misma Deck de antes importa: si se cae al
// indice de al lado, olvidar una Deck deja seleccionada otra distinta y la
// siguiente conexion va a una maquina que el usuario no ha elegido.
func (c *Config) RemoveDeck(i int) (Deck, bool) {
	if i < 0 || i >= len(c.Decks) {
		return Deck{}, false
	}
	quitada := c.Decks[i]
	activa := c.ActiveDeck()
	huboActiva := c.Active >= 0 && c.Active < len(c.Decks)

	c.Decks = append(c.Decks[:i], c.Decks[i+1:]...)

	c.Active = -1
	if huboActiva && !activa.Same(quitada) {
		for j, d := range c.Decks {
			if d.Same(activa) {
				c.Active = j
				break
			}
		}
	}
	if c.Active < 0 && len(c.Decks) > 0 {
		c.Active = 0
	}
	return quitada, true
}

// normalize deja la configuracion utilizable venga de donde venga: migra la
// forma antigua de una sola Deck, rellena los valores por defecto y se asegura
// de que Active apunta a algo que existe.
func (c *Config) normalize() {
	if len(c.Decks) == 0 && c.LegacyHost != "" {
		c.Decks = []Deck{{Host: c.LegacyHost, Port: c.LegacyPort, User: c.LegacyUser}}
		c.Active = 0
	}
	c.LegacyHost, c.LegacyPort, c.LegacyUser = "", 0, ""

	for i := range c.Decks {
		if c.Decks[i].Port == 0 {
			c.Decks[i].Port = 22
		}
		if c.Decks[i].User == "" {
			c.Decks[i].User = "deck"
		}
	}
	if len(c.Decks) == 0 {
		c.Active = -1
	} else if c.Active < 0 || c.Active >= len(c.Decks) {
		c.Active = 0
	}
}

// Dir es la carpeta de configuracion, dependiente del sistema:
// ~/.config/deckman en Linux y %AppData%\deckman en Windows.
// Dentro de Flatpak, XDG_CONFIG_HOME apunta a ~/.var/app/<id>/config.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "deckman")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	migrateOnce.Do(func() { migrateFromLegacy(dir) })
	return dir, nil
}

var migrateOnce sync.Once

// migrateFromLegacy copia la configuracion de ~/.config/deckman al sandbox la
// primera vez que deckman corre como Flatpak. Sin esto, quien venia usando el
// binario suelto tendria que volver a conectar con contrasena y dejaria una
// segunda clave SSH huerfana en la Deck. Solo lectura del lado viejo: el
// Flatpak tiene el home de solo lectura y nunca toca la carpeta original.
func migrateFromLegacy(dir string) {
	if os.Getenv("FLATPAK_ID") == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		return // ya hay configuracion propia
	}
	legacy := filepath.Join(os.Getenv("HOME"), ".config", "deckman")
	if legacy == dir {
		return
	}
	migrated := false
	for _, name := range []string{"config.json", "id_ed25519", "id_ed25519.pub"} {
		data, err := os.ReadFile(filepath.Join(legacy, name))
		if err != nil {
			continue
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(name, ".pub") {
			mode = 0o644
		}
		if os.WriteFile(filepath.Join(dir, name), data, mode) == nil {
			migrated = true
		}
	}
	if !migrated {
		return
	}
	// La ruta de la clave guardada en config.json apunta a la carpeta vieja;
	// dentro del sandbox la clave buena es la copia.
	p := filepath.Join(dir, "config.json")
	if data, err := os.ReadFile(p); err == nil {
		var c Config
		if json.Unmarshal(data, &c) == nil && c.KeyPath != "" {
			c.KeyPath = filepath.Join(dir, "id_ed25519")
			if out, err := json.MarshalIndent(&c, "", "  "); err == nil {
				os.WriteFile(p, out, 0o600)
			}
		}
	}
}

func filePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load lee la configuracion. Si no hay fichero devuelve los valores por defecto.
func Load() *Config {
	c := &Config{Active: -1}
	p, err := filePath()
	if err != nil {
		return c
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return c
	}
	json.Unmarshal(data, c)
	c.normalize()
	return c
}

// Save persiste la configuracion.
//
// Escribe a un temporal del mismo directorio y renombra encima: os.WriteFile
// trunca el fichero antes de escribirlo, y un corte de luz o un cierre a
// destiempo dejaba un config.json vacio. Perderlo significa perder keyPath, y
// con el la clave SSH instalada en la Deck queda huerfana.
func (c *Config) Save() error {
	p, err := filePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	// El temporal va al lado del destino: renombrar solo es atomico dentro del
	// mismo sistema de ficheros.
	f, err := os.CreateTemp(filepath.Dir(p), ".config-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // si todo va bien ya no existe y no pasa nada

	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// EnsureKey devuelve la clave SSH de deckman, generandola la primera vez.
// Devuelve la ruta de la privada y el texto de la publica.
func EnsureKey() (keyPath, pubKey string, err error) {
	dir, err := Dir()
	if err != nil {
		return "", "", err
	}
	keyPath = filepath.Join(dir, "id_ed25519")
	pubPath := keyPath + ".pub"

	if _, err := os.Stat(keyPath); err == nil {
		pub, err := os.ReadFile(pubPath)
		if err == nil {
			return keyPath, string(pub), nil
		}
		// Falta la publica: la reconstruimos desde la privada.
		priv, err := os.ReadFile(keyPath)
		if err != nil {
			return "", "", err
		}
		signer, err := ssh.ParsePrivateKey(priv)
		if err != nil {
			return "", "", i18n.Errorf("la clave guardada no es valida: %w", err)
		}
		text := authorizedKeyLine(signer.PublicKey())
		os.WriteFile(pubPath, []byte(text), 0o644)
		return keyPath, text, nil
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "deckman")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	text := authorizedKeyLine(sshPub)
	if err := os.WriteFile(pubPath, []byte(text), 0o644); err != nil {
		return "", "", err
	}
	return keyPath, text, nil
}

// authorizedKeyLine formatea la clave publica con un comentario que la
// identifique. Sin el, en el authorized_keys de la Deck aparece una linea
// anonima imposible de reconocer si algun dia se quiere retirar.
func authorizedKeyLine(pub ssh.PublicKey) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "pc"
	}
	return fmt.Sprintf("%s deckman@%s\n", line, host)
}
