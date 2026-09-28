// Browser smoke test against a running Filedeck with EMPTY volumes and an
// existing administrator. FILEDECK_URL (default https://localhost:8443),
// FILEDECK_ADMIN, FILEDECK_PASSWORD. Optional spaces, tested when present:
// "nas" (writable host directory) and "archiwum" (mounted :ro).
// Creates folders, files and user "jan" - use a throwaway instance.
import { chromium, devices } from 'playwright';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const O = process.env.FILEDECK_URL || 'https://localhost:8443';
const ADMIN = process.env.FILEDECK_ADMIN || 'admin';
const PASSWORD = process.env.FILEDECK_PASSWORD || 'correct horse battery';
const problems = [];
const step = (s) => console.log('✓', s);
fs.mkdirSync('out', { recursive: true });

const browser = await chromium.launch();
async function newPage() {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, acceptDownloads: true, viewport: { width: 1280, height: 820 } });
  const page = await ctx.newPage();
  // Expected HTTP errors (401 before login, 409 conflict) are logged by the browser; ignore those.
  page.on('console', (m) => { if ((m.type() === 'error' || m.type() === 'warning') && !m.text().startsWith('Failed to load resource')) problems.push('console: ' + m.text()); });
  page.on('pageerror', (e) => { problems.push('pageerror: ' + e.message); console.log('  pageerror:', e.message); });
  page.on('dialog', async (d) => { problems.push('NATIVE DIALOG (possible XSS): ' + d.message()); await d.dismiss(); });
  return page;
}
async function login(page, user, password) {
  await page.goto(O + '/');
  await page.locator('#login-form input[name=username]').fill(user);
  await page.locator('#login-form input[name=password]').fill(password);
  await page.locator('#login-form button[type=submit]').click();
  await page.locator('#files-view').waitFor();
}
// RFC 6238 code for a base32 key, computed independently of the server.
function totp(key) {
  const bits = [...key.replace(/\s/g, '')].map((c) => 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5, '0')).join('');
  const secret = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const h = crypto.createHmac('sha1', secret).update(msg).digest();
  return String((h.readUInt32BE(h[19] & 15) & 0x7fffffff) % 1000000).padStart(6, '0');
}
const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
// The name cell holds an icon followed by the name.
const row = (page, name) => page.locator('#entries tr').filter({ has: page.locator('td:nth-child(2)', { hasText: new RegExp(escapeRe(name) + '$') }) });
const act = (page, name, label) => row(page, name).getByRole('button', { name: label, exact: true });
async function confirmDialog(page, button, value) {
  const dialog = page.locator('#ask-dialog');
  await dialog.waitFor();
  if (value !== undefined) await dialog.locator('input[name=value]').fill(value);
  await dialog.getByRole('button', { name: button }).click();
  await dialog.waitFor({ state: 'hidden' });
}
const toast = (page, text) => page.locator('#toasts .toast', { hasText: text }).first();

const page = await newPage();
await page.goto(O + '/');
await page.getByRole('heading', { name: 'Sign in' }).waitFor();
step('English is the default language');
await page.locator('#login-form input[name=username]').fill(ADMIN);
await page.locator('#login-form input[name=password]').fill('wrong password!!');
await page.locator('#login-form button[type=submit]').click();
await toast(page, 'Invalid credentials').waitFor();
step('bad password rejected (toast)');
await login(page, ADMIN, PASSWORD);
step('logged in');

const tabs = await page.locator('#spaces .tab').allTextContents();
const hasNas = tabs.some((t) => t.startsWith('nas'));
const hasArchive = tabs.some((t) => t.startsWith('archiwum'));
console.log('  spaces:', tabs.join(', ') || '(only own space)');

for (const name of ['Dokumenty', 'Zdjęcia']) {
  await page.getByPlaceholder('New folder').fill(name);
  await page.getByRole('button', { name: 'Create' }).click();
  await row(page, name).waitFor();
}
step('mkdir');

