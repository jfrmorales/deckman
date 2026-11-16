'use strict';

// Explorador de ficheros de este PC, para elegir la carpeta del juego, el
// ejecutable o una ROM.

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
    if (!br.base) { banner(t('Elige primero la carpeta del juego.'), 'err'); return; }
    start = br.base;
  }

  $('brTitle').textContent = mode === 'rel' ? t('Elegir el ejecutable')
    : (mode === 'any' ? t('Elegir fichero o carpeta') : t('Elegir carpeta'));
  $('brHint').textContent = t('un clic selecciona · doble clic entra o elige');
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
    el.innerHTML = `<span class="crumb cur">${escapeHtml(t('Equipo'))}</span>`;
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
    // Al elegir el ejecutable se pide además que el servidor marque los
    // nativos de Linux: no llevan extensión y solo se reconocen por dentro.
    const r = await api('/api/browse?path=' + encodeURIComponent(path || '') +
      (br.mode === 'rel' ? '&exes=1' : ''));
    br.path = r.path;
    br.parent = r.parent;
    br.entries = r.entries || [];
    br.selected = null;
    renderCrumbs(r.path);
    $('brPathInput').value = r.path === '@roots' ? '' : r.path;
    renderEntries();
  } catch (e) {
    br.entries = [];
    br.selected = null;
    updateFooter();
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
    ul.innerHTML = `<li class="empty">${escapeHtml(br.entries.length ? t('Nada coincide con el filtro.') : t('Carpeta vacía.'))}</li>`;
    updateFooter();
    return;
  }

  ul.innerHTML = list.map((e, i) => {
    // Apagamos solo lo que no sirve para nada aquí. Las carpetas nunca: aunque
    // no se puedan elegir (modo ejecutable), sí se puede entrar en ellas, y
    // pintarlas grises haría pensar lo contrario.
    const dim = (selectable(e) || e.isDir) ? '' : ' dim';
    const exe = br.mode === 'rel' && !e.isDir && (e.native || /\.exe$/i.test(e.name)) ? ' exe' : '';
    const tipo = !exe ? '' : `<span class="kind">${escapeHtml(e.native ? 'Linux' : 'Windows')}</span>`;
    return `<li class="brrow${dim}${exe}" data-i="${i}">
      <span class="ic">${e.isDir ? '📁' : (exe ? (e.native ? '🐧' : '⚙️') : '📄')}</span>
      <span class="nm">${escapeHtml(e.name)}</span>${tipo}
      ${e.isDir ? `<span class="enter" title="${escapeHtml(t('Entrar'))}">›</span>` : `<span class="sz">${fmtBytes(e.size)}</span>`}
    </li>`;
  }).join('');

  ul.querySelectorAll('.brrow').forEach((li) => {
    const e = list[+li.dataset.i];
    li.onclick = (ev) => {
      if (ev.target.classList.contains('enter')) { browseTo(e.path); return; }
      selectEntry(e, li);
    };
    li.ondblclick = () => {
      if (e.isDir) { browseTo(e.path); return; }
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
    $('brSelected').textContent = t('Elegido: ') + br.selected.name;
    pick.disabled = false;
    pick.textContent = t('Seleccionar');
  } else if (br.mode === 'dir' && br.path && br.path !== '@roots') {
    // Sin nada marcado, el botón elige la carpeta en la que estás.
    $('brSelected').textContent = t('Se usará la carpeta actual');
    pick.disabled = false;
    pick.textContent = t('Usar esta carpeta');
  } else {
    $('brSelected').textContent = '';
    pick.disabled = true;
    pick.textContent = t('Seleccionar');
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
  applyPick(e.path, e.name, e);
}

function applyPick(fullPath, name, entry) {
  if (br.mode === 'rel') {
    let rel = fullPath;
    if (fullPath.startsWith(br.base)) rel = fullPath.slice(br.base.length).replace(/^[\\/]+/, '');
    $(br.target).value = rel;
    // Elegir un nativo de Linux con Proton puesto (o un .exe sin él) deja un
    // acceso directo que no arranca: el desplegable acompaña a la elección.
    if (entry) protonSegunEjecutable(!!entry.native, /\.exe$/i.test(name));
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
