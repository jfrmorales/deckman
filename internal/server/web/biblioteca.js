'use strict';

// Biblioteca: el inventario de la Deck, la tabla de juegos con sus portadas, y
// lo que se hace con un juego desde ella (mover, eliminar, reiniciar Steam).

// ---------- inventario ----------

async function loadInventory() {
  $('emptyLib').textContent = t('Leyendo la Deck…');
  $('emptyLib').classList.remove('hidden');
  try {
    inventory = await api('/api/inventory');
    renderStorage();
    renderGames();
    renderSendOptions();
    renderOperations();
    banner('');
  } catch (e) {
    banner(t('No se pudo leer la biblioteca: {0}', e.message), 'err');
    $('emptyLib').textContent = t('Sin datos.');
  }
}

function renderStorage() {
  const el = $('storage');
  el.innerHTML = '';
  const allLibs = inventory.libraries || [];
  const libs = allLibs.some((l) => l.total > 0) ? allLibs.filter((l) => l.total > 0) : allLibs;
  for (const lib of libs) {
    const pct = lib.total ? (lib.used / lib.total) * 100 : 0;
    const div = document.createElement('div');
    div.className = 'disk';
    div.innerHTML = `
      <h3>${escapeHtml(t(lib.label))}
        <span class="badge ${lib.removable ? 'sd' : ''}">${lib.removable ? 'microSD' : t('interno')}</span>
      </h3>
      <div class="nums">${t('{0} usados', fmtBytes(lib.used))} · <strong>${t('{0} libres', fmtBytes(lib.free))}</strong> ${t('de {0}', fmtBytes(lib.total))}</div>
      <div class="bar ${pct > 92 ? 'full' : ''} ${lib.removable ? 'sd' : ''}"><div style="width:${pct.toFixed(1)}%"></div></div>`;
    el.appendChild(div);
  }
  $('steamState').textContent = inventory.steamRunning
    ? t('Steam abierto: los cambios se aplican al instante')
    : t('Steam cerrado: los cambios se verán al abrirlo');
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
    else r = (a.name || '').localeCompare(b.name || '', localeActual());
    return r * sortDir;
  });
  return list;
}

