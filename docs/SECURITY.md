# Bezpieczeństwo Filedeck — mapowanie advisory File Browser

Stan na 2026-09-28. Źródło listy: [rejestr 62 advisory](analysis/05-advisories.md) (GitHub Security Advisories projektu File Browser). Dla każdego zgłoszenia: klasa problemu, jak Filedeck ją eliminuje i czym to jest sprawdzone.

Statusy:
- ✅ **zaadresowane** — mechanizm w kodzie i test automatyczny (nazwy testów w kolumnie „Dowód”);
- 🚫 **nie dotyczy** — Filedeck nie ma tej funkcji; przy jej dodaniu wpis wraca do analizy;
- ⚠️ **częściowo** — ograniczenie ryzyka bez pełnego dowodu.

**Podsumowanie: 38 ✅, 23 🚫, 1 ⚠️.**

## Ścieżki i autoryzacja

| Advisory | Klasa | Status | Mechanizm | Dowód |
|---|---|---|---|---|
| GHSA-7w29-q235-57m9 | symlink omija reguły | ✅ | `openat2` z `RESOLVE_BENEATH\|NO_SYMLINKS\|NO_XDEV`; symlinki nigdy nie są rozwiązywane; brak reguł deny do obejścia | `TestReadRejectsSymlinksAndFIFO`, `TestConcurrentDirectorySymlinkSwapCannotReadOutside` |
| GHSA-8q5j-8wcr-8v2v | FIFO blokuje pobieranie | ✅ | typ sprawdzany przez `O_PATH` przed otwarciem danych; kopia pomija pliki specjalne; brak archiwów | `TestReadRejectsSymlinksAndFIFO`, `TestCopyTreeBetweenSpaces` |
| GHSA-77x8-73f4-5485 | operacje rekurencyjne ignorują reguły potomne | ✅ | uprawnienia są per przestrzeń, bez reguł dla podścieżek — nie ma czego ominąć | `TestPermissionsArePerSpace`, `TestRenameRequiresModify` |
| GHSA-7whw-q6gh-xr59 | ujawnienie sumy kontrolnej bez prawa pobrania | ✅ | brak endpointu sum; każdy odczyt treści (`content`, `text`, `preview`) wymaga Read | `TestAdvisoryRegressions` |
| GHSA-fgm5-pw99-w2p7 | warianty wielkości liter i `\` | ✅ | `\` odrzucany; brak normalizacji ścieżek; `.filedeck` chroniony bez względu na wielkość liter i po inode | `TestPathContract`, `TestMetadataDirectoryIsHiddenAndUnreachable`, `TestAdvisoryRegressions` |
| GHSA-83xp-526h-j3ww | zip-slip w archiwach | 🚫 | brak budowania archiwów; kopia tworzy wpisy względem deskryptorów z nazw z `readdir` | — |
| GHSA-8wc8-hf36-mjh9 | zapis przez wiszący symlink | ✅ | każde utworzenie: `O_EXCL\|O_NOFOLLOW` lub `renameat2(RENAME_NOREPLACE)` — symlink to istniejący wpis → konflikt | `TestPublishNeverOverwritesAnyExistingEntry`, `TestRenameNeverReplacesOrEscapes` |
| GHSA-gxjx-7m74-hcq8 | traversal w zip/tar przez `\` | 🚫 | brak archiwów | — |
| GHSA-239w-m3h6-ch8v | katalogi-symlinki poza zakresem | ✅ | jak GHSA-7w29; zmiana nazwy i kosz nie przechodzą przez symlinki; purge nie podąża za symlinkami | `TestRenameNeverReplacesOrEscapes`, `TestTrashRestoreAndPurge` |
| GHSA-67cg-cpj7-qgc9 | treść pliku tekstowego bez prawa pobrania | ✅ | `/api/text` wymaga Read | `TestAdvisoryRegressions`, `TestPreviewAndTextEditorOverHTTP` |
| GHSA-5q48-q4fm-g3m6 | `HasPrefix` bez separatora | ✅ | autoryzacja nie porównuje prefiksów ścieżek (przestrzeń = osobny uchwyt); jedyne porównanie (state vs przestrzeń) używa separatora | `TestStateIsPrivateLockedAndDisjoint` |
| GHSA-9f3r-2vgw-m8xp | traversal w celu kopiowania/zmiany nazwy | ✅ | `ValidUserPath` dla źródła i celu przed wykonaniem pracy | `TestAdvisoryRegressions`, `TestCopyAndMoveBetweenSpaces` |
| GHSA-4mh3-h929-w968 | wiele ukośników w URL | ✅ | ścieżka tylko w parametrze `path`, dekodowana raz; absolutne i z `//` odrzucane | `TestAdvisoryRegressions`, `TestPathDecodingAndCookieAmbiguity` |

