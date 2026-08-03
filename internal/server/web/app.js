'use strict';

const $ = (id) => document.getElementById(id);

let state = { connected: false, os: 'linux' };
let inventory = null;
let sortBy = 'name';
let sortDir = 1;

// ---------- utilidades ----------

async function api(path, body) {
  const opts = { headers: { 'X-Deckman': '1' } };
  if (body !== undefined) {
    opts.method = 'POST';
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch { data = { error: text }; }
  if (!res.ok) throw new Error(data.error || `error ${res.status}`);
  return data;
}

function fmtBytes(b) {
  if (!b) return '—';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return `${b.toFixed(i >= 2 ? 1 : 0)} ${u[i]}`;
}

function fmtDate(ts) {
  if (!ts) return 'nunca';
  return new Date(ts * 1000).toLocaleDateString('es-ES');
}

function banner(msg, kind) {
  const el = $('banner');
  el.textContent = msg;
  el.className = 'banner' + (kind ? ' ' + kind : '');
  if (!msg) el.classList.add('hidden');
  clearTimeout(banner._t);
  if (msg && kind === 'ok') banner._t = setTimeout(() => banner(''), 6000);
}

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
  $('status').textContent = on ? `${state.user || 'deck'}@${state.host || ''}` : 'sin conexión';
  $('status').classList.toggle('mono', on);
  $('connLed').className = 'led ' + (on ? 'on' : 'off');
  $('btnChange').classList.toggle('hidden', !on);

  document.querySelectorAll('.tab').forEach((t) => {
    t.disabled = !on;
    t.title = on ? '' : 'Conecta primero con la Deck';
  });
}

function connError(msg) {
  const el = $('connError');
  el.textContent = msg || '';
  el.classList.toggle('hidden', !msg);
}

async function refreshState() {
  try {
    state = await api('/api/state');
    $('host').value = state.host || '';
    $('user').value = state.user || 'deck';
    $('port').value = state.port || 22;
    $('appVersion').textContent = state.version || '';
    $('password').placeholder = state.hasKey
      ? 'clave SSH instalada — no hace falta'
      : 'contraseña del usuario deck';
    applyConnState();
    updateArtworkHint();
    updateJobButtons(); // puede haber un trabajo en curso de antes de recargar
    if (state.connected && !inventory) loadInventory();
  } catch (e) { /* el servidor aún arrancando */ }
}

async function connect() {
  const btn = $('btnConnect');
  btn.disabled = true;
  btn.textContent = 'Conectando…';
  $('connLed').className = 'led busy';
  connError('');
  banner('Conectando…');
  try {
    const r = await api('/api/connect', {
      host: $('host').value.trim(),
      user: $('user').value.trim() || 'deck',
      password: $('password').value,
      // El puerto ya no está fijado a 22: hay Decks con SSH movido de sitio.
      port: parseInt($('port').value, 10) || 22,
    });
    $('password').value = '';
    showConnectForm = false;
    banner(r.note || 'Conectado.', 'ok');
    await refreshState();
    await loadInventory();
  } catch (e) {
    // El error va también dentro de la tarjeta: el banner de arriba queda
    // lejos del botón que se acaba de pulsar y pasa desapercibido.
    connError('No se pudo conectar: ' + e.message);
    banner('No se pudo conectar: ' + e.message, 'err');
    applyConnState();
  } finally {
    btn.disabled = false;
    btn.textContent = 'Conectar';
  }
}

// ---------- inventario ----------

async function loadInventory() {
  $('emptyLib').textContent = 'Leyendo la Deck…';
  $('emptyLib').classList.remove('hidden');
  try {
    inventory = await api('/api/inventory');
    renderStorage();
    renderGames();
    renderSendOptions();
    banner('');
  } catch (e) {
    banner('No se pudo leer la biblioteca: ' + e.message, 'err');
    $('emptyLib').textContent = 'Sin datos.';
  }
}

function renderStorage() {
  const el = $('storage');
  el.innerHTML = '';
  for (const lib of inventory.libraries || []) {
    const pct = lib.total ? (lib.used / lib.total) * 100 : 0;
    const div = document.createElement('div');
    div.className = 'disk';
    div.innerHTML = `
      <h3>${escapeHtml(lib.label)}
        <span class="badge ${lib.removable ? 'sd' : ''}">${lib.removable ? 'microSD' : 'interno'}</span>
      </h3>
      <div class="nums">${fmtBytes(lib.used)} usados · <strong>${fmtBytes(lib.free)} libres</strong> de ${fmtBytes(lib.total)}</div>
      <div class="bar ${pct > 92 ? 'full' : ''} ${lib.removable ? 'sd' : ''}"><div style="width:${pct.toFixed(1)}%"></div></div>`;
    el.appendChild(div);
  }
  $('steamWarn').classList.toggle('hidden', !inventory.steamRunning);
}

function visibleGames() {
  const q = $('filter').value.trim().toLowerCase();
  const wantSteam = $('showSteam').checked;
  const wantNon = $('showNonSteam').checked;
  let list = (inventory.games || []).filter((g) => {
    if (g.kind === 'steam' && !wantSteam) return false;
    if (g.kind === 'nonsteam' && !wantNon) return false;
    return !q || (g.name || '').toLowerCase().includes(q);
  });
  list.sort((a, b) => {
    let r;
    if (sortBy === 'size') r = (a.size || 0) - (b.size || 0);
    else r = (a.name || '').localeCompare(b.name || '', 'es');
    return r * sortDir;
  });
  return list;
}

// emptyLibText dice por qué la tabla está vacía y cómo salir de ahí: "ningún
// juego coincide con el filtro" no ayuda si lo que sobra es un check apagado.
function emptyLibText() {
  if (!inventory) return 'Conecta con la Deck para ver la biblioteca.';
  const total = (inventory.games || []).length;
  const cuantos = total === 1 ? 'el único juego' : `los ${total} juegos`;
  const q = $('filter').value.trim();
  if (!total) return 'La Deck no tiene juegos instalados.';
  if (q) return `Ningún juego coincide con «${q}». Borra el filtro para ver ${cuantos}.`;
  if (!$('showSteam').checked || !$('showNonSteam').checked) {
    return `Los filtros Steam / No-Steam esconden ${cuantos}. Marca alguna casilla para verlos.`;
  }
  return 'Ningún juego que mostrar.';
}

// moveTarget dice a qué unidad se puede mover un juego, o null si no procede.
//
// Los de Steam se mueven siempre que haya otra unidad. Los no-Steam necesitan
// dos cosas: que se sepa cuál es su carpeta (gameRoot, que el servidor solo
// rellena si está dentro de un Games) y que el juego se lance desde dentro de
// ella. Un acceso directo puede arrancar otra cosa —Heroic lanza «flatpak», los
// de EmuDeck un emulador que vive fuera—, y ahí mover la carpeta no arreglaría
// nada.
function moveTarget(g, libs) {
  if (!g.libraryPath) return null;
  const target = libs.find((l) => l.path !== g.libraryPath);
  if (!target) return null;
  if (g.kind === 'steam') return target;
  if (!g.gameRoot || !g.exe) return null;
  return g.exe.startsWith(g.gameRoot + '/') ? target : null;
}

