# Stan realizacji

2026-09-28 — etap 1: prototyp rdzenia; etap 2: konta, sesje i API HTTP; etap 3: trwałe i wznawialne uploady; etap 4: interfejs WWW i wdrożenie Docker Compose; etap 5: przestrzenie, uprawnienia per przestrzeń, zmiana nazwy i kosz; etap 6: podgląd, edytor, kopiowanie/przenoszenie, drag & drop, motyw, przegląd bezpieczeństwa; etap 7: EN/PL, powiadomienia, zaznaczanie, wysyłanie folderów; etap 8: poprawka reauth, selftest udziałów, sortowanie, wyszukiwanie.

## Etap 1 — rdzeń

Nowy moduł Go, Linuxowy storage, polityka dostępu, cykl uploadu w pamięci, lokalne CLI, obraz Docker oraz konfiguracja CI dla przyszłego repo Filedeck. Repo `reference` pozostaje materiałem porównawczym i nie było modyfikowane.

| Kontrakt | Główne testy |
|---|---|
| Granica ścieżki i zakaz symlinków | `TestPathContract`, `TestReadRejectsSymlinksAndFIFO`, `TestConcurrentDirectorySymlinkSwapCannotReadOutside`, `FuzzValidPath` |
| Ograniczone listowanie | `TestListIsBoundedAndOmitsSpecialEntries` |
| Prywatny state i jedna instancja na state | `TestOpenRejectsUnsafeStateAndConcurrentInstance` |
| Bezpieczne odzyskanie po restarcie | `TestRecoveryOnlyDeletesOwnedStagingNames`, `TestRecoveryRefusesSymlinkInsteadOfFollowingIt` |
| Brak utraty istniejących danych | `TestPublishNeverOverwritesAnyExistingEntry`, `TestFailedUploadCannotDeleteOrTruncateDestination` |
| Atomowa publikacja i konflikty | `TestUploadPublishesOnlyAfterCompletion`, `TestConcurrentCommitsNeverOverwrite` |
| Serializacja fragmentów | `TestConcurrentPatchAtSameOffset` |
| Limity, błędy i anulowanie | `TestFailedAndOversizedChunksRollBack`, `TestChunkCapIndependentOfFileSize`, `TestCancellationRollsBack`, `TestQuotasExpiryAndAbort` |
| Właściciel i odwołanie dostępu | `TestOwnershipAndRevocation` |
| Kontrola przed publikacją | `TestCommitChecksActualStagingLength`, `TestExpiredCommitCleansOnlyStaging`, `TestCommitRejectsSymlinkParentAndAllowsEmptyFile` |
| CLI | `TestOperatorCLI` |

Wykonano testy Go, `go vet`, testy z race detectorem w obrazie Go, fuzzing ścieżek (181880 wykonań w sesji 10 s) i `govulncheck` v1.8.0 — bez wykrytych podatności w skanowanym kodzie. Race detector dotyczy wyścigów pamięci; osobny test zamienia katalog z symlinkiem podczas odczytów. Żaden pojedynczy test nie dowodzi kompletnej odporności na wszystkie wyścigi filesystemu.

Zbudowano obraz `filedeck:prototype` i wykonano test CLI w kontenerze bez sieci, capabilities i zapisu do warstwy obrazu: upload, odczyt, listowanie, zachowanie danych przy konflikcie, opróżnienie staging oraz odmowa konfiguracji z osobnymi bind mountami root/state. Workflow CI jest przygotowany, ale nie był uruchamiany w GitHub Actions.

## Co te testy adresują w rejestrze GHSA

- Cleanup uploadu nie otrzymuje ścieżki celu: klasa GHSA-c4fr-5f24-4wrj i GHSA-fmm7-x4gx-8jhr.
- Fragmenty mają limit i serializację: klasy GHSA-ffv3-7h97-993q i GHSA-4r8p-gqj2-mwgm. Nie jest to jeszcze implementacja protokołu TUS.
- Symlinki nie są dozwoloną drogą dostępu: klasy GHSA-239w-m3h6-ch8v, GHSA-8wc8-hf36-mjh9 i GHSA-7w29-q235-57m9, w granicach jawnie opisanego modelu uchwytów.
- Odczyt odrzuca FIFO przed otwarciem danych. Archiwizacja, z której pochodzi GHSA-8q5j-8wcr-8v2v, nie jest jeszcze implementowana i będzie wymagać własnych testów.

