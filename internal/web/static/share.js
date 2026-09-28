'use strict';
// Public link page. Same rules as app.js: untrusted names only via textContent,
// no innerHTML (Trusted Types), every text through t('key') from lang-*.json.
// The page is read-only; the server re-checks the link on every request.

const $ = (id) => document.getElementById(id);
const LANGS = ['en', 'pl'];
const token = location.pathname.split('/')[2] || '';
const base = '/api/public/' + encodeURIComponent(token);
let dict = {};
let lang = 'en';
let info = null;

function t(key, vars = {}) {
  const s = dict[key] ?? key;
  return s.replace(/\{(\w+)\}/g, (_, k) => (k in vars ? String(vars[k]) : '{' + k + '}'));
}
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
const icon = (name) => { const s = document.createElement('span'); s.className = 'ic ic-' + name; s.setAttribute('aria-hidden', 'true'); return s; };
const FILE_ICONS = [
  [/\.(jpe?g|png|gif|webp|bmp|svg|heic|tiff?|avif)$/i, 'photo'],
  [/\.(mp4|mkv|mov|avi|webm|m4v)$/i, 'movie'],
  [/\.(mp3|flac|wav|ogg|m4a|aac|opus)$/i, 'music'],
  [/\.pdf$/i, 'file-type-pdf'],
  [/\.(zip|7z|rar|tar|gz|tgz|bz2|xz|zst)$/i, 'file-zip'],
  [/\.(txt|md|log|csv|rtf|docx?|odt)$/i, 'file-text'],
];
const fileIcon = (name) => (FILE_ICONS.find(([re]) => re.test(name)) || [null, 'file'])[1];
function formatSize(n) {
  if (n < 1024) return n + ' B';
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let v = n;
  let i = -1;
  do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
  return v.toLocaleString(lang, { maximumFractionDigits: v < 10 ? 1 : 0 }) + ' ' + units[i];
}
const fmtTime = (d) => new Date(d).toLocaleString(lang);
function toast(text) {
  const node = el('div', { className: 'toast error', role: 'alert' }, icon('alert-circle'), el('span', { text }));
  $('toasts').replaceChildren(node);
  setTimeout(() => node.remove(), 8000);
}

async function call(method, url, json) {
  const opts = { method, credentials: 'same-origin', cache: 'no-store', redirect: 'error', headers: {} };
  if (json !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(json); }
  const res = await fetch(url, opts);
  const data = (res.headers.get('Content-Type') || '').startsWith('application/json') ? await res.json().catch(() => null) : null;
  if (!res.ok) { const e = new Error((data && data.error) || 'HTTP ' + res.status); e.code = data && data.error; throw e; }
  return data;
}
const describe = (e) => (e.code && dict['err.' + e.code] ? t('err.' + e.code) : e.message);

// ---------- language and theme (same storage keys as the application) ----------

async function loadLanguage(name) {
  dict = await (await fetch('/assets/lang-' + name + '.json', { cache: 'no-store' })).json();
  lang = name;
  document.documentElement.lang = name;
  $('lang-label').textContent = name.toUpperCase();
  for (const e of document.querySelectorAll('[data-i18n]')) e.textContent = t(e.dataset.i18n);
  for (const e of document.querySelectorAll('[data-i18n-title]')) e.title = t(e.dataset.i18nTitle);
  for (const e of document.querySelectorAll('[data-i18n-aria-label]')) e.setAttribute('aria-label', t(e.dataset.i18nAriaLabel));
  applyTheme(document.documentElement.dataset.theme || 'auto');
}
function savedLanguage() {
  try { const l = localStorage.getItem('filedeck-lang'); if (LANGS.includes(l)) return l; } catch (e) { /* default */ }
  return 'en';
}
$('lang').addEventListener('click', async () => {
  const next = LANGS[(LANGS.indexOf(lang) + 1) % LANGS.length];
  try { localStorage.setItem('filedeck-lang', next); } catch (e) { /* optional */ }
  await loadLanguage(next);
  render();
});
const THEMES = [['auto', 'device-desktop'], ['light', 'sun'], ['dark', 'moon']];
function applyTheme(name) {
  const [, ic] = THEMES.find((x) => x[0] === name) || THEMES[0];
  if (name === 'auto') delete document.documentElement.dataset.theme; else document.documentElement.dataset.theme = name;
  try { if (name === 'auto') localStorage.removeItem('filedeck-theme'); else localStorage.setItem('filedeck-theme', name); } catch (e) { /* optional */ }
  $('theme').replaceChildren(icon(ic), el('span', { text: t('theme.' + name) }));
  $('theme').title = t('theme.title', { name: t('theme.' + name + '_long') });
  $('theme').setAttribute('aria-label', $('theme').title);
}
$('theme').addEventListener('click', () => {
  const i = THEMES.findIndex((x) => x[0] === (document.documentElement.dataset.theme || 'auto'));
  applyTheme(THEMES[(i + 1) % THEMES.length][0]);
});

