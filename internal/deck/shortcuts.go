package deck

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
)

// shortcuts.vdf es VDF *binario*, no texto. La gramatica es:
//
//	0x00 <clave NUL> ...hijos... 0x08   -> mapa
//	0x01 <clave NUL> <valor NUL>        -> cadena
//	0x02 <clave NUL> <int32 LE>         -> entero
//
// El fichero entero es un mapa "shortcuts" cuyos hijos son "0", "1", "2"...
// y termina con dos 0x08 (cierre del mapa y cierre de la raiz).
const (
	binMap    byte = 0x00
	binString byte = 0x01
	binInt32  byte = 0x02
	binEnd    byte = 0x08
)

// ShortcutField es un campo de un acceso directo. Guardamos el tipo y el orden
// tal cual venian para poder reescribir el fichero sin perder campos que no
// conozcamos (Steam anade nuevos cada pocas versiones).
type ShortcutField struct {
	Type     byte
	Key      string
	Str      string
	Int      int32
	Children []*ShortcutField // solo si Type == binMap (p. ej. "tags")
}

// Shortcut es una entrada de juego no-Steam.
type Shortcut struct {
	Fields []*ShortcutField
}

// ShortcutsFile es el contenido completo de shortcuts.vdf.
type ShortcutsFile struct {
	Entries []*Shortcut
}

// ParseShortcuts lee un shortcuts.vdf binario. Un fichero vacio o inexistente
// se representa como una lista sin entradas.
func ParseShortcuts(data []byte) (*ShortcutsFile, error) {
	sf := &ShortcutsFile{}
	if len(data) == 0 {
		return sf, nil
	}
	r := &binReader{data: data}
	// Raiz: 0x00 "shortcuts" 0x00
	t, err := r.readByte()
	if err != nil {
		return nil, err
	}
	if t != binMap {
		return nil, fmt.Errorf("shortcuts.vdf: se esperaba un mapa en la raiz, encontrado 0x%02x", t)
	}
	if _, err := r.readCString(); err != nil { // nombre de la raiz ("shortcuts")
		return nil, err
	}
	for {
		t, err := r.readByte()
		if err != nil {
			return nil, err
		}
		if t == binEnd {
			break
		}
		if t != binMap {
			return nil, fmt.Errorf("shortcuts.vdf: entrada de tipo inesperado 0x%02x", t)
		}
		if _, err := r.readCString(); err != nil { // indice "0", "1", ...
			return nil, err
		}
		fields, err := r.readFields()
		if err != nil {
			return nil, err
		}
		sf.Entries = append(sf.Entries, &Shortcut{Fields: fields})
	}
	return sf, nil
}

type binReader struct {
	data []byte
	pos  int
}

func (r *binReader) readByte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, fmt.Errorf("shortcuts.vdf: fin de fichero inesperado")
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *binReader) readCString() (string, error) {
	i := bytes.IndexByte(r.data[r.pos:], 0)
	if i < 0 {
		return "", fmt.Errorf("shortcuts.vdf: cadena sin terminador NUL")
	}
	s := string(r.data[r.pos : r.pos+i])
	r.pos += i + 1
	return s, nil
}

func (r *binReader) readInt32() (int32, error) {
	if r.pos+4 > len(r.data) {
		return 0, fmt.Errorf("shortcuts.vdf: entero truncado")
	}
	v := int32(binary.LittleEndian.Uint32(r.data[r.pos : r.pos+4]))
	r.pos += 4
	return v, nil
}

// readFields consume campos hasta el 0x08 que cierra el mapa actual.
func (r *binReader) readFields() ([]*ShortcutField, error) {
	var out []*ShortcutField
	for {
		t, err := r.readByte()
		if err != nil {
			return nil, err
		}
		if t == binEnd {
			return out, nil
		}
		key, err := r.readCString()
		if err != nil {
			return nil, err
		}
		f := &ShortcutField{Type: t, Key: key}
		switch t {
		case binString:
			if f.Str, err = r.readCString(); err != nil {
				return nil, err
			}
		case binInt32:
			if f.Int, err = r.readInt32(); err != nil {
				return nil, err
			}
		case binMap:
			if f.Children, err = r.readFields(); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("shortcuts.vdf: tipo de campo desconocido 0x%02x en %q", t, key)
		}
		out = append(out, f)
	}
}

// Marshal reconstruye el fichero binario.
func (sf *ShortcutsFile) Marshal() []byte {
	var buf bytes.Buffer
	buf.WriteByte(binMap)
	buf.WriteString("shortcuts")
	buf.WriteByte(0)
	for i, e := range sf.Entries {
		buf.WriteByte(binMap)
		buf.WriteString(strconv.Itoa(i))
		buf.WriteByte(0)
		writeFields(&buf, e.Fields)
		buf.WriteByte(binEnd)
	}
	buf.WriteByte(binEnd) // cierra "shortcuts"
	buf.WriteByte(binEnd) // cierra la raiz
	return buf.Bytes()
}

