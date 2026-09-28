# Rejestr publicznych advisory File Browser

Pobrano: 2026-09-28T08:25:20.485233+00:00. Źródło: [GitHub API](https://api.github.com/repos/filebrowser/filebrowser/security-advisories?per_page=100).

62 wpisy; 19 bez wskazanej poprawionej wersji w metadanych. Kategorie i wymagania Filedeck są naszą klasyfikacją, nie danymi upstreamu. Wersje pozostawiono zgodnie z API; nie wywnioskowano numeru wydania lokalnego HEAD. Każdy wpis nadal wymaga testu Filedeck albo uzasadnienia braku danej funkcji.

Statusy odnoszą się wyłącznie do badanego checkoutu. „Ochrona widoczna” nie oznacza dowodu braku wariantów ataku. Brak wskazanej poprawki upstreamu nie jest tożsamy z potwierdzonym brakiem zabezpieczenia w kodzie.

## Ścieżki i autoryzacja

Granica przestrzeni, wspólna polityka operacji, kontrola aliasów i wyścigów; testy traversal i macierz wszystkich dróg dostępu.

| Advisory | Waga | Zakres wg upstreamu | Poprawka wg upstreamu | Lokalna ocena |
|---|---|---|---|---|
| [GHSA-7w29-q235-57m9](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-7w29-q235-57m9) — Symlink aliases inside a user scope bypass path deny rules | medium | <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-8q5j-8wcr-8v2v](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-8q5j-8wcr-8v2v) — Archive downloads hang forever on named pipes | medium | <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-77x8-73f4-5485](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-77x8-73f4-5485) — Recursive copy, rename and delete ignore rules denying descendants | high | <= 2.63.21 | v2.63.22 | Odpowiednia ochrona widoczna w kodzie; nie uruchomiono testów. |
| [GHSA-7whw-q6gh-xr59](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-7whw-q6gh-xr59) — File checksum disclosure via /api/resources endpoint bypassing Perm.Download check | medium | <= 2.63.18 | 2.63.19 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-fgm5-pw99-w2p7](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-fgm5-pw99-w2p7) — Access rule bypass via case-variant and Windows-separator paths | medium | <= 2.63.20 | v2.63.21 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-83xp-526h-j3ww](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-83xp-526h-j3ww) — Archive builder turns backslash filenames into path traversal (zip-slip) | medium | >= 2.63.6, <= 2.63.16 | 2.63.17 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-8wc8-hf36-mjh9](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-8wc8-hf36-mjh9) — ScopedFs follows a dangling symlink on write, letting a scoped user create files outside their scope | medium | <= 2.63.15 | 2.63.16 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-gxjx-7m74-hcq8](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-gxjx-7m74-hcq8) — Path traversal in download-as-zip/tar via Windows-style backslash separators in stored filenames | medium | <= 2.63.5 | 2.63.6 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-239w-m3h6-ch8v](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-239w-m3h6-ch8v) — Symlinked directories let scoped users and public-share recipients read and write files outside their scope | high | <= 2.63.13 | 2.63.14 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-67cg-cpj7-qgc9](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-67cg-cpj7-qgc9) — Text file content disclosure via /api/resources endpoint bypassing Perm.Download check | medium | <= 2.62.2 | 2.63.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-5q48-q4fm-g3m6](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-5q48-q4fm-g3m6) — Access rule bypass via HasPrefix without trailing separator in path matching | medium | <= 2.62.2 | 2.63.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-9f3r-2vgw-m8xp](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-9f3r-2vgw-m8xp) — Access Rule Bypass via Path Traversal in Copy/Rename Destination Parameter | medium | <= 2.61.2 | 2.62.0 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-4mh3-h929-w968](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-4mh3-h929-w968) — Path-Based Access Control Bypass via Multiple Leading Slashes in URL | high | <= 2.57.0 | 2.57.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |

## Upload i spójność zapisu

Prywatny staging, właściciel uploadu, serializacja i limity rzeczywistych bajtów; testy błędu, konkurencji i restartu.

