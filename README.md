# Filedeck

Przeglądarka plików przez WWW, napisana od nowa jako następca File Browser — z kontraktami i testami wynikającymi z analizy jego podatności. Obecny etap: interfejs WWW, konta z odwoływalnymi sesjami, wiele przestrzeni (własna „Moje pliki” i katalogi z hosta), uprawnienia per przestrzeń, listowanie, pobieranie, tworzenie folderów, wznawialny upload (także przeciągnięciem na okno lub folder), podgląd zdjęć, wideo, audio, PDF i tekstu, edytor plików tekstowych z kontrolą wersji, kopiowanie i przenoszenie między przestrzeniami (także wielu zaznaczonych pozycji), wysyłanie całych folderów, zmiana nazwy, kosz, powiadomienia z historią ostatnich operacji, motyw jasny/ciemny oraz interfejs po angielsku (domyślnie) i polsku. Głównym środowiskiem jest Docker. Model bezpieczeństwa względem 62 advisory File Browser: [SECURITY.md](docs/SECURITY.md). Lista zmian: [CHANGELOG.md](CHANGELOG.md).

Polecenia `list`, `read` i `put` działają z uprawnieniami operatora systemu i nie są granicą uwierzytelniania użytkowników sieciowych — tę rolę pełni wyłącznie `serve`.

## Docker Compose

Wymagania: Docker z Compose v2, host Linux z kernelem co najmniej 5.8.

```sh
docker compose up -d --build
docker compose logs filedeck      # adres, odcisk SHA-256 certyfikatu i kod konfiguracyjny
```

Otwórz **https://localhost:8443** (koniecznie `https://`) i przy pierwszym uruchomieniu wpisz jednorazowy **kod konfiguracyjny** z logów, nazwę i hasło administratora. Kod jest losowy, zmienia się przy każdym starcie i przestaje działać po utworzeniu administratora. Alternatywnie, przy zatrzymanej usłudze: `printf '%s\n' 'haslo' | docker compose run --rm -T filedeck bootstrap admin`.

Dostęp z innych komputerów w sieci wymaga pliku `.env` — patrz „Ustawienia” niżej; bez niego port słucha tylko na `127.0.0.1` hosta i przeglądarka z innego komputera dostanie „odmowa połączenia”. Domyślnie Filedeck używa certyfikatu self-signed generowanego w state (przeglądarka pokaże ostrzeżenie — porównaj odcisk z logiem) i słucha tylko na `127.0.0.1` hosta. Dane: wolumen `filedeck-data` (`/data/state` — konta, sesje, certyfikat, rejestr uploadów i kosza) oraz wolumen `filedeck-files` (`/files/own` — przestrzeń „Moje pliki”). Kolejnych użytkowników i ich uprawnienia do przestrzeni ustawia administrator w interfejsie („Użytkownicy”).

Kontener działa jako UID 65532, z systemem plików tylko do odczytu, bez capabilities i z `no-new-privileges`. Jego stan (sesje, uploady w toku, certyfikat) przetrwa `docker compose down`/`up`; `down -v` usuwa wolumen z danymi.

### Zarządzanie

| Zadanie | Polecenie |
|---|---|
| Reset hasła (np. zapomniane hasło administratora) | `docker compose stop` → `printf '%s\n' 'nowe-haslo' \| docker compose run --rm -T filedeck reset-password admin` → `docker compose start` |
| Aktualizacja po zmianach kodu | `docker compose build && docker compose up -d` |
| Podgląd logów (m.in. wykryte przestrzenie) | `docker compose logs -f filedeck` |
| Stan zdrowia | `docker compose ps` (kolumna STATUS: `healthy`) |

Polecenia kont wymagają zatrzymanej usługi — działająca instancja blokuje state. Bez aktywnego administratora usługa startuje w trybie konfiguracji (kod w logach).

### Ustawienia

Skopiuj `.env.example` do `.env`. Najważniejsze:

- `FILEDECK_ORIGIN` — dokładnie ten adres, który wpisujesz w przeglądarce (inny `Host` daje 421). Dostęp z sieci lokalnej: `FILEDECK_BIND=0.0.0.0` i `FILEDECK_ORIGIN=https://<IP-lub-nazwa-serwera>:8443`; certyfikat zostanie wygenerowany dla tej nazwy.
- `FILEDECK_FILE_MODE` / `FILEDECK_DIR_MODE` — tryb plików i folderów tworzonych przez Filedeck (domyślnie `0640`/`0750`, czyli odczyt dla grupy). Na montowaniach SMB tryb wynika z opcji montowania i te ustawienia nie mają wpływu.
- `FILEDECK_USER` — UID:GID procesu (domyślnie `65532:65532`), patrz niżej.