Nie oznaczamy wszystkich 62 advisory jako zamkniętych. Brak sesji, udziałów czy rendererów w prototypie nie jest dowodem bezpieczeństwa ich przyszłych implementacji.

## Etap 2 — konta, sesje i API HTTP

Pakiety `internal/identity` (konta, hasła Argon2id, sesje serwerowe w bbolt) i `internal/api` (adapter HTTP), polecenia `bootstrap`, `reset-password` i `serve`. Szczegóły: sekcja „Konta, sesje i API HTTP” w [CONTRACT.md](CONTRACT.md).

| Kontrakt | Główne testy |
|---|---|
| Bootstrap tylko raz, hash hasła i sesji zamiast sekretów w bazie, trwałość po restarcie | `TestBootstrapPersistenceAndSecretStorage`, `TestHTTPSTransportAndRestartedIdentityStore` |
| Zmiana/reset hasła, blokada i zmiana praw unieważniają sesje | `TestPasswordChangeRevokesAllSessions`, `TestPasswordChangeAndAdminDisableRevokeSessions`, `TestAdministrativeResetAndPermissionsInvalidateOldCookies` |
| Ostatni administrator, rola i reauth | `TestAccountDisablePermissionsAndLastAdministrator`, `TestAdminRequiresRoleAndReauthentication` |
| Wygaśnięcie i ograniczenie sesji oraz pracy logowania | `TestIdleAndAbsoluteExpiry`, `TestLoginHasBoundedSessionsAndWork` |
| Prywatna baza, odrzucenie symlinku i praw publicznych | `TestDatabaseSymlinkAndPublicPermissionsRejected` |
| Cookie, wylogowanie, odrzucenie odtworzonej sesji | `TestLoginCookieLogoutAndReplayedSession` |
| CSRF, `Origin`, `Host`, `Sec-Fetch-Site` | `TestCSRFOriginAndHostBoundaries` |
| Limity i ścisłość JSON, jedno dekodowanie ścieżki, niejednoznaczne cookie | `TestJSONLimitsAndUnknownFields`, `TestPathDecodingAndCookieAmbiguity` |
| Upload/pobieranie/Range/konflikt przez HTTP | `TestUploadDownloadRangeAndConflict` |
| Proxy i ignorowanie nagłówków forwarded, limit logowań | `TestProxyBoundaryAndForwardedHeadersIgnored`, `TestLoginRateCannotUseForwardedIPToBypassLimit`, `TestRateLimitAndBoundedBookkeeping` |
| Wylogowanie w trakcie uploadu blokuje publikację; upload ID prywatne | `TestLogoutDuringUploadPreventsPublication`, `TestUploadIDsRemainPrivateBetweenWriters` |

Weryfikacja: `go test` i `go vet` lokalnie; `go test -race` w obrazie `golang:1.27.1-bookworm` bez sieci — wszystkie pakiety przechodzą. `govulncheck` v1.8.0: kod nie wywołuje żadnej znanej podatności; na poziomie modułu zgłoszono GO-2026-5932 (`x/crypto/openpgp`, bez poprawki), pakietu, którego Filedeck nie importuje — z `x/crypto` używany jest tylko `argon2`. Test end-to-end zbudowanej binarki w trybie `-insecure-local`: bootstrap, złe hasło → 401, brak CSRF → 403, brak `Origin` → 403, upload/commit/listowanie/pobieranie z `Content-Disposition: attachment` i CSP, traversal `../state/identity.db` → 400, wylogowanie, odtworzone cookie → 401, obcy `Host` → 421. Nie testowano jeszcze serwera w kontenerze z TLS ani za rzeczywistym reverse proxy.

### Co etap 2 adresuje w rejestrze GHSA

