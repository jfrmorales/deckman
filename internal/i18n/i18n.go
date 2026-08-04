// Package i18n traduce a ingles y frances lo que ve el usuario.
//
// La decision de diseno que lo explica todo: **la clave es el texto en
// castellano**, no un identificador inventado.
//
//	return i18n.Errorf("no se pudo borrar %q de %s: %w", nombre, sistema, err)
//
// Con identificadores (`err.roms.delete_failed`) el codigo deja de decir que
// pasa y hay que ir al catalogo para leerlo; y cada error nuevo obliga a
// inventar un nombre, que es justo el tipo de tarea que se hace mal con prisa.
// Asi el original vive donde se usa, se lee igual que antes, y el catalogo solo
// añade los otros dos idiomas. Si falta una traduccion, sale el castellano: se
// entiende menos, pero nunca aparece una clave cruda en pantalla.
//
// El otro motivo es que un error de Go se construye en el fondo (deck, config)
// y se traduce en el borde (HTTP), cuando ya se sabe que idioma quiere quien
// mira. Por eso Mensaje guarda el formato y sus argumentos **sin juntarlos**:
// juntarlos antes seria cocer el castellano dentro de la cadena.
package i18n

import (
	"errors"
	"fmt"
	"strings"
)

// Idiomas que se ofrecen. El castellano es el original.
const (
	ES = "es"
	EN = "en"
	FR = "fr"
)

// Idiomas soportados, en el orden en que se ofrecen.
var Idiomas = []string{ES, EN, FR}

// Soportado dice si el codigo de idioma es uno de los nuestros.
func Soportado(lang string) bool {
	for _, l := range Idiomas {
		if l == lang {
			return true
		}
	}
	return false
}

// Mensaje es un error cuyo texto todavia no se ha decidido en que idioma va.
//
// Guarda el formato en castellano (la clave) y sus argumentos. Error() da el
// castellano, que es lo que ven los registros y las pruebas; Traducir(lang) lo
// rehace en otro idioma con los mismos argumentos.
type Mensaje struct {
	formato string
	args    []any
}

// Errorf crea un error traducible. Sustituye a fmt.Errorf en todo lo que pueda
// acabar delante de una persona. Admite %w igual que fmt.Errorf.
func Errorf(formato string, args ...any) error {
	return &Mensaje{formato: formato, args: args}
}

// Error devuelve el castellano: es el original y el que va a los registros.
//
// Se formatea con fmt.Errorf y no con Sprintf porque los formatos llevan %w y
// Sprintf no lo entiende: pintaria «%!w(...)» en mitad de la frase.
func (m *Mensaje) Error() string { return fmt.Errorf(m.formato, m.args...).Error() }

// Unwrap mantiene vivo errors.Is/As a traves de los %w. Sin esto, envolver un
// error con Errorf(...%w...) rompia las comprobaciones de tipo de error, que
// es un fallo silencioso y feo de encontrar.
//
// Se devuelven todos los argumentos que sean errores, no solo los que fueran
// con %w. En este codigo un error pasado como argumento siempre es la causa,
// asi que la diferencia no se nota, y la alternativa era parsear el formato.
func (m *Mensaje) Unwrap() []error {
	var envueltos []error
	for _, a := range m.args {
		if err, ok := a.(error); ok {
			envueltos = append(envueltos, err)
		}
	}
	return envueltos
}

// Formato devuelve la plantilla en castellano. Lo usan las pruebas del
// catalogo para comprobar que no falta ninguna ni sobra ninguna.
func (m *Mensaje) Formato() string { return m.formato }

// Traducir rehace el mensaje en el idioma pedido.
//
// Los argumentos que a su vez sean traducibles se traducen tambien: un error
// envuelto con %w es lo normal en este codigo, y dejarlo en castellano dentro
// de una frase en ingles queda peor que no traducir nada.
func (m *Mensaje) Traducir(lang string) string {
	formato := traduceFormato(m.formato, lang)
	args := make([]any, len(m.args))
	for i, a := range m.args {
		args[i] = traduceArg(a, lang)
	}
	return fmt.Errorf(formato, args...).Error()
}

// Traducir es el punto de entrada para un error cualquiera: si es traducible
// (o envuelve algo que lo sea) lo traduce, y si no devuelve su texto tal cual.
//
// Los errores que no son nuestros —los de la biblioteca de SSH, los del
// sistema de ficheros— salen en ingles porque asi vienen. Traducirlos exigiria
// reconocerlos por su texto, que cambia entre versiones sin avisar.
func Traducir(err error, lang string) string {
	if err == nil {
		return ""
	}
	if lang == ES || !Soportado(lang) {
		return err.Error()
	}
	var m *Mensaje
	if errors.As(err, &m) {
		return m.Traducir(lang)
	}
	return err.Error()
}

// traduceArg traduce los argumentos que son a su vez errores traducibles.
func traduceArg(a any, lang string) any {
	if err, ok := a.(error); ok {
		return errorTraducido{texto: Traducir(err, lang)}
	}
	return a
}

// errorTraducido lleva el texto ya traducido de vuelta a Sprintf. Es un error
// para que %w y %v lo pinten igual que el original; %q tambien lo entrecomilla
// como antes.
type errorTraducido struct{ texto string }

func (e errorTraducido) Error() string  { return e.texto }
func (e errorTraducido) String() string { return e.texto }

// traduceFormato busca la plantilla en el catalogo. Sin traduccion, devuelve
// el castellano: mejor entender menos que ver una clave.
func traduceFormato(formato, lang string) string {
	if cat, ok := catalogo[lang]; ok {
		if t, ok := cat[formato]; ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return formato
}

// T traduce un texto suelto (sin argumentos) del castellano al idioma pedido.
// Para los pocos sitios donde hace falta texto y no un error.
func T(texto, lang string) string {
	if lang == ES || !Soportado(lang) {
		return texto
	}
	return traduceFormato(texto, lang)
}

// Negociar elige idioma a partir de la cabecera Accept-Language del navegador.
// Devuelve "" si no reconoce ninguno, para que el llamante decida el defecto.
//
// No se implementa el RFC entero: basta con recorrer las preferencias en orden
// y quedarse con la primera que sepamos hablar. Un `q=` mal puesto no cambia
// nada aqui, y no vale la pena una dependencia por esto.
func Negociar(accept string) string {
	for _, parte := range strings.Split(accept, ",") {
		parte = strings.TrimSpace(parte)
		if i := strings.IndexByte(parte, ';'); i >= 0 {
			parte = parte[:i]
		}
		// "es-ES" vale como "es".
		if i := strings.IndexByte(parte, '-'); i >= 0 {
			parte = parte[:i]
		}
		parte = strings.ToLower(strings.TrimSpace(parte))
		if Soportado(parte) {
			return parte
		}
	}
	return ""
}
