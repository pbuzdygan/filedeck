'use strict';
// Filedeck browser client. Security rules for this file:
// - untrusted strings (file names, user names) are inserted only via textContent;
// - no innerHTML/eval (enforced by CSP Trusted Types);
// - the CSRF token lives only in memory and is re-read from /api/auth/me.
// Every user-visible text goes through t('key') with lang-en.json as the
// default; TestTranslations checks that each key exists in every language.

const PERM = { list: 1, read: 2, create: 4, modify: 8 };
const PERM_KEYS = [['list', 'perm.l', 'perm.list'], ['read', 'perm.r', 'perm.read'], ['create', 'perm.c', 'perm.create'], ['modify', 'perm.m', 'perm.modify']];
const LANGS = ['en', 'pl'];

const state = {
  csrf: null, user: null, limits: null, spaces: [], space: null, path: '.', busy: false, files: [], entries: [],
  selecting: false, selected: new Set(), anchor: null,
  sort: loadSort(), // {key: 'name'|'size'|'modified', dir: 1|-1}, remembered per browser
  search: null, // {q, results, truncated} while showing search results
};
function loadSort() {
  try {
    const s = JSON.parse(localStorage.getItem('filedeck-sort'));
    if (s && ['name', 'size', 'modified'].includes(s.key) && (s.dir === 1 || s.dir === -1)) return s;
  } catch (e) { /* default */ }
  return { key: 'name', dir: 1 };
}
const $ = (id) => document.getElementById(id);

// ---------- i18n ----------

let dict = {};
let lang = 'en';
function t(key, vars = {}) {
  const s = dict[key] ?? key;
  return s.replace(/\{(\w+)\}/g, (_, k) => (k in vars ? String(vars[k]) : '{' + k + '}'));
}
async function loadLanguage(name) {
  const res = await fetch('/assets/lang-' + name + '.json', { cache: 'no-store' });
  dict = await res.json();
  lang = name;
  document.documentElement.lang = name;
  $('lang-label').textContent = name.toUpperCase();
  for (const e of document.querySelectorAll('[data-i18n]')) e.textContent = t(e.dataset.i18n);
  for (const e of document.querySelectorAll('[data-i18n-placeholder]')) e.placeholder = t(e.dataset.i18nPlaceholder);
  for (const e of document.querySelectorAll('[data-i18n-title]')) e.title = t(e.dataset.i18nTitle);
  for (const e of document.querySelectorAll('[data-i18n-aria-label]')) e.setAttribute('aria-label', t(e.dataset.i18nAriaLabel));
  for (const e of document.querySelectorAll('[data-tip-i18n]')) e.dataset.tip = t(e.dataset.tipI18n);
  applyTheme(currentTheme());
}
function savedLanguage() {
  try { const l = localStorage.getItem('filedeck-lang'); if (LANGS.includes(l)) return l; } catch (e) { /* default */ }
  return 'en';
}
$('lang').addEventListener('click', async () => {
  const next = LANGS[(LANGS.indexOf(lang) + 1) % LANGS.length];
  try { localStorage.setItem('filedeck-lang', next); } catch (e) { /* optional */ }
  await loadLanguage(next);
  rerender();
});

// ---------- helpers ----------

function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === 'text') node.textContent = v;
    else if (k === 'on') for (const [ev, fn] of Object.entries(v)) node.addEventListener(ev, fn);
    else if (k === 'tip') { node.dataset.tip = v; node.setAttribute('aria-label', v); }
    else if (k in node) node[k] = v;
    else node.setAttribute(k, v);
  }
  for (const c of children) if (c != null) node.append(c);
  return node;
}
// Icons are embedded Tabler SVGs drawn via CSS masks (see app.css).
const icon = (name) => { const s = document.createElement('span'); s.className = 'ic ic-' + name; s.setAttribute('aria-hidden', 'true'); return s; };
const FILE_ICONS = [
  [/\.(jpe?g|png|gif|webp|bmp|svg|heic|tiff?|avif)$/i, 'photo'],
  [/\.(mp4|mkv|mov|avi|webm|m4v)$/i, 'movie'],
  [/\.(mp3|flac|wav|ogg|m4a|aac|opus)$/i, 'music'],
  [/\.pdf$/i, 'file-type-pdf'],
  [/\.(zip|7z|rar|tar|gz|tgz|bz2|xz|zst)$/i, 'file-zip'],
  [/\.(txt|md|log|csv|rtf|docx?|odt)$/i, 'file-text'],
  [/\.(js|ts|go|py|sh|json|ya?ml|toml|html?|css|c|h|cpp|rs|java|xml|sql)$/i, 'file-code'],
];
const fileIcon = (name) => (FILE_ICONS.find(([re]) => re.test(name)) || [null, 'file'])[1];
// iconButton: icon-only action with a tooltip (data-tip) and an accessible name.
const iconButton = (ic, label, onClick, extra = '') => el('button', { type: 'button', className: ('icon-action ' + extra).trim(), tip: label, on: { click: onClick } }, icon(ic));
// rowActions renders actions as icons plus a "⋯" button that opens the same
// actions in a bottom sheet; CSS shows one or the other by screen width.
function rowActions(items, title) {
  const box = el('span', { className: 'row-actions' });
  for (const it of items) {
    box.append(it.href
      ? el('a', { href: it.href, download: it.download, className: 'icon-action', tip: it.label }, icon(it.icon))
      : iconButton(it.icon, it.label, it.run, it.danger ? 'danger' : ''));
  }
  if (items.length) box.append(iconButton('dots', t('action.more'), () => openSheet(title, items), 'more'));
  return box;
}
function openSheet(title, items) {
  const dialog = $('action-sheet');
  $('sheet-title').textContent = title;
  const list = $('sheet-actions');
  list.replaceChildren();
  for (const it of items) {
    const content = [icon(it.icon), el('span', { text: it.label })];
    list.append(it.href
      ? el('a', { href: it.href, download: it.download, className: 'button sheet-item', on: { click: () => dialog.close() } }, ...content)
      : el('button', { type: 'button', className: 'sheet-item' + (it.danger ? ' danger' : ''), on: { click: () => { dialog.close(); it.run(); } } }, ...content));
  }
  dialog.showModal();
}
$('sheet-cancel').addEventListener('click', () => $('action-sheet').close());
// A tap on the backdrop (outside the sheet) closes it.
$('action-sheet').addEventListener('click', (ev) => { if (ev.target === $('action-sheet')) $('action-sheet').close(); });

const describe = (e) => (e && e.code && dict['err.' + e.code] ? t('err.' + e.code) : (e && e.message) || String(e));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function formatSize(n) {
  if (n < 1024) return n + ' B';
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let v = n;
  let i = -1;
  do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
  return v.toLocaleString(lang, { maximumFractionDigits: v < 10 ? 1 : 0 }) + ' ' + units[i];
}
const fmtTime = (d) => new Date(d).toLocaleString(lang);

class ApiError extends Error {
  constructor(status, code) { super(code || ('HTTP ' + status)); this.status = status; this.code = code; }
}

async function api(method, url, { json, body, headers = {} } = {}) {
  const h = { ...headers };
  if (method !== 'GET' && state.csrf) h['X-CSRF-Token'] = state.csrf;
  let payload = body;
  if (json !== undefined) { h['Content-Type'] = 'application/json'; payload = JSON.stringify(json); }
  const res = await fetch(url, { method, headers: h, body: payload, credentials: 'same-origin', cache: 'no-store', redirect: 'error' });
  let data = null;
  if ((res.headers.get('Content-Type') || '').startsWith('application/json')) data = await res.json().catch(() => null);
  if (!res.ok) {
    const err = new ApiError(res.status, data && data.error);
    if (res.status === 401 && url !== '/api/auth/login' && state.user) sessionEnded();
    throw err;
  }
  return data;
}

// ---------- toasts and operation history ----------
// Short confirmations appear as toasts that disappear; the bell keeps the
// recent operations (in memory, this browser tab) with their status.

const MAX_TOASTS = 3;
function toast(text, kind = 'ok') {
  const node = el('div', { className: 'toast ' + kind, role: kind === 'error' ? 'alert' : 'status' },
    icon(kind === 'error' ? 'alert-circle' : 'circle-check'), el('span', { text }),
    el('button', { type: 'button', className: 'toast-close', tip: t('common.close'), on: { click: () => node.remove() } }, icon('x')));
  const box = $('toasts');
  box.append(node);
  // Never stack up over the page: the bell keeps the full history.
  while (box.children.length > MAX_TOASTS) box.firstElementChild.remove();
  setTimeout(() => node.remove(), kind === 'error' ? 8000 : 4000);
}
const notify = (text, kind = 'error') => { if (text) toast(text, kind === 'ok' ? 'ok' : 'error'); };

