# Kontrakt prototypu Filedeck

## Zakres i granica zaufania

Linux/Docker, jedna instancja, jedna lub więcej przestrzeni (katalogów udostępnianych użytkownikom). Katalogi mogą być zmieniane poza Filedeck. Konfiguracja mountów, katalogów przestrzeni, prywatnego state i tożsamości procesu jest zaufana. Wywołujący `core.Service` dostarcza tożsamość po uwierzytelnieniu. W API `subject` to wyłącznie ID konta z zweryfikowanej sesji serwerowej; nie pochodzi z nagłówka ani parametru żądania. Polecenia `list`/`read`/`put` są narzędziem lokalnego operatora.

`SetPermissions` to zaufana operacja administracyjna. Nie jest przeznaczona do bezpośredniej ekspozycji przez API. Polityka to mapa przestrzeń → uprawnienia (List, Read, Create, Modify); klucz `*` dotyczy wszystkich przestrzeni, także dodanych później, i sumuje się z wpisem konkretnej przestrzeni. Brak wpisu oznacza odmowę; nieznana przestrzeń jest odrzucana jak brak uprawnień. Odczyt treści i listowanie są niezależne od tworzenia i zmian. Cofnięcie praw blokuje kolejne operacje i publikację uploadu; już otwartego uchwytu odczytu nie odbiera. W przyszłym serwerze uchwyty muszą pozostać pod kontrolą warstwy operacji.

## Znaczenie ścieżki i uchwytu

Ścieżka jest względna, poprawna UTF-8, do 4096 bajtów, do 128 segmentów po najwyżej 255 bajtów. Odrzucamy ścieżki absolutne, puste, NUL, backslash, segmenty `.` i `..`, powtarzane i końcowe separatory. `.` jest wyjątkiem wyłącznie dla listowania root. Procenty to literalne znaki nazw; storage niczego nie URL-dekoduje. Adapter HTTP dekoduje query dokładnie raz (`url.ParseQuery`), akceptuje dokładnie jeden `space` i najwyżej jeden `path` i przekazuje wynik bez dalszego dekodowania. Ścieżki są zawsze względne wobec przestrzeni; ta sama ścieżka w innej przestrzeni to inny obiekt.

Przy rozwiązywaniu ścieżki `openat2` wymusza `RESOLVE_BENEATH`, `RESOLVE_NO_SYMLINKS` i `RESOLVE_NO_XDEV`. W przypadku odczytu `O_PATH` pozwala sprawdzić typ obiektu przed otwarciem danych; następnie `/proc/self/fd` daje dostęp do tego samego obiektu. Późniejsza zamiana nazwy na symlink nie przekierowuje otwartego uchwytu.

**Granica tożsamości obiektu:** uchwyt zachowuje dostęp do obiektu także po rename/unlink. Jeśli zewnętrzny proces może przenieść otwarty katalog do innej strefy zaufania, zapis względem tego uchwytu nadal dotyczy tego katalogu. Prototyp nie zapewnia ciągłego sprawdzania aktualnej ścieżki wszystkich otwartych obiektów. Zewnętrznych procesów uprawnionych do przenoszenia katalogów między strefami nie traktujemy jako odizolowanych przeciwników. To ograniczenie trzeba uwzględnić przy konfiguracji udziałów i przed obsługą rozbudowanych polityk wielu przestrzeni.

Podobnie hardlink do poufnego pliku umieszczony przez uprawniony proces w eksportowanym katalogu nie zostaje rozpoznany jako „zewnętrzny” na podstawie ścieżki. Uprawnienia OS i konfiguracja eksportów pozostają częścią ochrony. Symlinki, urządzenia i zagnieżdżone mounty nie są sposobem rozszerzania przestrzeni: listowanie ich nie pokazuje, a `RESOLVE_NO_XDEV` nie pozwala do nich wejść. Każdy katalog z hosta to osobna przestrzeń (osobny mount).

