'use strict';

// Enviar un juego de Windows a la Deck: opciones del formulario,
// autodetección de la carpeta y el envío.

function renderSendOptions() {
  $('gamesDirLabel').textContent = inventory.gamesDir || '~/Games';

  const sel = $('sendProton');
  const cur = sel.value;
  sel.innerHTML = `<option value="">${escapeHtml(t('Ninguno (ejecutable nativo de Linux)'))}</option>`;
  for (const t of inventory.compatTools || []) {
    const o = document.createElement('option');
    o.value = t.name;
    o.textContent = t.displayName;
    sel.appendChild(o);
  }
  // Por defecto Proton Experimental, que es lo que mejor funciona en la Deck.
  const exp = (inventory.compatTools || []).find((t) => t.name === 'proton_experimental');
  sel.value = cur || (exp ? exp.name : '');

  // Enviar y Descargar ofrecen TODOS los sistemas: la primera ROM de un
  // sistema va a una carpeta que aún está vacía. Gestionar es al revés y se
  // llena aparte, desde /api/roms/systems.
  const selectores = ['romSystem', 'downloadRomSystem'].map($);
  for (const sel of selectores) sel.innerHTML = '';
  for (const s of inventory.romSystems || []) {
    for (const sel of selectores) {
      const o = document.createElement('option');
      o.value = s; o.textContent = s;
      sel.appendChild(o);
    }
  }
  mostrarSistemaBusqueda();
  $('romsDirLabel').textContent = inventory.romsDir || t('no se encontró EmuDeck');
  updateJobButtons();
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
  $('detectTitle').textContent = t('Analizando la carpeta…');
  $('detectList').innerHTML = '';
  $('detectAlt').classList.add('hidden');

  try {
    detection = await api('/api/detect', { localPath: path, overrideAppId: overrideAppId || '' });
  } catch (e) {
    $('detectTitle').textContent = t('No se pudo analizar');
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
      t('encontrado en {0}', `<code>${escapeHtml(local.appIdSource)}</code>`)]);
  } else if (online && online.appId) {
    rows.push(['warn', `Steam appid <strong>${escapeHtml(online.appId)}</strong>`,
      t('deducido del nombre, no venía en la carpeta')]);
  } else {
    rows.push(['warn', t('Sin identificar en Steam'),
      online && online.note ? escapeHtml(online.note) : t('puede ser un juego que no está en la tienda')]);
  }

  // Nombre
  const officialName = online && online.name ? online.name : local.cleanName;
  if (officialName) {
    const extra = online && online.developer ? escapeHtml(online.developer) : t('nombre limpiado de la carpeta');
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
      detail += ' · ' + t('descartados: {0}', escapeHtml(local.skipped.slice(0, 4).join(', ')));
    }
    rows.push(['ok', `<code>${escapeHtml(best.rel)}</code>`, detail]);
    if (exes.length > 1) {
      rows.push(['info', t('Hay {0} ejecutables candidatos', exes.length),
        t('si no arranca, cámbialo abajo: {0}', escapeHtml(exes.slice(1, 4).map((e) => e.rel).join(', ')))]);
    }
  } else {
    rows.push(['bad', t('No se encontró ningún .exe'), t('tendrás que indicarlo a mano')]);
  }

  // Proton
  if (online && online.proton) {
    const p = online.proton;
    const tool = protonFor(p.suggested);
    const kind = ['platinum', 'gold', 'native'].includes(p.tier) ? 'ok'
      : (p.tier === 'borked' ? 'bad' : 'warn');
    rows.push([kind, `ProtonDB: <strong>${escapeHtml(p.tier || '?')}</strong>` +
      (p.bestTier && p.bestTier !== p.tier ? ' ' + t('(mejor reportado: {0})', escapeHtml(p.bestTier)) : ''),
      escapeHtml(p.advice || '')]);
    if (tool) {
      $('sendProton').value = tool;
      const label = (inventory.compatTools.find((t) => t.name === tool) || {}).displayName || tool;
      rows.push(['info', t('Proton sugerido: {0}', `<strong>${escapeHtml(label)}</strong>`), t('puedes cambiarlo abajo')]);
    }
  }

  $('detectTitle').textContent = t('Detección');
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

// detectedAppId devuelve el appid con el que buscar las carátulas: manda el
// que el usuario haya elegido entre las alternativas.
function detectedAppId() {
  if (!detection) return '';
  const alt = $('detectMatches');
  if (!$('detectAlt').classList.contains('hidden') && alt.value) return alt.value;
  return (detection.online && detection.online.appId) || (detection.local && detection.local.steamAppId) || '';
}

async function sendGame() {
  const path = $('sendPath').value;
  if (!path) { banner(t('Elige la carpeta del juego.'), 'err'); return; }
  const addShortcut = $('addShortcut').checked;
  if (addShortcut && !$('sendExe').value) { banner(t('Elige el ejecutable.'), 'err'); return; }

  const name = $('sendName').value.trim() || path.split(/[\\/]/).pop();
  const folderName = path.split(/[\\/]/).filter(Boolean).pop() || name;
  const compatTool = $('sendProton').value;
  const artwork = addShortcut && $('sendArtwork').checked;

  const items = [
    {
      path: `~/Games/${folderName}`,
      desc: t('Copia la carpeta del juego desde este PC.'),
    },
  ];
  if (addShortcut) {
    items.push({
      path: 'userdata/<id>/config/shortcuts.vdf',
      desc: inventory && inventory.steamRunning
        ? t('Registra "{0}" mediante la API de Steam (conserva tus accesos directos actuales).', name)
        : t('Añade "{0}" conservando el resto de accesos directos (guarda copia .deckman.bak).', name),
    });
  }
  if (compatTool) {
    items.push({
      path: 'config/config.vdf',
      desc: t('Asigna {0} en CompatToolMapping para este juego (conserva el resto; copia .deckman.bak).', compatTool),
    });
  }
  if (artwork) {
    items.push({
      path: 'userdata/<id>/config/grid/',
      desc: t('Descarga las carátulas faltantes desde SteamGridDB (no borra ficheros existentes).'),
    });
  }

  const ok = await confirmChanges({
    summary: t('Enviar "{0}" a SteamOS.', name),
    items,
    confirmLabel: t('Enviar juego'),
  });
  if (!ok) return;

  await startJob('/api/send-game', {
    localPath: path,
    name: $('sendName').value.trim(),
    exe: $('sendExe').value.trim(),
    compatTool,
    launchOptions: $('sendOpts').value.trim(),
    addShortcut,
    artwork,
    steamAppId: detectedAppId(),
  }, t('Enviando juego'), t('No se pudo enviar el juego'));
}