## Upload i spójność zapisu

| Advisory | Klasa | Status | Mechanizm | Dowód |
|---|---|---|---|---|
| GHSA-c4fr-5f24-4wrj | cleanup kasuje katalogi | ✅ | cleanup usuwa wyłącznie własną, rozpoznaną nazwę stagingu; nigdy celu | `TestFailedUploadCannotDeleteOrTruncateDestination`, `TestRecoveryOnlyDeletesOwnedStagingNames` |
| GHSA-4r8p-gqj2-mwgm | równoległe fragmenty ponad długość | ✅ | blokada uploadu, kontrola offsetu i limitu pod nią | `TestConcurrentPatchAtSameOffset` |
| GHSA-m9f5-2232-frp6 | usuwanie przez symlink w cache | ✅ | staging w prywatnym `.filedeck/staging`; symlink/hardlink zatrzymuje recovery | `TestRecoveryRefusesSymlinkInsteadOfFollowingIt` |
| GHSA-ffv3-7h97-993q | ignorowanie zadeklarowanej długości | ✅ | limit pliku, fragmentu, rezerwacji; rezerwa wolnego miejsca | `TestFailedAndOversizedChunksRollBack`, `TestChunkCapIndependentOfFileSize`, `TestFreeSpaceReserve` |
| GHSA-fmm7-x4gx-8jhr | `RemoveAll` przez symlink | ✅ | jak GHSA-c4fr; trwałe usuwanie tylko w koszu, bez podążania za symlinkami | `TestTrashRestoreAndPurge` |
| GHSA-ffx7-75gc-jg7c | ujemna długość | ✅ | rozmiar < 0 → błąd; brak hooków | `TestFailedUploadCannotDeleteOrTruncateDestination` |
| GHSA-79pf-vx4x-7jmm | usunięcie uploadu bez prawa | ✅ | anulowanie tylko własnego uploadu, tylko jego stagingu | `TestOwnershipAndRevocation`, `TestUploadIDsRemainPrivateBetweenWriters` |

## Udostępnianie

GHSA-r6pg-pg54-rcr5, GHSA-m8v4-4w34-rrvf, GHSA-833g-cqhp-h72j, GHSA-pp88-jhwj-5qh5, GHSA-3q2p-72cj-682c, GHSA-5ww9-jg6q-38r7, GHSA-j9jx-hp4c-ghhh, GHSA-v9w4-gm2x-6rvf, GHSA-68j5-4m99-w9w9, GHSA-mr74-928f-rw69, GHSA-6cqf-cfhv-659g, GHSA-3v48-283x-f2w4 — **🚫 nie dotyczy (12)**: Filedeck nie ma publicznych linków. Wymagania na przyszłość wynikające z tych zgłoszeń: link wskazuje obiekt, nie prefiks ścieżki; odebranie praw lub usunięcie/zmiana nazwy unieważnia link; hasło linku nigdy nie wraca w API; brak tworzenia linków do nieistniejących ścieżek; własność linku sprawdzana przy każdej operacji.

## Konta i sesje