await row(page, 'Dokumenty').locator('button.name').click();
await page.waitForFunction(() => location.hash === '#/files/Dokumenty');
const big = Buffer.alloc(20 * 1024 * 1024, 7);
await page.setInputFiles('#upload-input', [{ name: 'duzy.bin', mimeType: 'application/octet-stream', buffer: big }, { name: '<img src=x onerror=alert(1)>.txt', mimeType: 'text/plain', buffer: Buffer.from('xss') }]);
await toast(page, 'Uploaded 2 file(s)').waitFor({ timeout: 60000 });
await row(page, 'duzy.bin').waitFor();
if (await page.locator('#entries img').count() !== 0) problems.push('HTML injected from file name');
if (await page.locator('#uploads, #transfers').count() !== 0) problems.push('old operation sections still present');
step('chunked upload, toast, hostile file name rendered as text');

if (await page.locator('#toasts .toast').count() > 3) problems.push('more than 3 toasts visible');
await page.locator('#bell').click();
await page.locator('#history .hist.done', { hasText: 'Upload 2 files' }).waitFor();
await page.screenshot({ path: 'out/bell.png' });
await page.locator('#bell').click();
step('bell shows the operation history');

const [download] = await Promise.all([page.waitForEvent('download'), row(page, 'duzy.bin').getByRole('link', { name: 'Download' }).click()]);
if (fs.statSync(await download.path()).size !== big.length) problems.push('download size mismatch');
step('download');

