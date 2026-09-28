# Funkcje: zachować rezultat, zaprojektować mechanizm od nowa

Mapa opiera się na `reference/http/http.go`, modułach backendu i widokach `reference/frontend/src/views`. Etapy oznaczają proponowaną kolejność, nie trwałe usunięcie funkcji.

| Funkcja użytkownika | Obecne miejsca | Propozycja Filedeck | Etap |
|---|---|---|---|
| Listowanie, sortowanie, widoki, ukryte pliki | `files/listing.go`, `files/sorting.go`, `FileListing.vue` | Listowanie z limitami; preferencja ukrywania osobna od uprawnień | 1 |
| Pobieranie pliku, audio/wideo | `http/raw.go`, `Preview.vue` | Streaming i Range; wspólne uprawnienie odczytu treści | 1 / podgląd 3 |
| Upload wielu plików i folderów | `resource.go`, frontend upload | Kolejka w UI; prywatny staging i jawne zatwierdzenie | 1 |
| Wznawianie uploadu | `tus_handlers.go`, cache memory/Redis | Jeden model uploadu, identyfikator i właściciel; adapter TUS jeśli potrzebny | 2 |
| Tworzenie katalogów | `resource.go` | Jawna operacja, walidowana przed efektem na dysku | 1 |
| Zmiana nazwy, przeniesienie, kopiowanie, usuwanie | `resource.go`, `fileutils` | Operacje domenowe, kontrola źródła i celu, jasne konflikty | 2 |
| Edycja tekstu i podgląd Markdown | `files/file.go`, `Editor.vue` | Limit rzeczywiście odczytanych bajtów, ochrona przed utratą cudzych zmian | 2 |
| Wyszukiwanie nazw, listowanie rekursywne | `http/search.go`, `search/`, `resource.go` | Limit czasu, wyników, głębokości i równoległości | 2 |
| Pobieranie katalogu jako archiwum | `raw.go` | Najpierw ZIP, bez plików specjalnych, symlinków i niebezpiecznych nazw | 2 |
| Linki publiczne, hasło, wygasanie | `share/`, `http/public.go` | Osobne uprawnienie odbiorcy; odwołanie i jednoznaczna semantyka zasobu | 3 |
| Miniatury, obrazy, napisy, PDF, EPUB, CSV | `img/`, `http/subtitle.go`, frontend previews | Macierz wspieranych formatów; limity parserów, izolacja aktywnych formatów | 3 |
| Konta, hasła, profil, administrator | `users/`, `http/users.go` | Konta oddzielone od przestrzeni; sesje serwerowe, role jako zestawy uprawnień | 1 |
| Rejestracja i automatyczne konta | `auth.go`, `auth/proxy.go`, `settings/dir.go` | Domyślnie wyłączone; nowy użytkownik bez dostępu, dopóki go nie przydzielono | później |
| Logowanie przez proxy/hook/no-auth | `auth/` | Najpierw konta lokalne; OIDC jako preferowana integracja. Proxy wymaga jawnej granicy zaufania | później |
| Uprawnienia i reguły ścieżek | `Permissions`, `rules/`, `data.go` | Uprawnienia do całych, jawnych przestrzeni; bez regexów i nakładających się wyjątków w v1 | 1 |
| Terminal i hooki poleceń | `runner/`, `http/commands.go` | Rekomendacja: brak w procesie serwera. Jeśli niezbędne — osobny projekt izolowanego wykonawcy | osobna decyzja |
| Branding, języki, motyw, preferencje | `branding/`, frontend, settings | Dane i dozwolone opcje wyglądu; bez wykonywalnych szablonów użytkownika | 3 |
| CLI, konfiguracja, Docker, backup bazy | `cmd/`, `storage/`, Dockerfile | Małe CLI administracyjne, jedna konfiguracja, jawne migracje i backup | 1 |
| Statystyki miejsca i sumy kontrolne | `diskUsage`, `resourceGetHandler` | Informacja o przestrzeni/quocie; checksum wymaga odczytu i limitu pracy | 2 |

## Najważniejsze uproszczenie modelu dostępu

Przestrzeń (`Space`) reprezentuje jawnie wskazany katalog. Konto dostaje zestaw praw do przestrzeni, np. listowanie, odczyt treści, tworzenie, nadpisywanie, przenoszenie, usuwanie i udostępnianie. Role czytelnik/edytor są presetami tych praw. Administracja kontami nie musi automatycznie dawać dostępu do treści.

W v1 przestrzenie o różnych politykach nie powinny się nakładać ani zawierać siebie nawzajem. Wydzielenie poufnego podkatalogu wymaga rozdzielenia granic storage; ukrycie go w interfejsie nie jest zabezpieczeniem. Jeżeli potrzebna jest zgodność z obecnymi wyjątkami per plik, wymaga to odrębnego projektu ACL i testów — nie wolno automatycznie spłaszczyć ich do szerokiego dostępu.

Podgląd i pobranie przekazują użytkownikowi treść. Nie obiecujemy zabezpieczenia „może obejrzeć, ale nie może skopiować”. Można osobno sterować funkcją eksportu ZIP, ale nie traktować braku przycisku pobierania jako ochrony poufności.