func writeFields(buf *bytes.Buffer, fields []*ShortcutField) {
	for _, f := range fields {
		buf.WriteByte(f.Type)
		buf.WriteString(f.Key)
		buf.WriteByte(0)
		switch f.Type {
		case binString:
			buf.WriteString(f.Str)
			buf.WriteByte(0)
		case binInt32:
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], uint32(f.Int))
			buf.Write(b[:])
		case binMap:
			writeFields(buf, f.Children)
			buf.WriteByte(binEnd)
		}
	}
}

// GetStr lee un campo de texto sin distinguir mayusculas: los distintos
// clientes de Steam escriben "AppName", "appname" o "Appname" indistintamente.
func (s *Shortcut) GetStr(key string) string {
	if f := s.find(key); f != nil {
		return f.Str
	}
	return ""
}

// GetInt lee un campo entero.
func (s *Shortcut) GetInt(key string) int32 {
	if f := s.find(key); f != nil {
		return f.Int
	}
	return 0
}

// SetStr fija un campo de texto, respetando la clave original si ya existia.
func (s *Shortcut) SetStr(key, val string) {
	if f := s.find(key); f != nil {
		f.Type, f.Str, f.Children = binString, val, nil
		return
	}
	s.Fields = append(s.Fields, &ShortcutField{Type: binString, Key: key, Str: val})
}

// SetInt fija un campo entero.
func (s *Shortcut) SetInt(key string, val int32) {
	if f := s.find(key); f != nil {
		f.Type, f.Int, f.Children = binInt32, val, nil
		return
	}
	s.Fields = append(s.Fields, &ShortcutField{Type: binInt32, Key: key, Int: val})
}

func (s *Shortcut) find(key string) *ShortcutField {
	for _, f := range s.Fields {
		if strings.EqualFold(f.Key, key) {
			return f
		}
	}
	return nil
}

// AppID devuelve el identificador de 32 bits del acceso directo.
//
// El valor guardado manda siempre. Comprobado contra una Deck real: de 7
// accesos directos, solo el que habia creado EmuDeck seguia la formula CRC; los
// que anade el propio Steam llevan un appid ALEATORIO. Asi que no se puede
// recalcular, hay que respetar el que ya esta escrito o el mapeo de Proton
// acabaria apuntando a otro juego.
func (s *Shortcut) AppID() uint32 {
	if f := s.find("appid"); f != nil && f.Int != 0 {
		return uint32(f.Int)
	}
	return ShortcutAppID(s.GetStr("Exe"), s.GetStr("AppName"))
}

// ShortcutAppID genera el appid de un acceso directo nuevo: CRC32 de
// exe+nombre con el bit 31 forzado a 1.
//
// Steam usa un aleatorio para los suyos, pero a nosotros nos conviene que sea
// determinista: reenviar el mismo juego actualiza su entrada en vez de
// duplicarla, y el mapeo de Proton se mantiene estable. Es tambien lo que hacen
// EmuDeck y Steam ROM Manager, y Steam respeta el id que encuentre escrito.
//
// exe debe pasarse tal cual se guarda en shortcuts.vdf, incluidas las comillas.
func ShortcutAppID(exe, appName string) uint32 {
	return crc32.ChecksumIEEE([]byte(exe+appName)) | 0x80000000
}

// GridID es el identificador de 64 bits que usan los ficheros de caratulas
// dentro de userdata/<id>/config/grid/.
func GridID(appID uint32) uint64 {
	return uint64(appID)<<32 | 0x02000000
}

// NewShortcut construye una entrada con el juego de campos que Steam espera.
// exe y startDir se guardan entrecomillados, que es como los escribe Steam.
func NewShortcut(appName, exePath, startDir, launchOptions string) *Shortcut {
	exe := strconv.Quote(exePath)
	dir := strconv.Quote(startDir)
	s := &Shortcut{}
	s.SetInt("appid", int32(ShortcutAppID(exe, appName)))
	s.SetStr("AppName", appName)
	s.SetStr("Exe", exe)
	s.SetStr("StartDir", dir)
	s.SetStr("icon", "")
	s.SetStr("ShortcutPath", "")
	s.SetStr("LaunchOptions", launchOptions)
	s.SetInt("IsHidden", 0)
	s.SetInt("AllowDesktopConfig", 1)
	s.SetInt("AllowOverlay", 1)
	s.SetInt("OpenVR", 0)
	s.SetInt("Devkit", 0)
	s.SetStr("DevkitGameID", "")
	s.SetInt("DevkitOverrideAppID", 0)
	s.SetInt("LastPlayTime", 0)
	s.SetStr("FlatpakAppID", "")
	s.Fields = append(s.Fields, &ShortcutField{Type: binMap, Key: "tags"})
	return s
}

// Unquote quita las comillas con las que Steam envuelve Exe y StartDir.
func Unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
		return s[1 : len(s)-1]
	}
	return s
}