function renderGames() {
  const body = $('gamesBody');
  body.innerHTML = '';
  const list = visibleGames();
  $('emptyLib').classList.toggle('hidden', list.length > 0);
  if (!list.length) {
    $('emptyLib').textContent = emptyLibText();
    return;
  }

  const libs = inventory.libraries || [];
  const tools = inventory.compatTools || [];

  for (const g of list) {
    const tr = document.createElement('tr');

    const lib = libs.find((l) => l.path === g.libraryPath);
    const locLabel = lib ? lib.label : (g.libraryPath || g.startDir || '—');

    // Selector de Proton. Si el juego tiene asignada una versión que ya no
    // está instalada, la añadimos igualmente para no cambiarla sin querer al
    // tocar cualquier otra cosa de la fila.
    let compatCell = '<span class="loc">—</span>';
    if (tools.length) {
      const shown = tools.slice();
      if (g.compatTool && !shown.some((t) => t.name === g.compatTool)) {
        shown.push({ name: g.compatTool, displayName: g.compatTool + ' (no instalado)' });
      }
      const opts = ['<option value="">Por defecto</option>']
        .concat(shown.map((t) =>
          `<option value="${escapeHtml(t.name)}"${t.name === g.compatTool ? ' selected' : ''}>${escapeHtml(t.displayName)}</option>`));
      compatCell = `<select class="compat" data-appid="${escapeHtml(g.appId)}">${opts.join('')}</select>`;
    }

    const extras = [];
    if (g.compatSize) extras.push(`prefijo ${fmtBytes(g.compatSize)}`);
    if (g.shaderSize) extras.push(`shaders ${fmtBytes(g.shaderSize)}`);

    const target = moveTarget(g, libs);
    const moveBtn = target
      ? `<button data-move="${escapeHtml(g.appId)}" data-target="${escapeHtml(target.path)}" title="Mover a ${escapeHtml(target.label)}">→ ${escapeHtml(target.label)}</button>`
      : '';

    // La ruta completa solo en los no-Steam: en los de Steam no aporta nada
    // (siempre es steamapps/common) y en los otros es donde está de verdad.
    const locSub = g.kind === 'nonsteam' && g.startDir
      ? `<div class="loc locpath" title="${escapeHtml(g.startDir)}">${escapeHtml(g.startDir)}</div>` : '';

    tr.innerHTML = `
      <td class="covercell">${coverCell(g)}</td>
      <td>
        <div class="gname">
          <span>${escapeHtml(g.name || '(sin nombre)')}</span>
          <span class="kind ${g.kind === 'nonsteam' ? 'ns' : ''}">${g.kind === 'nonsteam' ? 'no-Steam' : 'Steam'}</span>
        </div>
        <div class="loc">jugado: ${fmtDate(g.lastPlayed)}</div>
      </td>
      <td class="num">${fmtBytes(g.size)}</td>
      <td class="locc"><span class="loc">${escapeHtml(locLabel)}</span>${locSub}</td>
      <td class="protonc">${compatCell}</td>
      <td class="num extrasc"><span class="loc">${extras.join('<br>') || '—'}</span></td>
      <td><div class="acts">
        ${moveBtn}<button class="danger" data-del="${escapeHtml(g.appId)}">Eliminar</button>
      </div></td>`;
    body.appendChild(tr);
  }
  watchCovers();
  updateJobButtons(); // los botones de mover se acaban de recrear
}

// ---------- portadas ----------
//
// La miniatura no viene en el inventario: se pide juego a juego a /api/cover,
// que la trae de la Deck por SSH. Cada una se queda en un blob mientras dure la
// sesión, porque la tabla se vuelve a pintar entera con cada tecla del filtro y
// volver a bajarlas por SSH cada vez sería absurdo.

const covers = new Map();      // appId -> objectURL, o null si no hay ninguna
const coversPedidas = new Set();
const coverCola = [];
let coverEnCurso = 0;
const COVER_A_LA_VEZ = 3;      // son descargas por SSH: mejor no atropellarlas

// Solo se piden las portadas de las filas que se ven (o están a punto): con
// cuarenta juegos, bajarlas todas de golpe al abrir es tráfico que nadie mira.
const coverObserver = new IntersectionObserver((entries) => {
  for (const e of entries) {
    if (!e.isIntersecting) continue;
    coverObserver.unobserve(e.target);
    pedirCover(e.target.dataset.art);
  }
}, { rootMargin: '300px' });

// steamCoverURL es el último recurso para los juegos de Steam: la portada de la
// tienda, la misma que enseña Steam. Hace falta porque la caché local de la
// Deck no está completa —solo guarda la de los juegos cuya ficha se ha abierto
// alguna vez—, y un juego instalado sin portada queda muy pobre. Los no-Steam
// no están en la tienda, así que ahí no hay red que valga.
function steamCoverURL(g) {
  if (g.kind !== 'steam') return '';
  return `https://cdn.cloudflare.steamstatic.com/steam/apps/${encodeURIComponent(g.appId)}/library_600x900.jpg`;
}

function coverCell(g) {
  const url = covers.get(g.appId);
  const inicial = (g.name || '?').trim().charAt(0).toUpperCase() || '?';
  const cdn = g.hasCover ? '' : steamCoverURL(g);
  // hasCover lo dice el servidor al escanear la Deck: si no hay nada que traer,
  // ni se pide y se tira de la tienda o, en su defecto, de la inicial.
  return `<button class="cover${url ? ' has' : ''}" data-art="${escapeHtml(g.appId)}"${g.hasCover ? ' data-cover="1"' : ''}
      title="Carátulas de ${escapeHtml(g.name || '')}">
      <img alt=""${url ? ` src="${url}"` : ''}${cdn ? ` data-cdn="${escapeHtml(cdn)}"` : ''}>
      <span class="ph">${escapeHtml(inicial)}</span>
      <span class="cta">Arte</span>
    </button>`;
}

function watchCovers() {
  document.querySelectorAll('#gamesBody .cover[data-cover]').forEach((el) => {
    if (covers.has(el.dataset.art)) return; // ya resuelta, o ya sabemos que no hay
    coverObserver.observe(el);
  });
  // La de la tienda la pinta el propio navegador. Solo se marca como puesta
  // cuando ha cargado de verdad: sin conexión se queda la inicial en vez de un
  // recuadro roto.
  document.querySelectorAll('#gamesBody .cover img[data-cdn]').forEach((img) => {
    img.onload = () => img.parentElement.classList.add('has');
    img.src = img.dataset.cdn;
    img.removeAttribute('data-cdn');
  });
}

function pedirCover(appId) {
  if (!appId || covers.has(appId) || coversPedidas.has(appId)) return;
  coversPedidas.add(appId);
  coverCola.push(appId);
  siguienteCover();
}