| Advisory | Klasa | Status | Mechanizm | Dowód |
|---|---|---|---|---|
| GHSA-v3jv-rmh2-635j | wygasłe JWT przy proxy auth | ✅ | brak JWT i proxy auth; sesje serwerowe z wygaśnięciem bezczynności i bezwzględnym | `TestIdleAndAbsoluteExpiry`, `TestProxyBoundaryAndForwardedHeadersIgnored` |
| GHSA-576v-w77m-gr84 | kolizja nazw przez wielkość liter | ✅ | nazwy tylko `[a-z0-9._-]`, bez normalizacji; brak katalogów domowych z nazw | `TestUsernamesCannotCollideByCaseOrUnicode` |
| GHSA-j7jh-37pf-mf8h | auto-provisioning z zakresem root | ✅ | brak auto-provisioningu; nowe konto bez grantów nie widzi żadnej przestrzeni | `TestPermissionsArePerSpace` |
| GHSA-7rc3-g7h6-22m7 | kolizja po normalizacji nazw | ✅ | jak GHSA-576v | `TestUsernamesCannotCollideByCaseOrUnicode` |
| GHSA-6759-996p-gpj6 | samorejestracja z zakresem root | ✅ | brak samorejestracji; dostęp tylko przez jawne granty | `TestFirstRunSetupCodeCreatesAdministratorOnce`, `TestPermissionsArePerSpace` |
| GHSA-v7vv-5wj2-gfcj | reset hasła nie unieważnia sesji | ✅ | wersja konta w sesji; zmiana hasła/praw/blokada usuwa sesje | `TestPasswordChangeRevokesAllSessions`, `TestAdministrativeResetAndPermissionsInvalidateOldCookies` |
| GHSA-7526-j432-6ppp | proxy auth + prawo Execute | 🚫 | brak proxy auth i wykonywania poleceń | `TestNoProcessExecutionOrPlugins` |
| GHSA-x8jc-jvqm-pm3f | signup nadaje Execute | 🚫 | brak signup i poleceń | `TestNoProcessExecutionOrPlugins` |
| GHSA-5gg9-5g7w-hm73 | signup nadaje admina | ✅ | administrator tylko przez bootstrap/kod konfiguracyjny albo panel z reauth | `TestAdminRequiresRoleAndReauthentication`, `TestFirstRunSetupCodeCreatesAdministratorOnce` |
| GHSA-xqp3-jq6g-x3qm | podrobiony nagłówek proxy auth | ✅ | nagłówki tożsamości i `X-Forwarded-*` ignorowane | `TestLoginCookieLogoutAndReplayedSession`, `TestProxyBoundaryAndForwardedHeadersIgnored` |
| GHSA-hxw8-4h9j-hq2r | zmiana hasła bez obecnego | ✅ | wymaga obecnego hasła, wersjonowana | `TestPasswordChangeRevokesAllSessions` |
| GHSA-43mm-m3h2-3prc | enumeracja nazw przez czas | ⚠️ | hash atrapa dla nieistniejącego konta; brak pomiaru rozkładu czasów | — |
| GHSA-w5fm-68j4-fpc4 | DoS logowania | ✅ | limit prób per adres i globalny, bramka 2 obliczeń Argon2 | `TestRateLimitAndBoundedBookkeeping`, `TestLoginHasBoundedSessionsAndWork` |
| GHSA-7xwp-2cpp-p8r7 | replay po wylogowaniu | ✅ | sesja usuwana po stronie serwera | `TestLoginCookieLogoutAndReplayedSession` |
| GHSA-rmwh-g367-mj4x | dane wrażliwe w URL | ✅ | token tylko w cookie `HttpOnly`, CSRF w nagłówku, hasła w treści JSON; URL zawierają jedynie przestrzeń i ścieżkę | przegląd kodu (`internal/api`, `app.js`) |
| GHSA-cm2r-rg7r-p7gg | niebezpieczne hasła | ✅ | Argon2id z solą, porównanie w stałym czasie, min. 12 znaków | `TestBootstrapPersistenceAndSecretStorage` |

## Polecenia i hooki

GHSA-39cx-23x9-5c8p, GHSA-8c9q-7855-wfxq, GHSA-jvpw-637p-h3pw, GHSA-m93h-4hw7-5qcm, GHSA-3q2w-42mv-cph4, GHSA-hc8f-m8g5-8362, GHSA-w7qc-6grj-w7r8 — **🚫 nie dotyczy (7)**: Filedeck nie uruchamia programów, nie ma hooków ani WebSocketów. `TestNoProcessExecutionOrPlugins` pilnuje, by kod nie importował `os/exec`, `plugin`, CGI/FastCGI ani silników szablonów i nie wywoływał `Exec`/`ForkExec`.

## Podglądy i zasoby

