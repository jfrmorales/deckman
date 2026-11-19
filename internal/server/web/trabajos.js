'use strict';

// Trabajos largos: el panel de progreso, el canal de eventos del servidor y el
// historial y la cola de operaciones.

// ---------- progreso ----------

function showProgress(title) {
  $('pTitle').textContent = title;
  $('pPhase').textContent = t('empezando…');
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
  $('pTitle').textContent = msg.title || t('Trabajando…');
  $('pPhase').textContent = p.error ? p.error : (p.phase || '');
  const pct = p.bytesTotal ? (p.bytesDone / p.bytesTotal) * 100 : (p.done ? 100 : 0);
  const fill = $('pFill');
  fill.style.width = Math.min(100, pct).toFixed(1) + '%';
  fill.className = 'pfill' + (p.error ? ' err' : (p.done ? ' ok' : ''));

  $('pBytes').textContent = p.bytesTotal
    ? t('{0} de {1}', fmtBytes(p.bytesDone), fmtBytes(p.bytesTotal)) +
      (p.filesTotal ? ' · ' + t('{0}/{1} ficheros', p.filesDone, p.filesTotal) : '')
    : '';
  $('pSpeed').textContent = p.speed ? fmtBytes(p.speed) + '/s' : '';
  $('pFile').textContent = p.message || p.file || '';
  renderProgressQueue();

  if (p.done) {
    $('btnCancel').classList.add('hidden');
    $('btnCloseProgress').classList.remove('hidden');
    renderOperations();
    if (!p.error) loadInventory();
  } else {
    $('btnCancel').classList.remove('hidden');
    $('btnCloseProgress').classList.add('hidden');
  }
}

function renderProgressQueue() {
  const el = $('pQueue');
  if (!el) return;
  const queued = (state.operations || []).filter((op) => op.status === 'en_cola').length;
  el.classList.toggle('hidden', queued === 0);
  if (queued > 0) {
    el.textContent = queued === 1
      ? t('1 movimiento más esperando en la cola')
      : t('{0} movimientos más esperando en la cola', queued);
  }
}

function connectEvents() {
  const es = new EventSource('/api/events');
  es.onmessage = (ev) => {
    // Un evento mal formado no puede tumbar el manejador: si esto revienta,
    // dejan de llegar TODOS los partes de progreso hasta recargar la página.
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    if (msg && msg.type === 'operations') {
      state.operations = msg.operations || [];
      pintarOperaciones();
      return;
    }
    if (msg && msg.type === 'update') {
      handleUpdateEvent(msg.update);
      return;
    }
    if (msg && msg.type === 'progress') {
      // El estado del trabajo se sigue por aquí: es lo que decide si los
      // botones de enviar y mover están disponibles.
      state.job = { id: msg.job, kind: msg.kind, title: msg.title, progress: msg.progress || {} };
      updateJobButtons();
      if ($('progress').classList.contains('hidden')) showProgress(msg.title);
      updateProgress(msg);
      avisarFinDeTrabajo(msg);
    }
  };
  es.onerror = () => { /* EventSource reconecta solo */ };
}

// alTerminar es lo que hay que hacer cuando acabe el trabajo que se acaba de
// lanzar (hoy, enseñar el resumen de carátulas de ROMs). Llega por el mismo
// canal de eventos que el progreso, así que no hace falta preguntar cada poco
// si ya ha terminado.
//
// Va atado al tipo de trabajo: si el que se esperaba no llegó a arrancar, el
// siguiente que termine no puede heredar su aviso.
let alTerminar = null;   // { kind, fn }

function avisarFinDeTrabajo(msg) {
  const p = msg.progress || {};
  if (!p.done || !alTerminar) return;
  const { kind, fn } = alTerminar;
  alTerminar = null;
  if (kind === msg.kind && !p.error) fn();
}

// pintarOperaciones repinta todo lo que depende de la lista de operaciones: el
// panel, los botones de mover y el aviso de cola del progreso. Juntos porque
// olvidarse de uno de los tres dejaba la pantalla contando dos cosas distintas.
function pintarOperaciones() {
  renderOperations();
  updateJobButtons();
  renderProgressQueue();
}

// jobBusy dice si hay un trabajo largo en marcha. El servidor solo admite uno
// a la vez para copias/subidas, mientras que los movimientos extra entran en cola.
function jobBusy() {
  const j = state.job;
  return !!(j && j.progress && !j.progress.done && !j.progress.error);
}