// ---------- views ----------

const VIEWS = ['loading-view', 'unlock-view', 'file-view', 'folder-view', 'gone-view'];
function show(view) { for (const id of VIEWS) $(id).hidden = id !== view; }
function gone(e) { if (e.code === 'link_unavailable') show('gone-view'); else toast(describe(e)); }
const contentURL = (path) => base + '/content' + (path ? '?path=' + encodeURIComponent(path) : '');
// Folder navigation lives in the fragment (#/sub/folder), never sent to the server.
function subPath() {
  const h = decodeURIComponent(location.hash.replace(/^#\/?/, ''));
  return h && !h.split('/').some((p) => p === '' || p === '.' || p === '..') ? h : '.';
}

async function load() {
  try {
    info = await call('GET', base);
  } catch (e) { gone(e); return; }
  if (info.needs_password) { show('unlock-view'); return; }
  render();
}

async function render() {
  if (!info || info.needs_password) return;
  const expires = t('public.expires', { time: fmtTime(info.expires) });
  document.title = info.name + ' — Filedeck';
  if (!info.directory) {
    $('file-icon').className = 'ic ic-' + fileIcon(info.name);
    $('file-name').textContent = info.name;
    $('file-meta').textContent = formatSize(info.size) + ' · ' + fmtTime(info.modified);
    $('file-download').href = contentURL('');
    $('file-download').download = info.name;
    $('file-expires').textContent = expires;
    show('file-view');
    return;
  }
  const path = subPath();
  let entries;
  try {
    entries = await call('GET', base + '/files' + (path === '.' ? '' : '?path=' + encodeURIComponent(path)));
  } catch (e) {
    if (path !== '.' && e.code !== 'link_unavailable') { location.hash = ''; return; }
    gone(e);
    return;
  }
  const crumbs = $('crumbs');
  crumbs.replaceChildren(el('li', {}, el('a', { href: '#', className: 'link' }, icon('folder'), info.name)));
  let acc = '';
  for (const part of path === '.' ? [] : path.split('/')) {
    acc = acc ? acc + '/' + part : part;
    crumbs.append(el('li', {}, el('a', { href: '#/' + acc.split('/').map(encodeURIComponent).join('/'), className: 'link', text: part })));
  }
  entries.sort((a, b) => (b.directory - a.directory) || a.name.localeCompare(b.name, lang, { numeric: true }));
  const body = $('entries');
  body.replaceChildren();
  for (const e of entries) {
    const target = path === '.' ? e.name : path + '/' + e.name;
    const label = el('span', { className: 'label', text: e.name });
    const name = e.directory
      ? el('a', { href: '#/' + target.split('/').map(encodeURIComponent).join('/'), className: 'link name', title: e.name }, icon('folder'), label)
      : el('span', { className: 'name', title: e.name }, icon(fileIcon(e.name)), label);
    const actions = el('span', { className: 'row-actions' });
    if (!e.directory) actions.append(el('a', { href: contentURL(target), download: e.name, className: 'icon-action', tip: t('action.download') }, icon('download')));
    body.append(el('tr', {}, el('td', {}, name),
      el('td', { className: 'num', text: e.directory ? '—' : formatSize(e.size) }),
      el('td', { className: 'num date-col muted', text: fmtTime(e.modified) }),
      el('td', { className: 'num' }, actions)));
  }
  $('empty').hidden = entries.length > 0;
  $('folder-expires').textContent = expires;
  show('folder-view');
}
window.addEventListener('hashchange', render);

$('unlock-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const input = ev.target.elements.password;
  try {
    await call('POST', base + '/unlock', { password: input.value });
    input.value = '';
    $('toasts').replaceChildren();
    await load();
  } catch (e) {
    if (e.code === 'link_unavailable') gone(e); else { toast(describe(e)); input.select(); }
  }
});

(async () => {
  await loadLanguage(savedLanguage());
  await load();
})();