await page.setInputFiles('#upload-input', [{ name: 'duzy.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('other') }]);
await toast(page, 'already exists').waitFor();
step('conflict reported, no overwrite');

await act(page, 'duzy.bin', 'Rename').click();
await confirmDialog(page, 'Rename', 'raport.bin');
await row(page, 'raport.bin').waitFor();
step('rename via icon button');

await act(page, 'raport.bin', 'Move to trash').click();
await confirmDialog(page, 'Move to trash');
await row(page, 'raport.bin').waitFor({ state: 'detached' });
await page.getByRole('button', { name: 'Trash', exact: true }).click();
const trashed = page.locator('#trash-entries tr').filter({ hasText: 'Dokumenty/raport.bin' });
await trashed.waitFor();
await trashed.getByRole('button', { name: 'Restore' }).click();
await page.locator('#trash-empty').waitFor();
await page.getByRole('button', { name: 'Back to files' }).click();
await row(page, 'raport.bin').waitFor();
await act(page, 'raport.bin', 'Move to trash').click();
await confirmDialog(page, 'Move to trash');
await page.getByRole('button', { name: 'Trash', exact: true }).click();
await page.locator('#trash-entries tr').filter({ hasText: 'raport.bin' }).getByRole('button', { name: 'Delete permanently' }).click();
await confirmDialog(page, 'Delete permanently');
await page.locator('#trash-empty').waitFor();
await page.getByRole('button', { name: 'Back to files' }).click();
step('trash, restore, permanent delete');

// ---- folder upload: a nested tree through the folder picker
const tree = fs.mkdtempSync(path.join(os.tmpdir(), 'fd-'));
fs.mkdirSync(path.join(tree, 'projekt', 'src', 'lib'), { recursive: true });
fs.writeFileSync(path.join(tree, 'projekt', 'README.md'), '# projekt\n');
fs.writeFileSync(path.join(tree, 'projekt', 'src', 'main.go'), 'package main\n');
fs.writeFileSync(path.join(tree, 'projekt', 'src', 'lib', 'util.go'), 'package lib\n');
await page.setInputFiles('#upload-dir-input', path.join(tree, 'projekt'));
await toast(page, 'Uploaded 3 file(s)').waitFor();
await row(page, 'projekt').locator('button.name').click();
await row(page, 'src').locator('button.name').click();
await row(page, 'lib').locator('button.name').click();
await row(page, 'util.go').waitFor();
await page.locator('#crumbs button', { hasText: 'Dokumenty' }).click();
// Wait until the folder is shown: a search typed earlier would be cleared by the navigation.
await row(page, 'projekt').waitFor();
step('folder upload recreates the tree');

// ---- search below the current folder, open a hit in its folder
await page.locator('#search-form input[name=q]').fill('util');
await page.locator('#search-form input[name=q]').press('Enter');
const hit = page.locator('#entries tr', { hasText: 'projekt/src/lib/util.go' });
await hit.waitFor();
await page.locator('#search-summary', { hasText: '1 result' }).waitFor();
await page.screenshot({ path: 'out/search.png' });
await hit.locator('button.name').click();
await page.locator('#preview-name', { hasText: 'util.go' }).waitFor();
await page.locator('#preview-close').click();
if (!await page.evaluate(() => location.hash.endsWith(encodeURIComponent('Dokumenty/projekt/src/lib')))) problems.push('search hit did not open its folder');
await page.locator('#crumbs button', { hasText: 'Dokumenty' }).click();
step('search and open a result in its folder');

// ---- sorting by size and modification dates
await page.locator('button.sort[data-sort=size]').click();
const sizes = await page.evaluate(() => state.view.filter((e) => !e.directory).map((e) => e.size));
if (sizes.some((v, i) => i > 0 && v > sizes[i - 1])) problems.push('size sort is not descending: ' + sizes.join(','));
if (await page.locator('th[aria-sort=descending] button[data-sort=size]').count() !== 1) problems.push('aria-sort not set');
if (!(await page.locator('#entries tr td.date-col').first().textContent()).trim()) problems.push('modification date missing');
await page.locator('button.sort[data-sort=name]').click();
step('sort by size, modification date column');

// ---- theme switch: auto -> light -> dark, remembered across reloads
const bg = () => page.evaluate(() => getComputedStyle(document.body).backgroundColor);
await page.locator('#theme').click();
const light = await bg();
await page.locator('#theme').click();
const dark = await bg();
if (light === dark || await page.evaluate(() => document.documentElement.dataset.theme) !== 'dark') problems.push('theme switch has no effect');
await page.reload();
await page.locator('#files-view').waitFor();
if (await page.evaluate(() => document.documentElement.dataset.theme) !== 'dark') problems.push('theme not remembered');
await page.screenshot({ path: 'out/dark.png', fullPage: true });
await page.locator('#theme').click();
await page.locator('#theme').click();
step('theme switch (light/dark/auto, remembered)');

// ---- drag & drop: onto the window (current folder) and onto a folder row
async function dropFiles(selector, files) {
  await page.evaluate(({ selector, files }) => {
    const dt = new DataTransfer();
    for (const f of files) dt.items.add(new File([f.content], f.name, { type: 'text/plain' }));
    const target = document.querySelector(selector);
    for (const type of ['dragenter', 'dragover', 'drop']) target.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }));
  }, { selector, files });
}
await page.locator('#crumbs button', { hasText: 'My files' }).click();
await row(page, 'Dokumenty').waitFor();
await dropFiles('#drop', [{ name: 'upuszczony.txt', content: 'drop' }]);
await row(page, 'upuszczony.txt').waitFor();
await dropFiles('#entries tr[data-folder="Zdjęcia"] td:nth-child(2)', [{ name: 'do-folderu.txt', content: 'x' }]);
// 1 B tells this upload apart from the previous one (4 B) whose toast may still be shown.
await toast(page, 'Uploaded 1 file(s), 1 B').waitFor();
await row(page, 'Zdjęcia').locator('button.name').click();
await row(page, 'do-folderu.txt').waitFor();
step('drag & drop onto window and onto a folder');