// updateJobButtons bloquea envíos mientras corre un trabajo y marca en los
// botones de mover qué juegos están ya en curso o esperando en la cola.
function updateJobButtons() {
  const busy = jobBusy();
  const moveStates = new Map();
  for (const op of (state.operations || [])) {
    if (op.kind === 'move' && op.appId && (op.status === 'en_curso' || op.status === 'en_cola')) {
      moveStates.set(op.appId, op.status);
    }
  }
  $('btnSend').disabled = busy;
  $('btnSendRom').disabled = busy || !(inventory && inventory.romsDir);
  document.querySelectorAll('#gamesBody button[data-move]').forEach((b) => {
    const st = moveStates.get(b.dataset.move);
    if (st === 'en_curso') {
      b.disabled = true;
      b.textContent = t('Moviendo…');
    } else if (st === 'en_cola') {
      b.disabled = true;
      b.textContent = t('En cola');
    } else {
      b.disabled = !state.connected;
      if (b.dataset.label) b.textContent = b.dataset.label;
    }
  });
}

// startJob lanza una operación larga. Un fallo AL ARRANCARLA no es progreso de
// nada: va al banner, no al panel de progreso.
async function startJob(path, body, titulo, fallo) {
  if (jobBusy()) {
    banner(t('Ya hay una operación en curso: espera a que termine o cancélala.'), 'err');
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

// ---------- historial y cola de operaciones ----------

function fmtOpTime(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  return d.toLocaleString(localeActual(), {
    day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit',
  });
}

function liveOpLocation(op) {
  if (inventory && op.appId) {
    const g = (inventory.games || []).find((x) => x.appId === op.appId);
    if (g) {
      const lib = (inventory.libraries || []).find((l) => l.path === g.libraryPath);
      if (lib) return { label: lib.label, removable: !!lib.removable, path: lib.path };
    }
  }
  const label = op.currentLocation || '';
  const libs = (inventory && inventory.libraries) || [];
  const matchingLib = libs.find((l) => l.label === label);
  return {
    label,
    removable: matchingLib ? !!matchingLib.removable : (!!label && label !== 'Interno'),
    path: matchingLib ? matchingLib.path : '',
  };
}

function isResumableMove(op) {
  if (op.kind !== 'move' || (op.status !== 'interrumpido' && op.status !== 'error')) return false;
  if (!op.appId || !op.targetPath) return false;
  if (inventory) {
    const g = (inventory.games || []).find((x) => x.appId === op.appId);
    if (!g || g.libraryPath === op.targetPath) return false;
  } else if (op.currentLocation && op.targetLabel && op.currentLocation === op.targetLabel) {
    return false;
  }
  return true;
}

function renderOperations() {
  const panel = $('opsPanel');
  if (!panel) return;
  const ops = state.operations || [];
  panel.classList.toggle('hidden', ops.length === 0);
  if (!ops.length) {
    panel.innerHTML = '';
    return;
  }

  let running = 0;
  let queued = 0;
  let done = 0;
  let issues = 0;
  let finished = 0;
  const resumable = [];

  for (const op of ops) {
    if (op.status === 'en_curso') running++;
    else if (op.status === 'en_cola') queued++;
    else if (op.status === 'completado') { done++; finished++; }
    else if (op.status === 'interrumpido' || op.status === 'error') {
      issues++;
      finished++;
      if (isResumableMove(op)) resumable.push(op);
    }
  }

  panel.textContent = '';

  const head = document.createElement('div');
  head.className = 'ops-head';

  const left = document.createElement('div');
  left.className = 'ops-head-left';
  const h3 = document.createElement('h3');
  h3.textContent = t('Historial y cola de operaciones');
  left.appendChild(h3);

  const summary = document.createElement('div');
  summary.className = 'ops-summary';
  const addPill = (text, cls) => {
    const s = document.createElement('span');
    s.className = 'ops-pill ' + cls;
    s.textContent = text;
    summary.appendChild(s);
  };
  if (running > 0) addPill(t('{0} en curso', running), 'run');
  if (queued > 0) addPill(t('{0} en cola', queued), 'queue');
  if (done > 0) addPill(done === 1 ? t('{0} completada', done) : t('{0} completadas', done), 'ok');
  if (issues > 0) addPill(issues === 1 ? t('{0} pendiente', issues) : t('{0} pendientes', issues), 'warn');
  left.appendChild(summary);
  head.appendChild(left);

  const actions = document.createElement('div');
  actions.className = 'ops-actions';
  if (resumable.length > 0 && state.connected) {
    const btnRes = document.createElement('button');
    btnRes.className = 'mini primary';
    btnRes.textContent = t('Reanudar pendientes ({0})', resumable.length);
    btnRes.onclick = () => resumePendingOperations();
    actions.appendChild(btnRes);
  }
  if (finished > 0) {
    const btnClr = document.createElement('button');
    btnClr.className = 'mini';
    btnClr.textContent = t('Limpiar historial');
    btnClr.onclick = () => clearOperations('');
    actions.appendChild(btnClr);
  }
  head.appendChild(actions);
  panel.appendChild(head);

  const ul = document.createElement('ul');
  ul.className = 'ops-list';

  for (const op of ops) {
    const li = document.createElement('li');
    li.className = 'ops-row';

    const badge = document.createElement('span');
    let bCls = 'ok';
    let bTxt = op.kind === 'move' ? t('Movido') : t('Completado');
    if (op.status === 'en_curso') { bCls = 'run'; bTxt = t('En curso'); }
    else if (op.status === 'en_cola') { bCls = 'queue'; bTxt = t('En cola'); }
    else if (op.status === 'interrumpido') { bCls = 'warn'; bTxt = t('Cortado / pendiente'); }
    else if (op.status === 'error') { bCls = 'err'; bTxt = t('Error'); }
    badge.className = 'ops-badge ' + bCls;
    badge.textContent = bTxt;
    li.appendChild(badge);

    const main = document.createElement('div');
    main.className = 'ops-main';

    const title = document.createElement('div');
    title.className = 'ops-title';
    const nameEl = document.createElement('span');
    nameEl.textContent = op.gameName || op.title || '';
    title.appendChild(nameEl);
    if (op.kind === 'move' && op.fromLabel && op.targetLabel) {
      const route = document.createElement('span');
      route.className = 'ops-route';
      route.textContent = `${t(op.fromLabel)} → ${t(op.targetLabel)}`;
      title.appendChild(route);
    }
    main.appendChild(title);

    const metaParts = [];
    if (op.message) metaParts.push(op.message);
    if (op.bytesTotal) metaParts.push(fmtBytes(op.bytesTotal));
    const ts = fmtOpTime(op.finishedAt || op.startedAt);
    if (ts) metaParts.push(ts);
    if (metaParts.length) {
      const meta = document.createElement('div');
      meta.className = 'ops-meta';
      meta.textContent = metaParts.join(' · ');
      main.appendChild(meta);
    }
    li.appendChild(main);

    const locInfo = liveOpLocation(op);
    if (locInfo.label) {
      const loc = document.createElement('span');
      loc.className = 'ops-loc' + (locInfo.removable ? ' sd' : '');
      loc.textContent = t('Está en: {0}', t(locInfo.label));
      li.appendChild(loc);
    }

    const rowActs = document.createElement('div');
    rowActs.className = 'ops-row-acts';
    if (op.status === 'en_cola') {
      const qBtn = document.createElement('button');
      qBtn.className = 'mini';
      qBtn.textContent = t('Quitar');
      qBtn.onclick = () => clearOperations(op.id);
      rowActs.appendChild(qBtn);
    } else if (isResumableMove(op) && state.connected) {
      const rBtn = document.createElement('button');
      rBtn.className = 'mini';
      rBtn.textContent = t('Reintentar');
      rBtn.onclick = () => retryMoveOperation(op);
      rowActs.appendChild(rBtn);
    }
    if (op.status !== 'en_curso') {
      const delBtn = document.createElement('button');
      delBtn.className = 'mini ops-x';
      delBtn.title = t('Quitar del historial');
      delBtn.textContent = '✕';
      delBtn.onclick = () => clearOperations(op.id);
      rowActs.appendChild(delBtn);
    }
    li.appendChild(rowActs);

    ul.appendChild(li);
  }
  panel.appendChild(ul);
}

async function clearOperations(id) {
  try {
    const res = await api('/api/operations/clear', { id: id || '' });
    state.operations = res.operations || [];
    pintarOperaciones();
  } catch (e) {
    banner(e.message, 'err');
  }
}

// relanzarMovimiento vuelve a pedir un movimiento que se quedó a medias y
// quita su entrada vieja del historial. Devuelve si quedó en cola, o lanza el
// error del servidor.
async function relanzarMovimiento(op) {
  const res = await api('/api/move', { appId: op.appId, target: op.targetPath });
  await api('/api/operations/clear', { id: op.id }).catch(() => {});
  if (!res.queued && $('progress').classList.contains('hidden')) {
    showProgress(t('Moviendo {0}', op.gameName || op.title));
  }
  return !!res.queued;
}

async function retryMoveOperation(op) {
  try {
    const enCola = await relanzarMovimiento(op);
    await refreshState();
    if (enCola) banner(t('Añadido a la cola: {0}', op.gameName || op.title), 'ok');
  } catch (e) {
    banner(t('No se pudo mover el juego') + ': ' + e.message, 'err');
  }
}

async function resumePendingOperations() {
  const resumable = (state.operations || []).filter(isResumableMove).reverse();
  if (!resumable.length) return;
  banner(t('Reanudando movimientos…'), '');
  let started = 0;
  for (const op of resumable) {
    try {
      await relanzarMovimiento(op);
      started++;
    } catch (e) {
      banner(t('No se pudo mover el juego') + ': ' + e.message, 'err');
    }
  }
  await refreshState();
  if (started > 0) banner(t('Cola reanudada ({0}).', started), 'ok');
}