function siguienteCover() {
  while (coverEnCurso < COVER_A_LA_VEZ && coverCola.length) {
    const appId = coverCola.shift();
    coverEnCurso++;
    traerCover(appId).finally(() => {
      coverEnCurso--;
      coversPedidas.delete(appId);
      siguienteCover();
    });
  }
}

async function traerCover(appId) {
  try {
    const res = await fetch('/api/cover?appId=' + encodeURIComponent(appId), {
      headers: { 'X-Deckman': '1' },
    });
    if (!res.ok) throw new Error('sin portada');
    covers.set(appId, URL.createObjectURL(await res.blob()));
  } catch {
    covers.set(appId, null); // se queda la inicial, que no es un error
  }
  pintarCover(appId);
}

function pintarCover(appId) {
  const url = covers.get(appId);
  if (!url) return;
  document.querySelectorAll('#gamesBody .cover').forEach((el) => {
    if (el.dataset.art !== appId) return;
    el.querySelector('img').src = url;
    el.classList.add('has');
  });
}

// olvidarCover hay que llamarlo al cambiar el arte de un juego: si no, la tabla
// seguiría enseñando la portada de antes.
function olvidarCover(appId) {
  const url = covers.get(appId);
  if (url) URL.revokeObjectURL(url);
  covers.delete(appId);
}

function renderSendOptions() {
  $('gamesDirLabel').textContent = inventory.gamesDir || '~/Games';

  const sel = $('sendProton');
  const cur = sel.value;
  sel.innerHTML = '<option value="">Ninguno (ejecutable nativo de Linux)</option>';
  for (const t of inventory.compatTools || []) {
    const o = document.createElement('option');
    o.value = t.name;
    o.textContent = t.displayName;
    sel.appendChild(o);
  }
  // Por defecto Proton Experimental, que es lo que mejor funciona en la Deck.
  const exp = (inventory.compatTools || []).find((t) => t.name === 'proton_experimental');
  sel.value = cur || (exp ? exp.name : '');

  const rs = $('romSystem');
  rs.innerHTML = '';
  for (const s of inventory.romSystems || []) {
    const o = document.createElement('option');
    o.value = s; o.textContent = s;
    rs.appendChild(o);
  }
  $('romsDirLabel').textContent = inventory.romsDir || 'no se encontró EmuDeck';
  updateJobButtons();
}

function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// ---------- explorador de ficheros ----------

let br = {
  target: null,      // id del input a rellenar
  mode: 'dir',       // dir | any | rel
  base: '',          // en modo rel: raíz de la que colgar la ruta relativa
  path: '',          // carpeta actual
  entries: [],       // contenido de la carpeta actual
  selected: null,    // entrada elegida
  places: null,      // accesos rápidos (se piden una vez)
};

async function openBrowser(targetId, mode) {
  br.target = targetId;
  br.mode = mode;
  br.selected = null;
  br.base = '';

  let start = state.lastLocal || '';
  if (mode === 'rel') {
    br.base = $('sendPath').value;
    if (!br.base) { banner('Elige primero la carpeta del juego.', 'err'); return; }
    start = br.base;
  }

  $('brTitle').textContent = mode === 'rel' ? 'Elegir el ejecutable'
    : (mode === 'any' ? 'Elegir fichero o carpeta' : 'Elegir carpeta');
  $('brHint').textContent = 'un clic selecciona · doble clic entra o elige';
  $('brFilter').value = '';
  $('browser').classList.remove('hidden');

  if (!br.places) {
    try { br.places = (await api('/api/places')).places || []; } catch { br.places = []; }
  }
  renderPlaces();
  await browseTo(start);
  $('brList').focus();
}

function closeBrowser() {
  $('browser').classList.add('hidden');
  $('brPathInput').classList.add('hidden');
  $('brCrumbs').classList.remove('hidden');
}

const PLACE_ICON = { home: '🏠', recent: '🕘', drive: '💾', dir: '📁' };

function renderPlaces() {
  $('brPlaces').innerHTML = (br.places || []).map((p) => `
    <button class="place" data-path="${escapeHtml(p.path)}" title="${escapeHtml(p.path)}">
      <span>${PLACE_ICON[p.kind] || '📁'}</span><span class="pname">${escapeHtml(p.name)}</span>
    </button>`).join('');
  $('brPlaces').querySelectorAll('.place').forEach((b) => {
    b.onclick = () => browseTo(b.dataset.path);
  });
}

// Migas de pan: cada tramo de la ruta es pulsable, para no tener que subir de
// uno en uno hasta la raíz.
function renderCrumbs(path) {
  const el = $('brCrumbs');
  if (path === '@roots') {
    el.innerHTML = '<span class="crumb cur">Equipo</span>';
    return;
  }
  const win = /^[A-Za-z]:[\\/]/.test(path);
  const sep = win ? '\\' : '/';
  const parts = path.split(/[\\/]+/).filter(Boolean);
  const crumbs = [];
  if (win) {
    crumbs.push({ label: parts[0] || path, path: (parts[0] || '') + sep });
    for (let i = 1; i < parts.length; i++) {
      crumbs.push({ label: parts[i], path: parts[0] + sep + parts.slice(1, i + 1).join(sep) });
    }
  } else {
    crumbs.push({ label: '/', path: '/' });
    for (let i = 0; i < parts.length; i++) {
      crumbs.push({ label: parts[i], path: '/' + parts.slice(0, i + 1).join('/') });
    }
  }
  el.innerHTML = crumbs.map((c, i) =>
    `<button class="crumb${i === crumbs.length - 1 ? ' cur' : ''}" data-path="${escapeHtml(c.path)}">${escapeHtml(c.label)}</button>`
  ).join('<span class="csep">›</span>');
  el.querySelectorAll('.crumb').forEach((b) => {
    b.onclick = () => browseTo(b.dataset.path);
  });
  el.scrollLeft = el.scrollWidth;
}

async function browseTo(path) {
  try {
    const r = await api('/api/browse?path=' + encodeURIComponent(path || ''));
    br.path = r.path;
    br.parent = r.parent;
    br.entries = r.entries || [];
    br.selected = null;
    renderCrumbs(r.path);
    $('brPathInput').value = r.path === '@roots' ? '' : r.path;
    renderEntries();
  } catch (e) {
    $('brList').innerHTML = `<li class="empty">${escapeHtml(e.message)}</li>`;
  }
}

// selectable indica si una entrada se puede elegir en el modo actual.
function selectable(e) {
  if (br.mode === 'dir') return e.isDir;
  if (br.mode === 'rel') return !e.isDir;
  return true;
}

