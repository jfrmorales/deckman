'use strict';

// Autoactualización: consulta de nuevas releases en GitHub, aviso discreto en
// la barra superior, ajustes y progreso de descarga/instalación.

let updatePollStarted = false;

function updatePhaseLabel(phase) {
  switch (phase) {
    case 'verificando':
      return t('Verificando firma y sumas SHA-256…');
    case 'descargando':
      return t('Descargando actualización…');
    case 'instalando':
      return t('Instalando actualización…');
    case 'reiniciando':
      return t('Reiniciando deckman…');
    default:
      return phase || '';
  }
}

function manualUpdateReason(code) {
  switch (code) {
    case 'dev':
      return t('Compilación de desarrollo: no se actualiza sola.');
    case 'unsupported_platform':
      return t('Tu sistema no tiene autoactualización integrada; descárgala desde GitHub.');
    case 'flatpak_no_bundle':
      return t('Esta versión todavía no incluye paquete Flatpak en GitHub.');
    case 'no_asset':
      return t('No se encontró el binario para este sistema en la release.');
    case 'no_sums':
      return t('La release no incluye SHA256SUMS, así que no se puede verificar.');
    case 'flatpak_system':
      return t('El Flatpak está instalado a nivel de sistema o sin acceso al host; actualízalo desde tu gestor o descarga el paquete en GitHub.');
    case 'no_write_permission':
      return t('No hay permiso de escritura en la carpeta del ejecutable; sustitúyelo a mano desde GitHub.');
    default:
      return t('Descarga esta versión manualmente desde GitHub.');
  }
}

function renderUpdateUI() {
  const u = state.update || {};

  const autoChk = $('autoCheckUpdates');
  if (autoChk) autoChk.checked = state.autoCheckUpdates !== false;

  const badge = $('btnUpdateBadge');
  if (badge) {
    const showBadge = !!(u.available && u.latest && u.status !== 'restarting');
    badge.classList.toggle('hidden', !showBadge);
    if (showBadge) {
      badge.textContent = `↑ ${u.latest}`;
      badge.title = t('Nueva versión disponible: {0}', u.latest);
    }
  }

  const btnChk = $('btnCheckUpdate');
  if (btnChk) {
    const busy = u.status === 'checking' || u.status === 'applying' || u.status === 'restarting';
    btnChk.disabled = busy;
    btnChk.textContent = u.status === 'checking' ? t('Buscando…') : t('Buscar actualizaciones');
  }

  const btnApply = $('btnApplyUpdate');
  if (btnApply) {
    const showApply = !!(u.available && u.canApply && u.status !== 'applying' && u.status !== 'restarting');
    btnApply.classList.toggle('hidden', !showApply);
    btnApply.textContent = u.latest ? t('Instalar {0}', u.latest) : t('Instalar actualización');
  }

  const relLink = $('updateReleaseLink');
  if (relLink) {
    relLink.classList.toggle('hidden', !u.releaseUrl);
    if (u.releaseUrl) relLink.href = u.releaseUrl;
  }

  const stEl = $('updateState');
  if (!stEl) return;

  if (u.status === 'checking') {
    stEl.textContent = t('Buscando nuevas versiones en GitHub…');
  } else if (u.status === 'applying') {
    stEl.textContent = updatePhaseLabel(u.phase);
  } else if (u.status === 'restarting') {
    stEl.textContent = t('Actualización instalada. Reiniciando…');
  } else if (u.status === 'error') {
    stEl.textContent = t('Error al actualizar: {0}', u.error || t('error desconocido'));
  } else if (u.available) {
    if (u.canApply) {
      stEl.textContent = u.signed
        ? t('Disponible {0} ({1}, firma cosign y SHA-256).', u.latest, fmtBytes(u.assetSize))
        : t('Disponible {0} ({1}, SHA-256).', u.latest, fmtBytes(u.assetSize));
    } else {
      stEl.textContent = t('Disponible {0}.', u.latest) + ' ' + manualUpdateReason(u.manualReason);
    }
  } else if (u.status === 'up_to_date') {
    stEl.textContent = u.manualReason === 'dev'
      ? manualUpdateReason('dev')
      : t('Tienes la última versión disponible.');
  } else {
    stEl.textContent = state.version === 'dev' ? manualUpdateReason('dev') : '';
  }
}

async function checkForUpdates() {
  state.update = { ...(state.update || {}), status: 'checking', error: '' };
  renderUpdateUI();
  try {
    const u = await api('/api/update/check', {});
    state.update = u;
    renderUpdateUI();
    if (u.available) {
      banner(t('Nueva versión disponible: {0}', u.latest), 'ok');
    } else if (u.manualReason !== 'dev') {
      banner(t('Tienes la última versión disponible.'), 'ok');
    }
  } catch (e) {
    state.update = { ...(state.update || {}), status: 'error', error: e.message };
    renderUpdateUI();
    banner(t('No se pudo comprobar si hay actualizaciones: {0}', e.message), 'err');
  }
}

async function toggleAutoCheckUpdates() {
  const chk = $('autoCheckUpdates');
  const next = !!chk.checked;
  try {
    const r = await api('/api/settings', { autoCheckUpdates: next });
    state.autoCheckUpdates = r.autoCheckUpdates;
  } catch (e) {
    chk.checked = !next;
    banner(t('No se pudo guardar: {0}', e.message), 'err');
  }
}

