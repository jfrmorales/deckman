'use strict';

// Emulación: enviar, gestionar, buscar carátulas y descargar ROMs.

async function sendRom() {
  const path = $('romPath').value;
  if (!path) { banner(t('Elige la ROM.'), 'err'); return; }
  const sys = $('romSystem').value;
  const fileName = path.split(/[\\/]/).filter(Boolean).pop() || path;
  const ok = await confirmChanges({
    summary: t('Copiar ROM "{0}" al sistema {1}.', fileName, sys),
    items: [
      {
        path: `${inventory.romsDir}/${sys}/${fileName}`,
        desc: t('Copia el fichero sin modificar configuraciones de Steam ni otras ROMs.'),
      },
    ],
    confirmLabel: t('Copiar ROM'),
  });
  if (!ok) return;

  await startJob('/api/send-rom', {
    localPath: path,
    romsDir: inventory.romsDir,
    system: sys,
  }, t('Enviando ROM'), t('No se pudo enviar la ROM'));
}

// Sistemas que tienen ROMs, con su cuenta. Se guarda cuáles se saben
// scrapear para no ofrecer un botón que no haría nada.
let manageSystems = { scrapeable: {} };

async function loadManageSystems() {
  const sel = $('manageRomSystem');
  const previo = sel.value;
  const hint = $('manageSystemsHint');
  hint.textContent = t('Mirando qué sistemas tienen ROMs…');

  try {
    const res = await api('/api/roms/systems');
    manageSystems = res;
    const lista = res.systems || [];
    sel.innerHTML = '';
    for (const s of lista) {
      const o = document.createElement('option');
      o.value = s.name;
      o.textContent = t(s.count === 1 ? '{0} — {1} ROM' : '{0} — {1} ROMs', s.name, s.count);
      sel.appendChild(o);
    }
    if (previo && lista.some((s) => s.name === previo)) sel.value = previo;
    if (lista.length) loadManageRoms();
    else $('manageRomsList').classList.add('hidden');

    hint.textContent = lista.length
      ? t(lista.length === 1 ? '{0} sistema con ROMs en {1}' : '{0} sistemas con ROMs en {1}', lista.length, res.romsDir)
      : t('No hay ninguna ROM en {0}.', res.romsDir || t('la Deck'));
  } catch (err) {
    hint.textContent = t('No se pudieron leer los sistemas: ') + err.message;
  }
}

async function loadManageRoms() {
  const sys = $('manageRomSystem').value;
  if (!sys) return;
  $('scrapeBox').classList.add('hidden');
  $('scrapeResumen').classList.add('hidden');

  try {
    const res = await api('/api/roms/list?system=' + encodeURIComponent(sys));
    // Si mientras llegaba la respuesta se eligió otro sistema, esta lista ya
    // no es la que se está mirando.
    if ($('manageRomSystem').value !== sys) return;
    const roms = res.roms || [];
    const body = $('manageRomsBody');
    body.innerHTML = '';

    $('manageRomsList').classList.remove('hidden');
    $('emptyRoms').classList.toggle('hidden', roms.length > 0);

    for (const r of roms) {
      const tr = document.createElement('tr');
      // Solo viaja el nombre: la ruta la arma el servidor a partir del
      // sistema, que es lo que impide que esto acabe borrando cualquier
      // fichero de la Deck.
      tr.innerHTML = `
        <td><div class="gname"><span>${escapeHtml(r.name)}</span></div></td>
        <td class="num">${fmtBytes(r.size)}</td>
        <td><div class="acts">
          <button data-romren="${escapeHtml(r.name)}">${t('Renombrar')}</button>
          <button class="danger" data-romdel="${escapeHtml(r.name)}">${t('Eliminar')}</button>
        </div></td>
      `;
      body.appendChild(tr);
    }

    // Las carátulas se buscan del sistema que se está mirando, así que el
    // bloque solo aparece cuando hay algo cargado.
    const puede = manageSystems.scrapeable && manageSystems.scrapeable[sys];
    $('scrapeBox').classList.toggle('hidden', !roms.length);
    $('scrapeResumen').classList.add('hidden');
    $('btnScrapeSystem').disabled = !puede;
    $('scrapeStatus').textContent = puede
      ? '' : t('libretro no tiene carátulas de {0}.', sys);
  } catch (err) {
    banner(t('No se pudieron listar las ROMs: {0}', err.message), 'err');
  }
}

async function scrapeSystem() {
  const sys = $('manageRomSystem').value;
  if (!sys) return;

  const rehacer = $('scrapeRehacer').checked;
  const okConfirm = await confirmChanges({
    summary: t('Buscar carátulas de ROMs para {0}.', sys),
    items: [
      {
        path: `ES-DE/downloaded_media/${sys}/`,
        desc: rehacer
          ? t('Descarga y sobrescribe carátulas, pantallas de título y capturas desde libretro-thumbnails.')
          : t('Descarga solo las imágenes que falten desde libretro-thumbnails (conserva las existentes).'),
      },
    ],
    confirmLabel: t('Buscar carátulas'),
  });
  if (!okConfirm) return;

  $('scrapeStatus').textContent = '';
  $('scrapeResumen').classList.add('hidden');
  // El resumen se pide cuando el trabajo acaba: lo interesante (qué juegos se
  // quedaron sin carátula) no cabe en la barra de progreso. Si falla, el error
  // ya lo cuenta el panel de progreso, y el resumen guardado sería el de la
  // vez anterior: engañaría más que ayudar. Se apunta ANTES de lanzarlo: con
  // un sistema de dos ROMs el final puede llegar antes que la respuesta.
  alTerminar = { kind: 'scrape', fn: mostrarResumenScrape };
  const ok = await startJob('/api/roms/scrape',
    { system: sys, rehacer },
    t('Buscando carátulas'), t('No se pudo empezar la búsqueda de carátulas'));
  if (!ok) alTerminar = null;
}