function renderEntries() {
  const q = $('brFilter').value.trim().toLowerCase();
  const list = br.entries.filter((e) => !q || e.name.toLowerCase().includes(q));
  const ul = $('brList');

  if (!list.length) {
    ul.innerHTML = `<li class="empty">${br.entries.length ? 'Nada coincide con el filtro.' : 'Carpeta vacía.'}</li>`;
    updateFooter();
    return;
  }

  ul.innerHTML = list.map((e, i) => {
    // Apagamos solo lo que no sirve para nada aquí. Las carpetas nunca: aunque
    // no se puedan elegir (modo ejecutable), sí se puede entrar en ellas, y
    // pintarlas grises haría pensar lo contrario.
    const dim = (selectable(e) || e.isDir) ? '' : ' dim';
    const exe = br.mode === 'rel' && /\.exe$/i.test(e.name) ? ' exe' : '';
    return `<li class="brrow${dim}${exe}" data-i="${i}">
      <span class="ic">${e.isDir ? '📁' : (exe ? '⚙️' : '📄')}</span>
      <span class="nm">${escapeHtml(e.name)}</span>
      ${e.isDir ? '<span class="enter" title="Entrar">›</span>' : `<span class="sz">${fmtBytes(e.size)}</span>`}
    </li>`;
  }).join('');

  ul.querySelectorAll('.brrow').forEach((li) => {
    const e = list[+li.dataset.i];
    li.onclick = (ev) => {
      if (ev.target.classList.contains('enter')) { browseTo(e.path); return; }
      selectEntry(e, li);
    };
    li.ondblclick = () => {
      if (e.isDir && br.mode !== 'dir') { browseTo(e.path); return; }
      if (e.isDir && br.mode === 'dir') { browseTo(e.path); return; }
      if (selectable(e)) confirmPick(e);
    };
  });
  updateFooter();
}

function selectEntry(e, li) {
  if (!selectable(e)) return;
  br.selected = e;
  $('brList').querySelectorAll('.brrow').forEach((x) => x.classList.remove('sel'));
  if (li) li.classList.add('sel');
  updateFooter();
}

function updateFooter() {
  const pick = $('brPick');
  if (br.selected) {
    $('brSelected').textContent = 'Elegido: ' + br.selected.name;
    pick.disabled = false;
    pick.textContent = 'Seleccionar';
  } else if (br.mode === 'dir' && br.path && br.path !== '@roots') {
    // Sin nada marcado, el botón elige la carpeta en la que estás.
    $('brSelected').textContent = 'Se usará la carpeta actual';
    pick.disabled = false;
    pick.textContent = 'Usar esta carpeta';
  } else {
    $('brSelected').textContent = '';
    pick.disabled = true;
    pick.textContent = 'Seleccionar';
  }
}

function confirmPick(entry) {
  const e = entry || br.selected;
  if (!e) {
    // Modo carpeta sin selección: vale la carpeta actual.
    if (br.mode === 'dir' && br.path && br.path !== '@roots') {
      applyPick(br.path, br.path.split(/[\\/]/).filter(Boolean).pop() || '');
    }
    return;
  }
  if (!selectable(e)) return;
  applyPick(e.path, e.name);
}

function applyPick(fullPath, name) {
  if (br.mode === 'rel') {
    let rel = fullPath;
    if (fullPath.startsWith(br.base)) rel = fullPath.slice(br.base.length).replace(/^[\\/]+/, '');
    $(br.target).value = rel;
  } else {
    $(br.target).value = fullPath;
  }
  const target = br.target;
  closeBrowser();
  if (target === 'sendPath') runDetect();
}

// Teclado: moverse con las flechas, entrar con Enter, subir con Retroceso.
function browserKeys(ev) {
  if ($('browser').classList.contains('hidden')) return;
  if (ev.target === $('brPathInput')) return;

  const rows = [...$('brList').querySelectorAll('.brrow')];
  const cur = rows.findIndex((r) => r.classList.contains('sel'));

  switch (ev.key) {
    case 'Escape': ev.preventDefault(); closeBrowser(); return;
    case 'Backspace':
      if (ev.target === $('brFilter')) return;
      ev.preventDefault(); if (br.parent) browseTo(br.parent); return;
    case 'ArrowDown':
    case 'ArrowUp': {
      ev.preventDefault();
      const dir = ev.key === 'ArrowDown' ? 1 : -1;
      let i = cur;
      for (let n = 0; n < rows.length; n++) {
        i = (i + dir + rows.length) % rows.length;
        if (!rows[i].classList.contains('dim')) break;
      }
      if (rows[i]) { rows[i].click(); rows[i].scrollIntoView({ block: 'nearest' }); }
      return;
    }
    case 'Enter': {
      ev.preventDefault();
      if (br.selected && br.selected.isDir && br.mode === 'dir') { browseTo(br.selected.path); return; }
      confirmPick();
      return;
    }
  }
  // Cualquier letra empieza a filtrar sin tener que pinchar el campo.
  if (ev.key.length === 1 && !ev.ctrlKey && !ev.altKey && ev.target !== $('brFilter')) {
    $('brFilter').focus();
  }
}

// ---------- galería de carátulas ----------
//
// Equivalente al plugin de SteamGridDB para Decky: elegir el arte entre las
// opciones disponibles, no quedarse con la primera.

const ART_KINDS = [
  { key: 'grid',  label: 'Portada vertical', aspect: '2 / 3' },
  { key: 'gridh', label: 'Portada horizontal', aspect: '92 / 43' },
  { key: 'hero',  label: 'Fondo', aspect: '96 / 31' },
  { key: 'logo',  label: 'Logo', aspect: '16 / 9' },
  { key: 'icon',  label: 'Icono', aspect: '1 / 1' },
];

let art = { appId: 0, name: '', steamAppId: '', gameId: 0, kind: 'grid', current: {}, busy: false, live: false };

// Cada petición de la galería lleva número. Si al volver ya no es la última,
// se descarta: cambiar de pestaña rápido hacía que una respuesta vieja
// sobrescribiera a la nueva y se veía el tipo equivocado.
let artRequestSeq = 0;

async function openArtwork(appId, name, steamAppId) {
  art = { appId, name, steamAppId: steamAppId || '', gameId: 0, kind: 'grid', current: {}, busy: false };
  $('artTitle').textContent = 'Carátulas — ' + name;
  $('artGameSel').innerHTML = '';
  $('artBody').innerHTML = '<p class="empty">Buscando el juego en SteamGridDB…</p>';
  $('artStatus').textContent = '';
  $('artModal').classList.remove('hidden');

  // Pestañas
  $('artTabs').innerHTML = ART_KINDS.map((k) =>
    `<button class="arttab${k.key === art.kind ? ' active' : ''}" data-kind="${k.key}">${k.label}</button>`).join('');

  // Qué hay puesto ya
  try {
    const r = await api('/api/artwork/current', { appId });
    art.current = r.current || {};
    art.live = !!r.live;
    markTabs();
    // Si Steam acepta cambios en caliente, reiniciarlo deja de hacer falta
    // para casi todo: el botón pasa a segundo plano.
    $('artRestart').classList.toggle('hidden', art.live);
    $('artLive').textContent = art.live
      ? 'Los cambios se aplican al instante, sin reiniciar Steam.'
      : 'Steam no responde en caliente: los cambios necesitarán reiniciarlo.';
  } catch { /* no es crítico */ }

  try {
    const r = await api('/api/artwork/games', { name, steamAppId: art.steamAppId });
    const games = r.games || [];
    if (!games.length) {
      $('artBody').innerHTML = `<p class="empty">SteamGridDB no encuentra "${escapeHtml(name)}".</p>`;
      return;
    }
    $('artGameSel').innerHTML = games.map((g) =>
      `<option value="${g.id}">${escapeHtml(g.name)}</option>`).join('');
    art.gameId = games[0].id;
    loadArtOptions();
  } catch (e) {
    $('artBody').innerHTML = `<p class="empty">${escapeHtml(e.message)}</p>`;
  }
}