| Advisory | Klasa | Status | Mechanizm | Dowód |
|---|---|---|---|---|
| GHSA-448h-jr2h-3vhp | konwersja napisów do pamięci | ✅ | brak konwersji; tekst max 2 MiB | `TestTextEditorRejectsUnsuitableFiles` |
| GHSA-xfqj-3vmx-63wv | XSS przez szablon brandingu | ✅ | brak szablonów i brandingu; interfejs to statyczne pliki | `TestNoProcessExecutionOrPlugins` |
| GHSA-5vpr-4fgw-f69h | XSS przez EPUB | ✅ | brak renderowania EPUB/HTML; podgląd tylko z listy typów, `nosniff`, CSP `sandbox` (SVG nie wykona skryptu nawet otwarty w karcie) | `TestPreviewAndTextEditorOverHTTP`, test przeglądarkowy (SVG ze skryptem) |
| GHSA-7xqm-7738-642x | pamięć przy dużych plikach | ✅ | JSON 16 KiB, tekst 2 MiB, maks. 4 buforowane zapisy, brak przetwarzania obrazów | `TestAdvisoryRegressions`, `TestJSONLimitsAndUnknownFields` |
| GHSA-4wx8-5gm2-2j97 | stored XSS | ✅ | CSP bez `unsafe-inline`, Trusted Types (`trusted-types 'none'`), nazwy wyłącznie przez `textContent` | `TestInterfaceHeadersAndFolders`, test przeglądarkowy (nazwa `<img onerror>`) |

## Zależności i wdrożenie

| Advisory | Klasa | Status | Mechanizm | Dowód |
|---|---|---|---|---|
| GHSA-6jqf-mv7m-3q7p | request smuggling w zależności | ✅ | HTTP wyłącznie z biblioteki standardowej Go; `govulncheck`: kod nie wywołuje żadnej znanej podatności | `govulncheck` v1.8.0 |
| GHSA-jj2r-455p-5gvf | niebezpieczne prawa plików | ✅ | state `0700`, bazy `0600`, `.filedeck` `0700`, staging `0600`, tryby nowych plików konfigurowalne; obraz bez powłoki, `read_only`, `cap_drop: ALL` | `TestStateIsPrivateLockedAndDisjoint`, `TestMkdirUsesConfiguredMode`, `TestUnsafeMetadataDirectoryIsRejected` |

## Dodatkowe ustalenia z przeglądu (etap 6)

Przegląd nowych funkcji (podgląd, edytor, kopiowanie) znalazł i naprawił:

1. **Pamięć przy zapisie edytora** — zapis buforuje do ~12 MiB przed blokadą; przy 64 równoległych żądaniach ~770 MiB. Teraz osobny limit 4 równoległych zapisów (HTTP 429).
2. **Zapełnienie dysku** — kopia do 256 GiB, uploady i zapisy mogły zająć cały dysk (także pod bazą stanu). Teraz każda z tych operacji zostawia co najmniej 512 MiB wolnego (`MinFreeBytes`, HTTP 507 `no_space`).
3. **Cele w `.filedeck`** — upload/kopia do zarezerwowanej ścieżki były odrzucane dopiero przy publikacji (bez skutków, ale z marnowaną pracą). Teraz `ValidUserPath` odrzuca je na starcie.

Narzędzia: `go vet`, `staticcheck` (czysto), `govulncheck` (0 wywołanych podatności; moduł `x/crypto/openpgp` zgłaszany, nieużywany), race detector, fuzzing ścieżek, test przeglądarkowy Chromium bez naruszeń CSP. `gosec` nie działa z Go 1.27 (błąd wewnętrzny w starszej wersji, brak pamięci przy kompilacji najnowszej).

## Otwarte ryzyka

- Enumeracja kont przez czas odpowiedzi — tylko ograniczenie (GHSA-43mm).
- Zmiany źródła w trakcie kopiowania/przenoszenia między przestrzeniami nie są snapshotem; przy przenoszeniu źródło trafia do kosza, więc nic nie ginie.
- Zadanie kopiowania należy do konta, nie sesji: wylogowanie nie przerywa kopii (blokada konta i odebranie praw — tak).
- PDF w podglądzie działa bez `sandbox` (przeglądarki nie renderują PDF w piaskownicy); `nosniff` i wymuszony `application/pdf` uniemożliwiają potraktowanie go jako HTML.
- Nie testowano na prawdziwym udziale SMB/NFS.