const history = [];
let unseen = 0;
function track(title) {
  const item = { id: Math.random().toString(36).slice(2), title, status: 'running', detail: '', progress: null, time: Date.now() };
  history.unshift(item);
  history.length = Math.min(history.length, 40);
  renderHistory();
  return {
    progress(done, total, detail = '') { item.progress = total ? done / total : null; item.detail = detail; renderHistory(); },
    done(detail = '') { item.status = 'done'; item.detail = detail; item.progress = null; unseen++; renderHistory(); },
    fail(detail) { item.status = 'failed'; item.detail = detail; item.progress = null; unseen++; renderHistory(); },
    set(status, detail) { item.status = status; item.detail = detail; renderHistory(); },
  };
}
function renderHistory() {
  const list = $('history');
  list.replaceChildren();
  for (const h of history) {
    const ic = h.status === 'running' ? 'loader-2' : h.status === 'done' ? 'circle-check' : 'alert-circle';
    const li = el('li', { className: 'hist ' + h.status }, icon(ic),
      el('div', {}, el('div', { className: 'hist-title', text: h.title }),
        el('div', { className: 'hist-detail muted', text: [h.detail, new Date(h.time).toLocaleTimeString(lang)].filter(Boolean).join(' · ') }),
        h.progress !== null ? el('progress', { max: 1, value: h.progress }) : null));
    list.append(li);
  }
  $('history-empty').hidden = history.length > 0;
  const running = history.some((h) => h.status === 'running');
  const badge = $('bell-badge');
  badge.hidden = !running && unseen === 0;
  badge.textContent = running ? '' : String(unseen);
  badge.classList.toggle('busy', running);
}
// Phones: account links fold into a menu under the "☰" button.
function setMenu(open) {
  $('nav-menu').classList.toggle('open', open);
  $('menu-toggle').setAttribute('aria-expanded', String(open));
}
$('menu-toggle').addEventListener('click', (ev) => { ev.stopPropagation(); setMenu(!$('nav-menu').classList.contains('open')); });
$('nav-menu').addEventListener('click', (ev) => { if (ev.target.closest('button')) setMenu(false); });
document.addEventListener('click', (ev) => { if (!ev.target.closest('#nav-menu, #menu-toggle')) setMenu(false); });
document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') setMenu(false); });

$('bell').addEventListener('click', (ev) => {
  ev.stopPropagation();
  const panel = $('bell-panel');
  panel.hidden = !panel.hidden;
  $('bell').setAttribute('aria-expanded', String(!panel.hidden));
  if (!panel.hidden) { unseen = 0; renderHistory(); }
});
document.addEventListener('click', (ev) => {
  if (!$('bell-panel').hidden && !ev.target.closest('.bell-wrap')) { $('bell-panel').hidden = true; $('bell').setAttribute('aria-expanded', 'false'); }
});
$('bell-clear').addEventListener('click', () => {
  for (let i = history.length - 1; i >= 0; i--) if (history[i].status !== 'running') history.splice(i, 1);
  unseen = 0;
  renderHistory();
});

// ---------- theme ----------
// "auto" follows the operating system; the choice is a per-browser convenience.
const THEMES = [['auto', 'device-desktop'], ['light', 'sun'], ['dark', 'moon']];
function currentTheme() { return document.documentElement.dataset.theme || 'auto'; }
function applyTheme(name) {
  const [, ic] = THEMES.find((x) => x[0] === name) || THEMES[0];
  if (name === 'auto') delete document.documentElement.dataset.theme; else document.documentElement.dataset.theme = name;
  try { if (name === 'auto') localStorage.removeItem('filedeck-theme'); else localStorage.setItem('filedeck-theme', name); } catch (e) { /* optional */ }
  const button = $('theme');
  button.replaceChildren(icon(ic), el('span', { text: t('theme.' + name) }));
  button.title = t('theme.title', { name: t('theme.' + name + '_long') });
  button.setAttribute('aria-label', button.title);
}
$('theme').addEventListener('click', () => {
  const i = THEMES.findIndex((x) => x[0] === currentTheme());
  applyTheme(THEMES[(i + 1) % THEMES.length][0]);
});

// ---------- dialogs (no window.prompt: consistent look, testable) ----------

function ask({ title, text = '', label = '', value = null, ok = t('common.ok') }) {
  const dialog = $('ask-dialog');
  const input = $('ask-form').elements.value;
  $('ask-title').textContent = title;
  $('ask-text').textContent = text;
  $('ask-text').hidden = !text;
  $('ask-label-text').textContent = label;
  $('ask-label').hidden = value === null;
  input.value = value ?? '';
  input.required = value !== null;
  $('ask-ok').textContent = ok;
  return new Promise((resolve) => {
    const finish = (result) => {
      dialog.removeEventListener('close', onClose);
      $('ask-form').removeEventListener('submit', onSubmit);
      $('ask-cancel').removeEventListener('click', onCancel);
      if (dialog.open) dialog.close();
      resolve(result);
    };
    const onSubmit = (ev) => { ev.preventDefault(); finish(value === null ? true : input.value.trim()); };
    const onCancel = () => finish(null);
    const onClose = () => finish(null);
    dialog.addEventListener('close', onClose);
    $('ask-form').addEventListener('submit', onSubmit);
    $('ask-cancel').addEventListener('click', onCancel);
    dialog.showModal();
    if (value !== null) { input.focus(); input.select(); }
  });
}

// ---------- session ----------

const VIEWS = ['setup-view', 'login-view', 'files-view', 'editor-view', 'trash-view', 'links-view', 'admin-view'];
function show(view) {
  for (const id of VIEWS) $(id).hidden = id !== view;
  document.body.dataset.view = view; // the sign-in screens show the banner instead of the header logo
  $('session').hidden = view === 'login-view' || view === 'setup-view';
}
const currentView = () => VIEWS.find((id) => !$(id).hidden);

function sessionEnded(message = t('session.expired')) {
  state.csrf = null;
  state.user = null;
  resetLogin();
  show('login-view');
  notify(message);
}

async function applySession(me) {
  state.csrf = me.csrf;
  state.user = me.user;
  state.limits = me.limits;
  $('whoami').textContent = me.user.username + (me.user.admin ? ' (' + t('admin.short') + ')' : '');
  $('open-admin').hidden = !me.user.admin;
  state.spaces = await api('GET', '/api/spaces');
  pollTransfers();
}

const spaceInfo = (name) => state.spaces.find((s) => s.name === name);
function can(p, space = state.space) {
  const s = spaceInfo(space);
  return !!s && (s.permissions & p) === p;
}
const writable = (space = state.space) => !spaceInfo(space)?.read_only;
const browsable = () => state.spaces.filter((s) => (s.permissions & PERM.list) === PERM.list);
const spaceLabel = (name) => (name === 'files' ? t('space.own') : name);

async function enter() {
  const { space, path } = locationFromHash();
  await openFolder(space, path);
}

async function start() {
  await loadLanguage(savedLanguage());
  try {
    const setup = await api('GET', '/api/setup');
    if (setup.required) { show('setup-view'); return; }
  } catch (e) { /* older server or transient error: fall through to login */ }
  try {
    await applySession(await api('GET', '/api/auth/me'));
    await enter();
  } catch (e) {
    show('login-view');
    if (e.status !== 401) notify(describe(e));
  }
}

// Re-render the visible view after a language change.
function rerender() {
  if (!state.user) return;
  $('whoami').textContent = state.user.username + (state.user.admin ? ' (' + t('admin.short') + ')' : '');
  renderHistory();
  const view = currentView();
  if (view === 'files-view' && state.space) openFolder(state.space, state.path);
  else if (view === 'trash-view') openTrash();
  else if (view === 'links-view') openLinks();
  else if (view === 'admin-view') openAdmin();
}

// Accounts with two-factor authentication get a second field after a correct
// password; the form then sends the password again together with the code.
function resetLogin() {
  const f = $('login-form').elements;
  f.password.value = '';
  f.code.value = '';
  f.code.inputMode = 'numeric';
  $('login-code-row').hidden = true;
}
$('login-recovery').addEventListener('click', () => {
  const code = $('login-form').elements.code;
  code.inputMode = 'text';
  code.value = '';
  code.placeholder = 'XXXXX-XXXXX';
  code.focus();
});
$('login-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const f = ev.target.elements;
  const json = { username: f.username.value.trim(), password: f.password.value };
  if (!$('login-code-row').hidden) json.code = f.code.value.trim();
  try {
    const res = await api('POST', '/api/auth/login', { json });
    resetLogin();
    await applySession(await api('GET', '/api/auth/me'));
    if (res && res.recovery_used) notify(t('login.recovery_used'));
    await enter();
  } catch (e) {
    if (e.code === 'totp_required') {
      $('login-code-row').hidden = false;
      f.code.focus();
      return;
    }
    if (e.code === 'totp_invalid') { f.code.value = ''; f.code.focus(); }
    notify(describe(e));
  }
});

$('setup-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const f = ev.target.elements;
  if (f.password.value !== f.repeat.value) { notify(t('setup.mismatch')); return; }
  const username = f.username.value.trim();
  try {
    await api('POST', '/api/setup', { json: { setup_code: f.code.value.trim(), username, password: f.password.value } });
    await api('POST', '/api/auth/login', { json: { username, password: f.password.value } });
    ev.target.reset();
    notify(t('setup.done', { name: username }), 'ok');
    await applySession(await api('GET', '/api/auth/me'));
    await enter();
  } catch (e) {
    if (e.code === 'setup_complete') show('login-view');
    notify(describe(e));
  }
});

$('logout').addEventListener('click', async () => {
  try { await api('POST', '/api/auth/logout'); } catch (e) { /* session is gone either way */ }
  state.csrf = null;
  state.user = null;
  resetLogin();
  show('login-view');
  notify(t('session.logged_out'), 'ok');
});

$('open-password').addEventListener('click', () => $('password-dialog').showModal());
$('password-cancel').addEventListener('click', () => $('password-dialog').close());
$('password-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const f = ev.target.elements;
  try {
    await api('POST', '/api/auth/password', { json: { current_password: f.current.value, new_password: f.next.value } });
    ev.target.reset();
    $('password-dialog').close();
    state.csrf = null;
    state.user = null;
    show('login-view');
    notify(t('password.changed'), 'ok');
  } catch (e) {
    notify(describe(e));
  }
});