function markTabs() {
  document.querySelectorAll('.arttab').forEach((b) => {
    b.classList.toggle('has', !!art.current[b.dataset.kind]);
    b.classList.toggle('active', b.dataset.kind === art.kind);
  });
  $('artRemove').disabled = !art.current[art.kind];
}

async function loadArtOptions() {
  if (!art.gameId) return;
  const seq = ++artRequestSeq;
  const kind = art.kind;
  const spec = ART_KINDS.find((k) => k.key === kind);
  $('artBody').innerHTML = '<p class="empty">Cargando imágenes…</p>';
  markTabs();
  try {
    const r = await api('/api/artwork/list', {
      gameId: art.gameId, kind, animated: $('artAnimated').checked,
    });
    if (seq !== artRequestSeq) return; // llegó tarde: manda otra petición
    const opts = r.options || [];
    if (!opts.length) {
      $('artBody').innerHTML = '<p class="empty">No hay imágenes de este tipo para este juego.</p>';
      return;
    }
    // Las animadas traen la miniatura como vídeo .webm: un <img> no puede
    // reproducirla y dejaría el hueco en blanco.
    const media = (o) => o.animated
      ? `<video src="${escapeHtml(o.thumb)}" autoplay loop muted playsinline preload="metadata"></video>`
      : `<img loading="lazy" src="${escapeHtml(o.thumb || o.url)}" alt="">`;

    $('artBody').innerHTML = `<div class="artgrid" style="--aspect:${spec.aspect}">` + opts.map((o, i) => `
      <figure class="artitem" data-i="${i}">
        ${media(o)}
        <figcaption>
          ${o.width && o.height ? `${o.width}×${o.height}` : ''}
          ${o.animated ? '<span class="tag anim">animada</span>' : ''}
          ${o.style && o.style !== 'alternate' ? `<span class="tag">${escapeHtml(o.style)}</span>` : ''}
        </figcaption>
      </figure>`).join('') + '</div>';

    $('artBody').querySelectorAll('.artitem').forEach((el) => {
      el.onclick = () => openPreview(opts[+el.dataset.i], el, kind);
    });
  } catch (e) {
    if (seq !== artRequestSeq) return;
    $('artBody').innerHTML = `<p class="empty">${escapeHtml(e.message)}</p>`;
  }
}

async function applyArt(opt, el, kind) {
  if (art.busy) return;
  kind = kind || art.kind;
  art.busy = true;
  $('artBody').querySelectorAll('.artitem').forEach((x) => x.classList.remove('chosen'));
  el.classList.add('working');
  $('artStatus').textContent = 'Instalando…';
  try {
    const r = await api('/api/artwork/apply', {
      appId: art.appId, kind, url: opt.url, ext: opt.ext || '',
    });
    art.current[kind] = r.file;
    olvidarCover(String(art.appId)); // la miniatura de la tabla ya no vale
    el.classList.add('chosen');
    $('artStatus').textContent = `${fmtBytes(r.bytes)} · ${r.message || 'instalada'}`;
    // Los iconos no pasan por la API de Steam y sí necesitan reinicio.
    if (!r.live) $('artRestart').classList.remove('hidden');
    markTabs();
  } catch (e) {
    $('artStatus').textContent = 'Error: ' + e.message;
  } finally {
    el.classList.remove('working');
    art.busy = false;
  }
}

// ---------- vista previa ----------

let preview = null;   // { opt, el, kind }

async function openPreview(opt, el, kind) {
  preview = { opt, el, kind };
  const spec = ART_KINDS.find((k) => k.key === kind);
  $('prevTitle').textContent = (spec ? spec.label : kind) + (opt.animated ? ' · animada' : '');

  // En las animadas mostramos la miniatura .webm: el fichero real es un .webp
  // que puede pasar de 40 MB y no tiene sentido bajarlo solo para mirarlo.
  $('prevBody').innerHTML = opt.animated
    ? `<video src="${escapeHtml(opt.thumb)}" autoplay loop muted playsinline></video>`
    : `<img src="${escapeHtml(opt.url)}" alt="">`;

  const partes = [`${opt.width}×${opt.height}`];
  if (opt.style) partes.push(opt.style);
  if (opt.author) partes.push('por ' + opt.author);
  if (opt.animated) partes.push('se muestra la previa animada; se instalará el original en alta resolución');
  $('prevMeta').textContent = partes.join(' · ');

  $('prevApply').disabled = false;
  $('prevApply').textContent = 'Aplicar esta imagen';
  $('artPreview').classList.remove('hidden');

  // El tamaño real importa: las animadas pesan decenas de MB.
  try {
    const r = await api('/api/artwork/size', { url: opt.url });
    if (preview && preview.opt === opt && r.bytes) {
      $('prevMeta').textContent = partes.join(' · ') + ` · ${fmtBytes(r.bytes)}`;
    }
  } catch { /* el tamaño es informativo */ }
}

function closePreview() {
  $('artPreview').classList.add('hidden');
  $('prevBody').innerHTML = '';   // detiene el vídeo
  preview = null;
}

async function applyFromPreview() {
  if (!preview) return;
  const { opt, el, kind } = preview;
  $('prevApply').disabled = true;
  $('prevApply').textContent = 'Instalando…';
  await applyArt(opt, el, kind);
  closePreview();
}

// ---------- reiniciar Steam ----------

async function restartSteam(btn) {
  if (!confirm('¿Reiniciar Steam en la Deck?\n\nSe cerrará cualquier juego que tengas abierto. ' +
               'Hace falta para que aparezcan las carátulas y los juegos añadidos.')) return;
  const prev = btn.textContent;
  btn.disabled = true;
  btn.textContent = 'Reiniciando…';
  try {
    const r = await api('/api/restart-steam', {});
    banner(r.message || 'Steam reiniciado.', 'ok');
  } catch (e) {
    banner('No se pudo reiniciar Steam: ' + e.message, 'err');
  } finally {
    btn.disabled = false;
    btn.textContent = prev;
  }
}

async function removeArt() {
  try {
    const r = await api('/api/artwork/remove', { appId: art.appId, kind: art.kind });
    delete art.current[art.kind];
    olvidarCover(String(art.appId));
    $('artBody').querySelectorAll('.artitem').forEach((x) => x.classList.remove('chosen'));
    $('artStatus').textContent = r.live
      ? 'Quitada. Steam ha vuelto a su imagen original.'
      : 'Quitada. Reinicia Steam para verlo.';
    if (!r.live) $('artRestart').classList.remove('hidden');
    markTabs();
  } catch (e) {
    $('artStatus').textContent = 'Error: ' + e.message;
  }
}