### Katalogi z hosta (przestrzenie)

Serwer sam montuje udziały (SMB/NFS, dyski) w swoich ścieżkach; Filedeck dostaje je jako katalogi. Każdy katalog zamontowany w kontenerze pod `/spaces/<nazwa>` staje się przestrzenią o tej nazwie (małe litery, cyfry, `. _ -`). Skopiuj `compose.override.example.yaml` do `compose.override.yaml` — Compose wczyta go automatycznie, a `compose.yaml` zostaje nietknięty przy aktualizacjach:

```yaml
services:
  filedeck:
    volumes:
      - /mnt/nas/wspolne:/spaces/nas          # przestrzeń „nas”
      - /srv/archiwum:/spaces/archiwum:ro     # tylko przeglądanie i pobieranie
```

Po `docker compose up -d` log pokaże `Space "nas": read-write`. Następnie administrator nadaje użytkownikom uprawnienia do przestrzeni w panelu „Użytkownicy” (nowa przestrzeń nie jest nikomu udostępniona automatycznie, poza grantem „wszystkie”).

- **Prawa zapisu.** Proces w kontenerze (`FILEDECK_USER`) musi móc pisać w katalogu, inaczej przestrzeń będzie tylko do odczytu (znacznik w interfejsie). Dla udziału SMB ustaw ten sam UID/GID w opcjach montowania (`uid=`, `gid=`) albo `FILEDECK_USER` na właściciela katalogu. Przy pierwszym starcie Filedeck sam tworzy `/data/state` (`0700`) i `/files/own` (`0750`) jako użytkownik `FILEDECK_USER` — w obrazie są tylko puste punkty montowania z bitem sticky (jak `/tmp`). Jeśli zmienisz `FILEDECK_USER` przy **istniejących** danych, zmień ich właściciela jednorazowo przy zatrzymanej usłudze: `docker run --rm -v filedeck_filedeck-data:/data -v filedeck_filedeck-files:/files busybox chown -R 1001:1001 /data/state /files/own` (obraz Filedeck nie ma powłoki).
- **Katalog `.filedeck`.** W każdej zapisywalnej przestrzeni Filedeck tworzy ukryty katalog `.filedeck` (tryb `0700`) na pliki w trakcie uploadu i kosz — muszą leżeć na tym samym systemie plików, żeby publikacja i przenoszenie do kosza były atomowe. Filedeck go nie pokazuje i nie pozwala do niego wejść; użytkownicy SMB mogą go widzieć — warto go wykluczyć z ich widoku (np. `veto files = /.filedeck/` w Samba).
- **Kosz** każdej przestrzeni jest w jej `.filedeck/trash`; elementy są trwale usuwane po 30 dniach albo ręcznie przez administratora.
- **Sprawdzenie udziału przed użyciem:** `docker compose run --rm filedeck selftest /spaces/<nazwa>` wykonuje na podmontowanym katalogu wszystkie operacje, na których Filedeck polega (publikacja bez nadpisywania, zmiana nazwy, kosz, zapis edytora z kontrolą wersji, kopiowanie, wyszukiwanie, ukrywanie `.filedeck` łącznie z wariantami wielkości liter), w tymczasowym folderze `filedeck-selftest-*`, który potem usuwa. Wypisuje PASS/FAIL i tryby nowych plików; kod wyjścia ≠ 0 oznacza, że katalogu nie należy używać do zapisu. Nie potrzebuje state, więc działa obok uruchomionej usługi.
- **Wymagania systemu plików:** `renameat2(RENAME_NOREPLACE)` (lokalne dyski, CIFS/SMB; NFS go nie obsługuje — publikacja zwróci błąd zamiast ryzykować nadpisanie). Wsparcie konkretnych serwerów SMB/NFS nie było jeszcze testowane.

### Za reverse proxy (produkcja)

Proxy (Caddy, Traefik, nginx) terminuje TLS z prawdziwym certyfikatem i łączy się z kontenerem po HTTP w sieci Dockera:

