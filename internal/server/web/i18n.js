// Traducción de la interfaz. Mismo criterio que en Go (internal/i18n): la
// clave es el texto en castellano, tal cual aparece escrito. Ver el porqué
// largo en i18n.go; en resumen, el HTML sigue leyéndose solo y no hay que
// inventar identificadores.
//
// El HTML no lleva marcas `data-i18n`. En vez de eso se recorre el DOM y se
// traduce cada nodo de texto buscándolo en el catálogo. Marcar 141 sitios a
// mano era trabajo mecánico con muchas ocasiones de equivocarse, y además
// obligaba a tocar el HTML cada vez que se añade una frase.
//
// El original se guarda en el propio nodo la primera vez (`__es`), porque tras
// traducir a francés ya no se puede volver a buscar la clave: el texto que hay
// en pantalla ya no es la clave. Sin esto, cambiar de idioma dos veces dejaba
// la interfaz a medias.

const I18N = {
  lang: 'es',

  // Atributos con texto visible. `value` solo en botones: en un input es dato.
  ATTRS: ['placeholder', 'title', 'aria-label'],

  t(texto, ...args) {
    let s = this.lang === 'es' ? texto : (CATALOGO[this.lang]?.[texto] ?? texto);
    // Huecos {0}, {1}… en vez de plantillas: una cadena traducible no puede
    // llevar la interpolación dentro, y el orden de los huecos cambia entre
    // idiomas.
    return s.replace(/\{(\d+)\}/g, (m, i) => (args[i] !== undefined ? args[i] : m));
  },

  // Traduce el documento entero. Idempotente: se puede llamar al cambiar de
  // idioma tantas veces como haga falta.
  aplicar(lang) {
    this.lang = CATALOGO[lang] || lang === 'es' ? lang : 'es';
    document.documentElement.lang = this.lang;
    this.traducirNodo(document.body);
    this.traducirNodo(document.head);   // el <title>
  },

  traducirNodo(raiz) {
    if (!raiz) return;

    const paseo = document.createTreeWalker(raiz, NodeFilter.SHOW_TEXT);
    const nodos = [];
    for (let n = paseo.nextNode(); n; n = paseo.nextNode()) nodos.push(n);
    for (const n of nodos) {
      // El texto dentro de <script>/<style> no es texto de interfaz.
      const padre = n.parentNode && n.parentNode.nodeName;
      if (padre === 'SCRIPT' || padre === 'STYLE') continue;

      if (n.__es === undefined) {
        const limpio = n.nodeValue.trim();
        if (!limpio) continue;
        n.__es = limpio;
      }
      const traducido = this.t(n.__es);
      if (traducido !== n.__es || n.nodeValue.trim() !== n.__es) {
        // Se conservan los espacios de alrededor: en frases partidas por un
        // <a> o un <code>, quitarlos pega las palabras.
        n.nodeValue = n.nodeValue.replace(n.nodeValue.trim(), traducido);
      }
    }

    const elems = raiz.querySelectorAll ? raiz.querySelectorAll('*') : [];
    for (const el of elems) {
      for (const attr of this.ATTRS) {
        if (!el.hasAttribute(attr)) continue;
        const clave = '__es_' + attr;
        if (el[clave] === undefined) el[clave] = el.getAttribute(attr);
        el.setAttribute(attr, this.t(el[clave]));
      }
    }
  },
};

// t() suelta, que es como se usa desde app.js.
function t(texto, ...args) { return I18N.t(texto, ...args); }
