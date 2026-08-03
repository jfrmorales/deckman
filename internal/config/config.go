package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Config es lo que deckman recuerda entre ejecuciones.
//
// A proposito no guardamos la contrasena: en cuanto la conexion funciona
// instalamos una clave SSH y a partir de ahi se usa esa.
type Config struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	KeyPath   string `json:"keyPath,omitempty"`
	LastLocal string `json:"lastLocal,omitempty"` // ultima carpeta usada al enviar
	GridKey   string `json:"gridKey,omitempty"`   // clave de SteamGridDB (caratulas)
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
	c := &Config{Port: 22, User: "deck"}
	p, err := filePath()
	if err != nil {
		return c
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return c
	}
	json.Unmarshal(data, c)
	if c.Port == 0 {
		c.Port = 22
	}
	if c.User == "" {
		c.User = "deck"
	}
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
			return "", "", fmt.Errorf("la clave guardada no es valida: %w", err)
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