Mechanizmy i testy tego etapu odpowiadają klasom z sekcji „Konta i sesje” [rejestru advisory](analysis/05-advisories.md):

- Sesje serwerowe z odwołaniem: GHSA-7xwp-2cpp-p8r7 (replay po wylogowaniu), GHSA-v7vv-5wj2-gfcj (reset hasła nie unieważnia sesji), GHSA-v3jv-rmh2-635j (wygasłe JWT przy proxy auth).
- Brak auth nagłówkiem proxy i brak auto-provisioningu: GHSA-xqp3-jq6g-x3qm, GHSA-j7jh-37pf-mf8h, GHSA-7526-j432-6ppp.
- Brak samorejestracji, jawne prawa przy tworzeniu kont, bez prawa wykonywania poleceń: GHSA-6759-996p-gpj6, GHSA-5gg9-5g7w-hm73, GHSA-x8jc-jvqm-pm3f, GHSA-576v-w77m-gr84 (nazwy tylko małymi literami, brak katalogów domowych z nazwy).
- Zmiana hasła wymaga obecnego hasła i jest wersjonowana: GHSA-hxw8-4h9j-hq2r, GHSA-cm2r-rg7r-p7gg.
- Hash atrapa dla nieistniejącego konta: GHSA-43mm-m3h2-3prc (ograniczenie, nie formalny dowód braku kanału czasowego).
- Limit logowań i bramka obliczeń Argon2: GHSA-w5fm-68j4-fpc4.

Wpisy pozostają w rejestrze otwarte do czasu dopisania w nim odnośników do testów; część (np. timing) wymaga osobnego pomiaru.

## Znane kompromisy i otwarte punkty

- Każde uwierzytelnione żądanie zapisuje `LastSeen` z fsync — prosty i spójny model odwołania, ale limit wydajności. Do rozważenia: zapis co N sekund.
- Obliczenie Argon2 przy reauth administratora odbywa się pod wyłączną blokadą `security` (krótko wstrzymuje commity innych użytkowników).
- `Begin` zapisuje rekord (fsync) pod globalną blokadą serwisu, a każdy fragment wymaga dwóch fsync — prostota i poprawność kosztem przepustowości przy wielu równoległych uploadach.
- Jeżeli po zapisaniu zmiany konta nie uda się zaktualizować polityki w pamięci, API zwraca błąd, a zmiana w bazie zostaje; polityka zostanie odtworzona z bazy po restarcie.
- Za reverse proxy limit logowań jest wspólny dla adresu proxy.

## Etap 3 — trwałe i wznawialne uploady

Rekordy uploadów w `uploads.db` w state, odtwarzanie po restarcie i awarii, idempotentny commit i status wyniku publikacji. `Close` zachowuje uploady; `put` w CLI anuluje własny nieudany upload. Szczegóły i tabela odtwarzania: sekcje „Cykl uploadu” i „Awaria procesu i zasoby” w [CONTRACT.md](CONTRACT.md).

| Kontrakt | Główne testy |
|---|---|
| Wznowienie po restarcie z zachowaniem właściciela i rezerwacji | `TestResumeAfterRestart`, `TestPatchIsDurableOnlyAfterRecord` |
| Bajty bez zapisanego offsetu są odcinane; brak potwierdzonych bajtów unieważnia upload | `TestRecoveryTruncatesUnacknowledgedBytes`, `TestRecoveryDropsUploadWithMissingAcknowledgedBytes` |
| Awaria w trakcie publikacji: przed i po `rename` | `TestCrashBeforeRenameRevertsToUploading`, `TestCrashAfterRenameIsReportedAsPublished` |
| Idempotentny commit, prywatność i wygasanie wyników, limit liczby wyników | `TestPublicationResultSurvivesRestartAndExpires`, `TestResultsAreBounded`, `TestUploadPublishesOnlyAfterCompletion` |
| Rekordy uszkodzone, wskazujące poza przestrzeń lub cudzy staging; osierocony i wygasły staging | `TestRecoveryDiscardsOrphansCorruptAndExpiredRecords` |
| Baza nie może być symlinkiem | `TestDatabaseSymlinkIsRejected` |
| HTTP: wznowienie po restarcie całego procesu (to samo cookie), podwójny commit, brak anulowania po publikacji | `TestUploadResumesAcrossRestartAndCommitIsIdempotent` |
| CLI nie zostawia stagingu po konflikcie | `TestOperatorCLI` |

