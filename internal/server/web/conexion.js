'use strict';

// Conexión con la Deck: la tarjeta de conectar, las Decks guardadas, el estado
// general (/api/state), el idioma y salir.

// ---------- conexión ----------

// Con la Deck ya conectada, el botón "Cambiar" vuelve a sacar el formulario
// para apuntar a otra: es lo único que puede mostrar la tarjeta estando
// conectado, y cualquier pestaña lo deshace.
let showConnectForm = false;

// applyConnState decide de una vez lo que depende de la conexión: qué se ve
// (tarjeta o contenido), el chip de la barra y si las pestañas responden. Todo
// junto porque antes se olvidaba siempre alguna de las tres.
function applyConnState() {
  const on = !!state.connected;
  const form = !on || showConnectForm;

  document.body.classList.toggle('showconn', form);
  $('status').textContent = on ? `${state.user || 'deck'}@${state.host || ''}` : t('sin conexión');
  $('status').classList.toggle('mono', on);
  $('connLed').className = 'led ' + (on ? 'on' : 'off');
  $('btnChange').classList.toggle('hidden', !on);

  document.querySelectorAll('.tab').forEach((tab) => {
    tab.disabled = !on;
    tab.title = on ? '' : t('Conecta primero con la Deck');
  });
}

function connError(msg) {
  const el = $('connError');
  el.textContent = msg || '';
  el.classList.toggle('hidden', !msg);
}

// ---------- Decks guardadas ----------

// renderDecks pinta la lista de Decks recordadas. Se construye con el DOM y no
// con innerHTML a propósito: el nombre y el usuario los escribe una persona y
// acabarían interpretados como HTML.
function renderDecks() {
  const decks = state.decks || [];
  $('savedDecks').classList.toggle('hidden', decks.length === 0);
  const ul = $('deckList');
  ul.textContent = '';

  decks.forEach((d, i) => {
    const li = document.createElement('li');
    li.className = 'deckrow' + (i === state.active ? ' active' : '');

    const usar = document.createElement('button');
    usar.className = 'deckpick';
    usar.onclick = () => useDeck(i);
    usar.title = state.hasKey
      ? t('Conectar con esta Deck')
      : t('Rellenar los datos de esta Deck');

    const nom = document.createElement('span');
    nom.className = 'deckname';
    nom.textContent = d.name || d.host;
    const sub = document.createElement('span');
    sub.className = 'decksub mono';
    // Con nombre puesto, el host va debajo; sin él ya es el título y repetirlo
    // no aporta nada.
    sub.textContent = d.name ? `${d.user}@${d.host}${d.port === 22 ? '' : ':' + d.port}`
                             : `${d.user}${d.port === 22 ? '' : ' · ' + t('puerto {0}', d.port)}`;
    usar.append(nom, sub);

    const olvidar = document.createElement('button');
    olvidar.className = 'deckforget';
    olvidar.textContent = t('Olvidar');
    olvidar.title = t('Quitarla y retirarle la clave SSH de deckman');
    olvidar.onclick = () => forgetDeck(i);

    li.append(usar, olvidar);
    ul.append(li);
  });
}

// useDeck rellena el formulario con una Deck guardada. Si ya hay clave SSH
// instalada conecta directamente, que es el caso normal: para eso se instaló.
function useDeck(i) {
  const d = (state.decks || [])[i];
  if (!d) return;
  $('host').value = d.host;
  $('user').value = d.user || 'deck';
  $('port').value = d.port || 22;
  $('deckName').value = d.name || '';
  connError('');
  if (state.hasKey) connect();
  else $('password').focus();
}

// forgetDeck la quita de la lista y le retira la clave SSH. Se avisa de lo que
// implica de verdad: sin la clave, esa Deck vuelve a pedir contraseña.
async function forgetDeck(i) {
  const d = (state.decks || [])[i];
  if (!d) return;
  const nombre = d.name ? `${d.name} (${d.host})` : d.host;
  const ok = await confirmChanges({
    title: t('Olvidar esta Deck'),
    summary: t('¿Olvidar {0}?', nombre) + '\n\n' +
      t('Se retirará de esa Deck la clave SSH que instaló deckman, así que dejará de tener acceso desde este PC. La próxima vez habrá que volver a conectar con la contraseña.'),
    note: t('No se toca ningún juego.'),
    confirmLabel: t('Olvidar'),
    danger: true,
  });
  if (!ok) return;

  try {
    const r = await api('/api/decks/forget', { index: i });
    if (r.aviso) banner(r.aviso, 'warn');
    else if (r.revocada) banner(t('{0} olvidada y clave SSH retirada.', nombre), 'ok');
    else banner(t('{0} olvidada.', nombre), 'ok');
    inventory = null;
    await refreshState();
    applyConnState();
  } catch (e) {
    banner(t('No se pudo olvidar la Deck: {0}', e.message), 'err');
  }
}

async function refreshState() {
  try {
    state = await api('/api/state');
    // El idioma va antes que nada: si no, se ve un parpadeo de castellano.
    // idiomaEfectivo lo decide el servidor (lo elegido o, si no hay nada, lo
    // que pide el navegador), así que aquí no se vuelve a decidir.
    aplicarIdioma(state.idiomaEfectivo, state.idioma);
    $('host').value = state.host || '';
    $('user').value = state.user || 'deck';
    $('port').value = state.port || 22;
    $('appVersion').textContent = state.version || '';
    $('password').placeholder = state.hasKey
      ? t('clave SSH instalada — no hace falta')
      : t('contraseña del usuario deck');
    updateJobButtons(); // puede haber un trabajo en curso de antes de recargar
    renderUpdateUI();
    if (state.update && state.update.status === 'applying') {
      handleUpdateEvent(state.update);
    }
    if (state.connected && !inventory) loadInventory();
  } catch (e) { /* el servidor aún arrancando */ }
}

