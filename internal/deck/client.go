package deck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/jfrmorales/deckman/internal/i18n"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Client es una conexion viva con la Steam Deck: un canal SSH para ejecutar
// ordenes y otro SFTP para mover ficheros.
// Tanto ssh.Client como sftp.Client son seguros para uso concurrente, asi que
// el Client no necesita cerrojos propios y se puede copiar sin problema.
type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client

	// cefHTTP es el cliente HTTP hacia el depurador de Steam. Uno por conexion
	// y compartido: ver newCEFClient en cef.go.
	cefHTTP *http.Client

	// cache memoiza lo que no cambia entre operaciones (raiz de Steam, cuenta).
	// Es un puntero y no campos sueltos a proposito: el Client se copia
	// (WithHome) y un cerrojo copiado por valor es justo lo que caza vet.
	cache *remoteCache

	Host string
	User string
	Home string // home remoto resuelto, p. ej. /home/deck
}

// remoteCache guarda respuestas de la Deck que son estables durante una
// sesion. Se vacia en cada Scan (ver invalidateCache), que es el momento en
// que deckman vuelve a mirar el estado del aparato con ojos nuevos.
type remoteCache struct {
	mu        sync.Mutex
	steamRoot string
	userID    string

	// La pestana del depurador de Steam con la API buena (SharedJSContext).
	// Redescubrirla son dos viajes (listar pestanas y elegir); instalar un
	// juego con caratulas encadena media docena de ordenes en caliente y
	// pagaba el descubrimiento entero en cada una. Ver cefEval.
	cef   cefTarget
	cefAt time.Time
}

func (c *Client) invalidateCache() {
	if c.cache == nil {
		return
	}
	c.cache.mu.Lock()
	c.cache.steamRoot = ""
	c.cache.userID = ""
	c.cache.cef = cefTarget{}
	c.cache.mu.Unlock()
}

// WithHome devuelve una vista del mismo cliente con otro directorio personal.
// Comparte la conexion; sirve para trabajar contra un arbol de pruebas sin
// tocar la configuracion real.
func (c *Client) WithHome(home string) *Client {
	clone := *c
	clone.Home = home
	// Cache propia y vacia: la raiz de Steam y la cuenta dependen del home, y
	// heredar las del cliente real seria responder por el arbol equivocado.
	clone.cache = &remoteCache{}
	return &clone
}

// Credentials describe como autenticarse contra la Deck.
type Credentials struct {
	Host     string
	Port     int
	User     string
	Password string
	KeyPath  string // ruta a una clave privada; tiene prioridad sobre Password
}