// ---------- two-factor authentication ----------
// Each person manages their own: set up (scan a QR code, confirm one code),
// set up again on a new phone, renew recovery codes or turn it off. Every
// change asks for the current password.

function drawQR(canvas, qr) {
  const quiet = 4; // the white margin scanners need
  const scale = Math.max(1, Math.floor(200 / (qr.size + 2 * quiet)));
  const side = (qr.size + 2 * quiet) * scale;
  canvas.width = side;
  canvas.height = side;
  const g = canvas.getContext('2d');
  g.fillStyle = '#fff';
  g.fillRect(0, 0, side, side);
  g.fillStyle = '#000';
  qr.rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === '1') g.fillRect((x + quiet) * scale, (y + quiet) * scale, scale, scale);
  });
}

let totpCodes = [];
function showRecoveryCodes(codes) {
  totpCodes = codes;
  $('totp-code-list').replaceChildren(...codes.map((c) => el('li', { text: c })));
  $('totp-codes').hidden = false;
}

async function openTotp() {
  let status;
  try { status = await api('GET', '/api/auth/totp'); } catch (e) { notify(describe(e)); return; }
  const f = $('totp-form').elements;
  f.password.value = '';
  f.code.value = '';
  $('totp-setup').hidden = true;
  $('totp-codes').hidden = true;
  $('totp-manage').hidden = false;
  totpCodes = [];
  const box = $('totp-status');
  box.className = 'totp-status' + (status.enabled ? ' on' : '');
  box.replaceChildren(icon(status.enabled ? 'circle-check' : 'shield-lock'),
    el('span', { text: status.enabled ? t('totp.on', { n: status.recovery_codes_left }) : t('totp.off') }));
  $('totp-start').textContent = t(status.enabled ? 'totp.setup_again' : 'totp.setup');
  $('totp-renew').hidden = !status.enabled;
  $('totp-disable').hidden = !status.enabled;
  if (!$('totp-dialog').open) $('totp-dialog').showModal();
}
function totpPassword() {
  const input = $('totp-form').elements.password;
  if (!input.value) { notify(t('admin.reauth_needed')); input.focus(); return null; }
  return input.value;
}
$('open-totp').addEventListener('click', openTotp);
$('totp-close').addEventListener('click', () => $('totp-dialog').close());
$('totp-form').addEventListener('submit', (ev) => {
  ev.preventDefault();
  if (!$('totp-setup').hidden) $('totp-confirm').click();
});
$('totp-start').addEventListener('click', async () => {
  const password = totpPassword();
  if (!password) return;
  try {
    const res = await api('POST', '/api/auth/totp/setup', { json: { password } });
    $('totp-form').elements.password.value = '';
    drawQR($('totp-qr'), res.qr);
    $('totp-key').textContent = res.key.replace(/(.{4})/g, '$1 ').trim();
    $('totp-manage').hidden = true;
    $('totp-codes').hidden = true;
    $('totp-setup').hidden = false;
    $('totp-form').elements.code.focus();
  } catch (e) { notify(describe(e)); }
});
$('totp-confirm').addEventListener('click', async () => {
  const input = $('totp-form').elements.code;
  try {
    const res = await api('POST', '/api/auth/totp/enable', { json: { code: input.value.replace(/\s/g, '') } });
    input.value = '';
    await openTotp();
    $('totp-manage').hidden = true;
    showRecoveryCodes(res.recovery_codes);
    notify(t('totp.enabled'), 'ok');
  } catch (e) {
    input.value = '';
    input.focus();
    notify(describe(e));
  }
});
$('totp-renew').addEventListener('click', async () => {
  const password = totpPassword();
  if (!password) return;
  try {
    const res = await api('POST', '/api/auth/totp/recovery', { json: { password } });
    await openTotp();
    $('totp-manage').hidden = true;
    showRecoveryCodes(res.recovery_codes);
    notify(t('totp.renewed'), 'ok');
  } catch (e) { notify(describe(e)); }
});
$('totp-disable').addEventListener('click', async () => {
  const password = totpPassword();
  if (!password) return;
  if (!await ask({ title: t('totp.disable_title'), text: t('totp.disable_text'), ok: t('totp.disable') })) return;
  try {
    await api('POST', '/api/auth/totp/disable', { json: { password } });
    notify(t('totp.disabled'), 'ok');
    await openTotp();
  } catch (e) { notify(describe(e)); }
});
const codesText = () => t('totp.file_header', { user: state.user.username, host: location.host }) + '\n\n' + totpCodes.join('\n') + '\n';
$('totp-copy').addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(codesText());
    notify(t('totp.copied'), 'ok');
  } catch (e) { notify(t('totp.copy_failed')); }
});
$('totp-download').addEventListener('click', () => {
  const url = URL.createObjectURL(new Blob([codesText()], { type: 'text/plain' }));
  const a = el('a', { href: url, download: 'filedeck-recovery-codes.txt' });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
});

// ---------- browsing ----------
// Location: #/<space>/<path>, both URI-encoded; the path "." is empty.

function locationFromHash() {
  const raw = location.hash.startsWith('#/') ? location.hash.slice(2) : '';
  const slash = raw.indexOf('/');
  let space = slash < 0 ? raw : raw.slice(0, slash);
  let path = slash < 0 ? '' : raw.slice(slash + 1);
  try { space = decodeURIComponent(space); path = decodeURIComponent(path); } catch (e) { space = ''; path = ''; }
  if (!browsable().some((s) => s.name === space)) {
    // Default to the own space when accessible, otherwise the first one.
    space = browsable().some((s) => s.name === 'files') ? 'files' : (browsable()[0]?.name ?? '');
    path = '';
  }
  return { space, path: path === '' ? '.' : path };
}
function hashFor(space, path) {
  return '#/' + encodeURIComponent(space) + '/' + (path === '.' ? '' : encodeURIComponent(path));
}
const join = (dir, name) => (dir === '.' ? name : dir + '/' + name);
const q = (space, path) => 'space=' + encodeURIComponent(space) + '&path=' + encodeURIComponent(path);
const where = (space, path) => spaceLabel(space) + (path === '.' ? '' : ' / ' + path);

function renderSpaces() {
  const nav = $('spaces');
  nav.replaceChildren();
  // "My files" always comes first, set apart by a separator; the other spaces
  // follow in alphabetical order (as returned by the server).
  const list = browsable().sort((a, b) => (b.name === 'files') - (a.name === 'files'));
  nav.hidden = list.length < 2;
  list.forEach((s, i) => {
    if (i === 1 && list[0].name === 'files') nav.append(el('span', { className: 'tab-sep', 'aria-hidden': 'true', text: '|' }));
    nav.append(el('button', {
      type: 'button', className: 'tab' + (s.name === state.space ? ' active' : ''),
      title: s.read_only ? t('files.read_only') : '', on: { click: () => navigate(s.name, '.') },
    }, icon(s.name === 'files' ? 'home' : 'server-2'), spaceLabel(s.name), s.read_only ? icon('lock') : null));
  });
}

function renderCrumbs(path) {
  const list = $('crumbs');
  list.replaceChildren();
  const parts = path === '.' ? [] : path.split('/');
  const go = (text, p) => el('button', { type: 'button', className: 'link', text, on: { click: () => navigate(state.space, p) } });
  list.append(el('li', {}, parts.length ? go(spaceLabel(state.space), '.') : el('span', { text: spaceLabel(state.space) })));
  parts.forEach((name, i) => {
    const p = parts.slice(0, i + 1).join('/');
    list.append(el('li', {}, i === parts.length - 1 ? el('span', { text: name }) : go(name, p)));
  });
}

function navigate(space, path) {
  const hash = hashFor(space, path);
  if (location.hash !== hash) location.hash = hash; else openFolder(space, path);
}
window.addEventListener('hashchange', () => { if (state.user && !editorDirty()) enter(); });

async function openFolder(space, path) {
  show('files-view');
  if (!space) {
    state.space = null;
    renderSpaces();
    $('crumbs').replaceChildren();
    $('entries').replaceChildren();
    $('empty').hidden = true;
    for (const id of ['readonly', 'mkdir-form', 'new-folder', 'upload-label', 'upload-dir-label', 'open-trash', 'new-text']) $(id).hidden = true;
    notify(t('files.no_space'));
    return;
  }
  let entries;
  try {
    entries = await api('GET', '/api/files?' + q(space, path));
  } catch (e) {
    if (e.status === 401) return;
    notify(describe(e));
    if (path !== '.') navigate(space, '.');
    return;
  }
  if (state.space !== space || state.path !== path) { clearSelection(); state.search = null; $('search-form').elements.q.value = ''; }
  state.space = space;
  state.path = path;
  renderSpaces();
  renderCrumbs(path);
  const ro = !writable();
  $('readonly').hidden = !ro;
  $('mkdir-form').hidden = ro || !can(PERM.create);
  $('new-folder').hidden = ro || !can(PERM.create);
  $('upload-label').hidden = ro || !can(PERM.create);
  $('upload-dir-label').hidden = ro || !can(PERM.create);
  $('open-trash').hidden = ro || !can(PERM.modify);
  $('new-text').hidden = ro || !can(PERM.create);
  state.entries = entries;
  if (state.search) await runSearch(state.search.q); else renderEntries();
}

