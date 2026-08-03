package deck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	Host string
	User string
	Home string // home remoto resuelto, p. ej. /home/deck
}

// WithHome devuelve una vista del mismo cliente con otro directorio personal.
// Comparte la conexion; sirve para trabajar contra un arbol de pruebas sin
// tocar la configuracion real.
func (c *Client) WithHome(home string) *Client {
	clone := *c
	clone.Home = home
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
			return nil, fmt.Errorf("no se pudo leer la clave %s: %w", c.KeyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("clave privada invalida: %w", err)
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
		return nil, fmt.Errorf("hay que indicar contrasena o clave privada")
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
		return nil, fmt.Errorf("no se puede alcanzar %s: %w", addr, err)
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("fallo de autenticacion contra %s: %w", addr, err)
	}
	client := &Client{ssh: ssh.NewClient(sc, chans, reqs), Host: c.Host, User: c.User}
	client.cefHTTP = newCEFClient(client.ssh)

	// MaxPacket alto: mueve ficheros grandes bastante mas rapido que el defecto.
	client.sftp, err = sftp.NewClient(client.ssh, sftp.MaxPacket(1<<15), sftp.UseConcurrentWrites(true))
	if err != nil {
		client.ssh.Close()
		return nil, fmt.Errorf("no se pudo abrir SFTP: %w", err)
	}

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
			return stdout.String(), stderr.String(), fmt.Errorf("%s", msg)
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
				return fmt.Errorf("%s", msg)
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
func (c *Client) ReadFile(p string) ([]byte, error) {
	f, err := c.sftp.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
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
			return fmt.Errorf("no se pudo respaldar %s: %w", p, err)
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
			return fmt.Errorf("no se pudo reemplazar %s: %w", p, err)
		}
		if err2 := c.sftp.Rename(tmp, p); err2 != nil {
			return fmt.Errorf("no se pudo reemplazar %s: %w", p, err2)
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
		return fmt.Errorf("clave publica vacia")
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
			return fmt.Errorf("no se pudo leer %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no se pudo comprobar %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
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
		return fmt.Errorf("no se pudo escribir %s: %w", authPath, err)
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
		return 0, fmt.Errorf("clave publica invalida: %q", pubKey)
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
		return 0, fmt.Errorf("no se pudo comprobar %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
	}
	existing, err := c.ReadFile(authPath)
	if err != nil {
		return 0, fmt.Errorf("no se pudo leer %s en la Deck; no lo toco para no borrar tus otras claves: %w", authPath, err)
	}

	nuevo, quitadas := stripKeyLines(existing, material)
	if quitadas == 0 {
		return 0, nil
	}

	if err := c.WriteFileAtomic(authPath, nuevo); err != nil {
		return 0, fmt.Errorf("no se pudo escribir %s: %w", authPath, err)
	}
	if err := c.sftp.Chmod(authPath, 0o600); err != nil {
		return quitadas, err
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