Weryfikacja: `go test`, `go vet`, `gofmt`; `go test -race -count=3` w `golang:1.27.1-bookworm` bez sieci; fuzzing ścieżek 10 s. Test end-to-end binarki: upload 5/10 B, **`kill -9` serwera**, restart, `GET` statusu tym samym cookie → offset 5, dokończenie, dwukrotny commit → za każdym razem `published: true, durability_confirmed: true`, status `published`, w state zostają tylko bazy. Awarie w środku operacji testowane są przez odtworzenie stanu dysku (dopisane bajty, rekord `publishing` z/bez stagingu) — nie przez faktyczne wyłączenie zasilania; to wymagałoby testów na maszynie wirtualnej z utratą cache dysku.

### Co etap 3 adresuje

Rejestr GHSA nie zawiera osobnych zgłoszeń o utracie danych po restarcie; etap zamyka natomiast ryzyka z analizy projektu: ponowienie żądania po zgubionej odpowiedzi nie powoduje drugiej publikacji ani konfliktu mylonego z błędem, awaria nie zostawia pliku częściowego pod nazwą docelową, a sprzątanie po restarcie nadal nie dotyka niczego poza rozpoznanym stagingiem bez rekordu (klasa GHSA-c4fr-5f24-4wrj / GHSA-fmm7-x4gx-8jhr zostaje zachowana).

## Etap 4 — interfejs WWW i Docker Compose

Pakiet `internal/web` (HTML/CSS/JS osadzone w binarce, bez frameworka i kroku budowania), endpoint `POST /api/folders`, limity uploadu w `/api/auth/me`, konfiguracja przez `FILEDECK_*`, tryb `-tls-self-signed`, `compose.yaml` + `.env.example`, obraz z gotowym układem `/data`. Szczegóły: sekcje „Interfejs WWW” i „Wdrożenie w Dockerze” w [CONTRACT.md](CONTRACT.md).

Interfejs: logowanie, nawigacja po folderach z okruszkami i adresem w `#/ścieżka`, pobieranie, tworzenie folderów, upload wielu plików (wybór lub przeciągnięcie) z postępem, wznawianiem i ponawianiem, zmiana hasła, panel administratora (tworzenie kont, uprawnienia, blokada, reset hasła — każda zmiana z ponownym podaniem hasła). Kontrolki zapisu są ukryte dla kont bez Create; serwer i tak odrzuca takie żądania.

| Kontrakt | Test |
|---|---|
| Nagłówki strony (CSP bez `unsafe`, Trusted Types), brak dostępu do plików spoza listy zasobów, cross-site tylko dla UI, limity w `/me`, foldery przez HTTP z CSRF i uprawnieniami | `TestInterfaceHeadersAndFolders` |
| `mkdirat` bez symlinków, bez tworzenia pośrednich katalogów, bez zastępowania, wymaga Create | `TestMkdirIsBoundedAndNeverReplaces` |
| Certyfikat self-signed: ponowne użycie, związanie z hostem/IP, tryb 0600, odmowa dla `http` | `TestSelfSignedCertificateIsReusedAndBoundToHost` |
| Przeglądarka end-to-end (Chromium) na kontenerze z compose | `test/ui/smoke.mjs` |

Weryfikacja: testy Go, `go vet`, `gofmt`, `go test -race -count=2` i fuzzing w `golang:1.27.1-bookworm`; `node --check` dla JS. Obraz `filedeck:local` ma 11,8 MB. Scenariusze Docker Compose na osobnych projektach (usunięte po teście):