// Folders first, then by the chosen column; name breaks ties.
function sortEntries(list) {
  const { key, dir } = state.sort;
  const byName = (a, b) => (a.path || a.name).localeCompare(b.path || b.name, lang, { numeric: true });
  const cmp = {
    name: byName,
    size: (a, b) => (a.size - b.size) || byName(a, b),
    modified: (a, b) => (new Date(a.modified) - new Date(b.modified)) || byName(a, b),
  }[key];
  return [...list].sort((a, b) => (b.directory - a.directory) || dir * cmp(a, b));
}
function renderSortMarks() {
  for (const b of document.querySelectorAll('button.sort')) {
    const active = b.dataset.sort === state.sort.key;
    b.classList.toggle('active', active);
    b.querySelector('.sort-mark').textContent = active ? (state.sort.dir === 1 ? '▲' : '▼') : '';
    b.closest('th').setAttribute('aria-sort', active ? (state.sort.dir === 1 ? 'ascending' : 'descending') : 'none');
  }
}
for (const b of document.querySelectorAll('button.sort')) {
  b.addEventListener('click', () => {
    state.sort = state.sort.key === b.dataset.sort ? { key: b.dataset.sort, dir: -state.sort.dir } : { key: b.dataset.sort, dir: b.dataset.sort === 'name' ? 1 : -1 };
    try { localStorage.setItem('filedeck-sort', JSON.stringify(state.sort)); } catch (e) { /* optional */ }
    renderEntries();
  });
}

// ---------- search ----------
// Searches file and folder names below the current folder (server-side, bounded).

async function runSearch(query) {
  const op = { space: state.space, path: state.path };
  let res;
  try {
    res = await api('GET', '/api/search?' + q(op.space, op.path) + '&q=' + encodeURIComponent(query));
  } catch (e) { notify(describe(e)); return; }
  if (op.space !== state.space || op.path !== state.path) return;
  clearSelection();
  state.search = { q: query, results: res.results, truncated: res.truncated };
  renderEntries();
}
$('search-form').addEventListener('submit', (ev) => {
  ev.preventDefault();
  const value = ev.target.elements.q.value.trim();
  if (!value) { clearSearch(); return; }
  runSearch(value);
});
$('search-form').elements.q.addEventListener('search', (ev) => { if (!ev.target.value) clearSearch(); });
function clearSearch() {
  state.search = null;
  $('search-form').elements.q.value = '';
  renderEntries();
}
$('search-clear').addEventListener('click', clearSearch);

function renderEntries() {
  const { space, path } = state;
  const searching = !!state.search;
  const view = sortEntries(searching ? state.search.results : state.entries);
  state.view = view;
  if (!searching) state.files = view.filter((e) => !e.directory).map((e) => e.name);
  const body = $('entries');
  body.replaceChildren();
  const canRead = can(PERM.read);
  const canModify = can(PERM.modify) && writable();
  const selecting = state.selecting && !searching;
  $('file-table').classList.toggle('selecting', selecting);
  $('select-mode').hidden = searching;
  renderSortMarks();
  const base = path === '.' ? '' : path + '/';
  view.forEach((entry, index) => {
    const target = searching ? entry.path : join(path, entry.name);
    const label = searching ? target.slice(base.length) : entry.name;
    const parent = target.includes('/') ? target.slice(0, target.lastIndexOf('/')) : '.';
    // Long names are cut with an ellipsis (CSS); the full name is in the tooltip.
    const text = el('span', { className: 'label', text: label });
    let name;
    if (entry.directory) name = el('button', { type: 'button', className: 'link name', title: label, on: { click: () => navigate(space, target) } }, icon('folder'), text);
    else if (!canRead) name = el('span', { className: 'name', title: label }, icon(fileIcon(entry.name)), text);
    else if (searching) name = el('button', { type: 'button', className: 'link name', title: label, on: { click: () => showInFolder(parent, entry.name, true) } }, icon(fileIcon(entry.name)), text);
    else name = el('button', { type: 'button', className: 'link name', title: label, on: { click: () => openPreview(entry.name) } }, icon(fileIcon(entry.name)), text);
    // One list of actions: icons on wide screens, a "⋯" action sheet on phones.
    const items = [];
    if (!entry.directory && canRead) items.push({ icon: 'download', label: t('action.download'), href: '/api/content?' + q(space, target), download: entry.name });
    if (searching) {
      items.push({ icon: 'folder', label: t('search.show_in_folder'), run: () => showInFolder(parent, entry.name, false) });
    } else {
      if (!entry.directory && canRead && canModify && isTextName(entry.name)) items.push({ icon: 'pencil', label: t('action.edit'), run: () => openEditor(space, target) });
      if (canRead) items.push({ icon: 'copy', label: t('action.copy_move'), run: () => openTransfer([entry]) });
      if (canRead && (!entry.directory || can(PERM.list))) items.push({ icon: 'share', label: t('action.share'), run: () => openShare(entry, target) });
      if (canModify) {
        items.push({ icon: 'forms', label: t('action.rename'), run: () => renameEntry(entry, target) });
        items.push({ icon: 'trash', label: t('action.delete'), run: () => trashEntries([entry]), danger: true });
      }
    }
    const actions = rowActions(items, label);
    const box = el('input', { type: 'checkbox', checked: state.selected.has(entry.name), 'aria-label': t('select.item', { name: entry.name }) });
    box.addEventListener('click', (ev) => { ev.stopPropagation(); toggleSelect(index, ev.shiftKey); });
    const tr = el('tr', { className: state.selected.has(entry.name) ? 'selected' : '' },
      el('td', { className: 'sel-col' }, box), el('td', {}, name),
      el('td', { className: 'num', text: entry.directory ? '—' : formatSize(entry.size) }),
      el('td', { className: 'num date-col muted', text: entry.modified ? fmtTime(entry.modified) : '' }),
      el('td', { className: 'num' }, actions));
    if (entry.directory && !searching) tr.dataset.folder = target;
    // Ctrl/Cmd-click toggles, Shift-click selects a range (outside buttons/links).
    tr.addEventListener('click', (ev) => {
      if (searching || ev.target.closest('button, a, input')) return;
      if (ev.ctrlKey || ev.metaKey || ev.shiftKey || state.selecting) { ev.preventDefault(); toggleSelect(index, ev.shiftKey); }
    });
    body.append(tr);
  });
  $('search-bar').hidden = !searching;
  if (searching) {
    $('search-summary').textContent = t(state.search.truncated ? 'search.summary_truncated' : 'search.summary', { n: view.length, q: state.search.q, where: where(space, path) });
  }
  $('empty').textContent = t(searching ? 'search.none' : 'files.empty');
  $('empty').hidden = view.length > 0;
  updateBulkBar();
}

// showInFolder opens the folder of a search hit, optionally previewing it.
async function showInFolder(parent, name, preview) {
  state.search = null;
  $('search-form').elements.q.value = '';
  if (location.hash === hashFor(state.space, parent)) await openFolder(state.space, parent);
  else { navigate(state.space, parent); await new Promise((r) => setTimeout(r, 0)); await openFolder(state.space, parent); }
  if (preview && state.files.includes(name)) openPreview(name);
}

// ---------- selection ----------

function setSelecting(on) {
  state.selecting = on;
  $('select-mode').setAttribute('aria-pressed', String(on));
  $('select-mode').classList.toggle('active', on);
  if (!on) state.selected.clear();
  renderEntries();
}
function clearSelection() { state.selected.clear(); state.anchor = null; }
function toggleSelect(index, range) {
  if (state.search) return;
  if (!state.selecting) { state.selecting = true; $('select-mode').setAttribute('aria-pressed', 'true'); $('select-mode').classList.add('active'); }
  const name = state.view[index].name;
  if (range && state.anchor !== null) {
    const [a, b] = [Math.min(state.anchor, index), Math.max(state.anchor, index)];
    for (let i = a; i <= b; i++) state.selected.add(state.view[i].name);
  } else {
    if (state.selected.has(name)) state.selected.delete(name); else state.selected.add(name);
    state.anchor = index;
  }
  renderEntries();
}
function updateBulkBar() {
  const n = state.selected.size;
  $('bulk-bar').hidden = n === 0;
  $('bulk-count').textContent = t('select.count', { n });
  const canModify = can(PERM.modify) && writable();
  $('bulk-trash').hidden = !canModify;
  $('bulk-transfer').hidden = !can(PERM.read);
  const all = $('select-all');
  all.checked = n > 0 && n === state.view.length;
  all.indeterminate = n > 0 && n < state.view.length;
  $('bulk-download').hidden = !can(PERM.read) || !selectedEntries().some((e) => !e.directory);
}
const selectedEntries = () => (state.view || []).filter((e) => state.selected.has(e.name));
$('select-mode').addEventListener('click', () => setSelecting(!state.selecting));
$('select-all').addEventListener('change', (ev) => {
  if (ev.target.checked) for (const e of state.view) state.selected.add(e.name); else state.selected.clear();
  renderEntries();
});
$('bulk-clear').addEventListener('click', () => setSelecting(false));
$('bulk-trash').addEventListener('click', () => trashEntries(selectedEntries()));
$('bulk-transfer').addEventListener('click', () => openTransfer(selectedEntries()));
// Downloads selected files one by one (no server-side archives, see SECURITY.md).
$('bulk-download').addEventListener('click', async () => {
  const files = selectedEntries().filter((e) => !e.directory);
  for (const f of files) {
    const a = el('a', { href: '/api/content?' + q(state.space, join(state.path, f.name)), download: f.name, hidden: true });
    document.body.append(a);
    a.click();
    a.remove();
    await sleep(400); // browsers throttle bursts of downloads
  }
  if (files.length > 1) notify(t('select.download_started', { n: files.length }), 'ok');
});
document.addEventListener('keydown', (ev) => {
  if (ev.key === 'Escape' && state.selecting && !document.querySelector('dialog[open]')) setSelecting(false);
});

