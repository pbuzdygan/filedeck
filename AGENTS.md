# Filedeck — instrukcje dla agentów AI (Codex, Claude Code i inne)

Ten plik jest jedynym źródłem kontekstu projektu dla agentów. Czytaj go na starcie każdej sesji. Stan prac: `docs/PROGRESS.md` (sekcja „Następny etap”).

## Projekt

- **Filedeck** (to repozytorium, Go, tylko Linux + Docker) to napisany od zera, security-first następca File Browser. Cel: domknąć klasy błędów z 62 advisory GHSA oryginału prostszym projektem, nie przez port kodu.
- `reference/filebrowser/` to archiwalny fork oryginału — **tylko do czytania, nigdy nie modyfikuj**; jest wykluczony z gita (`.gitignore`), więc może nie istnieć w innym klonie.
- `docs/analysis/` — analiza oryginału (funkcje, znaleziska, projekt, rejestr advisory).
- Katalogi Filedeck są jednocześnie używane przez inne aplikacje i udziały SMB/NFS: nigdy nie nadpisuj, publikuj atomowo, zakładaj, że pliki zmieniają się pod spodem.

## Rozmowa i język

- Z użytkownikiem rozmawiaj **po polsku**. Dokumenty w `docs/` i README są po polsku.
- Interfejs jest **domyślnie po angielsku**, z przełącznikiem na polski. Każdy nowy lub zmieniony tekst UI musi trafić do obu plików: `internal/web/static/lang-en.json` i `lang-pl.json`. Nigdy nie wpisuj tekstu na sztywno w `app.js`/`index.html` — używaj `t('klucz')` i `data-i18n*`. `TestTranslations` pilnuje kompletności.

## Zasady kodu

- **Ikony:** tylko Tabler Icons, **offline**. Skopiuj `icons/outline/<nazwa>.svg` z `@tabler/icons` 3.48.0 do `internal/web/static/ti-<nazwa>.svg`, dodaj regułę `.ic-<nazwa>` w `app.css`, wpisz ikonę do `third_party/tabler-icons/README.md`, renderuj przez `icon('<nazwa>')`. Żadnych CDN ani zasobów z internetu.
- **Bezpieczeństwo:**
  - bez `os/exec`, pluginów i szablonów HTML (pilnuje tego `internal/audit`);
  - CSP bez `unsafe-*`, z Trusted Types;
  - operacje na plikach tylko przez `internal/storage` (`openat2` z BENEATH, NO_SYMLINKS i NO_XDEV oraz `RENAME_NOREPLACE`);
  - usuwanie idzie do kosza;
  - katalog `.filedeck` jest nieosiągalny.
- **Zmiany:**
  - każda funkcja dostaje testy Go;
  - przy zmianie kontraktu aktualizuj `docs/CONTRACT.md`;
  - po etapie dopisz sekcję w `docs/PROGRESS.md`;
  - wpływ na advisory opisz w `docs/SECURITY.md`;
  - wpis w `CHANGELOG.md` dodaj w sekcji New Features, Improvements albo Bug Fixes. Pisz nietechnicznie: co użytkownik zobaczy, z czego skorzysta, co odczuje.

## Środowisko (ważne — ograniczone zasoby)

Serwer to LXC z 6 GB RAM, 3 CPU i **bez swapu**. `/tmp` leży w RAM i znika po restarcie. Równoległe ciężkie zadania (build obrazu razem z Playwright) doprowadziły już do twardego resetu maszyny.

- Na hoście nie ma `go`, `gcc` ani `make`. Testy w kontenerze, z katalogu głównego repozytorium:
  ```sh
  docker run --rm --memory 2g --cpus 2 -u $(id -u):$(id -g) -v "$PWD":/src \
    -v ~/.cache/filedeck-go/mod:/go/pkg/mod -v ~/.cache/filedeck-go/build:/cache \
    -e GOCACHE=/cache -e GOFLAGS=-buildvcs=false -e HOME=/tmp -w /src \
    golang:1.27.1-bookworm sh -c 'test -z "$(gofmt -l cmd internal)" && go vet ./... && go test ./...'
  ```
  Dodaj `-race`, gdy zmieniasz współbieżność.
- Obraz: `docker build --memory 2g -t filedeck:local .`
- Test przeglądarkowy:
  - skrypt: `test/ui/smoke.mjs`, uruchamiany poleceniem `docker run` z celu `ui-test` w `Makefile` (na hoście nie ma `make`; to gotowy obraz Playwright z limitem 2 GB);
  - uruchamiaj go na **osobnej, jednorazowej instancji** (`docker compose -p filedeck-<nazwa>`, katalogi testowe w `~/.cache/filedeck-test`, nigdy w `/tmp`);
  - potem usuń instancję i `test/ui/node_modules`.
- **Nigdy nie uruchamiaj buildu i Playwright jednocześnie.**

## Instancja użytkownika (produkcyjna, nie psuj)

- Działa z katalogu głównego repozytorium przez `docker compose` (projekt `name: filedeck`, wolumeny `filedeck_*`), adres `https://192.168.68.6:8443`.
- Konfiguracja w `.env`: `FILEDECK_BIND`, `FILEDECK_ORIGIN`, `FILEDECK_USER=1001:1001`.
- Dodatkowe przestrzenie są w `compose.yaml`/`compose.override.yaml` jako `/spaces/<nazwa>`, np. `docker_dev`. Własna przestrzeń to `/files`.
- Wdrożenie po zmianach: `docker compose up -d --build`, potem sprawdź `docker compose ps` (musi być `healthy`).
- Nigdy nie testuj na wolumenach użytkownika. Nie zmieniaj `.env` bez pytania.
- Użytkownik ma własny reverse proxy — nie dodawaj profilu Caddy.

## Stan na 2026-09-28

Etapy 1–8 są zrobione i wdrożone:

- rdzeń, konta i sesje, wznawialne uploady;
- UI i Compose;
- przestrzenie, uprawnienia i kosz;
- podgląd, edytor, kopiowanie i przenoszenie, motyw;
- EN/PL, powiadomienia, zaznaczanie, wysyłanie folderów;
- poprawka reauth, `selftest`, sortowanie i wyszukiwanie.

Wszystkie testy Go przechodzą, a test przeglądarkowy przechodzi w 24 krokach.

Następne kroki (tylko po potwierdzeniu użytkownika):

1. Użytkownik uruchomi `docker compose run --rm filedeck selftest /spaces/<nazwa>` na prawdziwym udziale SMB. Po wyniku trzeba dopisać deklarację wsparcia. W tym LXC montowanie CIFS jest niemożliwe.
2. Publiczne linki, według wymagań w `docs/SECURITY.md`.
3. Wdrożenie za reverse proxy użytkownika.