- wolumen nazwany: start bez administratora kończy się instrukcją, `docker compose run --rm -T filedeck bootstrap admin`, start z certyfikatem self-signed, HTTPS, cookie `__Host-filedeck`, obcy `Host` → 421;
- bind mount istniejącego katalogu hosta z `FILEDECK_USER` = UID hosta: widoczne istniejące pliki, nowe tworzone z właścicielem z hosta, state 0600;
- Chromium (Playwright): złe hasło, logowanie, tworzenie folderów, upload 20 MiB w 3 fragmentach, plik o nazwie `<img src=x onerror=alert(1)>.txt` wyświetlony jako tekst (brak wstrzykniętego HTML i okna dialogowego), pobranie 20 971 520 B, konflikt bez nadpisania, utworzenie konta wymagające reauth, konto tylko do odczytu bez kontrolek zapisu, przeładowanie z zachowaniem sesji i folderu, wylogowanie — **zero błędów konsoli i naruszeń CSP**.

Nie testowano: Firefox/Safari, urządzenia mobilne, prawdziwy reverse proxy, wznowienie uploadu po przeładowaniu strony w przeglądarce (logika jest, test automatyczny obejmuje wznawianie na poziomie API).

**Poprawka po pierwszym wdrożeniu (2026-09-28):** pierwsze uruchomienie przez `docker compose run … bootstrap` okazało się kruche — kontener bez administratora restartował się w pętli i blokował state, a port domyślnie słuchał tylko na `127.0.0.1`. Dodano tryb konfiguracji z jednorazowym kodem w logach (`TestFirstRunSetupCodeCreatesAdministratorOnce`, test Chromium ekranu konfiguracji: zły kod odrzucony, poprawny tworzy konto i loguje, po przeładowaniu brak ekranu konfiguracji) oraz jaśniejszą instrukcję dostępu z LAN (`FILEDECK_BIND`, `FILEDECK_ORIGIN`, `https://`).

### Co etap 4 adresuje w rejestrze GHSA

Klasa XSS i aktywnej treści (sekcja „Limity wejścia/wyjścia… izolacja aktywnej treści”): interfejs nie ma podglądu ani renderowania plików, treść zawsze idzie jako załącznik z `sandbox`, a nazwy plików nie mogą stać się HTML dzięki Trusted Types. Wpisy pozostają otwarte w rejestrze do czasu dopisania odnośników.

## Etap 5 — przestrzenie, zmiana nazwy i kosz

Decyzje (2026-09-28): serwer sam montuje udziały, Filedeck dostaje katalogi przez mapowanie w compose; obok nich własna przestrzeń kontenera „Moje pliki”; usuwanie do kosza. Szczegóły: sekcje „Przestrzenie i katalog `.filedeck`” oraz „Zmiana nazwy i kosz” w [CONTRACT.md](CONTRACT.md).

Zmiana architektury: staging przeniesiony ze state do ukrytego `.filedeck/` każdej przestrzeni (publikacja i kosz muszą być jednym `rename` na tym samym systemie plików, a przestrzenie leżą na różnych mountach). Zniknął wymóg „state i pliki na jednym mouncie”. State zawiera tylko bazy i certyfikat. Dodano: wiele przestrzeni (`/files` + wykrywane `/spaces/*`), przestrzenie tylko do odczytu, uprawnienia per przestrzeń z `*`, prawo Modify, zmiana nazwy, kosz z przywracaniem i retencją, trwałe usuwanie tylko przez administratora, tryby nowych plików/folderów (`0640`/`0750`), `healthcheck` i `HEALTHCHECK` w obrazie, `compose.override.example.yaml`, log wykrytych przestrzeni, migracja kont i rekordów ze starszego formatu.