// ---- preview and editor with conflict handling
await page.setInputFiles('#upload-input', [
  { name: 'obraz.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><rect width="40" height="40" fill="red"/><script>alert("svg")</script></svg>') },
  { name: 'config.yaml', mimeType: 'text/yaml', buffer: Buffer.from('a: 1\n') },
]);
await row(page, 'config.yaml').waitFor();
await row(page, 'obraz.svg').locator('button.name').click();
await page.locator('#preview-body img').waitFor();
if (!await page.locator('#preview-body img').evaluate((img) => img.complete && img.naturalWidth > 0)) problems.push('image preview did not load');
await page.locator('#preview-prev').click();
await page.locator('#preview-name', { hasText: 'do-folderu.txt' }).waitFor();
await page.locator('#preview-close').click();
await row(page, 'config.yaml').locator('button.name').click();
await page.locator('#preview-body pre', { hasText: 'a: 1' }).waitFor();
await page.locator('#preview-edit').click();
await page.locator('#editor-view').waitFor();
await page.locator('#editor-text').fill('a: 2\nb: 3\n');
await page.locator('#editor-state', { hasText: 'unsaved' }).waitFor();
await page.keyboard.press('Control+s');
await page.locator('#editor-state', { hasText: 'saved' }).waitFor();
await page.evaluate(async () => {
  const me = await (await fetch('/api/auth/me')).json();
  const cur = await (await fetch('/api/text?space=files&path=Zdj%C4%99cia%2Fconfig.yaml')).json();
  await fetch('/api/text', { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrf }, body: JSON.stringify({ space: 'files', path: 'Zdjęcia/config.yaml', content: 'other: true\n', version: cur.version }) });
});
await page.locator('#editor-text').fill('a: 4\n');
await page.locator('#editor-save').click();
await confirmDialog(page, 'Save as');
await page.locator('#editor-name', { hasText: 'my version' }).waitFor();
await page.locator('#editor-close').click();
await row(page, 'config (my version).yaml').waitFor();
step('preview (image, text), editor save + conflict -> save as copy');

// ---- selection: bulk move into a folder, bulk delete
await page.getByPlaceholder('New folder').fill('wybrane');
await page.getByRole('button', { name: 'Create' }).click();
await row(page, 'wybrane').waitFor();
await page.locator('#select-mode').click();
await row(page, 'config.yaml').locator('input[type=checkbox]').check();
await row(page, 'obraz.svg').locator('td:nth-child(3)').click({ modifiers: ['Control'] });
await page.locator('#bulk-count', { hasText: '2 selected' }).waitFor();
await page.screenshot({ path: 'out/selection.png' });
await page.locator('#bulk-transfer').click();
await page.locator('#transfer-form input[value=move]').check();
await page.locator('#transfer-form input[name=path]').fill('Zdjęcia/wybrane');
await page.locator('#transfer-form').getByRole('button', { name: 'Run' }).click();
await row(page, 'obraz.svg').waitFor({ state: 'detached' });
await row(page, 'wybrane').locator('button.name').click();
await row(page, 'config.yaml').waitFor();
await row(page, 'obraz.svg').waitFor();
await page.locator('#select-mode').click();
await page.locator('#select-all').check();
await page.locator('#bulk-trash').click();
await confirmDialog(page, 'Move to trash');
await page.locator('#empty').waitFor();
await page.locator('#crumbs button', { hasText: 'Zdjęcia' }).click();
step('selection: bulk move to a folder, bulk delete');

// ---- copy and move between spaces
if (hasNas) {
  await act(page, 'config (my version).yaml', 'Copy or move').click();
  await page.locator('#transfer-form select[name=space]').selectOption('nas');
  await page.locator('#transfer-form input[name=path]').fill('config-kopia.yaml');
  await page.locator('#transfer-form').getByRole('button', { name: 'Run' }).click();
  await toast(page, 'Copy').waitFor();
  await act(page, 'do-folderu.txt', 'Copy or move').click();
  await page.locator('#transfer-form input[value=move]').check();
  await page.locator('#transfer-form select[name=space]').selectOption('nas');
  await page.locator('#transfer-form input[name=path]').fill('przeniesiony.txt');
  await page.locator('#transfer-form').getByRole('button', { name: 'Run' }).click();
  await row(page, 'do-folderu.txt').waitFor({ state: 'detached' });
  step('copy and move to host space');

  await page.locator('#spaces .tab', { hasText: 'nas' }).click();
  await page.waitForFunction(() => location.hash.startsWith('#/nas/'));
  await row(page, 'istniejacy').waitFor();
  await page.setInputFiles('#upload-input', [{ name: 'z-filedeck.txt', mimeType: 'text/plain', buffer: Buffer.from('hello nas') }]);
  await row(page, 'z-filedeck.txt').waitFor();
  await row(page, 'config-kopia.yaml').waitFor();
  await row(page, 'przeniesiony.txt').waitFor();
  if (await page.getByText('.filedeck', { exact: true }).count() !== 0) problems.push('metadata directory visible');
  step('upload into host space "nas"');
}
if (hasArchive) {
  await page.locator('#spaces .tab', { hasText: 'archiwum' }).click();
  await page.locator('#readonly').waitFor();
  if (await page.locator('#upload-label').isVisible() || await page.locator('#mkdir-form').isVisible()) problems.push('write controls in read-only space');
  step('read-only space: badge, no write controls');
}

