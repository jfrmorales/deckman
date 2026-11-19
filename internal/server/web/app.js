'use strict';

// Arranque: cablea los botones de index.html con las funciones del resto de
// ficheros. Se carga el último.

// ---------- arranque ----------

function init() {
  // Con `= connect` el MouseEvent llegaría como primer argumento; ver connect().
  $('btnConnect').onclick = () => connect();
  // Añadir otra: el formulario viene relleno con la Deck activa, así que sin
  // vaciarlo "añadir" acabaría actualizando la que ya estaba.
  $('btnNewDeck').onclick = () => {
    $('host').value = '';
    $('deckName').value = '';
    $('password').value = '';
    $('port').value = 22;
    $('user').value = 'deck';
    connError('');
    $('host').focus();
  };
  $('btnQuit').onclick = quit;
  $('idioma').onchange = cambiarIdioma;
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
  $('btnSettings').onclick = openSettings;
  $('artworkHint').onclick = openSettings;
  $('setClose').onclick = () => $('settingsModal').classList.add('hidden');
  $('btnUpdateBadge').onclick = promptUpdate;
  $('btnCheckUpdate').onclick = checkForUpdates;
  $('btnApplyUpdate').onclick = promptUpdate;
  $('autoCheckUpdates').onchange = toggleAutoCheckUpdates;
  $('gridKey').addEventListener('keydown', (e) => { if (e.key === 'Enter') saveGridKey(); });
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
    const prev = sel.dataset.prev ?? '';
    const appId = sel.dataset.appid;
    const g = (inventory.games || []).find((x) => String(x.appId) === String(appId));
    const toolLabel = sel.options[sel.selectedIndex]?.textContent || sel.value || t('Por defecto');

    const ok = await confirmChanges({
      summary: t('Cambiar Proton de "{0}" a {1}.', g ? g.name : appId, toolLabel),
      items: [
        {
          path: inventory && inventory.steamRunning ? 'Steam (API)' : 'config/config.vdf',
          desc: inventory && inventory.steamRunning
            ? t('Aplica el cambio directamente en Steam sin tocar otros ajustes.')
            : t('Actualiza CompatToolMapping para este juego conservando el resto (copia .deckman.bak).'),
        },
      ],
      confirmLabel: t('Cambiar Proton'),
    });
    if (!ok) {
      sel.value = prev;
      return;
    }

    try {
      const r = await api('/api/compat', { appId, tool: sel.value });
      sel.dataset.prev = sel.value;
      // Si lo ha aplicado el propio Steam, ya está hecho. Solo cuando se ha
      // escrito el fichero (Steam cerrado) hace falta arrancarlo: decir
      // "reinicia Steam" tras un cambio en caliente era justo lo contrario,
      // porque al salir Steam reescribe config.vdf desde su copia en memoria.
      banner(r.live
        ? t('Proton actualizado. Steam ya lo ha aplicado, sin reiniciar.')
        : t('Proton actualizado. Se verá la próxima vez que arranques Steam.'), 'ok');
    } catch (err) {
      sel.value = prev;
      banner(t('No se pudo cambiar Proton: {0}', err.message), 'err');
    }
  });

  connectEvents();
  refreshState();

  // Subpestañas de Emulación: solo se ve el panel de la elegida.
  document.querySelectorAll('.subtab').forEach((tab) => {
    tab.onclick = () => {
      document.querySelectorAll('.subtab').forEach((x) => x.classList.toggle('active', x === tab));
      document.querySelectorAll('.emu-subpanel').forEach((p) => {
        p.classList.toggle('hidden', p.id !== tab.dataset.subtab);
      });
      // Los sistemas con ROMs hay que ir a contarlos a la Deck, así que se
      // piden al abrir Gestionar y no al cargar la página.
      if (tab.dataset.subtab === 'emu-manage') loadManageSystems();
    };
  });

  // --- emulación ---
  $('btnScrapeSystem').onclick = scrapeSystem;
  // Elegir el sistema ya carga su lista: el botón «Cargar» era un paso que
  // nadie quería dar.
  $('manageRomSystem').onchange = loadManageRoms;
  $('btnDownloadRom').onclick = downloadRom;
  $('btnSearchRom').onclick = searchRom;
  // La casilla nombra el sistema al que acota, así que sigue al desplegable.
  $('downloadRomSystem').onchange = mostrarSistemaBusqueda;
  $('searchRomInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') searchRom();
  });
  // Escribir la URL a mano deshace la marca del resultado elegido: si no,
  // queda uno resaltado que ya no es lo que se va a descargar.
  $('downloadUrl').addEventListener('input', () => {
    for (const li of $('searchRomsList').querySelectorAll('.brrow.sel')) li.classList.remove('sel');
  });

  $('manageRomsBody').addEventListener('click', async (e) => {
    const btn = e.target.closest('button');
    if (!btn) return;
    // El sistema sale del desplegable, no de la fila: es el mismo con el que
    // se cargó la lista y el servidor solo acepta sistema + nombre.
    const system = $('manageRomSystem').value;

    if (btn.dataset.romdel) {
      const name = btn.dataset.romdel;
      const ok = await confirmChanges({
        summary: t('Borrar "{0}" de {1}.', name, system),
        items: [
          {
            path: `${inventory.romsDir}/${system}/${name}`,
            desc: t('Elimina el fichero de forma permanente (no hay papelera).'),
          },
        ],
        confirmLabel: t('Eliminar'),
        danger: true,
      });
      if (!ok) return;
      try {
        await api('/api/roms/delete', { system, name });
        banner(t('ROM borrada.'), 'ok');
        loadManageRoms();
      } catch (err) {
        banner(t('No se pudo borrar: {0}', err.message), 'err');
      }
    }

    if (btn.dataset.romren) {
      const name = btn.dataset.romren;
      // Un solo diálogo: el nombre se escribe y se confirma en el mismo
      // sitio, en vez de un prompt() del navegador seguido de otra pregunta.
      const newName = await confirmChanges({
        title: t('Renombrar ROM'),
        summary: t('Renombrar ROM en {0}.', system),
        items: [
          {
            path: `${inventory.romsDir}/${system}/${name}`,
            desc: t('Cambia únicamente el nombre del fichero.'),
          },
        ],
        input: { label: t('Nuevo nombre:'), value: name },
        confirmLabel: t('Renombrar'),
      });
      if (!newName || newName === name) return;
      try {
        await api('/api/roms/rename', { system, name, newName });
        banner(t('ROM renombrada.'), 'ok');
        loadManageRoms();
      } catch (err) {
        banner(t('No se pudo renombrar: {0}', err.message), 'err');
      }
    }
  });
}

document.addEventListener('DOMContentLoaded', init);