```sh
FILEDECK_TLS_SELF_SIGNED=false
FILEDECK_PROXY_CIDR=172.30.0.0/16     # podsieć, z której łączy się proxy
FILEDECK_ORIGIN=https://files.example.org
```

Połączenia spoza `FILEDECK_PROXY_CIDR` są odrzucane, a nagłówki `X-Forwarded-*` ignorowane (tożsamość pochodzi tylko z sesji). Proxy musi przekazywać oryginalny nagłówek `Host`. Port kontenera nie powinien być wtedy publikowany na hoście.

Wszystkie flagi CLI mają odpowiednik `FILEDECK_<NAZWA>` (np. `FILEDECK_TLS_CERT`, `FILEDECK_TLS_KEY` dla własnego certyfikatu); flaga ma pierwszeństwo przed zmienną.

## Uruchomienie bez Dockera (development)

Wymagania: Linux z `openat2` i `statx(STATX_MNT_ID)` (kernel co najmniej 5.8), dostępny `/proc/self/fd`, Go 1.27.1. Docelowy filesystem musi obsługiwać `renameat2(RENAME_NOREPLACE)`, `flock` i synchronizację katalogów. Brak wymaganych mechanizmów powoduje błąd, bez mniej bezpiecznego fallbacku.

Z katalogu `filedeck`:

```sh
go build -o bin/filedeck ./cmd/filedeck
mkdir -p demo/files demo/state
chmod 700 demo/state
printf 'Pierwszy plik Filedeck\n' > demo/source.txt
./bin/filedeck -root demo/files -state demo/state put demo/source.txt hello.txt
./bin/filedeck -root demo/files -state demo/state list
./bin/filedeck -root demo/files -state demo/state read hello.txt
```

Dodatkowe przestrzenie: `-spaces-dir DIR` (każdy podkatalog to przestrzeń), a `-space NAZWA` wybiera przestrzeń dla `list`/`read`/`put`. State musi leżeć poza wszystkimi przestrzeniami.

Ponowny upload do `hello.txt` zwróci konflikt, zachowując poprzednią zawartość. Zagnieżdżone cele są obsługiwane, jeżeli katalog nadrzędny już istnieje. Ścieżki względem przestrzeni używają `/`; `.` oznacza katalog główny wyłącznie przy listowaniu.

## Serwer i API

Pierwszego administratora tworzy się wyłącznie lokalnie; API nie ma otwartej rejestracji ani logowania na podstawie nagłówka. Hasło (min. 12 znaków) jest czytane z terminala lub stdin. Polecenia kont wymagają zatrzymanego serwera (blokada state).

```sh
./bin/filedeck -root demo/files -state demo/state bootstrap admin
./bin/filedeck -root demo/files -state demo/state \
  -listen 127.0.0.1:8080 -origin http://127.0.0.1:8080 -insecure-local serve
```

Interfejs WWW jest pod `-origin`. `-insecure-local` dopuszcza HTTP tylko na adresie loopback, do developmentu. Poza nim wymagane jest `-origin https://…` oraz TLS (`-tls-cert`/`-tls-key` albo `-tls-self-signed`) lub jawny `-proxy-cidr` reverse proxy terminującego TLS. Nagłówki `X-Forwarded-*` są ignorowane, więc limit logowania za proxy jest wspólny dla adresu proxy. `-origin` musi dokładnie odpowiadać adresowi w przeglądarce; inny `Host` daje 421.