$('refresh').addEventListener('click', () => openFolder(state.space, state.path));

function validName(name) {
  return name && !name.includes('/') && name !== '.' && name !== '..';
}

async function renameEntry(entry, target) {
  const name = await ask({ title: t('rename.title'), label: t('rename.label'), value: entry.name, ok: t('rename.submit') });
  if (name === null || name === entry.name) return;
  if (!validName(name)) { notify(t('err.name_slash')); return; }
  const op = track(t('op.rename', { from: entry.name, to: name }));
  try {
    await api('POST', '/api/rename', { json: { space: state.space, from: target, to: join(state.path, name) } });
    op.done();
    notify(t('rename.done', { name }), 'ok');
  } catch (e) { op.fail(describe(e)); notify(describe(e)); }
  openFolder(state.space, state.path);
}

async function trashEntries(entries) {
  if (!entries.length) return;
  const text = entries.length === 1
    ? t(entries[0].directory ? 'trash.confirm_folder' : 'trash.confirm_file', { name: entries[0].name })
    : t('trash.confirm_many', { n: entries.length });
  if (!await ask({ title: t('trash.confirm_title'), text, ok: t('trash.confirm_ok') })) return;
  const space = state.space;
  const op = track(entries.length === 1 ? t('op.trash_one', { name: entries[0].name }) : t('op.trash_many', { n: entries.length }));
  let failed = 0;
  for (const [i, entry] of entries.entries()) {
    try { await api('POST', '/api/trash', { json: { space, path: join(state.path, entry.name) } }); } catch (e) { failed++; op.set('running', describe(e)); }
    op.progress(i + 1, entries.length, (i + 1) + '/' + entries.length);
  }
  if (failed) { op.fail(t('op.failed_count', { n: failed })); notify(t('op.failed_count', { n: failed })); } else { op.done(); notify(t('trash.done'), 'ok'); }
  setSelecting(false);
  openFolder(state.space, state.path);
}

async function createFolder(name) {
  if (!validName(name)) { notify(t('err.name_slash')); return false; }
  try {
    await api('POST', '/api/folders', { json: { space: state.space, path: join(state.path, name) } });
    track(t('op.mkdir', { name })).done();
    notify(t('files.folder_created', { name }), 'ok');
    await openFolder(state.space, state.path);
    return true;
  } catch (e) {
    notify(describe(e));
    return false;
  }
}
$('mkdir-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const input = ev.target.elements.name;
  if (await createFolder(input.value.trim())) input.value = '';
});
// Phones: the inline form is hidden; this button asks for the name instead.
$('new-folder').addEventListener('click', async () => {
  const name = await ask({ title: t('files.new_folder'), label: t('newfile.folder_label'), value: '', ok: t('files.create') });
  if (name) await createFolder(name);
});

// ---------- trash ----------

async function openTrash() {
  let items;
  try { items = await api('GET', '/api/trash?space=' + encodeURIComponent(state.space)); } catch (e) { notify(describe(e)); return; }
  show('trash-view');
  $('trash-title').textContent = t('trash.title', { space: spaceLabel(state.space) });
  $('trash-hint').textContent = t(state.user.admin ? 'trash.hint_admin' : 'trash.hint');
  const body = $('trash-entries');
  body.replaceChildren();
  for (const it of items) {
    const origin = (it.path || t('trash.unknown')) + (it.replaced ? ' — ' + t('trash.previous_version') : '');
    const actions = el('span', { className: 'row-actions' }, iconButton('arrow-back-up', t('action.restore'), () => restoreItem(it)));
    if (state.user.admin) actions.append(iconButton('trash-x', t('action.purge'), () => purgeItem(it), 'danger'));
    body.append(el('tr', {},
      el('td', {}, el('span', { className: 'name' }, icon(it.directory ? 'folder' : fileIcon(it.path || '')), origin)),
      el('td', { className: 'num', text: it.directory ? '—' : formatSize(it.size) }),
      el('td', { text: fmtTime(it.deleted) }),
      el('td', { className: 'num' }, actions)));
  }
  $('trash-empty').hidden = items.length > 0;
}

async function restoreItem(it) {
  let path = '';
  if (!it.path) {
    path = await ask({ title: t('restore.title'), text: t('restore.unknown'), label: t('restore.as'), value: 'restored', ok: t('action.restore') });
    if (path === null) return;
  }
  for (;;) {
    try {
      await api('POST', '/api/trash/' + encodeURIComponent(it.id) + '/restore', { json: { path } });
      track(t('op.restore', { name: path || it.path })).done();
      notify(t('restore.done', { name: path || it.path }), 'ok');
      break;
    } catch (e) {
      if (e.code !== 'conflict' && e.code !== 'not_found') { notify(describe(e)); break; }
      const reason = t(e.code === 'conflict' ? 'restore.taken' : 'restore.no_parent');
      path = await ask({ title: t('restore.other_title'), text: reason, label: t('restore.as'), value: (path || it.path) + ' ' + t('restore.suffix'), ok: t('action.restore') });
      if (path === null) break;
    }
  }
  openTrash();
}

async function purgeItem(it) {
  if (!await ask({ title: t('purge.title'), text: t('purge.text', { name: it.path || it.id }), ok: t('action.purge') })) return;
  try {
    await api('DELETE', '/api/trash/' + encodeURIComponent(it.id));
    track(t('op.purge', { name: it.path || it.id })).done();
    notify(t('purge.done'), 'ok');
  } catch (e) { notify(describe(e)); }
  openTrash();
}

$('open-trash').addEventListener('click', openTrash);
$('close-trash').addEventListener('click', () => openFolder(state.space, state.path));

// ---------- public links ----------
// The server returns a link's address only when it is created (it keeps just a
// hash of the token), so the dialog shows it once, with a copy button.

let shareTarget = null;
function openShare(entry, path) {
  shareTarget = { space: state.space, path, name: entry.name };
  const form = $('share-form');
  form.reset();
  $('share-source').textContent = t(entry.directory ? 'transfer.source_folder' : 'transfer.source_file', { path: where(state.space, path) });
  $('share-hint').textContent = t(entry.directory ? 'share.hint_folder' : 'share.hint_file');
  $('share-settings').hidden = false;
  $('share-result').hidden = true;
  $('share-submit').hidden = false;
  $('share-copy').hidden = true;
  $('share-dialog').showModal();
}
$('share-cancel').addEventListener('click', () => $('share-dialog').close());
$('share-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  if ($('share-settings').hidden) return;
  const f = ev.target.elements;
  const { space, path, name } = shareTarget;
  try {
    const res = await api('POST', '/api/links', { json: { space, path, expires_in_hours: Number(f.hours.value), password: f.password.value } });
    f.password.value = '';
    $('share-url').value = res.url;
    $('share-settings').hidden = true;
    $('share-result').hidden = false;
    $('share-submit').hidden = true;
    $('share-copy').hidden = false;
    track(t('op.share', { name })).done();
    $('share-url').focus();
    $('share-url').select();
  } catch (e) { notify(describe(e)); }
});
$('share-copy').addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText($('share-url').value);
    notify(t('share.copied'), 'ok');
  } catch (e) {
    $('share-url').select();
    notify(t('share.copy_manual'));
  }
});

async function openLinks() {
  const all = state.user.admin && $('links-all').checked;
  let links;
  try { links = await api('GET', '/api/links' + (all ? '?all=1' : '')); } catch (e) { notify(describe(e)); return; }
  show('links-view');
  $('links-all-label').hidden = !state.user.admin;
  $('links-owner-col').hidden = !all;
  const body = $('links-entries');
  body.replaceChildren();
  for (const link of links) {
    const item = el('span', { className: 'name' }, icon(link.directory ? 'folder' : fileIcon(link.path)), where(link.space, link.path));
    if (link.has_password) item.append(el('span', { className: 'badge', tip: t('links.protected') }, icon('lock')));
    const parent = link.path.includes('/') ? link.path.slice(0, link.path.lastIndexOf('/')) : '.';
    const actions = el('span', { className: 'row-actions' });
    if (link.available && spaceInfo(link.space) && can(PERM.list, link.space)) {
      actions.append(iconButton('folder', t('search.show_in_folder'), () => { const h = hashFor(link.space, parent); if (location.hash === h) enter(); else location.hash = h; }));
    }
    actions.append(iconButton('link-off', t('links.revoke'), () => revokeLink(link), 'danger'));
    body.append(el('tr', {},
      el('td', {}, item),
      all ? el('td', { text: link.owner_name || '—' }) : null,
      el('td', { text: fmtTime(link.expires) }),
      el('td', {}, el('span', { className: 'status ' + (link.available ? 'ok' : 'gone'), text: t(link.available ? 'links.active' : 'links.unavailable') })),
      el('td', { className: 'num' }, actions)));
  }
  $('links-empty').hidden = links.length > 0;
}
async function revokeLink(link) {
  if (!await ask({ title: t('links.revoke_title'), text: t('links.revoke_text', { name: where(link.space, link.path) }), ok: t('links.revoke') })) return;
  try {
    await api('DELETE', '/api/links/' + encodeURIComponent(link.id));
    track(t('op.unshare', { name: link.path })).done();
    notify(t('links.revoked'), 'ok');
  } catch (e) { notify(describe(e)); }
  openLinks();
}
$('open-links').addEventListener('click', openLinks);
$('links-all').addEventListener('change', openLinks);
$('close-links').addEventListener('click', () => openFolder(state.space, state.path));