// ---- public links: folder with password, file without; revoke; rename ends a link
await page.evaluate(() => { location.hash = '#/files'; });
await page.getByPlaceholder('New folder').fill('Wspólne');
await page.getByRole('button', { name: 'Create' }).click();
await row(page, 'Wspólne').locator('button.name').click();
await page.waitForFunction(() => location.hash === '#/files/Wsp%C3%B3lne' || location.hash === '#/files/Wspólne');
await page.locator('#crumbs', { hasText: 'Wspólne' }).waitFor(); // listing opened before uploading into it
await page.setInputFiles('#upload-input', [{ name: 'plan.txt', mimeType: 'text/plain', buffer: Buffer.from('plan') }]);
await row(page, 'plan.txt').waitFor();
async function createLink(name, password) {
  await act(page, name, 'Share link').click();
  const dialog = page.locator('#share-dialog');
  await dialog.waitFor();
  if (password) await dialog.locator('input[name=password]').fill(password);
  await dialog.getByRole('button', { name: 'Create link' }).click();
  await page.locator('#share-url').waitFor();
  const url = await page.locator('#share-url').inputValue();
  await dialog.getByRole('button', { name: 'Close' }).click();
  return url;
}
const fileURL = await createLink('plan.txt', '');
await page.locator('.crumbs a, .crumbs button').first().click();
await row(page, 'Wspólne').waitFor();
const folderURL = await createLink('Wspólne', 'tajne-haslo');
if (!fileURL.includes('/s/') || fileURL === folderURL) problems.push('bad link URLs: ' + fileURL + ' ' + folderURL);
const guest = await newPage();
await guest.goto(folderURL);
await guest.getByRole('heading', { name: 'This link is protected' }).waitFor();
await guest.locator('#unlock-form input[name=password]').fill('zle-haslo-123');
await guest.getByRole('button', { name: 'Open' }).click();
await guest.locator('#toasts .toast', { hasText: 'Wrong password' }).waitFor();
await guest.locator('#unlock-form input[name=password]').fill('tajne-haslo');
await guest.getByRole('button', { name: 'Open' }).click();
await guest.locator('#entries tr', { hasText: 'plan.txt' }).waitFor();
const [guestDownload] = await Promise.all([guest.waitForEvent('download'), guest.locator('#entries tr', { hasText: 'plan.txt' }).getByRole('link', { name: 'Download' }).click()]);
if (fs.readFileSync(await guestDownload.path(), 'utf8') !== 'plan') problems.push('shared folder download content');
await guest.screenshot({ path: 'out/share-folder.png', fullPage: true });
await guest.goto(fileURL);
await guest.locator('#file-name', { hasText: 'plan.txt' }).waitFor();
await guest.screenshot({ path: 'out/share-file.png', fullPage: true });
step('public links: password-protected folder and file, no account needed');

