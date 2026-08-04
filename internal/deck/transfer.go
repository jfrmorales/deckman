package deck

import (
	"context"
	"fmt"
	"github.com/jfrmorales/deckman/internal/i18n"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Progress es un parte de avance de una operacion larga.
type Progress struct {
	Phase      string  `json:"phase"`
	File       string  `json:"file"`
	FilesDone  int     `json:"filesDone"`
	FilesTotal int     `json:"filesTotal"`
	BytesDone  uint64  `json:"bytesDone"`
	BytesTotal uint64  `json:"bytesTotal"`
	Speed      float64 `json:"speed"` // bytes/s
	Done       bool    `json:"done"`
	Error      string  `json:"error,omitempty"`
	Message    string  `json:"message,omitempty"`
}

// ProgressFunc recibe los partes de avance.
type ProgressFunc func(Progress)

type localFile struct {
	abs  string
	rel  string // ruta relativa con '/' como separador
	size uint64
}

// uploadConcurrency: 4 ficheros a la vez llena el wifi de la Deck sin
// atragantar el canal SSH, que es de por si el cuello de botella.
const uploadConcurrency = 4

// UploadTree copia un fichero o carpeta local a un directorio remoto.
// Los ficheros que ya existan con el mismo tamano se saltan, asi que una
// transferencia cortada se puede relanzar y continua donde iba.
func (c *Client) UploadTree(ctx context.Context, localPath, remoteDir string, onProgress ProgressFunc) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return i18n.Errorf("no se puede leer %s: %w", localPath, err)
	}

	report := throttle(onProgress)
	report(Progress{Phase: "indexando", Message: "calculando el tamano del origen"})

	var files []localFile
	var total uint64

	if info.IsDir() {
		base := filepath.Clean(localPath)
		err = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if !fi.Mode().IsRegular() {
				return nil // enlaces simbolicos y demas: fuera
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			files = append(files, localFile{abs: p, rel: filepath.ToSlash(rel), size: uint64(fi.Size())})
			total += uint64(fi.Size())
			return nil
		})
		if err != nil {
			return i18n.Errorf("error recorriendo %s: %w", localPath, err)
		}
		remoteDir = path.Join(remoteDir, filepath.Base(base))
	} else {
		files = append(files, localFile{abs: localPath, rel: filepath.Base(localPath), size: uint64(info.Size())})
		total = uint64(info.Size())
	}

	if len(files) == 0 {
		return i18n.Errorf("no hay ficheros que copiar en %s", localPath)
	}

	// Los mas grandes primero: reparte mejor el trabajo entre las 4 hebras.
	sort.Slice(files, func(i, j int) bool { return files[i].size > files[j].size })

	// Crear el arbol de directorios de una vez es mucho mas rapido que un
	// MkdirAll por fichero.
	dirs := map[string]bool{remoteDir: true}
	for _, f := range files {
		if d := path.Dir(f.rel); d != "." {
			dirs[path.Join(remoteDir, d)] = true
		}
	}
	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)
	for _, d := range dirList {
		if err := c.sftp.MkdirAll(d); err != nil {
			return i18n.Errorf("no se pudo crear %s en la Deck: %w", d, err)
		}
	}

	// Tamanos de lo que ya hay en el destino, con un ReadDir por directorio en
	// vez de un Stat por fichero: en una retransmision (que es cuando el "ya
	// estaba, me lo salto" trabaja) el Stat de cada fichero ERA la operacion, y
	// con veinte mil ficheros pequenos eso son veinte mil idas y vueltas.
	existentes := make(map[string]uint64, len(files))
	for _, d := range dirList {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries, err := c.sftp.ReadDir(d)
		if err != nil {
			continue // recien creado o ilegible: se subira todo, que es lo seguro
		}
		for _, e := range entries {
			if e.Mode().IsRegular() {
				existentes[path.Join(d, e.Name())] = uint64(e.Size())
			}
		}
	}

	var (
		bytesDone atomic.Uint64
		filesDone atomic.Int64
		skipped   atomic.Int64
		firstErr  error
		errOnce   sync.Once
	)
	start := time.Now()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan localFile)
	var wg sync.WaitGroup
	for i := 0; i < uploadConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				dst := path.Join(remoteDir, f.rel)
				copied := false
				// El mapa es de solo lectura desde que se construyo: sin cerrojo.
				if sz, ok := existentes[dst]; !ok || sz != f.size {
					var err error
					copied, err = c.uploadOne(ctx, f, dst)
					if err != nil {
						errOnce.Do(func() { firstErr = i18n.Errorf("%s: %w", f.rel, err); cancel() })
						return
					}
				}
				if !copied {
					skipped.Add(1)
				}
				bytesDone.Add(f.size)
				n := filesDone.Add(1)
				elapsed := time.Since(start).Seconds()
				bd := bytesDone.Load()
				var speed float64
				if elapsed > 0 {
					speed = float64(bd) / elapsed
				}
				report(Progress{
					Phase: "copiando", File: f.rel,
					FilesDone: int(n), FilesTotal: len(files),
					BytesDone: bd, BytesTotal: total, Speed: speed,
				})
			}
		}()
	}
	// Al cancelar se deja de alimentar del todo: sin el break, el bucle seguia
	// recorriendo los ficheros restantes entrando y saliendo del select. Break
	// etiquetado y no return: el close(jobs) y el wg.Wait() tienen que ocurrir
	// igualmente o las hebras quedarian colgadas.