// Connect abre la sesion. Aceptamos cualquier clave de host: esto es una LAN
// domestica y exigir un known_hosts solo conseguiria que el usuario no pueda
// conectarse tras reinstalar SteamOS.
func Connect(ctx context.Context, c Credentials) (*Client, error) {
	if c.Port == 0 {
		c.Port = 22
	}
	if c.User == "" {
		c.User = "deck"
	}

	var auths []ssh.AuthMethod
	if c.KeyPath != "" {
		key, err := os.ReadFile(c.KeyPath)
		if err != nil {
			return nil, i18n.Errorf("no se pudo leer la clave %s: %w", c.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, i18n.Errorf("clave privada invalida: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if c.Password != "" {
		auths = append(auths, ssh.Password(c.Password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				ans := make([]string, len(questions))
				for i := range ans {
					ans[i] = c.Password
				}
				return ans, nil
			}))
	}
	if len(auths) == 0 {
		return nil, i18n.Errorf("hay que indicar contrasena o clave privada")
	}

	cfg := &ssh.ClientConfig{
		User:            c.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         12 * time.Second,
	}

	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
	d := net.Dialer{Timeout: 12 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, i18n.Errorf("no se puede alcanzar %s: %w", addr, err)
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, i18n.Errorf("fallo de autenticacion contra %s: %w", addr, err)
	}
	client := &Client{ssh: ssh.NewClient(sc, chans, reqs), Host: c.Host, User: c.User, cache: &remoteCache{}}
	client.cefHTTP = newCEFClient(client.ssh)

	// Sin MaxPacket: se pedia 1<<15, que ES el valor por defecto de pkg/sftp
	// (y ademas el maximo que MaxPacket admite), o sea, un no-op con un
	// comentario que prometia velocidad. Paquetes mayores exigirian
	// MaxPacketUnchecked y medirlo contra la Deck antes de fiarse.
	client.sftp, err = sftp.NewClient(client.ssh, sftp.UseConcurrentWrites(true))
	if err != nil {
		client.ssh.Close()
		return nil, i18n.Errorf("no se pudo abrir SFTP: %w", err)
	}

	// Keepalive: deckman se queda abierto y una conexion parada un rato la
	// tira el router o sshd sin que nadie lo note hasta la siguiente operacion,
	// a veces a mitad de algo. El sondeo la mantiene viva; si falla es que la
	// conexion ya murio y la gorutina se retira sola (por eso no hace falta
	// canal de parada: tras Close, el siguiente sondeo falla y termina).
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			if _, _, err := client.ssh.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}()

	home, err := client.Run(ctx, "printf %s \"$HOME\"")
	if err != nil || home == "" {
		home = "/home/" + c.User
	}
	client.Home = strings.TrimSpace(home)
	return client, nil
}

// Close cierra ambos canales.
func (c *Client) Close() error {
	// Las conexiones ociosas del cliente CEF son canales SSH abiertos: si no se
	// cierran aqui, siguen vivos hasta que muere el proceso.
	if c.cefHTTP != nil {
		c.cefHTTP.CloseIdleConnections()
	}
	if c.sftp != nil {
		c.sftp.Close()
	}
	if c.ssh != nil {
		return c.ssh.Close()
	}
	return nil
}

// SFTP expone el cliente de ficheros.
func (c *Client) SFTP() *sftp.Client { return c.sftp }

// Run ejecuta una orden y devuelve su salida estandar ya recortada.
func (c *Client) Run(ctx context.Context, cmd string) (string, error) {
	out, _, err := c.RunFull(ctx, cmd)
	return strings.TrimSpace(out), err
}

// RunFull ejecuta una orden devolviendo stdout y stderr por separado.
func (c *Client) RunFull(ctx context.Context, cmd string) (string, string, error) {
	sess, err := c.ssh.NewSession()
	if err != nil {
		return "", "", err
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-ctx.Done():
		sess.Signal(ssh.SIGKILL)
		return stdout.String(), stderr.String(), ctx.Err()
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return stdout.String(), stderr.String(), i18n.Errorf("%s", msg)
		}
		return stdout.String(), stderr.String(), nil
	}
}

// RunStream ejecuta una orden y va entregando cada linea de salida segun
// aparece. Lo usamos para seguir el progreso de rsync en la propia Deck.
func (c *Client) RunStream(ctx context.Context, cmd string, onLine func(string)) error {
	sess, err := c.ssh.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return err
	}
	if err := sess.Start(cmd); err != nil {
		return err
	}

	var wg sync.WaitGroup
	var errText bytes.Buffer
	wg.Add(2)
	// rsync separa las lineas de progreso con \r, no con \n.
	go func() { defer wg.Done(); scanLines(stdout, onLine) }()
	go func() { defer wg.Done(); io.Copy(&errText, stderr) }()

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	select {
	case <-ctx.Done():
		sess.Signal(ssh.SIGKILL)
		// Cerrar la sesion ademas de matar el proceso: sshd no siempre reenvia
		// la senal, y si el proceso remoto sigue vivo las tuberias nunca ven el
		// EOF, las gorutinas de arriba no terminan y el wg.Wait() se queda
		// colgado para siempre (el boton "Cancelar" no respondia).
		sess.Close()
		wg.Wait()
		return ctx.Err()
	case err := <-done:
		wg.Wait()
		if err != nil {
			if msg := strings.TrimSpace(errText.String()); msg != "" {
				return i18n.Errorf("%s", msg)
			}
			return err
		}
		return nil
	}
}

func scanLines(r io.Reader, onLine func(string)) {
	buf := make([]byte, 4096)
	var acc []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			acc = append(acc, buf[:n]...)
			for {
				i := bytes.IndexAny(acc, "\r\n")
				if i < 0 {
					break
				}
				if line := strings.TrimSpace(string(acc[:i])); line != "" {
					onLine(line)
				}
				acc = acc[i+1:]
			}
		}
		if err != nil {
			if line := strings.TrimSpace(string(acc)); line != "" {
				onLine(line)
			}
			return
		}
	}
}