Źródła mechanizmów: [openat2](https://man7.org/linux/man-pages/man2/openat2.2.html), [renameat2](https://man7.org/linux/man-pages/man2/renameat2.2.html), [golang.org/x/sys/unix](https://pkg.go.dev/golang.org/x/sys/unix). Gwarancje opisane powyżej wynikają z użytych operacji i założeń wdrożenia, a nie tylko z tekstowej walidacji nazwy.

## Przestrzenie i katalog `.filedeck`

Przestrzeń to katalog otwarty raz przy starcie (`openat2` bez symlinków). W CLI i Dockerze: przestrzeń `files` (`-root`, w obrazie `/files`) oraz każdy podkatalog `-spaces-dir` (w obrazie `/spaces/<nazwa>`), nazwa `^[a-z0-9][a-z0-9._-]{0,63}$`. Symlink lub zła nazwa w katalogu przestrzeni zatrzymuje start (zamiast cichego pominięcia). State nie może leżeć w przestrzeni ani jej zawierać.

W każdej zapisywalnej przestrzeni Filedeck tworzy lub weryfikuje `.filedeck/` z podkatalogami `staging/` i `trash/`: własność procesu, bez zapisu dla grupy i innych (`0700` przy tworzeniu; na CIFS tryb wynika z opcji montowania, więc wymagany jest tylko brak `go-w`), ten sam mount co przestrzeń, otwierane bez symlinków. Umieszczenie ich w przestrzeni wynika z atomowości: publikacja i przeniesienie do kosza to jeden `renameat2` w obrębie jednego systemu plików; przestrzenie mogą leżeć na różnych mountach.

`.filedeck` jest niedostępny dla użytkowników na dwa sposoby: (1) pierwszy segment ścieżki równy `.filedeck` bez względu na wielkość liter i końcowe kropki/spacje (które serwery SMB ignorują) jest odrzucany; (2) pierwszy segment jest porównywany z `.filedeck` po (urządzenie, inode), co wyłapuje każdy alias tego samego katalogu — np. nazwy 8.3 czy odmienną normalizację Unicode po stronie serwera. Listowanie katalogu głównego pomija go również po inode. Dotyczy to wszystkich operacji: odczytu, listowania, uploadu, folderów, zmiany nazwy (źródła i celu), kosza i przywracania.

Przestrzeń jest **tylko do odczytu**, gdy system plików jest zamontowany `ro` (`ST_RDONLY`) albo proces nie ma prawa zapisu w jej katalogu głównym. Wtedy `.filedeck` nie jest tworzony (istniejący pozostaje ukryty), a operacje zapisu zwracają `ErrReadOnly` (HTTP 403 `read_only`). Uploady w toku przestrzeni, która po restarcie stała się tylko do odczytu lub zniknęła, są porzucane.

Tryby nowych obiektów: pliki `FILEDECK_FILE_MODE` (domyślnie `0640`, nadawany `fchmod` tuż przed publikacją; staging ma `0600`), foldery `FILEDECK_DIR_MODE` (domyślnie `0750`). Proces ustawia `umask 0`, więc tryby są dokładnie takie, jak skonfigurowano. Wymagane jest co najmniej `rw` dla właściciela plików i `rwx` dla katalogów. Na montowaniach, które odrzucają `chmod` (`EPERM`/`EOPNOTSUPP`, np. CIFS bez rozszerzeń Unix), tryb pozostaje taki, jak narzuca mount.

## Zmiana nazwy i kosz

Obie operacje wymagają Modify w danej przestrzeni, nie działają w przestrzeniach tylko do odczytu i są serializowane z odwołaniem sesji jak commit.

- **Zmiana nazwy/przeniesienie** w obrębie jednej przestrzeni: źródło musi być zwykłym plikiem lub katalogiem (symlinki i pliki specjalne są odrzucane), oba katalogi nadrzędne otwierane bez symlinków, `renameat2(RENAME_NOREPLACE)` — nigdy nie zastępuje celu. Przeniesienie katalogu do własnego podkatalogu i do `.filedeck` jest odrzucane. Między przestrzeniami nie ma przenoszenia (wymagałoby kopiowania).
- **Kosz**: rekord (`trash` w `uploads.db`: ID, przestrzeń, oryginalna ścieżka, typ, rozmiar, czas, kto usunął) jest zapisywany **przed** przeniesieniem do `.filedeck/trash/item-<64 hex>`. Przywrócenie przenosi element z powrotem pod oryginalną lub wskazaną ścieżkę, także `RENAME_NOREPLACE` — zajęta nazwa daje 409, brak folderu docelowego 404; interfejs pyta wtedy o inną ścieżkę.
- **Trwałe usunięcie** tylko z kosza: w API wyłącznie administrator (w rdzeniu Modify), automatycznie po `TrashRetention` (domyślnie 30 dni, przy okresowym `Expire`). Usuwanie drzewa idzie względem deskryptorów katalogów, bez podążania za symlinkami (symlink jest usuwany, cel nie), z weryfikacją, że otwarty katalog to ten sam inode, bez przekraczania urządzenia, z limitem głębokości 256 i 1 000 000 wpisów. Operacje na rekordach kosza są serializowane (`trashMu` zawsze przed `policyMu`).
- **Odtwarzanie przy starcie**: rekord bez elementu (awaria przed przeniesieniem albo po przywróceniu/usunięciu, a przed usunięciem rekordu) jest kasowany; element bez rekordu (awaria po przeniesieniu) jest przyjmowany jako rekord bez oryginalnej ścieżki — pozostaje widoczny, można go przywrócić pod wskazaną ścieżką i wygaśnie normalnie. Rekordy przestrzeni, której chwilowo nie ma, są zachowywane.
- Kosz widzą i przywracają osoby z Modify w danej przestrzeni — niezależnie od tego, kto usunął (rekord zapisuje ID usuwającego).

## Podgląd, edytor i kopiowanie

**Podgląd** (`/api/preview`, Read): typ wyłącznie z rozszerzenia z listy (obrazy łącznie z SVG, wideo, audio, PDF), nigdy z zawartości; `Content-Disposition: inline`, `nosniff`, `Cross-Origin-Resource-Policy: same-origin` i CSP z `sandbox` — SVG ze skryptem nie wykona go ani w `<img>`, ani otwarty w karcie. PDF dostaje `default-src 'none'; frame-ancestors 'self'` bez `sandbox` (przeglądarki nie renderują PDF w piaskownicy). HTML i inne typy są tylko do pobrania (415). Strona dopuszcza `media-src`/`frame-src 'self'`.

**Edytor** (`/api/text`): odczyt wymaga Read; tylko UTF-8 bez bajtów NUL, max 2 MiB; wersja = (inode, rozmiar, mtime) sprawdzana przed i po odczycie. Zapis nowego pliku wymaga Create i nigdy nie zastępuje istniejącego. Zapis istniejącego wymaga Read+Modify i wersji: nowa treść trafia do stagingu (fsync, zachowany tryb pliku), rekord kosza jest zapisywany, bieżący plik przenoszony do kosza (`RENAME_NOREPLACE`) i sprawdzany względem wersji — przy niezgodności wraca na miejsce (409 `changed`) — po czym staging zajmuje nazwę (`RENAME_NOREPLACE`). Między krokami nazwa jest przez chwilę nieobecna; awaria w tym momencie zostawia poprzednią wersję w koszu. Każdy zapis zostawia poprzednią wersję w koszu (`replaced: true`). Maks. 4 równoległe zapisy (bufor ~12 MiB każdy), treść czytana przed blokadą publikacji.

**Kopiowanie i przenoszenie** (`/api/transfers`): źródło wymaga Read (+Modify przy przenoszeniu), cel Create; uprawnienia sprawdzane przy starcie i ponownie tuż przed publikacją (pod blokadą z odwołaniem). Przenoszenie w obrębie przestrzeni = zmiana nazwy. Kopia idzie do `.filedeck/staging/copy-<hex>` przestrzeni docelowej względem deskryptorów, bez podążania za symlinkami i bez przekraczania montowań (symlinki, pliki specjalne i zagnieżdżone mounty są pomijane i liczone), pliki otwierane przez `O_PATH` + ponowne otwarcie tego samego obiektu; potem jeden `RENAME_NOREPLACE`. Przenoszenie między przestrzeniami: kopia, publikacja, następnie źródło do kosza przestrzeni źródłowej. Limity: 256 GiB i 200 000 wpisów na zadanie, 2 zadania na użytkownika, 4 łącznie. Zadania żyją w pamięci; przerwana kopia jest usuwana przy starcie. Kopia nie jest snapshotem źródła zmienianego w trakcie.

**Wyszukiwanie** (`/api/search`, List): nazwy zawierające frazę (1–100 znaków, bez `/` i NUL), poniżej wskazanego folderu. Przejście względem deskryptorów katalogów, bez podążania za symlinkami, bez wchodzenia w inne montowania i w `.filedeck` (po nazwie i inode), z ponownym sprawdzeniem inode otwartego katalogu. Limity: 200 000 przejrzanych wpisów, 500 wyników, głębokość 64, 10 s — po ich osiągnięciu wynik jest częściowy (`truncated`), a nie błędem. Listy i wyniki zawierają czas modyfikacji (`modified`).

**Rezerwa miejsca**: upload, zapis edytora i każdy kopiowany plik są odrzucane (`ENOSPC`, HTTP 507), jeśli zostawiłyby mniej niż `MinFreeBytes` (512 MiB) wolnego na systemie plików przestrzeni.

## Cykl uploadu

1. `Begin`: sprawdzenie Create, ścieżki, rozmiaru i kwot; rezerwacja deklarowanego rozmiaru; losowy ID, nowy plik `0600` w `.filedeck/staging` przestrzeni i trwały rekord (`uploading`, z nazwą przestrzeni). Jeśli zapis rekordu się nie uda, staging jest usuwany.
2. `Patch`: sprawdzenie właściciela i Create, blokada konkretnego uploadu, sprawdzenie offsetu i wygaśnięcia, ograniczony zapis i synchronizacja. Dodatkowy bajt jest wykrywany bez zapisywania go na dysk. Nowy offset trafia do rekordu **dopiero po fsync danych**; odpowiedź 200 oznacza, że fragment przetrwa awarię. Błąd zapisu, fsync lub rekordu cofa długość pliku do poprzedniego offsetu. Nieudany rollback zamyka możliwość kontynuacji uploadu.
3. `Commit`: kompletność, rzeczywisty rozmiar, aktualne Create w przestrzeni, nadanie trybu `FILEDECK_FILE_MODE` i synchronizacja pliku. Rekord przechodzi w `publishing`, następnie otwarcie katalogu docelowego bez symlinków i `RENAME_NOREPLACE`. Po sukcesie rekord zmienia się w wynik `published` (z informacją o potwierdzeniu trwałości); po odmowie wraca do `uploading`. Kontrola praw i publikacja są serializowane względem zmiany uprawnień.
4. `Abort` lub wygaśnięcie: unlink wyłącznie rozpoznanej nazwy staging, potem usunięcie rekordu. Właściciel może anulować upload także po odebraniu Create. Jeżeli usunięcie się nie uda, quota pozostaje zajęta, a uszkodzony upload nie może zostać opublikowany. Opublikowanego uploadu nie da się anulować.

Dwa uploady do tej samej nazwy rozstrzyga atomowa operacja OS — zwycięża najwyżej jeden. Nie ma opcji overwrite. Brak katalogu nadrzędnego daje błąd; aplikacja nie tworzy automatycznie całego drzewa. Zmiana nazwy i usuwanie są opisane w sekcji „Zmiana nazwy i kosz”; edycji ani nadpisywania nie ma.

`Commit` zwraca osobno `published` i `error`. `published=true, error!=nil` oznacza, że nazwa została opublikowana, ale np. fsync katalogu nie potwierdził trwałości. Nie wolno tego interpretować jako pozwolenia na usunięcie celu lub ponowne nadpisanie.

**Idempotencja.** Wynik publikacji jest przechowywany przez `ResultRetention` (24 h), także po restarcie. Ponowiony commit tego samego ID przez właściciela zwraca `published=true`, a jeśli trwałość nie była potwierdzona, ponawia fsync katalogu docelowego i state. `Status` zwraca wtedy `state: "published"` i `durability_confirmed`. Wynik nie ujawnia się innym użytkownikom (404) i nie wymaga ponownego sprawdzenia ścieżki celu — nie wykonuje żadnej operacji na danych poza fsync katalogu. Liczba wyników jest ograniczona do `8 × MaxUploads`; najstarsze są usuwane. Po wygaśnięciu wyniku ponowny commit zwraca 404, więc klient powinien ponawiać commit krótko po utracie odpowiedzi, nie po dobie.

## Awaria procesu i zasoby

State musi być prywatnym, wcześniej utworzonym katalogiem `0700` należącym do procesu, poza wszystkimi przestrzeniami (może leżeć na innym mouncie). Blokada `flock` state zapobiega równoczesnemu użyciu tego samego state przez dwa procesy. Operator nie może uruchamiać kilku instancji z różnymi state dla tych samych przestrzeni — limity i `.filedeck` są lokalne dla instancji. Pliki `upload-*.part` pozostałe w state po wersjach sprzed przestrzeni są przy starcie usuwane.

Stan uploadów i kosza jest w `uploads.db` (bbolt, schemat v2; rekordy v1 bez przestrzeni są odrzucane przy starcie) w state, otwieranym względem uchwytu state bez symlinków; plik musi być zwykły, `0600`, własnością procesu, bez hardlinków. Nazwa staging wynika z ID rekordu (`upload-<ID>.part`), nie jest zapisywana osobno. `Close` i zatrzymanie serwera zachowują uploady do wznowienia; polecenie CLI `put` samo anuluje swój nieudany upload.

Odtwarzanie przy starcie:

| Rekord | Staging | Wynik |
|---|---|---|
| nieczytelny lub niespójny (ID ≠ klucz, brak/zła przestrzeń, zła ścieżka celu, rozmiar ponad limit, offset poza zakresem) | dowolny | rekord usunięty, staging usunięty jako osierocony |
| `uploading` / `publishing`, przestrzeni brak lub jest tylko do odczytu | — | rekord usunięty |
| `uploading` / `publishing`, wygasły | jest | usunięty razem ze stagingiem |
| `uploading` / `publishing` | jest, dłuższy niż offset | przycięty do offsetu (bajty bez potwierdzenia), wznawialny jako `uploading` |
| `uploading` / `publishing` | jest, krótszy niż offset | potwierdzone bajty zniknęły — upload usunięty |
| `publishing` | brak | rename się wykonał: wynik `published`, ponowiony fsync decyduje o `durability_confirmed` |
| `uploading` | brak | usunięty (np. awaria między unlink a usunięciem rekordu) |
| `published` | — | zachowany do końca retencji |

`rename` jest atomowy, a staging usuwa tylko ścieżka `Abort`/wygaśnięcia (niemożliwa w trakcie `publishing`), więc obecność stagingu jednoznacznie rozstrzyga awarię w trakcie publikacji. Jeśli `rename` nie został utrwalony przed awarią systemu, staging wraca i upload jest znowu `uploading` — nigdy nie powstaje stan „opublikowano dwa razy”. Pliki staging bez rekordu są usuwane; symlink lub hardlink pod rozpoznaną nazwą zatrzymuje start (bez usuwania). Cleanup nie jest rekursywny. Skan stagingu ma limit 10000 nazw na przestrzeń. Katalogi state i `.filedeck` są własnością aplikacji, nie miejscem na pliki użytkowników.

Wygaśnięcie uploadu jest bezwzględne (`TTL` od `Begin`, domyślnie 1 h) i nie przedłuża się przy aktywności; restart go nie odnawia. Po zmniejszeniu limitów w konfiguracji uploady przekraczające nowy `MaxFileBytes` są odrzucane przy starcie, a nadmiar liczby lub rezerwacji jedynie blokuje nowe `Begin`. Uploady należą do ID konta; zablokowane konto nie może ich kontynuować ani opublikować, ale wygasną normalnie.

Koszt: każdy fragment to dwa fsync (dane i rekord), a `Begin` zapisuje rekord pod globalną blokadą serwisu. Brak sum kontrolnych treści — integralność względem klienta zapewnia offset i rozmiar, nie hash.

Limity bazują na rezerwacji zadeklarowanej wielkości; nie są pełną quotą filesystemu. Dane dopisane przez inne aplikacje, istniejące pliki użytkownika i narzut filesystemu nie mieszczą się w tej rezerwacji. Serwer wywołuje `Expire` co minutę i ustawia deadline 60 s na odczyt i zapis każdego żądania. `Context` sam nie przerywa arbitralnego zablokowanego `io.Reader`, syscalla czy zawieszonego mountu sieciowego. API ogranicza liczbę równoległych żądań do 64 (bez kolejki — nadmiar dostaje 503).

## Konta, sesje i API HTTP

**Magazyn.** `identity.db` (bbolt, schemat v1) leży w prywatnym state i jest otwierany względem uchwytu katalogu z `O_NOFOLLOW`; plik musi być zwykły, `0600`, własnością procesu, bez hardlinków. Recovery uploadów usuwa tylko nazwy `upload-*.part`, więc nie dotyka bazy. Plik jest zablokowany przez działający serwer, dlatego `bootstrap` i `reset-password` wymagają jego zatrzymania.

**Konta.** Nazwa `^[a-z0-9][a-z0-9._-]{2,63}$`, hasło 12–1024 znaków, Argon2id (t=3, 32 MiB, p=1). Wymagające hashowania operacje przechodzą przez bramkę 2 równoległych obliczeń; nadmiar dostaje 429 zamiast kolejki. Nieistniejący użytkownik jest weryfikowany przeciw hashowi atrapie. Maks. 1000 kont. Pierwszy administrator powstaje tylko w pustej bazie: poleceniem `bootstrap` albo w trybie konfiguracji (niżej). Uprawnienia kont to mapa przestrzeń → List/Read/Create/Modify (z `*` dla wszystkich); administrator nie dostaje dostępu do plików z samej roli, a pierwszy administrator dostaje `*` = wszystko. Konta zapisane przed wprowadzeniem przestrzeni (pole `permissions`) są przy odczycie zamieniane na `*`; administrator z pełnym ówczesnym zestawem (List|Read|Create) dostaje też Modify, zwykłe konta nie.

**Sesje.** Token 32 losowych bajtów; baza przechowuje tylko jego SHA-256. Każde konto ma licznik wersji; zmiana hasła, reset, zmiana roli, blokady lub praw zwiększa wersję i usuwa sesje konta w tej samej transakcji. Sesja jest ważna tylko przy zgodnej wersji. Wygasa po 30 min bezczynności lub 12 h. Nowe logowanie ponad 8 sesji usuwa najstarszą; logowanie z istniejącym cookie zastępuje poprzednią sesję. Każde uwierzytelnione żądanie zapisuje `LastSeen` (transakcja z fsync) — prostota kosztem wydajności przy dużym ruchu.

**Transport i pochodzenie.** Tryby: HTTPS z własnym TLS, jawny `ProxyCIDR` (połączenia spoza CIDR odrzucane, nagłówki forwarded ignorowane) albo `InsecureLocal` tylko dla origin i peer loopback. `Host` musi równać się origin. Żądania inne niż GET/HEAD wymagają dokładnego `Origin` oraz `X-CSRF-Token` = HMAC-SHA256(token sesji); `Sec-Fetch-Site: cross-site` jest odrzucany zawsze. Cookie: `__Host-filedeck`, `Secure`, `HttpOnly`, `SameSite=Strict` (w trybie lokalnym `filedeck-local` bez `Secure`). Dwa cookie o tej samej nazwie dają 401. Odpowiedzi: `no-store`, `nosniff`, CSP `default-src 'none'; sandbox`, brak referrera; treść plików zawsze jako `application/octet-stream` + `attachment`, więc przeglądarka nie renderuje plików użytkowników w origin aplikacji.

**Wejście.** JSON tylko `application/json`, maks. 16 KiB, bez nieznanych pól i bez danych po obiekcie. Fragment uploadu maks. `MaxChunkBytes`. Range tylko pojedynczy. Błędy mapowane na stałe kody bez szczegółów wewnętrznych.

**Operacje administracyjne** wymagają roli, CSRF i ponownego podania hasła (`reauth_password`), podlegają limitowi logowań. Zmiana konta aktualizuje politykę `core.Service` natychmiast; zablokowane konto ma pustą politykę.

**Kolejność odwołania i publikacji.** Logowanie, wylogowanie, zmiana hasła i operacje administracyjne biorą wyłączną blokadę `security`; commit uploadu bierze blokadę współdzieloną i dopiero pod nią uwierzytelnia sesję. Treść żądań objętych blokadą wyłączną jest czytana przed jej zajęciem, więc powolny klient nie blokuje kont. Gwarancja: po zakończonym wylogowaniu/blokadzie żaden commit ze starej sesji nie opublikuje pliku. Brak gwarancji: listowanie, pobieranie i `PATCH` rozpoczęte przed odwołaniem mogą się zakończyć (dane PATCH trafiają tylko do prywatnego staging).

**Limit logowań.** Token bucket: 10 prób na adres peer (odnowienie 10/min) i 20 globalnie (1/s), maks. 4096 śledzonych adresów. Obejmuje logowanie, zmianę hasła i reauth. Za proxy wszystkie próby dzielą adres proxy — świadomy kompromis wobec zaufania do `X-Forwarded-For`. Limit globalny chroni CPU, ale pozwala napastnikowi czasowo utrudnić logowanie innym.

**Poza zakresem.** MFA, blokada konta po nieudanych próbach, audyt logów bezpieczeństwa, lista i selektywne odwołanie własnych sesji.

## Interfejs WWW

`internal/web` serwuje wyłącznie osadzone pliki (`/` oraz `/assets/{nazwa}` bez podkatalogów), przez te same kontrole `Host`/TLS/proxy co API. Strona dostaje osobną CSP: `default-src 'none'`, skrypty, style, obrazy i połączenia tylko z własnego origin, `form-action 'none'`, `base-uri 'none'`, `frame-ancestors 'none'`, `require-trusted-types-for 'script'; trusted-types 'none'` — przeglądarka odrzuci każde przypisanie ciągu HTML do `innerHTML` i podobnych, więc nazwa pliku nie może stać się kodem nawet przy błędzie w JS. Odpowiedzi API zachowują CSP z `sandbox`.

Klient trzyma token CSRF tylko w pamięci i odczytuje go z `/api/auth/me` po przeładowaniu; cookie sesji pozostaje `HttpOnly`. Pobieranie to zwykły link do `/api/content` z `Content-Disposition: attachment` i `application/octet-stream`. Upload wysyła fragmenty zgodnie z offsetem potwierdzonym przez serwer, po błędach synchronizuje się przez status, a commit ponawia (idempotentny). ID uploadu jest zapamiętywane w `localStorage` pod kluczem użytkownik+ścieżka+rozmiar+data modyfikacji, żeby po przeładowaniu kontynuować ten sam plik — to wygoda, nie uprawnienie: serwer i tak sprawdza właściciela. Blokada `Sec-Fetch-Site: cross-site` dotyczy tylko `/api/`, więc link z innej strony otwiera interfejs, ale nie wykona żadnej operacji (cookie `SameSite=Strict` i tak nie zostanie wysłane).

Tworzenie folderu (`POST /api/folders`) wymaga Create, tworzy dokładnie jeden katalog w istniejącym rodzicu otwartym bez symlinków (`mkdirat`), nigdy nie zastępuje istniejącego obiektu i jest serializowane z odwołaniem sesji jak commit.

## Wdrożenie w Dockerze

Obraz `scratch` z jedną statyczną binarką. Domyślny układ: wolumeny `filedeck-data` (`/data`) i `filedeck-files` (`/files`) — w obrazie to puste punkty montowania z trybem `1777` (sticky, jak `/tmp`), kopiowane do nowych wolumenów nazwanych. Przy starcie proces tworzy w nich (tylko ostatni składnik ścieżki, bez nadpisywania istniejących wpisów) `/data/state` z trybem `0700` i przestrzeń `files` w `/files/own` z `FILEDECK_DIR_MODE`, jako faktyczny UID procesu — dowolne `FILEDECK_USER` działa bez `chown`. Istniejący wpis jest potem weryfikowany jak zwykle (symlink lub cudzy właściciel state jest odrzucany). Sticky bit sprawia, że inny UID nie usunie ani nie podmieni tych katalogów; na hoście wolumeny leżą w katalogu Dockera dostępnym tylko dla roota. Do tego pusty `/spaces` na katalogi z hosta. `HEALTHCHECK` wywołuje `filedeck healthcheck`, które łączy się z `127.0.0.1` na porcie nasłuchu (bez weryfikacji certyfikatu, połączenie nie opuszcza kontenera) i pyta o `/healthz`; ten endpoint odpowiada tylko połączeniom z loopback, przed wszelkimi innymi kontrolami, i nie zwraca żadnych danych. Konfiguracja przez `FILEDECK_*` (flagi mają pierwszeństwo). Tryby transportu w kontenerze:

- **self-signed (domyślny w compose)** — certyfikat ECDSA P-256 dla hosta z `FILEDECK_ORIGIN`, ważny 397 dni, zapisany w state jako `tls-self-signed.pem` (0600, zapis atomowy, odczyt bez podążania za symlinkiem). Generowany ponownie przy zmianie hosta lub na 30 dni przed wygaśnięciem. Odcisk SHA-256 w logu. Chroni transmisję i pozwala użyć cookie `__Host-`/`Secure`, ale zaufanie do certyfikatu zależy od ręcznej weryfikacji odcisku.
- **reverse proxy** — `FILEDECK_PROXY_CIDR` + HTTP w sieci Dockera; patrz sekcja o transporcie.
- **własny certyfikat** — `FILEDECK_TLS_CERT`/`FILEDECK_TLS_KEY`.

`-insecure-local` w kontenerze z opublikowanym portem nie zadziała: peer to brama sieci Dockera, nie loopback — i tak ma być. Compose domyślnie publikuje port tylko na `127.0.0.1` hosta. Reset hasła to jednorazowe `docker compose run` przy zatrzymanej usłudze (blokada state).

**Tryb konfiguracji.** Jeśli przy `serve` nie ma aktywnego administratora, serwer startuje i wypisuje w logu jednorazowy kod (80 losowych bitów, 16 znaków bez dwuznacznych liter, grupy po 4). Kod żyje tylko w pamięci i zmienia się przy każdym starcie. `POST /api/setup` z kodem, nazwą i hasłem przechodzi przez te same kontrole transportu, `Host` i `Origin` co reszta API, dzieli limit prób z logowaniem (10/min na adres, 20/min globalnie — odgadnięcie kodu jest niewykonalne), porównuje kod w stałym czasie i wykonuje `Bootstrap`, który sam odmawia, jeśli istnieje jakiekolwiek konto; po sukcesie kod jest kasowany. Kod nie jest hasłem: kto ma dostęp do logów kontenera, ma też dostęp do hosta Dockera. Hasła nigdy nie trafiają do logów ani zmiennych środowiskowych. Do czasu konfiguracji pozostałe endpointy działają normalnie (bez kont zwracają 401).

## SMB/NFS i zmiany zewnętrzne

Obsługa katalogu na lokalnym dysku, który jest równolegle eksportowany przez SMB/NFS, jest innym przypadkiem niż zapis na mountcie SMB/NFS po stronie Filedeck (przestrzeń z hosta, który sam montuje udział). W tym drugim przypadku wymagany jest `renameat2(RENAME_NOREPLACE)`: klient CIFS go obsługuje, NFS nie — publikacja, zmiana nazwy i kosz zwrócą wtedy błąd zamiast ryzykować nadpisanie. Porównanie (urządzenie, inode) chroniące `.filedeck` na CIFS zależy od stabilnych numerów inode (domyślne `serverino`; przy `noserverino` generuje je klient) — nie było testowane na prawdziwym udziale; ochrona po nazwie działa niezależnie. Mechanizmy testowano na lokalnym filesystemie i bind mountach w Dockerze. Nie przeprowadzono testów awarii serwera SMB/NFS, odłączenia sieci, semantyki cache ani reconnect.

Przed deklaracją wsparcia konkretnej konfiguracji potrzebne są testy `renameat2`, fsync, blokad, timeoutów i zachowania po awarii. Nie zastępujemy brakującej atomowości sekwencją Exists+Rename ani Copy+Delete. Jeżeli plik źródłowy jest jednocześnie edytowany z zewnątrz, odczyt/upload nie jest snapshotem. Publiczne linki, spójne snapshoty i bezpieczne nadpisywanie istniejących plików pozostają do osobnego projektu.