await page.getByRole('button', { name: 'Shared links' }).click();
await page.getByRole('heading', { name: 'Shared links' }).waitFor();
if (await page.locator('#links-entries tr').count() !== 2) problems.push('shared links list');
await page.screenshot({ path: 'out/links.png', fullPage: true });
await page.locator('#links-entries tr', { hasText: 'Wspólne' }).filter({ hasNotText: 'plan.txt' }).getByRole('button', { name: 'Revoke link' }).click();
await confirmDialog(page, 'Revoke link');
await page.locator('#links-entries tr').nth(1).waitFor({ state: 'detached' });
await guest.goto(folderURL);
await guest.getByRole('heading', { name: 'This link is not available' }).waitFor();
// Renaming the file (as any app or SMB user could) ends its link.
await page.locator('#links-entries tr', { hasText: 'plan.txt' }).getByRole('button', { name: 'Show in folder' }).click();
await row(page, 'plan.txt').waitFor();
await act(page, 'plan.txt', 'Rename').click();
await confirmDialog(page, 'Rename', 'plan-v2.txt');
await row(page, 'plan-v2.txt').waitFor();
await guest.goto(fileURL);
await guest.getByRole('heading', { name: 'This link is not available' }).waitFor();
await page.getByRole('button', { name: 'Shared links' }).click();
await page.locator('#links-entries tr', { hasText: 'Not working' }).waitFor();
await page.locator('#close-links').click();
step('shared links: revoke, rename ends the link, status shown');

// ---- long names are cut, the table keeps its width; "My files" is the first tab
await page.evaluate(() => { location.hash = '#/files'; });
await row(page, 'Dokumenty').waitFor();
const longName = 'a-very-long-video-file-name-'.repeat(7) + 'S01E01.mkv';
await page.setInputFiles('#upload-input', [{ name: longName, mimeType: 'video/x-matroska', buffer: Buffer.from('x') }]);
await row(page, longName).waitFor();
const layout = await page.evaluate(() => {
  const box = document.querySelector('#drop');
  const heights = [...document.querySelectorAll('#entries .row-actions')].map((a) => a.getBoundingClientRect().height);
  const label = document.querySelector('#entries .name .label');
  return { overflow: box.scrollWidth > box.clientWidth + 1, actions: Math.max(...heights), rows: Math.max(...[...document.querySelectorAll('#entries tr')].map((r) => r.getBoundingClientRect().height)), cut: [...document.querySelectorAll('#entries .name .label')].some((l) => l.scrollWidth > l.clientWidth) };
});
if (layout.overflow) problems.push('file table scrolls horizontally');
if (layout.actions > 40 || layout.rows > 60) problems.push('row actions wrap or rows grow: ' + JSON.stringify(layout));
if (!layout.cut) problems.push('long name not cut with an ellipsis');
await page.screenshot({ path: 'out/long-names.png' });
const orderedTabs = await page.locator('#spaces .tab').allTextContents();
if (orderedTabs.length > 1) {
  const rest = orderedTabs.slice(1);
  if (!orderedTabs[0].startsWith('My files') || await page.locator('#spaces .tab-sep').count() !== 1 || rest.join() !== [...rest].sort().join()) problems.push('tab order: ' + orderedTabs.join(', '));
}
step('long names cut, one row of actions, "My files" tab first');

// ---- phone layout: one-row header with a menu, compact toolbar, actions in a sheet, no zoom on inputs
{
  const ctx = await browser.newContext({ ...devices['iPhone 13'], ignoreHTTPSErrors: true });
  const phone = await ctx.newPage();
  phone.on('pageerror', (e) => problems.push('phone pageerror: ' + e.message));
  await phone.goto(O + '/');
  if (await phone.evaluate(() => getComputedStyle(document.querySelector('#login-form input')).fontSize) !== '16px') problems.push('phone inputs smaller than 16px (Safari zooms in)');
  await phone.locator('#login-form input[name=username]').fill(ADMIN);
  await phone.locator('#login-form input[name=password]').fill(PASSWORD);
  await phone.locator('#login-form button[type=submit]').click();
  await phone.locator('#entries tr').first().waitFor();
  const m = await phone.evaluate(() => ({ bar: document.querySelector('.bar').getBoundingClientRect().height, toolbar: document.querySelector('.toolbar').getBoundingClientRect().height, overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth }));
  if (m.bar > 70 || m.toolbar > 130 || m.overflow) problems.push('phone layout: ' + JSON.stringify(m));
  await phone.locator('#menu-toggle').click();
  await phone.getByRole('button', { name: 'Log out' }).waitFor();
  await phone.keyboard.press('Escape');
  await phone.locator('#entries tr').first().locator('.more').click();
  await phone.locator('#action-sheet').getByRole('button', { name: 'Rename' }).waitFor();
  await phone.screenshot({ path: 'out/phone.png' });
  await phone.locator('#sheet-cancel').click();
  await ctx.close();
}
step('phone: one-row header with menu, compact toolbar, action sheet');