// emptyLibText dice por qué la tabla está vacía y cómo salir de ahí: "ningún
// juego coincide con el filtro" no ayuda si lo que sobra es un check apagado.
function emptyLibText() {
  if (!inventory) return t('Conecta con la Deck para ver la biblioteca.');
  const total = (inventory.games || []).length;
  const q = $('filter').value.trim();
  if (!total) return t('La Deck no tiene juegos instalados.');
  if (q) return t('Ningún juego coincide con «{0}». Borra el filtro para verlos todos.', q);
  if (!$('showSteam').checked || !$('showNonSteam').checked) {
    return t('Las casillas Steam / No-Steam esconden los juegos. Marca alguna para verlos.');
  }
  return t('Ningún juego que mostrar.');
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
  // Una biblioteca sin tamaño es una unidad que no está montada (la microSD
  // fuera, por ejemplo): libraryfolders.vdf la sigue listando, pero no se
  // puede mover nada a ella, y si va primero tapaba a la que sí está.
  const target = libs.find((l) => l.path !== g.libraryPath && l.total > 0);
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
    const locLabel = lib ? t(lib.label) : (g.libraryPath || g.startDir || '—');

    // Selector de Proton. Si el juego tiene asignada una versión que ya no
    // está instalada, la añadimos igualmente para no cambiarla sin querer al
    // tocar cualquier otra cosa de la fila.
    let compatCell = '<span class="loc">—</span>';
    if (tools.length) {
      const shown = tools.slice();
      if (g.compatTool && !shown.some((t) => t.name === g.compatTool)) {
        shown.push({ name: g.compatTool, displayName: t('{0} (no instalado)', g.compatTool) });
      }
      const opts = [`<option value="">${escapeHtml(t('Por defecto'))}</option>`]
        .concat(shown.map((t) =>
          `<option value="${escapeHtml(t.name)}"${t.name === g.compatTool ? ' selected' : ''}>${escapeHtml(t.displayName)}</option>`));
      compatCell = `<select class="compat" data-appid="${escapeHtml(g.appId)}" data-prev="${escapeHtml(g.compatTool || '')}">${opts.join('')}</select>`;
    }

    const target = moveTarget(g, libs);
    const moveBtn = target
      ? `<button data-move="${escapeHtml(g.appId)}" data-target="${escapeHtml(target.path)}" data-label="→ ${escapeHtml(t(target.label))}" title="${escapeHtml(t('Mover a {0}', t(target.label)))}">→ ${escapeHtml(t(target.label))}</button>`
      : '';

    // La ruta completa solo en los no-Steam: en los de Steam no aporta nada
    // (siempre es steamapps/common) y en los otros es donde está de verdad.
    const locSub = g.kind === 'nonsteam' && g.startDir
      ? `<div class="loc locpath" title="${escapeHtml(g.startDir)}">${escapeHtml(g.startDir)}</div>` : '';

    tr.innerHTML = `
      <td class="covercell">${coverCell(g)}</td>
      <td>
        <div class="gname">
          <span>${escapeHtml(g.name || t('(sin nombre)'))}</span>
          <span class="kind ${g.kind === 'nonsteam' ? 'ns' : ''}">${g.kind === 'nonsteam' ? t('no-Steam') : 'Steam'}</span>
        </div>
        <div class="loc">${t('jugado: {0}', fmtDate(g.lastPlayed))}</div>
      </td>
      <td class="num">${fmtBytes(g.size)}</td>
      <td class="locc"><span class="loc">${escapeHtml(locLabel)}</span>${locSub}</td>
      <td class="protonc">${compatCell}</td>
      <td><div class="acts">
        ${moveBtn}<button class="danger" data-del="${escapeHtml(g.appId)}">${t('Eliminar')}</button>
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
      title="${escapeHtml(t('Carátulas de {0}', g.name || ''))}">
      <img alt=""${url ? ` src="${url}"` : ''}${cdn ? ` data-cdn="${escapeHtml(cdn)}"` : ''}>
      <span class="ph">${escapeHtml(inicial)}</span>
      <span class="cta">${escapeHtml(t('Arte'))}</span>
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

// ---------- reiniciar Steam ----------

async function restartSteam(btn) {
  const ok = await confirmChanges({
    title: t('Reiniciar Steam'),
    summary: t('¿Reiniciar Steam en la Deck?'),
    note: t('Se cerrará cualquier juego que tengas abierto.') + ' ' +
      t('Solo hace falta si un cambio no aparece en Steam.'),
    confirmLabel: t('Reiniciar Steam'),
    danger: true,
  });
  if (!ok) return;
  const prev = btn.textContent;
  btn.disabled = true;
  btn.textContent = t('Reiniciando…');
  try {
    const r = await api('/api/restart-steam', {});
    banner(r.message || t('Steam reiniciado.'), 'ok');
  } catch (e) {
    banner(t('No se pudo reiniciar Steam: {0}', e.message), 'err');
  } finally {
    btn.disabled = false;
    btn.textContent = prev;
  }
}

async function moveGame(appId, target) {
  const g = (inventory.games || []).find((x) => x.appId === appId);
  const lib = (inventory.libraries || []).find((l) => l.path === target);
  if (!g) return;

  const items = [];
  let note = inventory.steamRunning
    ? t('Steam está abierto: lo moverá él mismo, como desde Ajustes > Almacenamiento.')
    : t('Steam está cerrado: no lo abras en la Deck hasta que termine.');

  if (g.kind === 'nonsteam') {
    items.push({
      path: lib ? lib.gamesDir : target,
      desc: t('Copia la carpeta del juego ({0}) a la nueva ubicación.', fmtBytes(g.size)),
    });
    items.push({
      path: 'userdata/<id>/config/shortcuts.vdf',
      desc: inventory.steamRunning
        ? t('Actualiza la ruta del acceso directo mediante la API de Steam (conserva el resto).')
        : t('Actualiza la ruta del acceso directo conservando el resto (copia .deckman.bak).'),
    });
    note = inventory.steamRunning
      ? t('Steam está abierto: se lo pediremos a él, así que no hace falta cerrarlo.')
      : '';
    if (g.hasCompat) {
      note = (note ? note + ' ' : '') + t('El prefijo de Proton (partidas guardadas) se queda donde está, que es donde Steam lo busca.');
    }
  } else {
    items.push({
      path: `${target}/steamapps/common/`,
      desc: t('Mueve los ficheros del juego ({0}) y su manifiesto .acf.', fmtBytes(g.size)),
    });
  }

  const ok = await confirmChanges({
    summary: t('Mover "{0}" ({1}) a {2}.', g.name, fmtBytes(g.size), lib ? t(lib.label) : target),
    items,
    note,
    confirmLabel: t('Mover'),
  });
  if (!ok) return;
  try {
    const res = await api('/api/move', { appId, target });
    if (res.queued) {
      if (res.operations) {
        state.operations = res.operations;
        pintarOperaciones();
      }
      banner(t('Añadido a la cola: {0}', g.name), 'ok');
    } else if ($('progress').classList.contains('hidden')) {
      showProgress(t('Moviendo {0}', g.name));
    }
  } catch (e) {
    banner(t('No se pudo mover el juego') + ': ' + e.message, 'err');
  }
}

// --- borrado ---

let delAppId = null;

function openDelete(appId) {
  const g = (inventory.games || []).find((x) => x.appId === appId);
  if (!g) return;
  delAppId = appId;
  $('delTitle').textContent = t('Eliminar: ') + g.name;
  $('delGameLabel').textContent = t('Ficheros del juego ({0})', fmtBytes(g.size));
  $('delCompatLabel').textContent = t('Prefijo de Proton ({0})', fmtBytes(g.compatSize));
  $('delShaderLabel').textContent = t('Caché de shaders ({0})', fmtBytes(g.shaderSize));
  // Las rutas salen del inventario; la del prefijo y la de shaders cuelgan de
  // la biblioteca donde está el juego. Si no se sabe alguna, no se inventa.
  const steamapps = g.libraryPath ? `${g.libraryPath}/steamapps` : '';
  $('delGamePath').textContent = g.fullPath || g.gameRoot || '';
  $('delCompatPath').textContent = g.hasCompat && steamapps ? `${steamapps}/compatdata/${g.appId}` : '';
  $('delShaderPath').textContent = g.hasShaders && steamapps ? `${steamapps}/shadercache/${g.appId}` : '';
  $('delCompat').parentElement.classList.toggle('hidden', !g.hasCompat);
  $('delShader').parentElement.classList.toggle('hidden', !g.hasShaders);
  $('delShortcutRow').classList.toggle('hidden', g.kind !== 'nonsteam');
  const sinCarpeta = g.kind === 'nonsteam' && !g.gameRoot && !g.size;
  $('delGame').parentElement.classList.toggle('hidden', sinCarpeta);
  $('delGame').checked = !sinCarpeta;
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
    banner(t('No has marcado nada que borrar.'), 'err');
    return;
  }
  $('delModal').classList.add('hidden');
  try {
    const r = await api('/api/delete', { appId: delAppId, targets });
    banner(t('Liberados {0}.', fmtBytes(r.freed)), 'ok');
    await loadInventory();
  } catch (e) {
    banner(t('No se pudo eliminar: {0}', e.message), 'err');
  }
}