async function promptUpdate() {
  const u = state.update || {};
  if (!u.available) {
    openSettings();
    return;
  }
  if (u.status === 'applying') {
    $('progress').classList.remove('hidden');
    return;
  }
  if (!u.canApply) {
    const ok = await confirmChanges({
      title: t('Nueva versión disponible: {0}', u.latest),
      summary: manualUpdateReason(u.manualReason),
      items: u.assetName ? [{ path: u.assetName, desc: t('Paquete publicado en GitHub Releases.') }] : [],
      note: u.notes ? t('Novedades:') + '\n' + u.notes : '',
      confirmLabel: t('Abrir GitHub'),
    });
    if (ok && u.releaseUrl) {
      window.open(u.releaseUrl, '_blank', 'noopener');
    }
    return;
  }

  const verifyDesc = u.signed
    ? t('Verificación criptográfica cosign y SHA-256 antes de instalar.')
    : t('Verificación SHA-256 antes de instalar.');
  const sizePrefix = u.assetSize ? `${fmtBytes(u.assetSize)} · ` : '';

  const ok = await confirmChanges({
    title: t('Actualizar deckman'),
    summary: t('Actualizar deckman de {0} a {1}.', u.current || state.version || '', u.latest || ''),
    items: [
      {
        path: u.assetName || '',
        desc: sizePrefix + verifyDesc,
      },
      {
        path: u.isFlatpak ? 'io.github.jfrmorales.deckman (Flatpak --user)' : t('Ejecutable actual'),
        desc: u.isFlatpak
          ? t('Instala el paquete Flatpak del usuario y reinicia la aplicación.')
          : t('Sustituye el ejecutable en disco y reinicia deckman.'),
      },
    ],
    note: u.notes ? t('Novedades:') + '\n' + u.notes : '',
    confirmLabel: t('Actualizar y reiniciar'),
  });
  if (!ok) return;

  $('settingsModal').classList.add('hidden');
  await applyUpdate();
}

async function applyUpdate() {
  if (jobBusy()) {
    banner(t('Ya hay una operación en curso: espera a que termine o cancélala.'), 'err');
    return;
  }
  const u = state.update || {};
  try {
    await api('/api/update/apply', {});
    showProgress(t('Actualizando a {0}', u.latest || ''));
    $('pPhase').textContent = updatePhaseLabel('verificando');
  } catch (e) {
    banner(t('No se pudo actualizar: {0}', e.message), 'err');
  }
}

function handleUpdateEvent(u) {
  if (!u) return;
  const prevStatus = state.update ? state.update.status : '';
  state.update = u;
  renderUpdateUI();

  if (u.status === 'applying') {
    if ($('progress').classList.contains('hidden')) {
      showProgress(t('Actualizando a {0}', u.latest || ''));
    }
    $('pTitle').textContent = t('Actualizando a {0}', u.latest || '');
    $('pPhase').textContent = updatePhaseLabel(u.phase);
    const pct = u.bytesTotal ? (u.bytesDone / u.bytesTotal) * 100 : 0;
    const fill = $('pFill');
    fill.style.width = Math.min(100, pct).toFixed(1) + '%';
    fill.className = 'pfill';
    $('pBytes').textContent = (u.bytesTotal && u.bytesDone)
      ? t('{0} de {1}', fmtBytes(u.bytesDone), fmtBytes(u.bytesTotal))
      : '';
    $('pSpeed').textContent = '';
    $('pFile').textContent = u.assetName || '';
    const qEl = $('pQueue');
    if (qEl) qEl.classList.add('hidden');

    const lockCancel = u.phase === 'instalando' || u.phase === 'reiniciando';
    $('btnCancel').classList.toggle('hidden', lockCancel);
    $('btnCloseProgress').classList.add('hidden');
    return;
  }

  if (u.status === 'error' && prevStatus === 'applying' && !$('progress').classList.contains('hidden')) {
    $('pPhase').textContent = u.error || t('error desconocido');
    $('pFill').className = 'pfill err';
    $('btnCancel').classList.add('hidden');
    $('btnCloseProgress').classList.remove('hidden');
    return;
  }

  if (u.status === 'available' && prevStatus === 'applying' && !$('progress').classList.contains('hidden')) {
    $('progress').classList.add('hidden');
    banner(t('Actualización cancelada.'), '');
    return;
  }

  if (u.status === 'restarting') {
    $('progress').classList.add('hidden');
    document.body.innerHTML =
      `<div class="goodbye"><p>${escapeHtml(t('Actualización instalada. Reiniciando deckman…'))}</p><p class="hint" id="restartHint">${escapeHtml(t('La página volverá a cargar automáticamente en unos segundos.'))}</p></div>`;
    waitForRestart(u.latest || '');
  }
}

async function waitForRestart(targetVersion) {
  if (updatePollStarted) return;
  updatePollStarted = true;
  let sawDrop = false;

  for (let i = 0; i < 50; i++) {
    await new Promise((r) => setTimeout(r, 600));
    try {
      const res = await fetch('/api/state', {
        headers: { 'X-Deckman': '1' },
        cache: 'no-store',
      });
      if (!res.ok) {
        sawDrop = true;
        continue;
      }
      const data = await res.json();
      if (data && data.app === 'deckman' && ((targetVersion && data.version === targetVersion) || sawDrop)) {
        location.reload();
        return;
      }
    } catch {
      sawDrop = true;
    }
  }

  const hint = $('restartHint');
  if (hint) {
    hint.textContent = t('Si no se abre sola, vuelve a abrir deckman.');
  }
}