| Metoda i ścieżka | Opis |
|---|---|
| `POST /api/auth/login` | `{"username","password"}` → cookie sesji i token `csrf` |
| `GET /api/auth/me`, `POST /api/auth/logout` | bieżąca sesja z tokenem `csrf` i limitami uploadu, wylogowanie |
| `POST /api/auth/password` | zmiana hasła, unieważnia wszystkie sesje konta |
| `GET /api/spaces` | przestrzenie użytkownika z jego uprawnieniami (administrator widzi wszystkie) |
| `GET /api/files?space=&path=` | listowanie (`.` lub brak = katalog główny przestrzeni) |
| `POST /api/folders` | `{"space","path"}` — nowy folder w istniejącym katalogu, bez nadpisywania |
| `POST /api/rename` | `{"space","from","to"}` — zmiana nazwy/przeniesienie w obrębie przestrzeni, bez nadpisywania |
| `GET /api/search?space=&path=&q=` | wyszukiwanie po nazwie poniżej folderu (bez rozróżniania wielkości liter), maks. 500 wyników, 200 000 przejrzanych wpisów, 10 s — `truncated: true` przy limicie |
| `GET /api/preview?space=&path=` | podgląd inline tylko dla listy typów (obrazy, wideo, audio, PDF), `nosniff`, CSP `sandbox` |
| `GET`/`PUT /api/text` | edytor: `{"content","version"}`; zapis z `version` zastępuje plik tylko w tej wersji (409 `changed`), pusta `version` tworzy nowy plik; limit 2 MiB UTF-8 |
| `POST /api/transfers`, `GET /api/transfers[/{id}]`, `DELETE /api/transfers/{id}` | kopiowanie/przenoszenie w tle: `{"kind":"copy"\|"move","from":{space,path},"to":{space,path}}` albo `"items":[{from,to},…]` (do 1000, po kolei, pierwszy błąd zatrzymuje), postęp, anulowanie |
| `POST /api/trash`, `GET /api/trash?space=` | przeniesienie do kosza, zawartość kosza |
| `POST /api/trash/{id}/restore`, `DELETE /api/trash/{id}` | przywrócenie (`{"path"}`, pusta = oryginalna), trwałe usunięcie — tylko administrator |
| `GET /api/content?space=&path=` | pobieranie jako załącznik, pojedynczy `Range` |
| `POST /api/uploads` | `{"space","path","size"}` → ID uploadu |
| `PATCH /api/uploads/{id}` | fragment `application/octet-stream`, nagłówek `Upload-Offset` |
| `GET`/`DELETE /api/uploads/{id}`, `POST /api/uploads/{id}/commit` | status (`state`, zatwierdzony `offset`), anulowanie, publikacja — ponowienie commitu zwraca zapisany wynik |
| `GET`/`POST /api/users`, `PUT /api/users/{id}`, `POST /api/users/{id}/password` | administracja; zmiany wymagają `reauth_password` |

Każde żądanie inne niż GET/HEAD wymaga nagłówka `Origin` równego `-origin` oraz `X-CSRF-Token` z odpowiedzi logowania. Uprawnienia to maska bitowa per przestrzeń (`"spaces": {"files": 15, "*": 3}`; `*` = wszystkie przestrzenie): 1 listowanie, 2 odczyt, 4 tworzenie (upload, foldery), 8 zmiany (zmiana nazwy, kosz, przywracanie).

Upload przetrwa restart, a nawet awarię serwera: po utracie odpowiedzi lub połączenia klient pyta `GET /api/uploads/{id}` o zatwierdzony `offset` i wysyła dalej od tego miejsca. Jeśli odpowiedź na commit zginęła, klient ponawia commit albo sprawdza status (`state: "published"`) — przez 24 h dostanie ten sam wynik, bez ryzyka drugiej publikacji. Sesje też są trwałe, więc cookie działa po restarcie.

## Zaimplementowane kontrakty

- Interfejs WWW osadzony w binarce: tylko zasoby z własnego origin, bez skryptów i stylów inline, Trusted Types (żadnego `innerHTML`), nazwy plików wstawiane jako tekst, token CSRF wyłącznie w pamięci, pliki zawsze pobierane jako załącznik.

