package deck

import (
	"bytes"
	"github.com/jfrmorales/deckman/internal/i18n"
	"sort"
	"strings"
)

// VDF (KeyValues) en texto: el formato de libraryfolders.vdf, los
// appmanifest_*.acf y config.vdf.
//
// Un nodo o tiene Value (hoja) o tiene Children (mapa). Conservamos el orden
// original de las claves para poder reescribir el fichero sin barajarlo.
type VDFNode struct {
	Key      string
	Value    string
	Children []*VDFNode
	IsMap    bool
}

// ParseVDF interpreta un documento KeyValues. El nodo devuelto es un mapa
// sintetico cuyos hijos son las entradas de primer nivel del fichero.
func ParseVDF(data []byte) (*VDFNode, error) {
	p := &vdfParser{data: data}
	root := &VDFNode{IsMap: true}
	for {
		p.skipSpace()
		if p.eof() {
			break
		}
		if p.peek() == '}' {
			return nil, i18n.Errorf("llave de cierre inesperada en offset %d", p.pos)
		}
		node, err := p.parsePair()
		if err != nil {
			return nil, err
		}
		root.Children = append(root.Children, node)
	}
	return root, nil
}

type vdfParser struct {
	data []byte
	pos  int
}

func (p *vdfParser) eof() bool  { return p.pos >= len(p.data) }
func (p *vdfParser) peek() byte { return p.data[p.pos] }

func (p *vdfParser) skipSpace() {
	for !p.eof() {
		c := p.data[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.pos++
		case c == '/' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '/':
			for !p.eof() && p.data[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *vdfParser) parsePair() (*VDFNode, error) {
	key, err := p.parseString()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.eof() {
		// Clave suelta al final del fichero: la tratamos como hoja vacia en
		// vez de reventar, que algunos .acf vienen truncados.
		return &VDFNode{Key: key}, nil
	}
	if p.peek() == '{' {
		p.pos++
		node := &VDFNode{Key: key, IsMap: true}
		for {
			p.skipSpace()
			if p.eof() {
				return nil, i18n.Errorf("falta '}' para la clave %q", key)
			}
			if p.peek() == '}' {
				p.pos++
				return node, nil
			}
			child, err := p.parsePair()
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
		}
	}
	val, err := p.parseString()
	if err != nil {
		return nil, err
	}
	return &VDFNode{Key: key, Value: val}, nil
}

func (p *vdfParser) parseString() (string, error) {
	if p.eof() {
		return "", i18n.Errorf("fin de fichero inesperado")
	}
	if p.peek() != '"' {
		// Token sin comillas (Valve lo admite en algunos ficheros).
		start := p.pos
		for !p.eof() {
			c := p.data[p.pos]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '{' || c == '}' {
				break
			}
			p.pos++
		}
		if start == p.pos {
			return "", i18n.Errorf("token vacio en offset %d", p.pos)
		}
		return string(p.data[start:p.pos]), nil
	}
	p.pos++ // comilla de apertura
	var sb strings.Builder
	for !p.eof() {
		c := p.data[p.pos]
		if c == '"' {
			p.pos++
			return sb.String(), nil
		}
		if c == '\\' && p.pos+1 < len(p.data) {
			p.pos++
			switch p.data[p.pos] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(p.data[p.pos])
			}
			p.pos++
			continue
		}
		sb.WriteByte(c)
		p.pos++
	}
	return "", i18n.Errorf("cadena sin cerrar")
}

// Get busca un descendiente siguiendo la ruta de claves. La comparacion no
// distingue mayusculas porque Steam no es consistente: en unas versiones
// escribe "Valve" y en otras "valve".
func (n *VDFNode) Get(path ...string) *VDFNode {
	cur := n
	for _, key := range path {
		if cur == nil {
			return nil
		}
		var next *VDFNode
		for _, c := range cur.Children {
			if strings.EqualFold(c.Key, key) {
				next = c
				break
			}
		}
		cur = next
	}
	return cur
}

// GetString devuelve el valor de una hoja, o "" si no existe.
func (n *VDFNode) GetString(path ...string) string {
	if node := n.Get(path...); node != nil {
		return node.Value
	}
	return ""
}

// Ensure devuelve el mapa en la ruta indicada, creando los tramos que falten.
func (n *VDFNode) Ensure(path ...string) *VDFNode {
	cur := n
	for _, key := range path {
		var next *VDFNode
		for _, c := range cur.Children {
			if strings.EqualFold(c.Key, key) {
				next = c
				break
			}
		}
		if next == nil {
			next = &VDFNode{Key: key, IsMap: true}
			cur.Children = append(cur.Children, next)
		}
		next.IsMap = true
		cur = next
	}
	return cur
}

// SetString fija (o crea) una hoja hija con esa clave.
func (n *VDFNode) SetString(key, value string) {
	for _, c := range n.Children {
		if strings.EqualFold(c.Key, key) {
			c.Value = value
			c.IsMap = false
			c.Children = nil
			return
		}
	}
	n.Children = append(n.Children, &VDFNode{Key: key, Value: value})
}

// Remove borra el hijo con esa clave. Indica si existia.
func (n *VDFNode) Remove(key string) bool {
	for i, c := range n.Children {
		if strings.EqualFold(c.Key, key) {
			n.Children = append(n.Children[:i], n.Children[i+1:]...)
			return true
		}
	}
	return false
}

// SortChildrenNumeric reordena los hijos por clave numerica. Steam guarda los
// indices de libraryfolders como "0", "1", "2"... y espera que sean correlativos.
func (n *VDFNode) SortChildrenNumeric() {
	sort.SliceStable(n.Children, func(i, j int) bool {
		return atoiSafe(n.Children[i].Key) < atoiSafe(n.Children[j].Key)
	})
}

// Marshal serializa el arbol al formato de texto de Valve, con tabuladores.
func (n *VDFNode) Marshal() []byte {
	var buf bytes.Buffer
	for _, c := range n.Children {
		c.write(&buf, 0)
	}
	return buf.Bytes()
}

func (n *VDFNode) write(buf *bytes.Buffer, depth int) {
	indent := strings.Repeat("\t", depth)
	buf.WriteString(indent)
	buf.WriteString(quoteVDF(n.Key))
	if !n.IsMap {
		buf.WriteString("\t\t")
		buf.WriteString(quoteVDF(n.Value))
		buf.WriteString("\n")
		return
	}
	buf.WriteString("\n")
	buf.WriteString(indent)
	buf.WriteString("{\n")
	for _, c := range n.Children {
		c.write(buf, depth+1)
	}
	buf.WriteString(indent)
	buf.WriteString("}\n")
}

func quoteVDF(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteByte(s[i])
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func atoiSafe(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}