// ---------- autodetección ----------

let detection = null;   // última respuesta de /api/detect

// protonFor traduce la familia sugerida por ProtonDB a una versión que esté
// instalada de verdad en la Deck.
function protonFor(family) {
  const tools = (inventory && inventory.compatTools) || [];
  if (!tools.length) return '';
  if (family === 'ge') {
    // El GE-Proton más reciente: los nombres ordenan bien alfabéticamente
    // salvo por el número, así que comparamos numéricamente.
    const ge = tools.filter((t) => /^GE-Proton/i.test(t.name))
      .sort((a, b) => a.name.localeCompare(b.name, 'en', { numeric: true }));
    if (ge.length) return ge[ge.length - 1].name;
  }
  const exp = tools.find((t) => t.name === 'proton_experimental');
  return exp ? exp.name : '';
}

async function runDetect(overrideAppId) {
  const path = $('sendPath').value;
  if (!path) return;
  const box = $('detectBox');
  box.classList.remove('hidden');
  $('detectTitle').textContent = 'Analizando la carpeta…';
  $('detectList').innerHTML = '';
  $('detectAlt').classList.add('hidden');

  try {
    detection = await api('/api/detect', { localPath: path, overrideAppId: overrideAppId || '' });
  } catch (e) {
    $('detectTitle').textContent = 'No se pudo analizar';
    $('detectList').innerHTML = `<li class="bad">${escapeHtml(e.message)}</li>`;
    return;
  }
  renderDetection();
}

function renderDetection() {
  const local = detection.local || {};
  const online = detection.online || null;
  const rows = [];

  // Identificación
  if (local.steamAppId) {
    rows.push(['ok', `Steam appid <strong>${escapeHtml(local.steamAppId)}</strong>`,
      `encontrado en <code>${escapeHtml(local.appIdSource)}</code>`]);
  } else if (online && online.appId) {
    rows.push(['warn', `Steam appid <strong>${escapeHtml(online.appId)}</strong>`,
      'deducido del nombre, no venía en la carpeta']);
  } else {
    rows.push(['warn', 'Sin identificar en Steam',
      online && online.note ? escapeHtml(online.note) : 'puede ser un juego que no está en la tienda']);
  }

  // Nombre
  const officialName = online && online.name ? online.name : local.cleanName;
  if (officialName) {
    const extra = online && online.developer ? escapeHtml(online.developer) : 'nombre limpiado de la carpeta';
    rows.push(['ok', escapeHtml(officialName), extra]);
    $('sendName').value = officialName;
  }

  // Ejecutable
  const exes = local.executables || [];
  if (exes.length) {
    const best = exes[0];
    $('sendExe').value = best.rel;
    let detail = `${fmtBytes(best.size)}${best.why ? ' · ' + escapeHtml(best.why) : ''}`;
    if ((local.skipped || []).length) {
      detail += ` · descartados: ${escapeHtml(local.skipped.slice(0, 4).join(', '))}`;
    }
    rows.push(['ok', `<code>${escapeHtml(best.rel)}</code>`, detail]);
    if (exes.length > 1) {
      rows.push(['info', `Hay ${exes.length} ejecutables candidatos`,
        'si no arranca, cámbialo abajo: ' + escapeHtml(exes.slice(1, 4).map((e) => e.rel).join(', '))]);
    }
  } else {
    rows.push(['bad', 'No se encontró ningún .exe', 'tendrás que indicarlo a mano']);
  }

  // Proton
  if (online && online.proton) {
    const p = online.proton;
    const tool = protonFor(p.suggested);
    const kind = ['platinum', 'gold', 'native'].includes(p.tier) ? 'ok'
      : (p.tier === 'borked' ? 'bad' : 'warn');
    rows.push([kind, `ProtonDB: <strong>${escapeHtml(p.tier || '?')}</strong>` +
      (p.bestTier && p.bestTier !== p.tier ? ` (mejor reportado: ${escapeHtml(p.bestTier)})` : ''),
      escapeHtml(p.advice || '')]);
    if (tool) {
      $('sendProton').value = tool;
      const label = (inventory.compatTools.find((t) => t.name === tool) || {}).displayName || tool;
      rows.push(['info', `Proton sugerido: <strong>${escapeHtml(label)}</strong>`, 'puedes cambiarlo abajo']);
    }
  }

  $('detectTitle').textContent = 'Detección';
  $('detectList').innerHTML = rows.map(([kind, main, sub]) =>
    `<li class="${kind}"><span class="dmain">${main}</span><span class="dsub">${sub || ''}</span></li>`).join('');

  // Alternativas cuando la identificación vino de una búsqueda por nombre.
  const matches = (online && online.matches) || [];
  if (matches.length > 1) {
    const sel = $('detectMatches');
    sel.innerHTML = matches.map((m) =>
      `<option value="${escapeHtml(m.appId)}"${m.appId === (online.appId || '') ? ' selected' : ''}>${escapeHtml(m.name)} (${escapeHtml(m.appId)})</option>`).join('');
    $('detectAlt').classList.remove('hidden');
  }
}

async function saveGridKey() {
  try {
    const r = await api('/api/settings', { gridKey: $('gridKey').value });
    $('gridKey').value = '';
    state.hasGridKey = r.hasGridKey;
    updateArtworkHint();
    banner(r.hasGridKey ? 'Clave de SteamGridDB guardada.' : 'Clave borrada.', 'ok');
  } catch (e) {
    banner('No se pudo guardar: ' + e.message, 'err');
  }
}

function updateArtworkHint() {
  const has = !!state.hasGridKey;
  $('gridKeyState').textContent = has
    ? 'Hay una clave guardada. Para borrarla, guarda el campo vacío.'
    : 'Todavía no hay clave guardada.';
  $('artworkHint').textContent = has ? '' : '(falta la clave, mira Ajustes abajo)';
  $('sendArtwork').disabled = !has;
  if (!has) $('sendArtwork').checked = false;
}

// ---------- progreso ----------

function showProgress(title) {
  $('pTitle').textContent = title;
  $('pPhase').textContent = 'empezando…';
  $('pFill').style.width = '0%';
  $('pFill').className = 'pfill';
  $('pBytes').textContent = '';
  $('pSpeed').textContent = '';
  $('pFile').textContent = '';
  $('btnCancel').classList.remove('hidden');
  $('btnCloseProgress').classList.add('hidden');
  $('progress').classList.remove('hidden');
}

function updateProgress(msg) {
  const p = msg.progress || {};
  $('pTitle').textContent = msg.title || 'Trabajando…';
  $('pPhase').textContent = p.error ? p.error : (p.phase || '');
  const pct = p.bytesTotal ? (p.bytesDone / p.bytesTotal) * 100 : (p.done ? 100 : 0);
  const fill = $('pFill');
  fill.style.width = Math.min(100, pct).toFixed(1) + '%';
  fill.className = 'pfill' + (p.error ? ' err' : (p.done ? ' ok' : ''));

  $('pBytes').textContent = p.bytesTotal
    ? `${fmtBytes(p.bytesDone)} de ${fmtBytes(p.bytesTotal)}` +
      (p.filesTotal ? ` · ${p.filesDone}/${p.filesTotal} ficheros` : '')
    : '';
  $('pSpeed').textContent = p.speed ? fmtBytes(p.speed) + '/s' : '';
  $('pFile').textContent = p.message || p.file || '';

  if (p.done) {
    $('btnCancel').classList.add('hidden');
    $('btnCloseProgress').classList.remove('hidden');
    if (!p.error) loadInventory();
  }
}