- Dostęp do przestrzeni przez uchwyty, z odrzuceniem traversal, symlinków i przechodzenia do zagnieżdżonych mountów.
- Odczyt tylko zwykłych plików; sprawdzenie typu przez `O_PATH` przed otwarciem danych. Listowanie pomija symlinki i pliki specjalne, limituje liczbę przetwarzanych wpisów.
- Niezależne prawa listowania, odczytu, tworzenia i zmian, nadawane per przestrzeń; domyślna odmowa. Właściciel uploadu jest sprawdzany przy każdej operacji na nim.
- Ukryty katalog `.filedeck` każdej przestrzeni jest nieosiągalny także przez aliasy nazw (wielkość liter, końcowe kropki/spacje, inna nazwa tego samego katalogu — porównanie urządzenia i inode). Przestrzeń tylko do odczytu jest wykrywana automatycznie.
- Zmiana nazwy, kosz i przywracanie przez `renameat2(RENAME_NOREPLACE)` — nigdy nie nadpisują; trwałe usuwanie tylko z kosza, bez podążania za symlinkami i bez przekraczania montowań.
- Prywatny staging, limit pliku, fragmentu, zarezerwowanych bajtów oraz liczby uploadów globalnie i na użytkownika.
- Serializacja kontroli offsetu i zapisu fragmentu; rollback po błędzie, przekroczeniu limitu lub anulowaniu.
- Powtórna kontrola praw, kompletności i rozmiaru przed publikacją. Atomowe utworzenie nowej nazwy bez nadpisywania istniejącego pliku, katalogu lub symlinku.
- Cleanup usuwa wyłącznie własny plik staging. Błąd potwierdzenia trwałości po publikacji jest odróżniony od braku publikacji.
- Trwałe rekordy uploadów: offset zapisywany po fsync danych, dwuetapowa publikacja rozstrzygana po restarcie przez obecność pliku staging, idempotentny commit z wynikiem przechowywanym 24 h. Wygaśnięcie i anulowanie usuwają staging i rekord; pliki staging bez rekordu są usuwane przy starcie.

- Konta i sesje w transakcyjnej bazie bbolt w prywatnym state; hasła Argon2id; sesje losowe, przechowywane jako hash, z wygaśnięciem bezczynności (30 min) i bezwzględnym (12 h), maks. 8 na konto. Zmiana hasła, reset, blokada lub zmiana praw unieważnia wszystkie sesje konta; ostatniego aktywnego administratora nie da się zablokować ani zdegradować.
- Cookie `__Host-` `HttpOnly`, `Secure`, `SameSite=Strict`; CSRF przez token powiązany z sesją, dokładny `Origin` i `Sec-Fetch-Site`. Limit logowań per adres i globalny, limit równoległych żądań, deadline 60 s, limity JSON i nagłówków, ścisłe parsowanie JSON i query.
- Publikacja uploadu jest serializowana z wylogowaniem i zmianą kont: po udanym wylogowaniu lub blokadzie stare żądanie nie opublikuje pliku. Pobrania rozpoczęte wcześniej mogą się zakończyć.

Domyślnie: plik 1 GiB, fragment 8 MiB, staging 4 GiB, 32 aktywne uploady, 4 na użytkownika, ważność 1 godzina. Są to limity prototypu, do dopasowania do produktu. Wewnętrzny model pozwala na wiele fragmentów; CLI automatycznie dzieli lokalny plik.

## Tłumaczenia

Teksty interfejsu są w `internal/web/static/lang-en.json` (domyślny, źródłowy) i `lang-pl.json`. Nowy tekst dodaje się do **obu** plików; `TestTranslations` (w `go test ./...`) sprawdza, że języki mają te same klucze i zmienne (`{name}`), a każdy klucz użyty w HTML/JS i każdy kod błędu API ma tłumaczenie. Nowy język: kolejny plik `lang-<kod>.json`, wpis w `LANGS` w `app.js` i w teście.

## Testy

```sh
go test -count=1 -timeout=60s ./...
go vet ./...
go test -race -count=1 -timeout=120s ./...
go test ./internal/storage -run='^$' -fuzz=FuzzValidPath -fuzztime=10s -parallel=2
```

Race detector wymaga kompilatora C; bez niego można użyć obrazu `golang:1.27.1-bookworm`. Dostępne są też cele `make build`, `test`, `race`, `vet`, `fuzz` i `ui-test`.

Test przeglądarkowy (Chromium przez Playwright w kontenerze `node`) uruchamia się na **jednorazowej** instancji z pustym wolumenem — tworzy foldery, pliki i użytkownika `jan`:

```sh
FILEDECK_PORT=18443 docker compose -p filedeck-test up -d   # po bootstrapie z -p filedeck-test
FILEDECK_URL=https://localhost:18443 FILEDECK_PASSWORD='haslo-admina' make ui-test
FILEDECK_PORT=18443 docker compose -p filedeck-test down -v
``` Workflow CI przygotowano dla sytuacji, w której katalog `filedeck` jest korzeniem nowego repozytorium.

Szczegóły modelu bezpieczeństwa i ograniczeń: [CONTRACT.md](docs/CONTRACT.md). Stan wdrożenia i dalsze kroki: [PROGRESS.md](docs/PROGRESS.md).