alimentar:
	for _, f := range files {
		select {
		case jobs <- f:
		case <-ctx.Done():
			break alimentar
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	msg := fmt.Sprintf("%d ficheros copiados", len(files))
	if s := skipped.Load(); s > 0 {
		msg = fmt.Sprintf("%d ficheros (%d ya estaban y se han saltado)", len(files), s)
	}
	report(Progress{
		Phase: "listo", FilesDone: len(files), FilesTotal: len(files),
		BytesDone: total, BytesTotal: total, Done: true, Message: msg,
	})
	return nil
}

// uploadOne sube un fichero. El "ya estaba con este tamano, me lo salto" lo
// decide el llamante con el indice de ReadDir; aqui ya solo se copia.
func (c *Client) uploadOne(ctx context.Context, f localFile, dst string) (bool, error) {
	in, err := os.Open(f.abs)
	if err != nil {
		return false, err
	}
	defer in.Close()

	out, err := c.sftp.Create(dst)
	if err != nil {
		return false, err
	}
	// ReadFrom aprovecha la escritura concurrente de SFTP; va bastante mas
	// rapido que un io.Copy normal en ficheros grandes.
	if _, err := out.ReadFrom(&ctxReader{ctx: ctx, f: in}); err != nil {
		out.Close()
		c.sftp.Remove(dst)
		return false, err
	}
	if err := out.Close(); err != nil {
		return false, err
	}
	return true, nil
}

// ctxReader aborta la lectura si se cancela el contexto.
//
// El metodo Stat NO es decorativo: ReadFrom de pkg/sftp solo usa su ruta de
// escritura concurrente cuando puede averiguar de antemano el tamano del
// origen, y para eso busca un Len(), un Size() o un Stat() en el lector. Si el
// envoltorio lo esconde, cae en la ruta secuencial y la subida pasa de unos
// 100 MB/s a 5 MB/s (medido contra una Deck real: 19 veces mas lento).
type ctxReader struct {
	ctx context.Context
	f   *os.File
}

func (cr *ctxReader) Read(p []byte) (int, error) {
	select {
	case <-cr.ctx.Done():
		return 0, cr.ctx.Err()
	default:
	}
	return cr.f.Read(p)
}

func (cr *ctxReader) Stat() (os.FileInfo, error) { return cr.f.Stat() }

// throttle limita los partes a unos 10 por segundo, pero deja pasar siempre el
// final. Sin esto la interfaz recibe miles de eventos por segundo.
func throttle(f ProgressFunc) ProgressFunc {
	if f == nil {
		return func(Progress) {}
	}
	var mu sync.Mutex
	var last time.Time
	return func(p Progress) {
		mu.Lock()
		defer mu.Unlock()
		if p.Done || p.Error != "" || time.Since(last) > 100*time.Millisecond {
			last = time.Now()
			f(p)
		}
	}
}

// NonSteamOptions describe el juego no-Steam que se quiere registrar.
type NonSteamOptions struct {
	Name          string
	Exe           string // ruta remota del ejecutable
	StartDir      string // carpeta de trabajo; si va vacia, la del ejecutable
	LaunchOptions string
	CompatTool    string // proton_experimental, GE-Proton9-20... vacio = nativo
}

// AddNonSteamGame registra el juego editando shortcuts.vdf directamente.
//
// OJO: solo es seguro con Steam CERRADO. Steam guarda la lista de accesos
// directos en memoria y reescribe el fichero al salir, asi que con Steam
// abierto este cambio se pierde, y si Steam vuelca un estado incompleto puede
// llevarse por delante los demas accesos directos. Con Steam abierto hay que
// usar AddShortcutLive, que se lo pide a el.
//
// El llamante es responsable de comprobarlo; aqui no se impone para poder
// seguir usandolo cuando Steam esta cerrado, que es cuando toca.
func (c *Client) AddNonSteamGame(ctx context.Context, opts NonSteamOptions) (uint32, error) {
	if opts.Name == "" {
		return 0, i18n.Errorf("hace falta un nombre para el juego")
	}
	if opts.Exe == "" {
		return 0, i18n.Errorf("hace falta la ruta del ejecutable")
	}
	if opts.StartDir == "" {
		opts.StartDir = path.Dir(opts.Exe)
	}

	steamRoot := c.SteamRoot()
	userID, err := c.UserID(ctx, steamRoot)
	if err != nil {
		return 0, err
	}
	scPath := path.Join(steamRoot, "userdata", userID, "config", "shortcuts.vdf")

	data, _ := c.ReadFile(scPath) // que no exista es valido
	sf, err := ParseShortcuts(data)
	if err != nil {
		return 0, i18n.Errorf("shortcuts.vdf ilegible, no lo toco por seguridad: %w", err)
	}

	newSc := NewShortcut(opts.Name, opts.Exe, opts.StartDir, opts.LaunchOptions)
	appID := newSc.AppID()

	// Buscamos si el juego ya estaba para actualizarlo en vez de duplicarlo.
	// No vale comparar solo por appid: los accesos directos que crea Steam
	// llevan un id aleatorio que nunca coincidira con el que calculamos, asi
	// que emparejamos por ejecutable (y, en su defecto, por nombre).
	existing := -1
	for i, s := range sf.Entries {
		if Unquote(s.GetStr("Exe")) == opts.Exe {
			existing = i
			break
		}
	}
	if existing < 0 {
		for i, s := range sf.Entries {
			if strings.EqualFold(s.GetStr("AppName"), opts.Name) {
				existing = i
				break
			}
		}
	}

	if existing >= 0 {
		appID = updateShortcut(sf.Entries[existing], opts, appID)
	} else {
		sf.Entries = append(sf.Entries, newSc)
	}

	if err := c.MkdirAll(path.Dir(scPath)); err != nil {
		return 0, err
	}
	out := sf.Marshal()
	if err := checkNoShortcutsLost(data, out, nil); err != nil {
		return 0, err
	}
	if err := c.WriteFileAtomic(scPath, out); err != nil {
		return 0, i18n.Errorf("no se pudo escribir shortcuts.vdf: %w", err)
	}

	if opts.CompatTool != "" {
		if err := c.SetCompatTool(ctx, fmt.Sprint(appID), opts.CompatTool); err != nil {
			return appID, i18n.Errorf("juego anadido, pero no se pudo asignar Proton: %w", err)
		}
	}
	return appID, nil
}

// updateShortcut pone al dia un acceso directo que ya existia y devuelve el
// appid con el que se queda.
//
// Toca SOLO los cuatro campos que cambian al reenviar el juego. Antes se
// sustituia la entrada entera por una recien creada, y eso se llevaba por
// delante las etiquetas, el icono, las horas jugadas (LastPlayTime) y cualquier
// campo que Steam haya anadido y nosotros no conozcamos: justo lo que el parser
// se molesta en conservar (ver shortcuts.go).
//
// nuevoID solo se usa si la entrada no traia appid propio: el guardado manda
// siempre, porque es la clave con la que Steam tiene asociadas las caratulas y
// el mapeo de Proton, y los que crea Steam llevan un numero aleatorio que nunca
// coincidiria con el que calculamos.
func updateShortcut(e *Shortcut, opts NonSteamOptions, nuevoID uint32) uint32 {
	// Se lee antes de tocar nada: el respaldo de AppID() se calcula a partir de
	// Exe y AppName, que estamos a punto de cambiar.
	appID := uint32(e.GetInt("appid"))
	if appID == 0 {
		appID = nuevoID
		e.SetInt("appid", int32(appID))
	}
	// Exe y StartDir van entrecomillados, que es como los escribe Steam.
	e.SetStr("AppName", opts.Name)
	e.SetStr("Exe", strconv.Quote(opts.Exe))
	e.SetStr("StartDir", strconv.Quote(opts.StartDir))
	e.SetStr("LaunchOptions", opts.LaunchOptions)
	return appID
}

// RelocateShortcut reescribe las rutas de un acceso directo cuando su carpeta
// se ha movido de sitio.
//
// OJO: solo es seguro con Steam CERRADO, por lo mismo que AddNonSteamGame. Con
// Steam abierto hay que usar SetShortcutPathLive.
func (c *Client) RelocateShortcut(ctx context.Context, appID uint32, oldDir, newDir string) error {
	steamRoot := c.SteamRoot()
	userID, err := c.UserID(ctx, steamRoot)
	if err != nil {
		return err
	}
	scPath := path.Join(steamRoot, "userdata", userID, "config", "shortcuts.vdf")
	data, err := c.ReadFile(scPath)
	if err != nil {
		return err
	}
	sf, err := ParseShortcuts(data)
	if err != nil {
		return i18n.Errorf("shortcuts.vdf ilegible, no lo toco por seguridad: %w", err)
	}

	var entry *Shortcut
	for _, s := range sf.Entries {
		if s.AppID() == appID {
			entry = s
			break
		}
	}
	if entry == nil {
		return i18n.Errorf("no hay ningun acceso directo con id %d", appID)
	}

	relocateShortcut(entry, appID, oldDir, newDir)

	out := sf.Marshal()
	if err := checkNoShortcutsLost(data, out, nil); err != nil {
		return err
	}
	return c.WriteFileAtomic(scPath, out)
}

// relocateShortcut cambia de sitio las rutas de una entrada: el ejecutable, la
// carpeta de trabajo, el icono y lo que las opciones de lanzamiento mencionen
// de la carpeta vieja.
func relocateShortcut(e *Shortcut, appID uint32, oldDir, newDir string) {
	// Si la entrada no traia appid propio, el suyo sale de calcular el CRC de
	// Exe+AppName... que es justo lo que estamos a punto de cambiar. Se fija
	// antes, para que el juego no cambie de identidad y pierda de golpe sus
	// caratulas y su version de Proton.
	if e.GetInt("appid") == 0 {
		e.SetInt("appid", int32(appID))
	}

	mudar := func(p string) string {
		if !withinDir(oldDir, p) {
			return p
		}
		return newDir + strings.TrimPrefix(p, oldDir)
	}
	// Exe y StartDir se guardan entrecomillados, que es como los escribe Steam.
	// StartDir se muda como todo lo demas y no se fija al nuevo destino: puede
	// ser la carpeta del juego o una subcarpeta suya (System/Win32_x86 y
	// compania), y aplastarla cambiaria por donde arranca el juego.
	e.SetStr("Exe", strconv.Quote(mudar(Unquote(e.GetStr("Exe")))))
	e.SetStr("StartDir", strconv.Quote(mudar(Unquote(e.GetStr("StartDir")))))
	// El icono suele estar vacio o dentro de la propia carpeta del juego.
	if icon := Unquote(e.GetStr("icon")); icon != "" {
		e.SetStr("icon", mudar(icon))
	}
	// Las opciones de lanzamiento pueden llevar rutas dentro (un -mod=, un
	// fichero de configuracion): se cambian solo si mencionan la carpeta vieja.
	if opts := e.GetStr("LaunchOptions"); strings.Contains(opts, oldDir) {
		e.SetStr("LaunchOptions", strings.ReplaceAll(opts, oldDir, newDir))
	}
}

// RemoveShortcut borra un acceso directo no-Steam por su appid.
func (c *Client) RemoveShortcut(ctx context.Context, appID uint32) error {
	steamRoot := c.SteamRoot()
	userID, err := c.UserID(ctx, steamRoot)
	if err != nil {
		return err
	}
	scPath := path.Join(steamRoot, "userdata", userID, "config", "shortcuts.vdf")
	data, err := c.ReadFile(scPath)
	if err != nil {
		return err
	}
	sf, err := ParseShortcuts(data)
	if err != nil {
		return err
	}
	out := sf.Entries[:0]
	found := false
	for _, s := range sf.Entries {
		if s.AppID() == appID {
			found = true
			continue
		}
		out = append(out, s)
	}
	if !found {
		return i18n.Errorf("no hay ningun acceso directo con id %d", appID)
	}
	sf.Entries = out
	marshalled := sf.Marshal()
	if err := checkNoShortcutsLost(data, marshalled, &appID); err != nil {
		return err
	}
	return c.WriteFileAtomic(scPath, marshalled)
}

// checkNoShortcutsLost compara el shortcuts.vdf que vamos a escribir con el que
// habia y se niega si desaparece algun juego que no tocaba.
//
// Es una red de seguridad, no un adorno: perder este fichero borra de la
// biblioteca todos los juegos no-Steam del usuario de golpe, y recuperarlo sin
// copia es imposible. Si algo va mal, mejor no escribir nada.
func checkNoShortcutsLost(antes, despues []byte, borrado *uint32) error {
	if len(antes) == 0 {
		return nil // no habia nada que perder
	}
	viejo, err := ParseShortcuts(antes)
	if err != nil {
		return nil // si no se podia leer, no hay nada que comparar
	}
	nuevo, err := ParseShortcuts(despues)
	if err != nil {
		return i18n.Errorf("lo que ibamos a escribir no se puede releer; no toco shortcuts.vdf: %w", err)
	}

	presentes := map[uint32]bool{}
	for _, s := range nuevo.Entries {
		presentes[s.AppID()] = true
	}
	for _, s := range viejo.Entries {
		id := s.AppID()
		if borrado != nil && id == *borrado {
			continue // este si tocaba quitarlo
		}
		if !presentes[id] {
			return i18n.Errorf("me niego a escribir shortcuts.vdf: desapareceria %q (%d) y no era la intencion",
				s.GetStr("AppName"), id)
		}
	}
	return nil
}

// ApplyCompatTool fija la version de Proton por la via que toque y dice si se
// ha hecho en caliente.
//
// config.vdf tiene la misma trampa que shortcuts.vdf: Steam lo mantiene en
// memoria y lo reescribe al salir. Escribirlo con Steam abierto no solo no se
// ve, sino que se pierde en cuanto Steam se cierra... y cerrar o reiniciar
// Steam era precisamente lo que se le pedia al usuario para que el cambio
// surtiera efecto, asi que el consejo deshacia el cambio. Con Steam abierto se
// le pide a el; si no responde, mejor no tocar nada y decirlo.
func (c *Client) ApplyCompatTool(ctx context.Context, appID, tool string) (bool, error) {
	if !c.SteamRunning(ctx) {
		return false, c.SetCompatTool(ctx, appID, tool)
	}
	if !c.CEFAvailable(ctx) {
		return false, i18n.Errorf(
			"Steam esta abierto pero no responde: cierralo en la Deck y vuelve a intentarlo, " +
				"o reinicialo y espera a que cargue del todo. Cambiar la version de Proton con " +
				"Steam abierto se perderia al salir")
	}
	id, err := strconv.ParseUint(appID, 10, 32)
	if err != nil {
		return false, i18n.Errorf("id de juego invalido: %s", appID)
	}
	if err := c.SetCompatToolLive(ctx, uint32(id), tool); err != nil {
		return false, i18n.Errorf("no se pudo cambiar la version de Proton a traves de Steam: %w", err)
	}
	return true, nil
}

// SetCompatTool fija la version de Proton de un juego en config.vdf.
// Un tool vacio elimina la asignacion (vuelve al comportamiento por defecto).
//
// OJO: solo es seguro con Steam CERRADO, por lo mismo que AddNonSteamGame.
// Quien no sepa si Steam corre debe usar ApplyCompatTool.
func (c *Client) SetCompatTool(ctx context.Context, appID, tool string) error {
	cfgPath := path.Join(c.SteamRoot(), "config", "config.vdf")
	data, err := c.ReadFile(cfgPath)
	if err != nil {
		return i18n.Errorf("no se pudo leer config.vdf: %w", err)
	}
	root, err := ParseVDF(data)
	if err != nil {
		return i18n.Errorf("config.vdf ilegible, no lo toco por seguridad: %w", err)
	}

	store := root.Get("InstallConfigStore")
	if store == nil {
		return i18n.Errorf("config.vdf no tiene InstallConfigStore; formato inesperado")
	}
	mapping := store.Ensure("Software", "Valve", "Steam", "CompatToolMapping")

	if tool == "" {
		if !mapping.Remove(appID) {
			return nil
		}
	} else {
		entry := mapping.Ensure(appID)
		entry.SetString("name", tool)
		entry.SetString("config", "")
		entry.SetString("priority", "250")
	}
	return c.WriteFileAtomic(cfgPath, root.Marshal())
}

// UploadROM copia una ROM a la carpeta del sistema correspondiente de EmuDeck.
func (c *Client) UploadROM(ctx context.Context, localPath, romsDir, system string, onProgress ProgressFunc) error {
	if romsDir == "" || system == "" {
		return i18n.Errorf("hay que indicar la carpeta de ROMs y el sistema")
	}
	if strings.Contains(system, "/") || strings.Contains(system, "..") {
		return i18n.Errorf("nombre de sistema invalido: %q", system)
	}
	dst := path.Join(romsDir, system)
	if err := c.MkdirAll(dst); err != nil {
		return err
	}
	return c.UploadTree(ctx, localPath, dst, onProgress)
}