// ---- language: switch to Polish and back
await page.locator('#lang').click();
await page.locator('#upload-label:visible, #readonly:visible').filter({ hasText: /Wyślij pliki|tylko do odczytu/ }).first().waitFor();
if (await page.evaluate(() => document.documentElement.lang) !== 'pl') problems.push('lang attribute not updated');
await page.reload();
await page.locator('#files-view').waitFor();
if (await page.locator('#lang-label').textContent() !== 'PL') problems.push('language not remembered');
await page.screenshot({ path: 'out/polish.png', fullPage: true });
await page.locator('#lang').click();
await page.locator('#lang-label', { hasText: 'EN' }).waitFor();
step('language switch EN/PL, remembered');

await page.getByRole('button', { name: 'Users' }).click();
await page.getByRole('heading', { name: 'Users' }).waitFor();
await page.locator('#create-user input[name=username]').fill('jan');
await page.locator('#create-user input[name=password]').fill('haslo-dla-jana-123');
if (hasNas) {
  await page.locator('#create-grants').getByLabel('nas: List').check();
  await page.locator('#create-grants').getByLabel('nas: Read').check();
}
await page.getByRole('button', { name: 'Add' }).click();
await toast(page, 'Enter your password').waitFor();
// A wrong administrator password must not end the session (regression).
await page.locator('#reauth').fill('definitely-not-the-password');
await page.getByRole('button', { name: 'Add' }).click();
await toast(page, 'Wrong password').waitFor();
if (!await page.locator('#admin-view').isVisible() || await page.locator('#login-view').isVisible()) problems.push('wrong admin password logged the user out');
await page.locator('#reauth').fill(PASSWORD);
await page.getByRole('button', { name: 'Add' }).click();
await page.locator('#users .user').filter({ hasText: 'jan' }).waitFor();
step('admin: create user with per-space grants (reauth required)');
await page.screenshot({ path: 'out/admin.png', fullPage: true });
await page.locator('#close-admin').click();

const jan = await newPage();
await login(jan, 'jan', 'haslo-dla-jana-123');
await jan.locator('#entries tr').first().waitFor();
const janTabs = await jan.locator('#spaces .tab').allTextContents();
if (hasArchive && janTabs.some((t) => t.startsWith('archiwum'))) problems.push('space without grant visible');
if (hasNas && !janTabs.some((t) => t.startsWith('nas'))) problems.push('granted space missing');
if (await jan.locator('#upload-label').isVisible() || await jan.locator('#mkdir-form').isVisible() || await jan.locator('#open-trash').isVisible()) problems.push('read-only user sees write controls');
if (await jan.getByRole('button', { name: 'Move to trash' }).count() !== 0) problems.push('read-only user sees delete');
step('read-only user: only granted spaces, no write controls');