// ---------- uploads ----------
// Each file: create an upload, send chunks at the server-confirmed offset,
// resynchronise via GET status after errors, then commit (idempotent).
// The upload ID is remembered per file so a reload can resume it. Folders
// are recreated level by level before their files are uploaded.

const memory = {
  get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* optional */ } },
  del(k) { try { localStorage.removeItem(k); } catch (e) { /* optional */ } },
};
const queue = [];
let batch = null;
const MAX_FOLDER_FILES = 10000;

// enqueue takes {file, rel} items; rel is the relative path inside the drop
// (for folder uploads) or just the file name.
function enqueue(items, dir = state.path) {
  if (!items.length) return;
  if (!batch) {
    batch = { op: null, total: 0, done: 0, failed: 0, bytes: 0, sent: 0, dirs: new Map() };
  }
  const space = state.space;
  for (const it of items) {
    queue.push({ file: it.file, rel: it.rel || it.file.name, space, dir });
    batch.total++;
    batch.bytes += it.file.size;
  }
  const label = items.length === 1 ? t('op.upload_one', { name: items[0].rel || items[0].file.name, where: where(space, dir) }) : t('op.upload_many', { n: batch.total, where: where(space, dir) });
  if (!batch.op) batch.op = track(label);
  if (!state.busy) drain();
}

async function drain() {
  state.busy = true;
  while (queue.length) {
    const job = queue.shift();
    try {
      await uploadOne(job);
      batch.done++;
    } catch (e) {
      batch.failed++;
      batch.lastError = describe(e);
      if (e.status === 401) { queue.length = 0; break; }
    }
    batch.op.progress(batch.done + batch.failed, batch.total, t('op.files_progress', { done: batch.done + batch.failed, total: batch.total }));
  }
  const b = batch;
  batch = null;
  state.busy = false;
  if (b) {
    if (b.failed) {
      b.op.fail(t('op.upload_result_failed', { ok: b.done, failed: b.failed, error: b.lastError || '' }));
      notify(t('op.upload_result_failed', { ok: b.done, failed: b.failed, error: b.lastError || '' }));
    } else {
      b.op.done(t('op.upload_result', { n: b.done, size: formatSize(b.bytes) }));
      notify(t('op.upload_result', { n: b.done, size: formatSize(b.bytes) }), 'ok');
    }
  }
  if (state.user && !$('files-view').hidden) openFolder(state.space, state.path);
}

function retryable(e) {
  return !(e instanceof ApiError) || e.status >= 500 || e.status === 408 || e.status === 429;
}

// ensureDirs creates each missing folder of rel's parent path (once per batch).
async function ensureDirs(space, dir, rel) {
  const parts = rel.split('/').slice(0, -1);
  let cur = dir;
  for (const part of parts) {
    if (!validName(part)) throw new ApiError(400, 'invalid_input');
    cur = join(cur, part);
    const key = space + '\u0000' + cur;
    if (batch.dirs.has(key)) { await batch.dirs.get(key); continue; }
    const created = api('POST', '/api/folders', { json: { space, path: cur } }).catch((e) => { if (e.code !== 'conflict') throw e; });
    batch.dirs.set(key, created);
    await created;
  }
  return cur;
}

async function uploadOne({ file, rel, space, dir }) {
  if (file.size > state.limits.max_file_bytes) throw new ApiError(413, 'upload_size');
  const parent = rel.includes('/') ? await ensureDirs(space, dir, rel) : dir;
  const target = join(parent, file.name);
  const key = ['filedeck-upload', state.user.id, space, target, file.size, file.lastModified].join('\u0000');
  let u = null;
  const saved = memory.get(key);
  if (saved) {
    try { u = await api('GET', '/api/uploads/' + encodeURIComponent(saved)); } catch (e) { memory.del(key); }
    if (u && u.state === 'published') { memory.del(key); return; }
  }
  if (!u) {
    u = await api('POST', '/api/uploads', { json: { space, path: target, size: file.size } });
    memory.set(key, u.id);
  }
  const url = '/api/uploads/' + encodeURIComponent(u.id);
  let failures = 0;
  while (u.offset < u.size) {
    const end = Math.min(u.offset + state.limits.max_chunk_bytes, u.size);
    try {
      u = await api('PATCH', url, { body: file.slice(u.offset, end), headers: { 'Content-Type': 'application/octet-stream', 'Upload-Offset': String(u.offset) } });
      failures = 0;
      if (batch.total === 1) batch.op.progress(u.offset, u.size, Math.floor((u.offset / Math.max(u.size, 1)) * 100) + '%');
    } catch (e) {
      // 409 means the server has a different offset: resynchronise from status.
      if (!retryable(e) && e.status !== 409) throw e;
      if (++failures > 6) throw e;
      await sleep(Math.min(1000 * 2 ** failures, 15000));
      u = await statusWithRetry(url);
    }
  }
  for (let attempt = 1; ; attempt++) {
    try {
      await api('POST', url + '/commit');
      memory.del(key);
      return;
    } catch (e) {
      if (e.status === 409) {
        memory.del(key);
        await api('DELETE', url).catch(() => {});
        throw new ApiError(409, 'exists');
      }
      if (!retryable(e) || attempt > 5) throw e;
      await sleep(1000 * attempt);
    }
  }
}

async function statusWithRetry(url) {
  for (let attempt = 1; ; attempt++) {
    try { return await api('GET', url); } catch (e) {
      if (!retryable(e) || attempt > 5) throw e;
      await sleep(1000 * attempt);
    }
  }
}

const fromInput = (files) => [...files].map((file) => ({ file, rel: file.webkitRelativePath || file.name }));
$('upload-input').addEventListener('change', (ev) => { enqueue(fromInput(ev.target.files)); ev.target.value = ''; });
$('upload-dir-input').addEventListener('change', (ev) => {
  const items = fromInput(ev.target.files);
  ev.target.value = '';
  if (items.length > MAX_FOLDER_FILES) { notify(t('err.too_many_files', { n: MAX_FOLDER_FILES })); return; }
  enqueue(items);
});

// ---------- drag & drop ----------
// Dropping anywhere on the window uploads into the current folder, dropping on
// a folder row uploads into that folder. Dropped folders are walked and
// recreated. The browser never opens the files.

async function readAll(dirEntry) {
  const reader = dirEntry.createReader();
  const out = [];
  for (;;) {
    const batchEntries = await new Promise((resolve, reject) => reader.readEntries(resolve, reject));
    if (!batchEntries.length) return out;
    out.push(...batchEntries);
  }
}
async function collect(entry, prefix, out) {
  if (out.length > MAX_FOLDER_FILES) return;
  if (entry.isFile) {
    const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
    out.push({ file, rel: prefix + file.name });
  } else if (entry.isDirectory) {
    for (const child of await readAll(entry)) await collect(child, prefix + entry.name + '/', out);
  }
}

let dragDepth = 0;
const canDrop = () => !$('files-view').hidden && !$('upload-label').hidden;
const hasFiles = (ev) => [...(ev.dataTransfer?.types || [])].includes('Files');
function dropTarget(ev) {
  const row = ev.target instanceof Element ? ev.target.closest('tr[data-folder]') : null;
  return row && $('entries').contains(row) ? row : null;
}
function clearDropMarks() { for (const r of document.querySelectorAll('tr.drop-target')) r.classList.remove('drop-target'); }
function markDrop(ev) {
  clearDropMarks();
  const row = dropTarget(ev);
  if (row) row.classList.add('drop-target');
  $('drop-text').textContent = t('drop.to', { where: row ? row.dataset.folder : where(state.space, state.path) });
}
window.addEventListener('dragenter', (ev) => {
  if (!hasFiles(ev)) return;
  ev.preventDefault();
  dragDepth++;
  if (canDrop()) { $('drop-overlay').hidden = false; markDrop(ev); }
});
window.addEventListener('dragover', (ev) => {
  if (!hasFiles(ev)) return;
  ev.preventDefault(); // without this the browser would open the file
  ev.dataTransfer.dropEffect = canDrop() ? 'copy' : 'none';
  if (canDrop()) markDrop(ev);
});
window.addEventListener('dragleave', () => {
  if (--dragDepth <= 0) { dragDepth = 0; $('drop-overlay').hidden = true; clearDropMarks(); }
});
window.addEventListener('drop', async (ev) => {
  if (!hasFiles(ev)) return;
  ev.preventDefault();
  const row = dropTarget(ev);
  dragDepth = 0;
  $('drop-overlay').hidden = true;
  clearDropMarks();
  if (!canDrop()) { if (!$('files-view').hidden) notify(t('drop.denied')); return; }
  const dir = row ? row.dataset.folder : state.path;
  // Entries must be taken synchronously, before any await.
  const entries = [...ev.dataTransfer.items].filter((i) => i.kind === 'file').map((i) => (i.webkitGetAsEntry ? i.webkitGetAsEntry() : null));
  const plain = entries.some((e) => !e) ? [...ev.dataTransfer.files].map((file) => ({ file, rel: file.name })) : null;
  const out = plain || [];
  if (!plain) {
    try { for (const e of entries) await collect(e, '', out); } catch (e) { notify(describe(e)); return; }
  }
  if (out.length > MAX_FOLDER_FILES) { notify(t('err.too_many_files', { n: MAX_FOLDER_FILES })); return; }
  if (!out.length) { notify(t('drop.empty')); return; }
  enqueue(out, dir);
});

// ---------- preview ----------