// aplicarIdioma repinta la interfaz y deja el desplegable marcando lo que hay
// guardado, que puede ser "" (automático) aunque se esté viendo en francés.
function aplicarIdioma(efectivo, elegido) {
  I18N.aplicar(efectivo || 'es');
  const sel = $('idioma');
  if (sel && sel.value !== (elegido || '')) sel.value = elegido || '';
  // Lo pintado por JS (la tabla de juegos) no lo alcanza el recorrido del DOM
  // porque se genera después: se vuelve a pintar lo que haya.
  if (inventory) { renderStorage(); renderGames(); renderSendOptions(); }
  renderDecks();
  pintarOperaciones();
  // Y los textos sueltos que escribe JavaScript: un nodo que ya nació
  // traducido no tiene clave en castellano por la que volver a buscarlo, así
  // que al cambiar de idioma se quedaría en el anterior.
  applyConnState();
  updateArtworkHint();
  renderUpdateUI();
  if (!inventory) $('emptyLib').textContent = emptyLibText();
}

async function cambiarIdioma() {
  const lang = $('idioma').value;
  try {
    const r = await api('/api/settings', { idioma: lang });
    aplicarIdioma(r.idiomaEfectivo, r.idioma);
  } catch (e) {
    banner(t('No se pudo cambiar el idioma: {0}', e.message), 'err');
  }
}

// connect abre la sesión con la Deck.
//
// confiarClave solo se pone al reintentar después de que el usuario haya visto
// el aviso de clave de host cambiada y haya dicho que sí. Va como parámetro con
// nombre y no posicional porque el botón llama con `() => connect()`: si se
// enganchara directamente al onclick, el MouseEvent llegaría como primer
// argumento y sería truthy, o sea, confiaría siempre.
async function connect({ confiarClave = false } = {}) {
  const btn = $('btnConnect');
  btn.disabled = true;
  btn.textContent = t('Conectando…');
  $('connLed').className = 'led busy';
  connError('');
  banner(t('Conectando…'));
  const host = $('host').value.trim();
  try {
    const r = await api('/api/connect', {
      host,
      user: $('user').value.trim() || 'deck',
      name: $('deckName').value.trim(),
      password: $('password').value,
      // El puerto ya no está fijado a 22: hay Decks con SSH movido de sitio.
      port: parseInt($('port').value, 10) || 22,
      confiarClave,
    });
    $('password').value = '';
    showConnectForm = false;
    banner(r.note || t('Conectado.'), 'ok');
    await refreshState();
    await loadInventory();
  } catch (e) {
    // Clave de host distinta de la recordada: es la única situación en la que
    // hay algo que decidir, así que se pregunta con las dos huellas delante en
    // vez de dejar un error del que no se sale. Solo se ofrece una vez: si el
    // reintento con confiarClave vuelve a fallar, es otra cosa.
    if (e.data && e.data.claveHostCambio && !confiarClave) {
      btn.disabled = false;
      btn.textContent = t('Conectar');
      // En diálogo propio y no en un confirm() del navegador: es la decisión
      // más delicada de la aplicación y ahí las dos huellas salían como texto
      // corrido, sin tipografía de ancho fijo para compararlas.
      const seguir = await confirmChanges({
        title: t('La clave SSH ha cambiado'),
        summary: t('La Deck de {0} presenta una clave SSH distinta de la que deckman recuerda.', host),
        items: [
          { path: e.data.huellaPresentada || '?', desc: t('La que presenta ahora') },
          { path: e.data.huellaRecordada || '?', desc: t('La que deckman recordaba') },
        ],
        note: t('Si acabas de reinstalar SteamOS, esto es lo normal: acepta y deckman recordará la nueva.') + '\n\n' +
          t('Si no has hecho nada parecido, cancela: puede que quien conteste en esa dirección no sea tu Deck, y conectar le entregaría la contraseña.'),
        confirmLabel: t('Confiar en la clave nueva'),
        danger: true,
      });
      if (seguir) return connect({ confiarClave: true });
    }
    // El error va también dentro de la tarjeta: el banner de arriba queda
    // lejos del botón que se acaba de pulsar y pasa desapercibido.
    connError(t('No se pudo conectar: {0}', e.message));
    banner(t('No se pudo conectar: {0}', e.message), 'err');
    applyConnState();
  } finally {
    btn.disabled = false;
    btn.textContent = t('Conectar');
  }
}

// ---------- salir ----------

// Lanzado desde el escritorio (Flatpak, acceso directo) no hay consola con
// Ctrl+C: este boton es la unica forma visible de apagar el servidor.
async function quit() {
  const ok = await confirmChanges({
    title: t('Salir'),
    summary: t('¿Cerrar deckman? Se apaga el servidor local de este PC.'),
    confirmLabel: t('Salir'),
  });
  if (!ok) return;
  try {
    await api('/api/quit', {});
    document.body.innerHTML =
      `<div class="goodbye"><p>${escapeHtml(t('deckman se ha cerrado.'))}</p><p class="hint">${escapeHtml(t('Ya puedes cerrar esta pestaña.'))}</p></div>`;
    window.close(); // solo funciona si la pestaña la abrio deckman; si no, queda la despedida
  } catch (e) {
    banner(t('No se puede salir: {0}', e.message), 'err');
  }
}