// ReadFile trae un fichero remoto entero a memoria.
//
// io.Copy y no io.ReadAll a proposito: ReadAll encadena Reads de un paquete,
// pagando un viaje de ida y vuelta por cada 32 KB. Con io.Copy entra
// File.WriteTo, que pkg/sftp reparte en lecturas concurrentes; es la misma
// trampa documentada para la subida (HALLAZGOS-STEAM.md §9), en el otro
// sentido. Se nota en las portadas (varios MB) y en cada VDF por wifi.
func (c *Client) ReadFile(p string) ([]byte, error) {
	f, err := c.sftp.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		buf.Grow(int(fi.Size()))
	}
	if _, err := io.Copy(&buf, f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteFileAtomic escribe un fichero remoto de forma segura: primero deja una
// copia .deckman.bak del original, luego escribe a un temporal y renombra.
// Asi un corte a mitad no deja a Steam con un fichero de configuracion a medias.
func (c *Client) WriteFileAtomic(p string, data []byte) error {
	// Copia de seguridad del original (copiando, no moviendo: si algo falla
	// despues, el fichero bueno nunca ha dejado de estar en su sitio).
	if _, err := c.sftp.Stat(p); err == nil {
		bak := p + ".deckman.bak"
		c.sftp.Remove(bak)
		if err := c.copyRemote(p, bak); err != nil {
			return i18n.Errorf("no se pudo respaldar %s: %w", p, err)
		}
	}

	tmp := p + ".deckman.tmp"
	c.sftp.Remove(tmp)
	f, err := c.sftp.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		c.sftp.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		c.sftp.Remove(tmp)
		return err
	}

	// PosixRename sustituye el destino de golpe. Si el servidor SFTP no ofrece
	// esa extension, hay que apartar el original antes de renombrar.
	if err := c.sftp.PosixRename(tmp, p); err != nil {
		if rmErr := c.sftp.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			c.sftp.Remove(tmp)
			return i18n.Errorf("no se pudo reemplazar %s: %w", p, err)
		}
		if err2 := c.sftp.Rename(tmp, p); err2 != nil {
			return i18n.Errorf("no se pudo reemplazar %s: %w", p, err2)
		}
	}
	return nil
}