| Advisory | Waga | Zakres wg upstreamu | Poprawka wg upstreamu | Lokalna ocena |
|---|---|---|---|---|
| [GHSA-c4fr-5f24-4wrj](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-c4fr-5f24-4wrj) — Upload failure-cleanup recursively deletes directories, bypassing Perm.Delete and deny rules | high | >= 2.5.0, <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-4r8p-gqj2-mwgm](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-4r8p-gqj2-mwgm) — Concurrent TUS uploads write past the declared Upload-Length | low | >= 2.24.0, <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-m9f5-2232-frp6](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-m9f5-2232-frp6) — Out-of-scope file deletion via symlink-following delete in TUS upload-cache eviction | high | <= 2.63.18 | 2.63.19 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-ffv3-7h97-993q](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-ffv3-7h97-993q) — TUS uploads ignore the declared Upload-Length, allowing disk exhaustion by authenticated users | medium | <= 2.63.18 | 2.63.19 | Limit pojedynczego PATCH widoczny; osobne ryzyko konkurencji GHSA-4r8p-gqj2-mwgm. |
| [GHSA-fmm7-x4gx-8jhr](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-fmm7-x4gx-8jhr) — Out-of-scope file deletion by a Create-only scoped user via symlink-following RemoveAll in upload failure-cleanup | high | <= 2.63.15 | 2.63.16 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-ffx7-75gc-jg7c](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-ffx7-75gc-jg7c) — TUS Negative Upload-Length Fires Post-Upload Hooks Prematurely | medium | <= 2.61.2 | nie wskazano | Kod odrzuca ujemny Upload-Length; brak wersji poprawionej w metadanych nie przesądza aktualności. Osobno: efekt przed walidacją (02-findings). |
| [GHSA-79pf-vx4x-7jmm](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-79pf-vx4x-7jmm) — TUS Delete Endpoint Bypasses Delete Permission Check | critical | <= 2.61.0 | 2.61.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |

## Udostępnianie

Jawna semantyka zasobu i odwołania; testy IDOR, rename/delete/replace, haseł i zmiany praw.

| Advisory | Waga | Zakres wg upstreamu | Poprawka wg upstreamu | Lokalna ocena |
|---|---|---|---|---|
| [GHSA-r6pg-pg54-rcr5](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-r6pg-pg54-rcr5) — Deleting another user's shared file leaves their public share link behind | low | >= 2.63.6, <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-m8v4-4w34-rrvf](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-m8v4-4w34-rrvf) — Renaming a shared file leaves the public share link behind | low | <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-833g-cqhp-h72j](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-833g-cqhp-h72j) — Share API exposes the password hash and bypass token | low | <= 2.63.16 | 2.63.17 | Odpowiednia ochrona widoczna w kodzie; nie uruchomiono testów. |
| [GHSA-pp88-jhwj-5qh5](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-pp88-jhwj-5qh5) — Trailing-slash delete leaves a stale public share behind | low | <= 2.63.16 | 2.63.17 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-3q2p-72cj-682c](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-3q2p-72cj-682c) — Improper Access Control Occurs via Pre-Created Public Share for a Non-existent Path | high | <= 2.63.6 | 2.63.7 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-5ww9-jg6q-38r7](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-5ww9-jg6q-38r7) — Cross-user unauthorized share-link deletion via unbounded prefix match in DeleteWithPathPrefix | high | <= 2.63.5 | 2.63.6 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-j9jx-hp4c-ghhh](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-j9jx-hp4c-ghhh) — Incorrect access control in public directory shares via rule path rebasing | high | <= 2.63.5 | 2.63.6 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-v9w4-gm2x-6rvf](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-v9w4-gm2x-6rvf) — Share links remain accessible after Share/Download permissions are revoked | high | <= 2.62.2 | 2.63.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-68j5-4m99-w9w9](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-68j5-4m99-w9w9) — Authorization Policy Bypass in Public Share Download Flow | medium | <=2.61.0 | 2.62.0 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-mr74-928f-rw69](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-mr74-928f-rw69) — Path Traversal in Public Share Links Exposes Files Outside Shared Directory | high | <= 2.60.0 | 2.61.0 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-6cqf-cfhv-659g](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-6cqf-cfhv-659g) — Insecure Direct Object Reference (IDOR) in Share Deletion Function | high | <= 2.45.0 | 2.45.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-3v48-283x-f2w4](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-3v48-283x-f2w4) — Password Protection of Links Bypassable | low | 2.32.0 | nie wskazano | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |

## Konta i sesje

Sesje serwerowe, bezpieczny provisioning i jawne zaufanie proxy; testy odwołania, CSRF, rejestracji i kosztu logowania.

| Advisory | Waga | Zakres wg upstreamu | Poprawka wg upstreamu | Lokalna ocena |
|---|---|---|---|---|
| [GHSA-v3jv-rmh2-635j](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-v3jv-rmh2-635j) — Expired JWTs accepted under proxy auth with a custom logout page | medium | >= 2.50.0, <= 2.63.21 | v2.63.22 | Odpowiednia ochrona widoczna w kodzie; nie uruchomiono testów. |
| [GHSA-576v-w77m-gr84](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-576v-w77m-gr84) — Case-folded signup usernames share one home directory on case-insensitive filesystems | high | <= 2.63.18 | 2.63.19 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-j7jh-37pf-mf8h](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-j7jh-37pf-mf8h) — Proxy and hook auto-provisioning ignore createUserDir and grant the server root scope | high | <= 2.63.19 | 2.63.20 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-7rc3-g7h6-22m7](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-7rc3-g7h6-22m7) — Colliding username normalization gives two users the same home directory | high | <= 2.63.16 | 2.63.17 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-6759-996p-gpj6](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-6759-996p-gpj6) — Self-signup users inherit the server root as their scope | critical | <= 2.63.16 | nie wskazano | Ryzyko zależne od konfiguracji; przejrzano mechanizm (02-findings), bez PoC. |
| [GHSA-v7vv-5wj2-gfcj](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-v7vv-5wj2-gfcj) — Password reset does not invalidate existing JWT sessions | medium | 2.x | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-7526-j432-6ppp](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-7526-j432-6ppp) — Proxy auth auto-provisioned users inherit Execute permission and Commands | medium | <= 2.62.2 | 2.63.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-x8jc-jvqm-pm3f](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-x8jc-jvqm-pm3f) — Signup Grants Execution Permissions When Default Permissions Includes Execution | high | <= 2.62.1 | 2.62.2 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-5gg9-5g7w-hm73](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-5gg9-5g7w-hm73) — Signup Grants Admin When Default Permissions Include Admin | critical | <= 2.61.2 | 2.62.0 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-xqp3-jq6g-x3qm](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-xqp3-jq6g-x3qm) — Authentication Bypass via Proxy Auth Header Forgery | high | 2.x | nie wskazano | Ryzyko zależne od konfiguracji; przejrzano mechanizm (02-findings), bez PoC. |
| [GHSA-hxw8-4h9j-hq2r](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-hxw8-4h9j-hq2r) — Authentication Bypass in User Password Update | medium | <= 2.57.0 | 2.57.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-43mm-m3h2-3prc](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-43mm-m3h2-3prc) — Username Enumeration via Timing Attack in /api/login | medium | < 2.54.0 | 2.55.0 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-w5fm-68j4-fpc4](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-w5fm-68j4-fpc4) — DoS Vulnerability on Public Login API | medium | <= 2.63.5 | 2.63.6 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-7xwp-2cpp-p8r7](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-7xwp-2cpp-p8r7) — Insecure JWT Handling Allows Session Replay Attacks after Logout | high | 2.39.0 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-rmwh-g367-mj4x](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-rmwh-g367-mj4x) — Sensitive Data Transferred in URL | medium | <= 2.33.0 | >= 2.33.9 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-cm2r-rg7r-p7gg](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-cm2r-rg7r-p7gg) — Insecure Password Handling | medium | <= 2.34.0 | 2.34.1 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |

## Polecenia i hooki

Rekomendacja wyłączenia z zakresu rdzenia; przy powrocie funkcji osobna izolacja i model uprawnień.