// ---- two-factor authentication: set up by the user, recovery code, admin reset
await jan.getByRole('button', { name: 'Two-factor authentication' }).click();
const totpDialog = jan.locator('#totp-dialog');
await totpDialog.locator('#totp-status', { hasText: 'Off' }).waitFor();
await totpDialog.locator('input[name=password]').fill('haslo-dla-jana-123');
await totpDialog.getByRole('button', { name: 'Set up' }).click();
await totpDialog.locator('#totp-setup').waitFor();
const darkModules = await jan.locator('#totp-qr').evaluate((c) => {
  const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data;
  let n = 0;
  for (let i = 0; i < d.length; i += 4) if (d[i] < 128) n++;
  return n;
});
if (darkModules < 1000) problems.push('QR code not drawn');
const totpKey = await totpDialog.locator('#totp-key').textContent();
await totpDialog.locator('input[name=code]').fill('000000' === totp(totpKey) ? '111111' : '000000');
await totpDialog.getByRole('button', { name: 'Turn on' }).click();
await toast(jan, 'Wrong or expired code').waitFor();
await totpDialog.locator('input[name=code]').fill(totp(totpKey));
await totpDialog.getByRole('button', { name: 'Turn on' }).click();
await totpDialog.locator('#totp-code-list li').nth(9).waitFor();
const recovery = await totpDialog.locator('#totp-code-list li').allTextContents();
await totpDialog.locator('#totp-status', { hasText: '10 recovery codes left' }).waitFor();
await jan.screenshot({ path: 'out/totp.png' });
await totpDialog.getByRole('button', { name: 'Close' }).click();
await jan.getByRole('button', { name: 'Log out' }).click();
await jan.locator('#login-form input[name=username]').fill('jan');
await jan.locator('#login-form input[name=password]').fill('haslo-dla-jana-123');
await jan.locator('#login-form button[type=submit]').click();
await jan.locator('#login-code-row').waitFor();
await jan.getByRole('button', { name: 'Use a recovery code' }).click();
await jan.locator('#login-form input[name=code]').fill(recovery[0]);
await jan.locator('#login-form button[type=submit]').click();
await jan.locator('#files-view').waitFor();
await toast(jan, 'recovery code').waitFor();
await page.getByRole('button', { name: 'Users' }).click();
const janCard = page.locator('#users .user').filter({ hasText: 'jan' });
await janCard.locator('.badge', { hasText: '2FA' }).waitFor();
await page.locator('#reauth').fill(PASSWORD);
await janCard.getByRole('button', { name: 'Turn off 2FA' }).click();
await confirmDialog(page, 'Turn off 2FA');
await janCard.locator('.badge', { hasText: '2FA' }).waitFor({ state: 'detached' });
await page.locator('#close-admin').click();
await jan.reload();
await jan.getByRole('heading', { name: 'Sign in' }).waitFor();
// This test signs in and confirms passwords faster than the login rate limit
// (10 per minute per client) allows; wait for two attempts to refill
// (this sign-in and the password confirmation of the next step).
await jan.waitForTimeout(13000);
await login(jan, 'jan', 'haslo-dla-jana-123');
step('two-factor: QR setup, recovery code sign-in, admin reset ends sessions');

// ---- delete a user: their session ends
await page.getByRole('button', { name: 'Users' }).click();
await page.getByRole('heading', { name: 'Users' }).waitFor();
if (await page.locator('#users .user').filter({ has: page.locator('strong.name', { hasText: new RegExp('^' + ADMIN + '$') }) }).getByRole('button', { name: 'Delete user' }).count() !== 0) problems.push('own account can be deleted from the UI');
await page.locator('#reauth').fill(PASSWORD);
await page.locator('#users .user').filter({ hasText: 'jan' }).getByRole('button', { name: 'Delete user' }).click();
await confirmDialog(page, 'Delete user');
await page.locator('#users .user').filter({ hasText: 'jan' }).waitFor({ state: 'detached' });
await jan.reload();
await jan.getByRole('heading', { name: 'Sign in' }).waitFor();
await page.locator('#close-admin').click();
step('admin: delete a user, their session ends');

await page.getByRole('button', { name: 'Log out' }).click();
await page.getByRole('heading', { name: 'Sign in' }).waitFor();
await page.reload();
await page.getByRole('heading', { name: 'Sign in' }).waitFor();
step('logout');
await page.screenshot({ path: 'out/login.png' });
await browser.close();
console.log(problems.length ? 'PROBLEMS:\n' + problems.join('\n') : 'NO CONSOLE/CSP PROBLEMS');
process.exit(problems.length ? 1 : 0);