function connectEvents() {
  const es = new EventSource('/api/events');
  es.onmessage = (ev) => {
    // Un evento mal formado no puede tumbar el manejador: si esto revienta,
    // dejan de llegar TODOS los partes de progreso hasta recargar la página.
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    if (msg && msg.type === 'progress') {
      // El estado del trabajo se sigue por aquí: es lo que decide si los
      // botones de enviar y mover están disponibles.
      state.job = { id: msg.job, kind: msg.kind, title: msg.title, progress: msg.progress || {} };
      updateJobButtons();
      if ($('progress').classList.contains('hidden')) showProgress(msg.title);
      updateProgress(msg);
    }
  };
  es.onerror = () => { /* EventSource reconecta solo */ };
}

// ---------- acciones ----------

// detectedAppId devuelve el appid con el que buscar las carátulas: manda el
// que el usuario haya elegido entre las alternativas.
function detectedAppId() {
  if (!detection) return '';
  const alt = $('detectMatches');
  if (!$('detectAlt').classList.contains('hidden') && alt.value) return alt.value;
  return (detection.online && detection.online.appId) || (detection.local && detection.local.steamAppId) || '';
}

// jobBusy dice si hay un trabajo largo en marcha. El servidor solo admite uno
// a la vez y responde 409 al segundo.
function jobBusy() {
  const j = state.job;
  return !!(j && j.progress && !j.progress.done && !j.progress.error);
}

// updateJobButtons apaga lo que arrancaría un segundo trabajo. Antes se podía
// pulsar "Enviar" con una copia en curso: el 409 se pintaba como si el trabajo
// de verdad hubiera terminado, se escondía "Cancelar" y la transferencia seguía
// por detrás sin forma de pararla.
function updateJobButtons() {
  const busy = jobBusy();
  $('btnSend').disabled = busy;
  $('btnSendRom').disabled = busy || !(inventory && inventory.romsDir);
  document.querySelectorAll('#gamesBody button[data-move]').forEach((b) => { b.disabled = busy; });
}

// startJob lanza una operación larga. Un fallo AL ARRANCARLA no es progreso de
// nada: va al banner, no al panel de progreso.
async function startJob(path, body, titulo, fallo) {
  if (jobBusy()) {
    banner('Ya hay una operación en curso: espera a que termine o cancélala.', 'err');
    return false;
  }
  try {
    await api(path, body);
    // El panel se abre después de que el servidor acepte el trabajo, para no
    // machacar el del trabajo que ya estuviera pintado.
    if ($('progress').classList.contains('hidden')) showProgress(titulo);
    return true;
  } catch (e) {
    banner(fallo + ': ' + e.message, 'err');
    return false;
  }
}

async function sendGame() {
  const path = $('sendPath').value;
  if (!path) { banner('Elige la carpeta del juego.', 'err'); return; }
  const addShortcut = $('addShortcut').checked;
  if (addShortcut && !$('sendExe').value) { banner('Elige el ejecutable.', 'err'); return; }
  await startJob('/api/send-game', {
    localPath: path,
    name: $('sendName').value.trim(),
    exe: $('sendExe').value.trim(),
    compatTool: $('sendProton').value,
    launchOptions: $('sendOpts').value.trim(),
    addShortcut,
    artwork: addShortcut && $('sendArtwork').checked,
    steamAppId: detectedAppId(),
  }, 'Enviando juego', 'No se pudo enviar el juego');
}

async function sendRom() {
  const path = $('romPath').value;
  if (!path) { banner('Elige la ROM.', 'err'); return; }
  await startJob('/api/send-rom', {
    localPath: path,
    romsDir: inventory.romsDir,
    system: $('romSystem').value,
  }, 'Enviando ROM', 'No se pudo enviar la ROM');
}

async function moveGame(appId, target) {
  const g = (inventory.games || []).find((x) => x.appId === appId);
  const lib = (inventory.libraries || []).find((l) => l.path === target);
  if (!g) return;

  // Los dos casos tienen letra pequeña distinta: un juego de Steam necesita
  // Steam cerrado (al salir reescribe los .acf y deshace el cambio), mientras
  // que en uno no-Steam lo que se toca es el acceso directo, y eso Steam sí
  // sabe hacerlo en caliente.
  let aviso = '\n\nSteam debe estar cerrado en la Deck.';
  if (g.kind === 'nonsteam') {
    aviso = `\n\nSe copia la carpeta a ${lib ? lib.gamesDir : target} y se actualiza el acceso directo.`
      + (inventory.steamRunning
        ? ' Steam está abierto: se lo pediremos a él, así que no hace falta cerrarlo.'
        : '');
    if (g.hasCompat) {
      aviso += '\nEl prefijo de Proton (partidas guardadas) se queda donde está, que es donde Steam lo busca.';
    }
  }
  if (!confirm(`¿Mover "${g.name}" (${fmtBytes(g.size)}) a ${lib ? lib.label : target}?${aviso}`)) return;
  await startJob('/api/move', { appId, target }, `Moviendo ${g.name}`, 'No se pudo mover el juego');
}

// --- borrado ---

let delAppId = null;

function openDelete(appId) {
  const g = (inventory.games || []).find((x) => x.appId === appId);
  if (!g) return;
  delAppId = appId;
  $('delTitle').textContent = 'Eliminar: ' + g.name;
  $('delGameLabel').textContent = `Ficheros del juego (${fmtBytes(g.size)})`;
  $('delCompatLabel').textContent = `Prefijo de Proton (${fmtBytes(g.compatSize)})`;
  $('delShaderLabel').textContent = `Caché de shaders (${fmtBytes(g.shaderSize)})`;
  $('delCompat').parentElement.classList.toggle('hidden', !g.hasCompat);
  $('delShader').parentElement.classList.toggle('hidden', !g.hasShaders);
  $('delShortcutRow').classList.toggle('hidden', g.kind !== 'nonsteam');
  $('delGame').checked = true;
  $('delCompat').checked = false;
  $('delShader').checked = false;
  // También el acceso directo: si no se repone, queda como lo dejara el
  // borrado anterior y se desmarca sin que el usuario lo haya tocado.
  $('delShortcut').checked = true;
  $('delModal').classList.remove('hidden');
}