| Advisory | Waga | Zakres wg upstreamu | Poprawka wg upstreamu | Lokalna ocena |
|---|---|---|---|---|
| [GHSA-39cx-23x9-5c8p](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-39cx-23x9-5c8p) — Command WebSocket buffers unbounded messages before checking permissions | medium | <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-8c9q-7855-wfxq](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-8c9q-7855-wfxq) — Command Execution Allowlist Bypass via Shell Metacharacter Injection | high | 2.x | nie wskazano | Ryzyko zależne od konfiguracji; przejrzano mechanizm (02-findings), bez PoC. |
| [GHSA-jvpw-637p-h3pw](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-jvpw-637p-h3pw) — Command Injection via Hook Runner | high | >= 2.x | nie wskazano | Ryzyko zależne od konfiguracji; przejrzano mechanizm (02-findings), bez PoC. |
| [GHSA-m93h-4hw7-5qcm](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-m93h-4hw7-5qcm) — Command Injection via Authentication Hook Shell Substitution (Pre-Authentication RCE) | critical | <= 2.63.5 | 2.63.6 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-3q2w-42mv-cph4](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-3q2w-42mv-cph4) — Shell Commands Can Spawn Other Commands | high | 2.x | nie wskazano | Ryzyko zależne od konfiguracji; przejrzano mechanizm (02-findings), bez PoC. |
| [GHSA-hc8f-m8g5-8362](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-hc8f-m8g5-8362) — Command Execution not Limited to Scope | high | 2.x | nie wskazano | Ryzyko zależne od konfiguracji; przejrzano mechanizm (02-findings), bez PoC. |
| [GHSA-w7qc-6grj-w7r8](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-w7qc-6grj-w7r8) — Command Execution Allowlist Bypass | high | <= 2.33.8 | >= 2.33.10 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |

## Podglądy i zasoby

Limity wejścia/wyjścia i równoległości, izolacja aktywnej treści; testy XSS, parserów i przeciążenia.

| Advisory | Waga | Zakres wg upstreamu | Poprawka wg upstreamu | Lokalna ocena |
|---|---|---|---|---|
| [GHSA-448h-jr2h-3vhp](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-448h-jr2h-3vhp) — Subtitle conversion reads whole files into memory, allowing memory exhaustion | high | <= 2.63.23 | nie wskazano | Mechanizm obecny statycznie; PoC nie wykonano (02-findings). |
| [GHSA-xfqj-3vmx-63wv](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-xfqj-3vmx-63wv) — Stored Cross-Site Scripting via text/template branding injection | medium | <= 2.62.1 | 2.62.2 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-5vpr-4fgw-f69h](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-5vpr-4fgw-f69h) — Stored Cross-Site Scripting via crafted EPUB file | high | <= 2.62.1 | 2.62.2 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-7xqm-7738-642x](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-7xqm-7738-642x) — Uncontrolled Memory Consumption Due to Oversized File Processing | high | 2.38.0 | nie wskazano | Kod ma limit 10 MiB klasyfikacji tekstu; pozostałe ścieżki i zmiana rozmiaru wymagają weryfikacji. |
| [GHSA-4wx8-5gm2-2j97](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-4wx8-5gm2-2j97) — Stored Cross-Site Scripting | high | <= 2.33.6 | 2.33.7 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |

## Zależności i wdrożenie

Kontrola zależności i uprawnień OS, testy artefaktu i konfiguracji wydania.

| Advisory | Waga | Zakres wg upstreamu | Poprawka wg upstreamu | Lokalna ocena |
|---|---|---|---|---|
| [GHSA-6jqf-mv7m-3q7p](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-6jqf-mv7m-3q7p) — Risk of HTTP Request/Response smuggling through vulnerable dependency | critical | <= 2.45.1 | 2.45.2 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
| [GHSA-jj2r-455p-5gvf](https://github.com/filebrowser/filebrowser/security/advisories/GHSA-jj2r-455p-5gvf) — Insecure File Permissions | medium | <= 2.33.6 | 2.33.7 | Do szczegółowej weryfikacji kodu i konfiguracji; zinwentaryzowano advisory. |
