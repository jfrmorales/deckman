'use strict';

// Carátulas: la galería de SteamGridDB, su vista previa y los ajustes de la
// clave.

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

// artKindLabel da el nombre legible de un tipo de arte. ART_KINDS es una lista,
// no un diccionario: indexarla por la clave devolvía undefined y los avisos
// decían «Quitar carátula (grid)».
function artKindLabel(kind) {
  const spec = ART_KINDS.find((k) => k.key === kind);
  return spec ? t(spec.label) : kind;
}

let art = { appId: 0, name: '', steamAppId: '', gameId: 0, kind: 'grid', current: {}, busy: false, live: false };

// Cada petición de la galería lleva número. Si al volver ya no es la última,
// se descarta: cambiar de pestaña rápido hacía que una respuesta vieja
// sobrescribiera a la nueva y se veía el tipo equivocado.
let artRequestSeq = 0;

async function openArtwork(appId, name, steamAppId) {
  art = { appId, name, steamAppId: steamAppId || '', gameId: 0, kind: 'grid', current: {}, busy: false };
  $('artTitle').textContent = t('Carátulas — ') + name;
  $('artGameSel').innerHTML = '';
  $('artBody').innerHTML = `<p class="empty">${escapeHtml(t('Buscando el juego en SteamGridDB…'))}</p>`;
  $('artStatus').textContent = '';
  $('artModal').classList.remove('hidden');

  // Sin clave no hay nada que buscar: mejor decirlo con el botón que lo
  // arregla que dejar el error de SteamGridDB en mitad de la galería.
  if (!state.hasGridKey) {
    $('artTabs').innerHTML = '';
    $('artBody').innerHTML = `<div class="empty"><p>${escapeHtml(t('Para elegir carátulas hace falta una clave gratuita de SteamGridDB.'))}</p>
      <button class="primary" id="artNeedKey">${escapeHtml(t('Poner la clave'))}</button></div>`;
    $('artNeedKey').onclick = () => { $('artModal').classList.add('hidden'); openSettings(); };
    $('artRemove').disabled = true;
    return;
  }

  // Pestañas
  $('artTabs').innerHTML = ART_KINDS.map((k) =>
    `<button class="arttab${k.key === art.kind ? ' active' : ''}" data-kind="${k.key}">${escapeHtml(t(k.label))}</button>`).join('');

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
      ? t('Los cambios se aplican al instante, sin reiniciar Steam.')
      : t('Steam no responde en caliente: los cambios necesitarán reiniciarlo.');
  } catch { /* no es crítico */ }

  try {
    const r = await api('/api/artwork/games', { name, steamAppId: art.steamAppId });
    const games = r.games || [];
    if (!games.length) {
      $('artBody').innerHTML = `<p class="empty">${escapeHtml(t('SteamGridDB no encuentra "{0}".', name))}</p>`;
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
  $('artBody').innerHTML = `<p class="empty">${escapeHtml(t('Cargando imágenes…'))}</p>`;
  markTabs();
  try {
    const r = await api('/api/artwork/list', {
      gameId: art.gameId, kind, animated: $('artAnimated').checked,
    });
    if (seq !== artRequestSeq) return; // llegó tarde: manda otra petición
    const opts = r.options || [];
    if (!opts.length) {
      $('artBody').innerHTML = `<p class="empty">${escapeHtml(t('No hay imágenes de este tipo para este juego.'))}</p>`;
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
          ${o.animated ? `<span class="tag anim">${escapeHtml(t('animada'))}</span>` : ''}
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

// applyArt no pide una segunda confirmación: se llega aquí desde la vista
// previa, que ya enseña la imagen a tamaño real y tiene su propio «Aplicar».
// Cambiar una carátula se deshace con otro clic; preguntarlo dos veces solo
// estorbaba.
async function applyArt(opt, el, kind) {
  if (art.busy) return;
  kind = kind || art.kind;

  art.busy = true;
  $('artBody').querySelectorAll('.artitem').forEach((x) => x.classList.remove('chosen'));
  el.classList.add('working');
  $('artStatus').textContent = t('Instalando…');
  try {
    const r = await api('/api/artwork/apply', {
      appId: art.appId, kind, url: opt.url, ext: opt.ext || '',
    });
    art.current[kind] = r.file;
    olvidarCover(String(art.appId)); // la miniatura de la tabla ya no vale
    el.classList.add('chosen');
    $('artStatus').textContent = `${fmtBytes(r.bytes)} · ${r.message || t('instalada')}`;
    // Los iconos no pasan por la API de Steam y sí necesitan reinicio.
    if (!r.live) $('artRestart').classList.remove('hidden');
    markTabs();
  } catch (e) {
    $('artStatus').textContent = t('Error: ') + e.message;
  } finally {
    el.classList.remove('working');
    art.busy = false;
  }
}

// ---------- vista previa ----------

let preview = null;   // { opt, el, kind }

async function openPreview(opt, el, kind) {
  preview = { opt, el, kind };
  $('prevTitle').textContent = artKindLabel(kind) + (opt.animated ? ' · ' + t('animada') : '');

  // En las animadas mostramos la miniatura .webm: el fichero real es un .webp
  // que puede pasar de 40 MB y no tiene sentido bajarlo solo para mirarlo.
  $('prevBody').innerHTML = opt.animated
    ? `<video src="${escapeHtml(opt.thumb)}" autoplay loop muted playsinline></video>`
    : `<img src="${escapeHtml(opt.url)}" alt="">`;

  const partes = [`${opt.width}×${opt.height}`];
  if (opt.style) partes.push(opt.style);
  if (opt.author) partes.push(t('por {0}', opt.author));
  if (opt.animated) partes.push(t('se muestra la previa animada; se instalará el original en alta resolución'));
  $('prevMeta').textContent = partes.join(' · ');

  $('prevApply').disabled = false;
  $('prevApply').textContent = t('Aplicar esta imagen');
  $('artPreview').classList.remove('hidden');

  // Dónde va a parar y cuándo se verá: lo que antes contaba la segunda
  // confirmación, dicho aquí, al lado del botón.
  $('prevNote').textContent = (inventory && inventory.steamRunning && kind !== 'icon'
    ? t('Steam está abierto: se aplicará en caliente sin reiniciar.')
    : t('Si Steam está cerrado (o es un icono), se verá al reiniciar Steam.'))
    + ' ' + t('Sustituye solo esta imagen; el resto de carátulas no se toca.');

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
  $('prevApply').textContent = t('Instalando…');
  await applyArt(opt, el, kind);
  closePreview();
}

async function removeArt() {
  const ok = await confirmChanges({
    summary: t('Quitar carátula ({0}) de "{1}".', artKindLabel(art.kind), art.name),
    items: [
      {
        path: `userdata/<id>/config/grid/${art.appId}...`,
        desc: t('Elimina únicamente esa imagen; no toca otras carátulas ni configuraciones.'),
      },
    ],
    confirmLabel: t('Quitar imagen'),
    danger: true,
  });
  if (!ok) return;
  try {
    const r = await api('/api/artwork/remove', { appId: art.appId, kind: art.kind });
    delete art.current[art.kind];
    olvidarCover(String(art.appId));
    $('artBody').querySelectorAll('.artitem').forEach((x) => x.classList.remove('chosen'));
    $('artStatus').textContent = r.live
      ? t('Quitada. Steam ha vuelto a su imagen original.')
      : t('Quitada. Reinicia Steam para verlo.');
    if (!r.live) $('artRestart').classList.remove('hidden');
    markTabs();
  } catch (e) {
    $('artStatus').textContent = t('Error: ') + e.message;
  }
}

async function saveGridKey() {
  try {
    const r = await api('/api/settings', { gridKey: $('gridKey').value });
    $('gridKey').value = '';
    state.hasGridKey = r.hasGridKey;
    updateArtworkHint();
    banner(r.hasGridKey ? t('Clave de SteamGridDB guardada.') : t('Clave borrada.'), 'ok');
    if (r.hasGridKey) $('settingsModal').classList.add('hidden');
  } catch (e) {
    banner(t('No se pudo guardar: {0}', e.message), 'err');
  }
}

function updateArtworkHint() {
  const has = !!state.hasGridKey;
  $('gridKeyState').textContent = has
    ? t('Hay una clave guardada. Para borrarla, guarda el campo vacío.')
    : t('Todavía no hay clave guardada.');
  // El aviso es un botón que abre los Ajustes: «mira Ajustes abajo» mandaba a
  // buscar algo que no se podía pulsar.
  $('artworkHint').classList.toggle('hidden', has);
  $('sendArtwork').disabled = !has;
  if (!has) $('sendArtwork').checked = false;
}

function openSettings() {
  $('settingsModal').classList.remove('hidden');
  $('gridKey').focus();
}