| Kontrakt | Test |
|---|---|
| `.filedeck` ukryty i nieosiągalny — nazwy, warianty wielkości liter, końcowe kropki, Kelvin sign, alias po inode | `TestPathContract`, `TestMetadataDirectoryIsHiddenAndUnreachable` |
| Odrzucenie `.filedeck` zapisywalnego dla wszystkich, będącego symlinkiem, root-symlinku, złych trybów | `TestUnsafeMetadataDirectoryIsRejected` |
| Przestrzeń tylko do odczytu | `TestReadOnlySpace`, `TestReadOnlySpaceAndStatePlacement` |
| State prywatny, blokowany, rozłączny z przestrzeniami, sprzątanie starego stagingu | `TestStateIsPrivateLockedAndDisjoint` |
| Tryby nowych folderów, prywatny `.filedeck` | `TestMkdirUsesConfiguredMode` |
| Zmiana nazwy bez nadpisywania, bez ucieczki przez symlink, bez wejścia do `.filedeck`, bez symlinków jako źródła | `TestRenameNeverReplacesOrEscapes`, `TestRenameRequiresModify` |
| Kosz, przywracanie bez nadpisywania, trwałe usuwanie bez podążania za symlinkiem | `TestTrashRestoreAndPurge`, `TestTrashLifecycle` |
| Odtwarzanie kosza po awarii (rekord bez elementu, element bez rekordu), retencja | `TestTrashRecoveryAndRetention` |
| Uprawnienia per przestrzeń, `*`, izolacja ścieżek, cofnięcie Create blokuje publikację | `TestPermissionsArePerSpace` |
| Migracja starego formatu uprawnień | `TestLegacyPermissionsBecomeAllSpacesGrant` |
| HTTP: przestrzenie, zmiana nazwy, kosz, purge tylko admin, walidacja grantów | `TestSpacesRenameAndTrashOverHTTP` |
| `/healthz` tylko z loopback; wykrywanie przestrzeni | `TestHealthzIsLoopbackOnly`, `TestDiscoverSpaces` |

Weryfikacja: testy Go, `go vet`, `gofmt`; `go test -race -count=3` i fuzzing w `golang:1.27.1-bookworm` (race detector wykrył i pozwolił naprawić niezsynchronizowany odczyt przestrzeni uploadu w `Patch`/`Status`). Compose z dwiema przestrzeniami z hosta (`nas` zapisywalna — katalog należący do UID kontenera, `archiwum` jako `:ro`) i Chromium: foldery, upload 20 MiB, nazwa-pułapka XSS, pobranie, konflikt, zmiana nazwy, kosz → przywrócenie, kosz → trwałe usunięcie, upload do `nas` (plik na hoście z trybem `0640`, `.filedeck` `0700`, niewidoczny w UI), `archiwum` ze znacznikiem i bez kontrolek zapisu, konto z grantami tylko do „Moje pliki” i `nas` (nie widzi `archiwum`, brak kontrolek zapisu i usuwania), healthcheck `healthy` — **zero błędów konsoli i CSP**. Test znalazł błąd UX (domyślnie otwierała się pierwsza alfabetycznie przestrzeń, tu tylko do odczytu) — poprawiony.

Nie testowano: prawdziwy udział SMB/CIFS i NFS, serwer Samba z nazwami 8.3, bardzo duże drzewa w koszu.

**Poprawka po wdrożeniu (2026-09-28):** zmiana `FILEDECK_USER` przy nowych wolumenach kończyła się `permission denied`, bo Docker kopiował do wolumenów katalogi z obrazu należące do UID 65532. Teraz obraz zawiera tylko puste punkty montowania `1777`, a Filedeck sam tworzy `state` i własną przestrzeń jako faktyczny UID (`TestFirstStartCreatesPrivateDirectories`; sprawdzone w compose dla UID 1001 i 65532).

### Co etap 5 adresuje w rejestrze GHSA

- Usuwanie bez uprawnień / przez błędy ścieżek: zmiana nazwy i kosz wymagają osobnego Modify, idą przez `RENAME_NOREPLACE` i te same kontrole ścieżki co reszta; trwałe usuwanie wyłącznie z kosza i tylko przez administratora (klasa GHSA-c4fr-5f24-4wrj / GHSA-fmm7-x4gx-8jhr rozszerzona na nowe operacje).
- Symlinki przy operacjach na drzewach: źródła-symlinki odrzucane, purge nie podąża za symlinkami ani nie przekracza montowań (klasy GHSA-239w-m3h6-ch8v, GHSA-8wc8-hf36-mjh9, GHSA-7w29-q235-57m9).
- Aliasowanie nazw na systemach niewrażliwych na wielkość liter (klasa GHSA-576v-w77m-gr84): katalog metadanych chroniony po nazwie z case-folding i po inode.
- Zakres użytkownika: brak „scope = root serwera”; każda przestrzeń wymaga jawnego grantu, nowa przestrzeń nie jest nikomu udostępniana poza `*` (klasa GHSA-6759-996p-gpj6, GHSA-j7jh-37pf-mf8h).

