'use strict';

// Núcleo de la interfaz: el estado compartido y lo que usan todas las demás
// piezas (la llamada a la API, formatos, el banner y el diálogo de
// confirmación). Se carga el primero.
//
// Los ficheros son scripts clásicos, no módulos: comparten el ámbito global, y
// por eso una función de biblioteca.js puede llamar a otra de trabajos.js sin
// importarla. El orden de carga está en index.html; app.js va el último y es
// el que cablea los botones.

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
  if (!res.ok) {
    // El cuerpo entero viaja con el error: hay respuestas de fallo que traen
    // más que un texto (la de clave de host cambiada lleva las dos huellas y
    // el distintivo con el que se decide si ofrecer volver a confiar), y sin
    // esto se perdía al construir el Error.
    const err = new Error(data.error || `error ${res.status}`);
    err.data = data;
    err.status = res.status;
    throw err;
  }
  return data;
}

function fmtBytes(b) {
  if (!b) return '—';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return `${b.toFixed(i >= 2 ? 1 : 0)} ${u[i]}`;
}

// localeActual da el formato de fechas del idioma en que se está viendo la
// interfaz: con 'es-ES' fijo, en inglés salía «played: 21/9/2026».
function localeActual() {
  return { en: 'en-GB', fr: 'fr-FR' }[I18N.lang] || 'es-ES';
}

function fmtDate(ts) {
  if (!ts) return t('nunca');
  return new Date(ts * 1000).toLocaleDateString(localeActual());
}

function banner(msg, kind) {
  const el = $('banner');
  el.textContent = msg;
  el.className = 'banner' + (kind ? ' ' + kind : '');
  if (!msg) el.classList.add('hidden');
  clearTimeout(banner._t);
  if (msg && kind === 'ok') banner._t = setTimeout(() => banner(''), 6000);
}

function escapeHtml(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

// confirmChanges es el único diálogo de confirmación de la aplicación: enseña
// qué se va a hacer, con las rutas afectadas si las hay, y espera la decisión.
// Devuelve true o false.
//
// Con `input` ({ label, value }) pide además un texto, y entonces devuelve lo
// escrito, o null si se cancela o se deja vacío.
function confirmChanges({ title, summary, items, note, confirmLabel, danger, input }) {
  return new Promise((resolve) => {
    const modal = $('previewModal');
    const btnOk = $('chgConfirm');
    const btnCancel = $('chgCancel');
    const btnClose = $('chgClose');

    $('chgTitle').textContent = title || t('Cambios que se van a aplicar');
    $('chgSummary').textContent = summary || '';
    $('chgList').innerHTML = (items || []).map((it) =>
      `<li><span class="chg-path">${escapeHtml(it.path)}</span><span class="chg-desc">${escapeHtml(it.desc)}</span></li>`
    ).join('');

    const noteEl = $('chgNote');
    if (note) {
      noteEl.textContent = note;
      noteEl.classList.remove('hidden');
    } else {
      noteEl.textContent = '';
      noteEl.classList.add('hidden');
    }

    btnOk.textContent = confirmLabel || t('Aplicar cambios');
    btnOk.className = danger ? 'danger' : 'primary';

    const inp = $('chgInput');
    $('chgInputRow').classList.toggle('hidden', !input);
    if (input) {
      $('chgInputLabel').textContent = input.label || '';
      inp.value = input.value || '';
    }

    const done = (acepta) => {
      let res = acepta;
      if (input) res = acepta ? (inp.value.trim() || null) : null;
      modal.classList.add('hidden');
      document.removeEventListener('keydown', onKey);
      modal.onclick = null;
      btnOk.onclick = null;
      btnCancel.onclick = null;
      btnClose.onclick = null;
      resolve(res);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') done(false);
      if (e.key === 'Enter' && e.target === inp) { e.preventDefault(); done(true); }
    };

    btnOk.onclick = () => done(true);
    btnCancel.onclick = () => done(false);
    btnClose.onclick = () => done(false);
    modal.onclick = (e) => { if (e.target === modal) done(false); };
    document.addEventListener('keydown', onKey);

    modal.classList.remove('hidden');
    if (input) { inp.focus(); inp.select(); } else btnOk.focus();
  });
}