async function confirmDelete() {
  const targets = {
    game: $('delGame').checked,
    compatData: $('delCompat').checked,
    shaderCache: $('delShader').checked,
    shortcut: $('delShortcut').checked && !$('delShortcutRow').classList.contains('hidden'),
  };
  if (!targets.game && !targets.compatData && !targets.shaderCache && !targets.shortcut) {
    banner('No has marcado nada que borrar.', 'err');
    return;
  }
  $('delModal').classList.add('hidden');
  try {
    const r = await api('/api/delete', { appId: delAppId, targets });
    banner(`Liberados ${fmtBytes(r.freed)}.`, 'ok');
    await loadInventory();
  } catch (e) {
    banner('No se pudo eliminar: ' + e.message, 'err');
  }
}

// ---------- salir ----------

// Lanzado desde el escritorio (Flatpak, acceso directo) no hay consola con
// Ctrl+C: este boton es la unica forma visible de apagar el servidor.
async function quit() {
  if (!confirm('¿Cerrar deckman? Se apaga el servidor local de este PC.')) return;
  try {
    await api('/api/quit', {});
    document.body.innerHTML =
      '<div class="goodbye"><p>deckman se ha cerrado.</p><p class="hint">Ya puedes cerrar esta pestaña.</p></div>';
    window.close(); // solo funciona si la pestaña la abrio deckman; si no, queda la despedida
  } catch (e) {
    banner('No se puede salir: ' + e.message, 'err');
  }
}

// ---------- arranque ----------

function init() {
  $('btnConnect').onclick = connect;
  $('btnQuit').onclick = quit;
  $('btnChange').onclick = () => { showConnectForm = true; connError(''); applyConnState(); };
  // Enter en cualquier campo de la tarjeta conecta: es un formulario de tres
  // casillas y obligar a bajar al botón sobra.
  $('connectView').addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && e.target.tagName === 'INPUT') { e.preventDefault(); connect(); }
  });
  $('btnRefresh').onclick = loadInventory;
  $('filter').oninput = () => inventory && renderGames();
  $('showSteam').onchange = () => inventory && renderGames();
  $('showNonSteam').onchange = () => inventory && renderGames();

  document.querySelectorAll('.tab').forEach((t) => {
    t.onclick = () => {
      // Si la tarjeta estaba abierta por "Cambiar", tocar una pestaña vuelve al
      // contenido: si no, no habría forma de arrepentirse.
      showConnectForm = false;
      applyConnState();
      document.querySelectorAll('.tab').forEach((x) => x.classList.remove('active'));
      document.querySelectorAll('.panel').forEach((x) => x.classList.remove('active'));
      t.classList.add('active');
      $('tab-' + t.dataset.tab).classList.add('active');
    };
  });

  document.querySelectorAll('th.sortable').forEach((th) => {
    th.onclick = () => {
      const key = th.dataset.sort;
      sortDir = sortBy === key ? -sortDir : 1;
      sortBy = key;
      renderGames();
    };
  });

  document.querySelectorAll('[data-browse]').forEach((b) => {
    b.onclick = () => openBrowser(b.dataset.browse, b.dataset.mode);
  });

  $('brClose').onclick = $('brCancel').onclick = closeBrowser;
  $('brUp').onclick = () => br.parent && browseTo(br.parent);
  $('brPick').onclick = () => confirmPick();
  $('brFilter').oninput = renderEntries;
  $('brEditPath').onclick = () => {
    const inp = $('brPathInput');
    inp.classList.toggle('hidden');
    $('brCrumbs').classList.toggle('hidden');
    if (!inp.classList.contains('hidden')) { inp.focus(); inp.select(); }
  };
  $('brPathInput').onkeydown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      browseTo($('brPathInput').value.trim());
      $('brPathInput').classList.add('hidden');
      $('brCrumbs').classList.remove('hidden');
      $('brList').focus();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      $('brPathInput').classList.add('hidden');
      $('brCrumbs').classList.remove('hidden');
    }
  };
  document.addEventListener('keydown', browserKeys);

  $('addShortcut').onchange = () => {
    $('shortcutFields').classList.toggle('hidden', !$('addShortcut').checked);
  };

  $('btnRedetect').onclick = () => runDetect();
  $('detectMatches').onchange = (e) => runDetect(e.target.value);
  $('btnSaveKey').onclick = saveGridKey;
  $('btnSend').onclick = sendGame;
  $('btnSendRom').onclick = sendRom;

  $('btnCancel').onclick = () => api('/api/cancel', {}).catch(() => {});
  $('btnCloseProgress').onclick = () => $('progress').classList.add('hidden');

  $('artClose').onclick = $('artDone').onclick = () => {
    $('artModal').classList.add('hidden');
    loadInventory();
  };
  $('artRemove').onclick = removeArt;
  $('artRestart').onclick = (e) => restartSteam(e.target);
  $('btnRestartSteam').onclick = (e) => restartSteam(e.target);
  $('prevClose').onclick = $('prevCancel').onclick = closePreview;
  $('prevApply').onclick = applyFromPreview;
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !$('artPreview').classList.contains('hidden')) closePreview();
  });
  $('artAnimated').onchange = loadArtOptions;
  $('artGameSel').onchange = (e) => { art.gameId = Number(e.target.value); loadArtOptions(); };
  $('artTabs').addEventListener('click', (e) => {
    const b = e.target.closest('.arttab');
    if (!b) return;
    art.kind = b.dataset.kind;
    loadArtOptions();
  });

  $('delClose').onclick = $('delCancel').onclick = () => $('delModal').classList.add('hidden');
  $('delConfirm').onclick = confirmDelete;

  // Delegación para los botones y selectores de la tabla, que se recrean.
  $('gamesBody').addEventListener('click', (e) => {
    const btn = e.target.closest('button');
    if (!btn) return;
    if (btn.dataset.del) openDelete(btn.dataset.del);
    if (btn.dataset.move) moveGame(btn.dataset.move, btn.dataset.target);
    if (btn.dataset.art) {
      const g = (inventory.games || []).find((x) => x.appId === btn.dataset.art);
      // En los juegos de Steam el appid ES el de Steam, así que la
      // correspondencia con SteamGridDB es exacta. En los no-Steam es un id
      // inventado por el acceso directo y hay que buscar por nombre.
      if (g) openArtwork(Number(g.appId), g.name, g.kind === 'steam' ? g.appId : '');
    }
  });
  $('gamesBody').addEventListener('change', async (e) => {
    const sel = e.target.closest('select.compat');
    if (!sel) return;
    try {
      const r = await api('/api/compat', { appId: sel.dataset.appid, tool: sel.value });
      // Si lo ha aplicado el propio Steam, ya está hecho. Solo cuando se ha
      // escrito el fichero (Steam cerrado) hace falta arrancarlo: decir
      // "reinicia Steam" tras un cambio en caliente era justo lo contrario,
      // porque al salir Steam reescribe config.vdf desde su copia en memoria.
      banner(r.live
        ? 'Proton actualizado. Steam ya lo ha aplicado, sin reiniciar.'
        : 'Proton actualizado. Se verá la próxima vez que arranques Steam.', 'ok');
    } catch (err) {
      banner('No se pudo cambiar Proton: ' + err.message, 'err');
    }
  });

  connectEvents();
  refreshState();
}

document.addEventListener('DOMContentLoaded', init);