## Etap 6 — podgląd, edytor, kopiowanie, motyw i przegląd bezpieczeństwa

Zrealizowano: drag & drop na całe okno i na wiersz folderu (przeglądarka nie otwiera już upuszczonych plików); podgląd zdjęć, wideo, audio, PDF i tekstu z przechodzeniem ←/→; edytor plików tekstowych (Ctrl+S, znacznik niezapisanych zmian, konflikt wersji → „zapisz jako”, poprzednia wersja w koszu); „Nowy plik”; kopiowanie i przenoszenie między przestrzeniami w tle z postępem i anulowaniem; przełącznik motywu auto/jasny/ciemny z nową paletą jasną; ikony Tabler (offline). Szczegóły: sekcja „Podgląd, edytor i kopiowanie” w [CONTRACT.md](CONTRACT.md).

**Przegląd bezpieczeństwa:** wszystkie 62 advisory File Browser przypisane do statusu z dowodem — [SECURITY.md](SECURITY.md): 38 zaadresowanych z testami, 23 nie dotyczy (brak funkcji: udostępnianie, polecenia/hooki, archiwa), 1 częściowo (enumeracja kont przez czas). Znalezione i naprawione: limit pamięci dla zapisów edytora, rezerwa wolnego miejsca (upload/zapis/kopia), wczesne odrzucanie celów w `.filedeck`. Nowe testy regresji: `TestAdvisoryRegressions`, `TestUsernamesCannotCollideByCaseOrUnicode`, `TestFreeSpaceReserve`, `TestNoProcessExecutionOrPlugins` (brak `os/exec`, pluginów, szablonów).

| Kontrakt | Test |
|---|---|
| Zapis tekstu: wersja, konflikt, poprzednia wersja w koszu, tryb, uprawnienia | `TestTextEditorSavesSafely` |
| Odrzucanie binariów, złego UTF-8, za dużych plików, symlinków, katalogów, `.filedeck` | `TestTextEditorRejectsUnsuitableFiles` |
| Podgląd: typy, nagłówki, SVG w piaskownicy, HTML nie inline, Range | `TestPreviewAndTextEditorOverHTTP` |
| Kopia drzewa: pomijanie symlinków/FIFO, tryby, limity, anulowanie, sprzątanie | `TestCopyTreeBetweenSpaces` |
| Kopiowanie/przenoszenie między przestrzeniami, uprawnienia, izolacja zadań | `TestCopyAndMoveBetweenSpaces`, `TestTransfersOverHTTP` |

Weryfikacja: testy Go, `go vet`, `staticcheck` (czysto), `govulncheck`, race detector ×3, fuzzing; Chromium: motyw (zapamiętany po przeładowaniu), drag & drop na okno i folder, podgląd SVG ze skryptem i tekstu, edycja + konflikt → zapis jako kopia, kopiowanie i przenoszenie do przestrzeni z hosta — bez błędów konsoli i CSP. Wdrożone na instancji użytkownika.

## Etap 7 — język, powiadomienia, zaznaczanie, foldery