func (c *Client) copyRemote(src, dst string) error {
	in, err := c.sftp.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := c.sftp.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// Exists indica si existe una ruta remota.
func (c *Client) Exists(p string) bool {
	_, err := c.sftp.Stat(p)
	return err == nil
}

// MkdirAll crea un directorio remoto y sus padres.
func (c *Client) MkdirAll(p string) error { return c.sftp.MkdirAll(p) }

// SteamRunning indica si el cliente de Steam esta arrancado. Importa mucho:
// Steam mantiene su estado en memoria y reescribe shortcuts.vdf, config.vdf y
// los .acf al cerrarse, machacando cualquier cambio hecho por detras.
func (c *Client) SteamRunning(ctx context.Context) bool {
	out, _ := c.Run(ctx, "pgrep -x steam >/dev/null 2>&1 && echo si || echo no")
	return strings.TrimSpace(out) == "si"
}

// InstallPublicKey deja una clave publica en authorized_keys para no tener que
// guardar la contrasena. Es idempotente.
func (c *Client) InstallPublicKey(ctx context.Context, pubKey string) error {
	pubKey = strings.TrimSpace(pubKey)
	if pubKey == "" {
		return i18n.Errorf("clave publica vacia")
	}
	dir := path.Join(c.Home, ".ssh")
	if err := c.sftp.MkdirAll(dir); err != nil {
		return err
	}
	c.sftp.Chmod(dir, 0o700)

	authPath := path.Join(dir, "authorized_keys")

	// Distinguir "no existe" de "no se pudo leer" es critico: si el fichero
	// esta ahi pero da error (permisos, disco), tragarse el fallo y escribir
	// desde cero borraria las demas claves del usuario y lo dejaria fuera de su
	// propia Deck.
	var existing []byte
	if _, err := c.sftp.Stat(authPath); err == nil {
		existing, err = c.ReadFile(authPath)
		if err != nil {
			return i18n.Errorf("no se pudo leer %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return i18n.Errorf("no se pudo comprobar %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
	}

	if bytes.Contains(existing, []byte(pubKey)) {
		return nil
	}
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		existing = append(existing, '\n')
	}
	existing = append(existing, []byte(pubKey+"\n")...)

	// Atomico y con copia: authorized_keys es lo unico que separa al usuario de
	// tener que volver a entrar con contrasena.
	if err := c.WriteFileAtomic(authPath, existing); err != nil {
		return i18n.Errorf("no se pudo escribir %s: %w", authPath, err)
	}
	return c.sftp.Chmod(authPath, 0o600)
}

// RemovePublicKey retira nuestra clave de authorized_keys y dice cuantas
// lineas quito. Es el inverso de InstallPublicKey: sirve para que olvidar una
// Deck corte de verdad el acceso, y no solo deje de usarlo.
//
// Es idempotente: si la clave no esta, no toca el fichero y devuelve 0.
//
// Compara por el material de la clave (el bloque base64), no por la linea
// entera. El comentario final lo escribimos nosotros con el nombre del PC
// (deckman@equipo) y cambia si el usuario renombra la maquina; comparando la
// linea completa, la clave se quedaria puesta para siempre creyendo que ya no
// esta. Buscar el bloque tambien acierta si alguien le anadio opciones delante
// (from="...", command="...").
func (c *Client) RemovePublicKey(ctx context.Context, pubKey string) (int, error) {
	campos := strings.Fields(strings.TrimSpace(pubKey))
	if len(campos) < 2 {
		return 0, i18n.Errorf("clave publica invalida: %q", pubKey)
	}
	material := []byte(campos[1])

	authPath := path.Join(c.Home, ".ssh", "authorized_keys")

	// Igual que al instalar: "no existe" es que no hay nada que quitar, pero
	// "no se pudo leer" no puede tratarse como fichero vacio. Reescribirlo a
	// ciegas borraria las demas claves y dejaria al usuario fuera de su Deck.
	if _, err := c.sftp.Stat(authPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, i18n.Errorf("no se pudo comprobar %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
	}
	existing, err := c.ReadFile(authPath)
	if err != nil {
		return 0, i18n.Errorf("no se pudo leer %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
	}

	nuevo, quitadas := stripKeyLines(existing, material)
	if quitadas == 0 {
		return 0, nil
	}

	if err := c.WriteFileAtomic(authPath, nuevo); err != nil {
		return 0, i18n.Errorf("no se pudo escribir %s: %w", authPath, err)
	}
	if err := c.sftp.Chmod(authPath, 0o600); err != nil {
		return quitadas, err
	}

	// La copia que deja WriteFileAtomic contiene exactamente lo que nos han
	// pedido destruir: la version con nuestra clave todavia dentro, y ademas
	// legible por cualquiera dentro de ~/.ssh. En los ficheros de Steam esa
	// copia es una red de seguridad que interesa conservar; aqui seria dejar la
	// revocacion a medias y hacer dudar a quien luego audite el fichero.
	//
	// Se borra solo despues de releer y confirmar que el authorized_keys bueno
	// ha quedado sin nuestra clave: mientras no este comprobado, la copia es lo
	// unico que protege al usuario de quedarse fuera de su Deck.
	if comprobado, err := c.ReadFile(authPath); err == nil && !bytes.Contains(comprobado, material) {
		c.sftp.Remove(authPath + ".deckman.bak")
	}
	return quitadas, nil
}

// stripKeyLines quita de un authorized_keys las lineas que contienen el
// material de clave dado, y devuelve el contenido nuevo y cuantas quito.
//
// Va aparte de RemovePublicKey para poder probarla sin una Deck delante: es la
// parte que, si se equivoca, deja a alguien fuera de su propia maquina. Las
// lineas ajenas se conservan tal cual, comentarios y blancos incluidos: este
// fichero suele tener cosas que no ha puesto deckman.
func stripKeyLines(existing, material []byte) ([]byte, int) {
	lineas := bytes.Split(existing, []byte("\n"))
	quedan := make([][]byte, 0, len(lineas))
	quitadas := 0
	for _, l := range lineas {
		if len(material) > 0 && bytes.Contains(l, material) {
			quitadas++
			continue
		}
		quedan = append(quedan, l)
	}
	if quitadas == 0 {
		return existing, 0
	}

	nuevo := bytes.Join(quedan, []byte("\n"))
	if len(bytes.TrimSpace(nuevo)) == 0 {
		return nil, quitadas // solo quedaban blancos: mejor fichero vacio
	}
	if !bytes.HasSuffix(nuevo, []byte("\n")) {
		nuevo = append(nuevo, '\n')
	}
	return nuevo, quitadas
}

// ShellQuote entrecomilla un argumento para pasarlo sin sustos por el shell
// remoto. Los nombres de juego traen espacios, apostrofes y parentesis.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