const TEXT_EXT = /\.(txt|md|markdown|ya?ml|json|jsonc|toml|ini|cfg|conf|env|properties|log|csv|tsv|xml|html?|css|scss|js|mjs|ts|tsx|jsx|go|py|rb|php|sh|bash|zsh|ps1|bat|rs|c|h|cpp|hpp|java|kt|swift|sql|gitignore|dockerignore|editorconfig|service|nfo|srt)$/i;
const isTextName = (name) => TEXT_EXT.test(name) || /^(dockerfile|makefile|readme|license|\.env)$/i.test(name);
const MEDIA = [
  [/\.(png|jpe?g|gif|webp|avif|bmp|ico|svg)$/i, 'image'],
  [/\.(mp4|m4v|webm|mov)$/i, 'video'],
  [/\.(mp3|m4a|aac|ogg|oga|opus|wav|flac)$/i, 'audio'],
  [/\.pdf$/i, 'pdf'],
];
const mediaKind = (name) => (MEDIA.find(([re]) => re.test(name)) || [null, isTextName(name) ? 'text' : null])[1];
let previewIndex = -1;

async function openPreview(name) {
  previewIndex = state.files.indexOf(name);
  const dialog = $('preview-dialog');
  if (!dialog.open) dialog.showModal();
  await renderPreview();
}
async function renderPreview() {
  const name = state.files[previewIndex];
  const target = join(state.path, name);
  const url = '/api/preview?' + q(state.space, target);
  const body = $('preview-body');
  body.replaceChildren(el('p', { className: 'muted', text: t('common.loading') }));
  $('preview-name').textContent = name;
  $('preview-icon').className = 'ic ic-' + fileIcon(name);
  $('preview-download').href = '/api/content?' + q(state.space, target);
  $('preview-download').download = name;
  $('preview-prev').disabled = previewIndex <= 0;
  $('preview-next').disabled = previewIndex >= state.files.length - 1;
  const kind = mediaKind(name);
  $('preview-edit').hidden = !(kind === 'text' && can(PERM.modify) && writable());
  if (kind === 'image') body.replaceChildren(el('img', { src: url, alt: name }));
  else if (kind === 'video') body.replaceChildren(el('video', { src: url, controls: true, preload: 'metadata' }));
  else if (kind === 'audio') body.replaceChildren(el('audio', { src: url, controls: true, preload: 'metadata' }));
  else if (kind === 'pdf') body.replaceChildren(el('iframe', { src: url, title: name }));
  else if (kind === 'text') {
    try {
      const txt = await api('GET', '/api/text?' + q(state.space, target));
      if (state.files[previewIndex] !== name) return;
      body.replaceChildren(el('pre', { text: txt.content || t('preview.empty_file') }));
    } catch (e) { body.replaceChildren(el('p', { className: 'muted', text: describe(e) })); }
  } else body.replaceChildren(el('p', { className: 'muted', text: t('preview.unsupported') }));
}
function closePreview() {
  $('preview-body').replaceChildren(); // stops media playback
  if ($('preview-dialog').open) $('preview-dialog').close();
}
$('preview-close').addEventListener('click', closePreview);
$('preview-dialog').addEventListener('close', () => $('preview-body').replaceChildren());
$('preview-prev').addEventListener('click', () => { if (previewIndex > 0) { previewIndex--; renderPreview(); } });
$('preview-next').addEventListener('click', () => { if (previewIndex < state.files.length - 1) { previewIndex++; renderPreview(); } });
$('preview-dialog').addEventListener('keydown', (ev) => {
  if (ev.key === 'ArrowLeft' && !$('preview-prev').disabled) $('preview-prev').click();
  if (ev.key === 'ArrowRight' && !$('preview-next').disabled) $('preview-next').click();
});
$('preview-edit').addEventListener('click', () => { const name = state.files[previewIndex]; closePreview(); openEditor(state.space, join(state.path, name)); });

// ---------- text editor ----------

const editor = { space: null, path: null, version: null, saved: '', saving: false };
const editorDirty = () => !$('editor-view').hidden && $('editor-text').value !== editor.saved;
function editorStatus() {
  const dirty = editorDirty();
  $('editor-state').textContent = t(dirty ? 'editor.dirty' : 'editor.saved');
  $('editor-state').className = 'editor-state ' + (dirty ? 'dirty' : 'muted');
}
async function openEditor(space, path) {
  let txt;
  try { txt = await api('GET', '/api/text?' + q(space, path)); } catch (e) { notify(describe(e)); return; }
  Object.assign(editor, { space, path, version: txt.version, saved: txt.content });
  $('editor-name').textContent = spaceLabel(space) + ' / ' + path;
  $('editor-text').value = txt.content;
  show('editor-view');
  editorStatus();
  $('editor-text').focus();
}
async function saveEditor() {
  if (editor.saving) return;
  editor.saving = true;
  $('editor-save').disabled = true;
  const content = $('editor-text').value;
  try {
    const res = await api('PUT', '/api/text', { json: { space: editor.space, path: editor.path, content, version: editor.version } });
    editor.version = res.version;
    editor.saved = content;
    track(t('op.save', { name: editor.path })).done();
    notify(t('editor.saved_toast', { name: editor.path }), 'ok');
  } catch (e) {
    if (e.code === 'changed') {
      const copy = editor.path.replace(/(\.[^./]+)?$/, ' ' + t('editor.my_version') + '$1');
      const name = await ask({ title: t('editor.conflict_title'), text: t('editor.conflict_text'), label: t('editor.save_as_label'), value: copy, ok: t('editor.save_as') });
      if (name) {
        try {
          const res = await api('PUT', '/api/text', { json: { space: editor.space, path: name, content, version: '' } });
          Object.assign(editor, { path: name, version: res.version, saved: content });
          $('editor-name').textContent = spaceLabel(editor.space) + ' / ' + name;
          track(t('op.save', { name })).done();
          notify(t('editor.saved_as_toast', { name }), 'ok');
        } catch (e2) { notify(describe(e2)); }
      }
    } else notify(describe(e));
  } finally {
    editor.saving = false;
    $('editor-save').disabled = false;
    editorStatus();
  }
}
async function closeEditor() {
  if (editorDirty() && !await ask({ title: t('editor.discard_title'), text: t('editor.discard_text', { name: editor.path }), ok: t('editor.discard') })) return;
  editor.saved = $('editor-text').value;
  const dir = editor.path.includes('/') ? editor.path.slice(0, editor.path.lastIndexOf('/')) : '.';
  navigate(editor.space, dir);
  if (location.hash === hashFor(editor.space, dir)) openFolder(editor.space, dir);
}
$('editor-text').addEventListener('input', editorStatus);
$('editor-text').addEventListener('keydown', (ev) => {
  if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 's') { ev.preventDefault(); saveEditor(); }
  if (ev.key === 'Tab' && !ev.shiftKey && !ev.ctrlKey && !ev.altKey) {
    ev.preventDefault();
    const x = ev.target;
    x.setRangeText('  ', x.selectionStart, x.selectionEnd, 'end');
    editorStatus();
  }
});
$('editor-save').addEventListener('click', saveEditor);
$('editor-close').addEventListener('click', closeEditor);

$('new-text').addEventListener('click', async () => {
  const name = await ask({ title: t('newfile.title'), label: t('newfile.label'), value: 'new.md', ok: t('files.create') });
  if (name === null) return;
  if (!validName(name)) { notify(t('err.name_slash')); return; }
  const path = join(state.path, name);
  try {
    await api('PUT', '/api/text', { json: { space: state.space, path, content: '', version: '' } });
    track(t('op.new_file', { name })).done();
  } catch (e) { notify(describe(e)); return; }
  if (can(PERM.modify)) openEditor(state.space, path);
  else { notify(t('newfile.done', { name }), 'ok'); openFolder(state.space, state.path); }
});

// ---------- copy / move ----------
// One item: the destination is a full path. Several: a destination folder,
// each item keeping its name. One server job runs all items in order.

let transferSources = null;
const withSuffix = (p, suffix) => p.replace(/(\.[^./]+)?$/, ' ' + suffix + '$1');
function defaultTransferPath(space) {
  const f = $('transfer-form').elements;
  const one = transferSources.items.length === 1;
  if (!one) return transferSources.space === space ? '' : (state.path === '.' ? '' : state.path);
  const src = transferSources.items[0].path;
  return space === transferSources.space && f.kind.value === 'copy' ? withSuffix(src, t('transfer.copy_suffix')) : src;
}
function openTransfer(entries) {
  if (!entries.length) return;
  transferSources = { space: state.space, items: entries.map((e) => ({ entry: e, path: join(state.path, e.name) })) };
  const f = $('transfer-form').elements;
  const one = entries.length === 1;
  $('transfer-source').textContent = one
    ? t(entries[0].directory ? 'transfer.source_folder' : 'transfer.source_file', { path: where(state.space, transferSources.items[0].path) })
    : t('transfer.source_many', { n: entries.length, where: where(state.space, state.path) });
  $('transfer-path-label').textContent = t(one ? 'transfer.path' : 'transfer.folder');
  f.path.required = one;
  f.path.placeholder = one ? '' : t('transfer.folder_root');
  $('transfer-move-label').hidden = !(can(PERM.modify) && writable());
  f.kind.value = 'copy';
  const select = f.space;
  select.replaceChildren();
  for (const s of state.spaces.filter((x) => (x.permissions & PERM.create) === PERM.create && !x.read_only)) {
    select.append(el('option', { value: s.name, text: spaceLabel(s.name), selected: s.name === state.space }));
  }
  if (!select.options.length) { notify(t('transfer.no_target')); return; }
  f.path.value = defaultTransferPath(select.value);
  $('transfer-dialog').showModal();
}
$('transfer-form').elements.space.addEventListener('change', (ev) => { $('transfer-form').elements.path.value = defaultTransferPath(ev.target.value); });
for (const r of $('transfer-form').querySelectorAll('input[name=kind]')) r.addEventListener('change', () => { $('transfer-form').elements.path.value = defaultTransferPath($('transfer-form').elements.space.value); });
$('transfer-cancel').addEventListener('click', () => $('transfer-dialog').close());
$('transfer-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const f = ev.target.elements;
  const space = f.space.value;
  const dest = f.path.value.trim().replace(/^\/+|\/+$/g, '');
  const items = transferSources.items.map((it) => ({
    from: { space: transferSources.space, path: it.path },
    to: { space, path: transferSources.items.length === 1 ? dest : join(dest || '.', it.entry.name) },
  }));
  try {
    await api('POST', '/api/transfers', { json: { kind: f.kind.value, items } });
    $('transfer-dialog').close();
    setSelecting(false);
    pollTransfers();
  } catch (e) { notify(describe(e)); }
});