async function mostrarResumenScrape() {
  try {
    const { resumen } = await api('/api/roms/scrape-resumen');
    const caja = $('scrapeResumen');
    const sin = resumen.sinImagen || [];
    let html = '<p class="hint">'
      + escapeHtml(t('{0} de {1} juegos con carátula', resumen.conImagen, resumen.juegos)) + ' — '
      + escapeHtml(resumen.imagenes === 1 ? t('1 imagen nueva') : t('{0} imágenes nuevas', resumen.imagenes))
      + (resumen.saltados ? ', ' + escapeHtml(t('{0} ya estaban', resumen.saltados)) : '') + '.</p>';
    if (sin.length) {
      html += '<p class="hint">'
        + escapeHtml(t('Sin carátula en libretro ({0}). Suele ser que el nombre del fichero no coincide con el del volcado original:', sin.length))
        + '</p><ul class="brlist romresults">'
        + sin.map((n) => `<li class="brrow"><span class="nm">${escapeHtml(n)}</span></li>`).join('')
        + '</ul>';
    }
    html += '<p class="hint">'
      + t('Guardadas en {0}.', `<code>${escapeHtml(resumen.mediaDir || '')}</code>`) + ' '
      + escapeHtml(t('En la Deck, recarga la lista de juegos en ES-DE para verlas.')) + '</p>';
    caja.innerHTML = html;
    caja.classList.remove('hidden');
  } catch (err) {
    $('scrapeStatus').textContent = t('No se pudo leer el resumen: ') + err.message;
  }
}

// mostrarSistemaBusqueda pone en la casilla el sistema al que se acota, para
// que no haya que mirar arriba a ver cuál estaba elegido.
function mostrarSistemaBusqueda() {
  const el = $('searchScopeSys');
  if (el) el.textContent = $('downloadRomSystem').value || t('el de destino');
}

async function downloadRom() {
  const url = $('downloadUrl').value.trim();
  const sys = $('downloadRomSystem').value;
  if (!url) { $('downloadStatus').textContent = t('Elige un resultado o pega una URL.'); return; }
  if (!sys) { $('downloadStatus').textContent = t('Elige el sistema de destino.'); return; }

  const okConfirm = await confirmChanges({
    summary: t('Descargar ROM en el sistema {0}.', sys),
    items: [
      {
        path: `${inventory.romsDir}/${sys}/`,
        desc: t('El equipo SteamOS descarga el fichero directamente desde la URL indicada.'),
      },
    ],
    confirmLabel: t('Descargar'),
  });
  if (!okConfirm) return;

  $('downloadStatus').textContent = '';
  // La descarga es un trabajo largo del servidor: el avance y el botón de
  // cancelar salen por el panel de progreso, como enviar una ROM.
  const ok = await startJob('/api/roms/download', { url, system: sys },
    t('Descargando ROM'), t('No se pudo empezar la descarga'));
  if (ok) {
    $('downloadUrl').value = '';
    $('searchRomsList').classList.add('hidden');
  }
}

async function searchRom() {
  const q = $('searchRomInput').value.trim();
  if (!q) return;

  const btn = $('btnSearchRom');
  const lista = $('searchRomsList');
  btn.disabled = true;
  $('searchStatus').textContent = t('Buscando en archive.org…');
  lista.innerHTML = '';
  lista.classList.add('hidden');

  // Acotar a la plataforma elegida es lo normal: teniendo puesto arcade, los
  // resultados de PSP solo estorban. La casilla deja abrir la búsqueda a todo
  // cuando lo que quieres es ver qué hay y elegir.
  const sys = $('searchTodos').checked ? '' : $('downloadRomSystem').value;

  try {
    const res = await api('/api/roms/search?q=' + encodeURIComponent(q)
      + (sys ? '&system=' + encodeURIComponent(sys) : ''));
    const list = res.results || [];
    if (!list.length) {
      $('searchStatus').textContent = sys
        ? t('Sin resultados de {0}. Prueba a buscar en todos los sistemas.', sys)
        : t('Sin resultados con archivo descargable.');
      return;
    }

    $('searchStatus').textContent = '';
    lista.classList.remove('hidden');
    for (const r of list) {
      const li = document.createElement('li');
      li.className = 'brrow';
      li.innerHTML = `
        <span class="ic">🎮</span>
        <span class="nm">${escapeHtml(r.title)}</span>
        <span class="sz">${fmtBytes(r.size)}</span>
      `;
      // Elegir un resultado rellena la casilla de la URL en vez de guardarla
      // aparte: así lo que se va a descargar está a la vista antes de darle.
      li.onclick = () => {
        for (const otro of lista.querySelectorAll('.brrow')) otro.classList.remove('sel');
        li.classList.add('sel');
        $('downloadUrl').value = r.url;
      };
      lista.appendChild(li);
    }
  } catch (err) {
    $('searchStatus').textContent = t('La búsqueda falló: ') + err.message;
  } finally {
    btn.disabled = false;
  }
}