Na podstawie testów użytkownika:
- **Wysyłanie folderów** — przycisk „Upload folder” i przeciągnięcie folderu; struktura podfolderów odtwarzana poziom po poziomie (istniejące foldery są używane), limit 10 000 plików naraz.
- **Powiadomienia zamiast sekcji pod tabelą** — krótkie komunikaty znikające po 4–8 s (maks. 3 naraz, żeby nie zasłaniały strony) i dzwonek w górnym pasku z historią ostatnich 40 operacji (upload, kopiowanie/przenoszenie, zmiana nazwy, kosz, przywracanie, zapis, foldery) ze statusem i postępem. Zadania zakończone przed przeładowaniem strony trafiają do historii bez ponownego komunikatu.
- **Język** — angielski domyślny, przełącznik EN/PL (zapamiętany); słowniki `lang-en.json`/`lang-pl.json`, `TestTranslations` wymusza kompletność (klucze, zmienne, klucze użyte w HTML/JS, kody błędów API — test od razu znalazł 3 nieprzetłumaczone kody).
- **Akcje w wierszach jako ikony z dymkami**; zmiana nazwy ma ikonę pola tekstowego (`forms`).
- **Zaznaczanie** — przycisk trybu zaznaczania, pola wyboru, „zaznacz wszystko”, Ctrl/Cmd+klik, Shift+klik (zakres), Esc; pasek akcji zbiorczych: kopiuj/przenieś (do folderu docelowego) i do kosza. Serwer przyjmuje jedno zadanie z listą do 1000 par źródło→cel (`TestMultiItemTransfer`).

Weryfikacja: testy Go z race detectorem, `TestTranslations`; Chromium (22 kroki, w tym angielski domyślny, historia pod dzwonkiem, maks. 3 komunikaty, wysyłanie drzewa folderów, zaznaczanie z Ctrl, przeniesienie zbiorcze, kosz zbiorczy, przełączenie na PL i zapamiętanie) — bez błędów konsoli i CSP. Wdrożone.

## Etap 8 — poprawka reauth, test udziałów, sortowanie i wyszukiwanie

- **Błąd:** błędne hasło administratora przy tworzeniu konta (i błędne obecne hasło przy zmianie hasła) zwracało 401, które interfejs traktował jak wygaśnięcie sesji — użytkownik widział ekran logowania, choć sesja była ważna. Teraz 403 `wrong_password` z komunikatem „Wrong password” (`TestWrongPasswordKeepsSession`, krok w teście przeglądarkowym).
- **Test SMB:** próba z serwerem Samba w kontenerze i klientem CIFS jądra nie powiodła się z powodu środowiska — na tym hoście (LXC) montowanie CIFS jest niedozwolone nawet dla kontenera uprzywilejowanego. Zamiast tego podkomenda **`filedeck selftest DIR`** sprawdza na dowolnym podmontowanym katalogu wszystkie wymagane właściwości (`TestSelftestPassesOnLocalDirectory`). Wniosek dla wdrożenia na LXC: udział trzeba zamontować na hoście maszyn wirtualnych i przekazać jako katalog.
- **Wygoda przeglądania:** sortowanie po nazwie, rozmiarze i dacie modyfikacji (zapamiętane, `aria-sort`), kolumna daty, pobieranie zaznaczonych plików po kolei (bez archiwów ZIP — SECURITY.md).
- **Wyszukiwanie** po nazwie poniżej bieżącego folderu z limitami; wynik otwiera folder pliku i podgląd (`TestSearchIsBoundedAndStaysInside`, `TestSearchOverHTTP`).

Weryfikacja: testy Go (w kontenerze `golang` z limitami pamięci — twardy reset serwera przerwał poprzedni przebieg), Chromium 24 kroki bez błędów konsoli i CSP (gotowy obraz Playwright, limit 2 GB). Wdrożone.

## Znane ograniczenia

- Brak publicznych linków; wyszukiwanie tylko po nazwie (bez treści); historia operacji żyje w karcie przeglądarki (nie w serwerze).
- `.filedeck` jest widoczny dla użytkowników SMB tej samej przestrzeni (zalecane `veto files`).
- Kosz zajmuje miejsce do końca retencji; brak limitu jego rozmiaru.
- Każde uwierzytelnione żądanie zapisuje `LastSeen` z fsync; `Begin` zapisuje rekord pod globalną blokadą.

## Następny etap (propozycja)

1. `selftest` na prawdziwym udziale SMB użytkownika — potem deklaracja wsparcia.
3. Publiczne linki — według wymagań zebranych w SECURITY.md.
4. Wdrożenie za docelowym reverse proxy (bez osobnego profilu Caddy — decyzja użytkownika).

Prototyp jest fundamentem do dalszej implementacji, nie gotowym zamiennikiem produkcyjnego File Browser.