// Server transfers are mirrored into the history; finished ones raise a toast.
const knownJobs = new Map();
let transferTimer = null;
let firstPoll = true;
async function pollTransfers() {
  clearTimeout(transferTimer);
  let list;
  try { list = await api('GET', '/api/transfers'); } catch (e) { return; }
  let running = false;
  let refresh = false;
  for (const j of list.slice(0, 20).reverse()) {
    running ||= j.state === 'running';
    const title = t(j.kind === 'move' ? 'op.move' : 'op.copy', {
      what: j.items > 1 ? t('op.items', { n: j.items }) : where(j.from.space, j.from.path),
      to: where(j.to.space, j.items > 1 ? (j.to.path.includes('/') ? j.to.path.slice(0, j.to.path.lastIndexOf('/')) : '.') : j.to.path),
    });
    let known = knownJobs.get(j.id);
    if (!known) {
      if (j.state !== 'running' && Date.now() - new Date(j.finished).getTime() > 60000) { knownJobs.set(j.id, { final: true }); continue; }
      known = { op: track(title), final: false, quiet: firstPoll && j.state !== 'running' };
      knownJobs.set(j.id, known);
    }
    if (known.final) continue;
    const progress = t('op.transfer_progress', { files: j.files, size: formatSize(j.bytes), done: j.done, items: j.items });
    if (j.state === 'running') known.op.progress(j.items > 1 ? j.done / j.items : null, j.items > 1 ? 1 : 0, progress);
    else {
      known.final = true;
      refresh = true;
      if (j.state === 'done') {
        const detail = progress + (j.skipped ? ', ' + t('op.skipped', { n: j.skipped }) : '');
        known.op.done(detail);
        // Jobs that had already finished before this page load go to the history only.
        if (!known.quiet) notify(title + ' — ' + t('op.done'), 'ok');
      } else {
        const detail = j.state === 'canceled' ? t('op.canceled') : describe({ code: j.error, message: j.error });
        known.op.fail(detail + ' (' + t('op.items_done', { done: j.done, items: j.items }) + ')');
        if (!known.quiet) notify(title + ' — ' + detail);
      }
    }
  }
  firstPoll = false;
  if (running) transferTimer = setTimeout(pollTransfers, 1000);
  if (refresh && !$('files-view').hidden && state.space) openFolder(state.space, state.path);
}

window.addEventListener('beforeunload', (ev) => { if (state.busy || editorDirty()) ev.preventDefault(); });

// ---------- administration ----------

function reauth() {
  const value = $('reauth').value;
  if (!value) { notify(t('admin.reauth_needed')); $('reauth').focus(); return null; }
  return value;
}

// grantsEditor renders a space × permission checkbox table and reads it back.
function grantsEditor(grants, spaces) {
  const rows = ['*', ...spaces.map((s) => s.name)];
  const boxes = {};
  const table = el('table', { className: 'grants' },
    el('thead', {}, el('tr', {}, el('th', { text: t('admin.space') }), ...PERM_KEYS.map(([, short, long]) => el('th', { text: t(short), title: t(long) })))));
  const body = el('tbody');
  for (const name of rows) {
    const granted = grants[name] || 0;
    const label = name === '*' ? t('admin.all_spaces') : spaceLabel(name);
    boxes[name] = PERM_KEYS.map(([key, , long]) => el('input', { type: 'checkbox', checked: (granted & PERM[key]) !== 0, title: t(long), 'aria-label': label + ': ' + t(long) }));
    body.append(el('tr', {}, el('td', { className: 'name', text: label }), ...boxes[name].map((b) => el('td', {}, b))));
  }
  table.append(body);
  return {
    node: table,
    read() {
      const out = {};
      for (const name of rows) {
        const p = PERM_KEYS.reduce((acc, [key], i) => acc | (boxes[name][i].checked ? PERM[key] : 0), 0);
        if (p) out[name] = p;
      }
      return out;
    },
  };
}

let createGrants = null;

async function openAdmin() {
  let users;
  let spaces;
  try {
    [users, spaces] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/spaces')]);
  } catch (e) { notify(describe(e)); return; }
  show('admin-view');
  const list = $('users');
  list.replaceChildren();
  for (const u of users) {
    const admin = el('input', { type: 'checkbox', checked: u.admin });
    const disabled = el('input', { type: 'checkbox', checked: u.disabled });
    const grants = grantsEditor(u.spaces || {}, spaces);
    const save = el('button', { type: 'button', className: 'primary', text: t('admin.save'), on: { click: async () => {
      const password = reauth();
      if (!password) return;
      try {
        await api('PUT', '/api/users/' + encodeURIComponent(u.id), { json: { admin: admin.checked, disabled: disabled.checked, spaces: grants.read(), reauth_password: password } });
        notify(t('admin.saved', { name: u.username }), 'ok');
        if (u.id === state.user.id) { sessionEnded(t('admin.self_changed')); return; }
        openAdmin();
      } catch (e) { notify(describe(e)); }
    } } });
    const next = el('input', { type: 'password', autocomplete: 'new-password', minLength: 12, placeholder: t('admin.new_password'), 'aria-label': t('admin.new_password_for', { name: u.username }) });
    const reset = el('button', { type: 'button', text: t('admin.set_password'), on: { click: async () => {
      const password = reauth();
      if (!password) return;
      try {
        await api('POST', '/api/users/' + encodeURIComponent(u.id) + '/password', { json: { new_password: next.value, reauth_password: password } });
        next.value = '';
        notify(t('admin.password_set', { name: u.username }), 'ok');
        if (u.id === state.user.id) sessionEnded(t('admin.self_password'));
      } catch (e) { notify(describe(e)); }
    } } });
    // Own account cannot be deleted (it would end this session); the server enforces it too.
    const remove = u.id === state.user.id ? null : el('button', { type: 'button', className: 'danger', on: { click: async () => {
      const password = reauth();
      if (!password) return;
      if (!await ask({ title: t('admin.delete_title'), text: t('admin.delete_text', { name: u.username }), ok: t('admin.delete') })) return;
      try {
        await api('DELETE', '/api/users/' + encodeURIComponent(u.id), { json: { reauth_password: password } });
        notify(t('admin.deleted', { name: u.username }), 'ok');
        openAdmin();
      } catch (e) { notify(describe(e)); }
    } } }, icon('trash'), el('span', { text: t('admin.delete') }));
    // Two-factor authentication is set up by each person; an administrator
    // can only turn it off (for someone who lost their phone).
    const totp = !u.two_factor ? null : el('button', { type: 'button', on: { click: async () => {
      const password = reauth();
      if (!password) return;
      if (!await ask({ title: t('admin.totp_reset_title'), text: t('admin.totp_reset_text', { name: u.username }), ok: t('admin.totp_reset') })) return;
      try {
        await api('DELETE', '/api/users/' + encodeURIComponent(u.id) + '/totp', { json: { reauth_password: password } });
        notify(t('admin.totp_reset_done', { name: u.username }), 'ok');
        openAdmin();
      } catch (e) { notify(describe(e)); }
    } } }, icon('shield-lock'), el('span', { text: t('admin.totp_reset') }));
    const name = el('strong', { className: 'name', text: u.username });
    if (u.two_factor) name.append(el('span', { className: 'badge', tip: t('admin.totp_on') }, icon('shield-lock'), '2FA'));
    list.append(el('article', { className: 'user' },
      el('div', { className: 'row between wrap' },
        name,
        el('div', { className: 'row wrap' },
          el('label', { className: 'check' }, admin, ' ' + t('admin.administrator')),
          el('label', { className: 'check' }, disabled, ' ' + t('admin.disabled')),
          save, totp, remove)),
      grants.node,
      el('div', { className: 'row wrap' }, next, reset)));
  }
  createGrants = grantsEditor({ files: PERM.list | PERM.read }, spaces);
  $('create-grants').replaceChildren(createGrants.node);
}

$('open-admin').addEventListener('click', openAdmin);
$('close-admin').addEventListener('click', () => { $('reauth').value = ''; enter(); });
$('create-user').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const f = ev.target.elements;
  const password = reauth();
  if (!password) return;
  try {
    await api('POST', '/api/users', { json: { username: f.username.value.trim(), password: f.password.value, admin: f.admin.checked, spaces: createGrants.read(), reauth_password: password } });
    notify(t('admin.added', { name: f.username.value.trim() }), 'ok');
    ev.target.reset();
    openAdmin();
  } catch (e) { notify(describe(e)); }
});

start();
