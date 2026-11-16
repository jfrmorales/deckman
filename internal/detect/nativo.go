package detect

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Reconocer un ejecutable nativo de Linux por su CONTENIDO.
//
// No vale fiarse de la extension (casi nunca la llevan: "Zelda64Recompiled",
// "juego.x86_64", "start.sh") ni del permiso de ejecucion: una carpeta
// descomprimida en Windows no lo tiene, y es justo el caso que hay que cubrir.

// Nativo describe lo que se ha visto en la cabecera de un fichero.
type Nativo struct {
	ELF     bool   // empieza por la firma ELF
	Script  bool   // empieza por "#!"
	Tipo    uint16 // e_type: 2 = ejecutable, 3 = PIE o biblioteca compartida
	Maquina uint16 // e_machine: 3 = x86, 62 = x86-64, 183 = aarch64...
}

const (
	elfExec   = 2
	elfDyn    = 3
	elfX86    = 3
	elfX86_64 = 62
)

// Olfatear lee la cabecera del fichero. Un fichero ilegible o demasiado corto
// devuelve el valor cero: "no es nativo", que es lo prudente.
func Olfatear(ruta string) Nativo {
	f, err := os.Open(ruta)
	if err != nil {
		return Nativo{}
	}
	defer f.Close()
	var cab [20]byte
	n, _ := io.ReadFull(f, cab[:])
	return olfatearCabecera(cab[:n])
}

func olfatearCabecera(cab []byte) Nativo {
	if len(cab) >= 2 && cab[0] == '#' && cab[1] == '!' {
		return Nativo{Script: true}
	}
	if len(cab) < 20 || string(cab[:4]) != "\x7fELF" {
		return Nativo{}
	}
	// El byte 5 dice el orden de los bytes del resto de la cabecera.
	var orden binary.ByteOrder = binary.LittleEndian
	if cab[5] == 2 {
		orden = binary.BigEndian
	}
	return Nativo{ELF: true, Tipo: orden.Uint16(cab[16:18]), Maquina: orden.Uint16(cab[18:20])}
}

// Ejecutable indica si al fichero hay que ponerle permiso de ejecucion al
// copiarlo a la Deck. Va por lo ancho a proposito: tambien las bibliotecas y
// los binarios de otra arquitectura. Marcar de mas no rompe nada; marcar de
// menos deja un juego que no arranca y un "Permission denied" que solo se ve
// en una consola.
func (n Nativo) Ejecutable() bool { return n.ELF || n.Script }

// reBiblioteca casa "libfoo.so", "libfoo.so.2" y "libfoo.so.2.0.18".
var reBiblioteca = regexp.MustCompile(`(?i)\.so(\.\d+)*$`)

// Lanzable indica si el fichero puede ser LO QUE ARRANCA el juego en una
// Deck: un binario de x86 o x86-64 que no sea una biblioteca, o un script.
//
// Un PIE y una biblioteca compartida llevan el mismo e_type (3), asi que las
// bibliotecas solo se pueden apartar por el nombre.
func (n Nativo) Lanzable(nombre string) bool {
	if n.Script {
		return true
	}
	if !n.ELF || reBiblioteca.MatchString(nombre) {
		return false
	}
	if n.Tipo != elfExec && n.Tipo != elfDyn {
		return false // ficheros objeto, volcados de memoria
	}
	return n.Maquina == elfX86_64 || n.Maquina == elfX86
}

// Extensiones que nunca son un ejecutable nativo. Solo sirven para no abrir
// los miles de ficheros de datos de un juego: lo que no este aqui se mira por
// dentro, que es lo que decide.
var extNoNativas = map[string]bool{
	".exe": true, ".dll": true, ".pdb": true, ".bat": true, ".cmd": true, ".msi": true,
	".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".gif": true, ".tga": true,
	".dds": true, ".webp": true, ".ico": true, ".svg": true,
	".ogg": true, ".wav": true, ".mp3": true, ".flac": true, ".opus": true,
	".mp4": true, ".webm": true, ".mkv": true, ".bik": true, ".bk2": true, ".usm": true,
	".txt": true, ".md": true, ".json": true, ".xml": true, ".ini": true, ".cfg": true,
	".toml": true, ".yaml": true, ".yml": true, ".html": true, ".css": true, ".js": true,
	".pdf": true, ".rtf": true, ".log": true, ".csv": true,
	".pak": true, ".pck": true, ".assets": true, ".ress": true, ".resource": true,
	".bundle": true, ".arc": true, ".wad": true, ".vpk": true, ".rpa": true, ".rpyc": true,
	".zip": true, ".7z": true, ".rar": true, ".gz": true, ".xz": true, ".zst": true,
	".ttf": true, ".otf": true, ".lua": true, ".py": true, ".pyc": true, ".pyo": true,
	".z64": true, ".n64": true, ".v64": true, ".iso": true, ".sav": true,
}

// PuedeSerNativo descarta por el nombre lo que seguro que no es un ejecutable
// nativo, para ahorrarse abrir el fichero.
func PuedeSerNativo(nombre string) bool {
	return !extNoNativas[strings.ToLower(filepath.Ext(nombre))]
}

// LanzableEn junta las dos comprobaciones: nombre y contenido.
func LanzableEn(ruta string) bool {
	nombre := filepath.Base(ruta)
	return PuedeSerNativo(nombre) && Olfatear(ruta).Lanzable(nombre)
}
