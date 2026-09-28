# Filedeck — proponowany projekt bezpieczeństwa

Status: propozycja do doprecyzowania. Potwierdzony zakres: Linux/Docker i istniejące katalogi zmieniane również przez inne aplikacje lub SMB/NFS. Pojedyncza instancja serwera to rekomendacja architektoniczna. Inni użytkownicy sieciowi i dane plików są niezaufani. Operator hosta i konfiguracja mountów są zaufani; proces lokalny zmieniający pliki może powodować wyścigi. Kompromitacja roota hosta nie mieści się w gwarancjach aplikacji.

## Struktura

Modularny monolit: HTTP/UI → sesje → usługi operacji → polityka dostępu i storage. Osobne moduły kont/sesji, przestrzeni, operacji plikowych, uploadów i udziałów; zadania kosztowne mają ograniczoną kolejkę. Handler nie dostaje dowolnej ścieżki hosta ani ogólnego filesystemu do bezpośredniego użycia.

Go pozostaje sensownym kandydatem, bo nowy model nie wymaga zmiany języka. Proponowane minimum infrastruktury: jeden proces, frontend jako statyczne zasoby i jedna lokalna baza metadanych; SQLite jest kandydatem do oceny, nie zatwierdzonym wyborem. Nie dodawać Redis ani wielu replik w v1. Locki procesu nie są gwarancją w przyszłym modelu wieloinstancyjnym.

## Granice zaufania i niezmienne reguły

| Granica | Zasada | Weryfikacja |
|---|---|---|
| Przeglądarka → API | Klient nie określa właściciela, scope ani swoich praw | Modyfikacje ID, masowe przypisanie pól, obce zasoby |
| Konto → przestrzeń | Brak przydziału oznacza odmowę; kontrola każdej operacji | Ta sama macierz praw na listing/raw/preview/search/ZIP/edit |
| Ścieżka API → OS | Operacja pozostaje w otwartej granicy przestrzeni także przy wyścigu | Traversal, symlinki, podmiana katalogu, nazwy platformowe |
| Dane użytkownika → HTML/parser | Plik jest niezaufany niezależnie od rozszerzenia | XSS, aktywne SVG/HTML/EPUB, duże i błędne formaty |
| Upload → plik docelowy | Niekompletny upload nie zmienia istniejącego celu | Błąd, anulowanie, restart, konflikt, równoległy zapis |
| Udział → odbiorca anonimowy | Uprawnienie ograniczone do udziału i jego aktualnego stanu | Obcy ID, odwołanie, wygasanie, podmiana zasobu |
| Proxy → tożsamość | Nagłówek klienta nie staje się tożsamością | Bezpośrednie połączenie i sfałszowane forwarded headers |

## Dostęp do filesystemu

Używać uchwytu do katalogu i API odpornych na traversal; ocenić `os.Root` dla ustalonej wersji Go i pełnego zestawu operacji. Oficjalna dokumentacja opisuje jego ochronę i ograniczenia: [os.Root](https://pkg.go.dev/os#Root), [opis mechanizmu](https://go.dev/blog/osroot). `os.Root` dopuszcza pewne symlinki wewnątrz granicy — sam nie realizuje naszej polityki „bez symlinków” ani praw aplikacji.

Domyślnie nie przechodzić symlinków; jeśli w przyszłości będą potrzebne, zaprojektować ich semantykę oddzielnie. Nie wystarczy `Lstat` przed `Open`, bo to ponownie tworzy wyścig. Na Linux ocenić uchwyty i odpowiednie operacje OS; dla innych platform potrzebny osobny zestaw gwarancji i testów.

Obsługiwać zwykłe pliki i katalogi. FIFO, sockety i urządzenia muszą być odrzucone bez blokującego odczytu; nie wystarczy sprawdzić typu dopiero po potencjalnie blokującym Open. Hardlinki i mounty są osobnym problemem: izolacja ścieżki nie gwarantuje izolacji danych współdzielonych przez hardlink. Zaufana konfiguracja eksportowanych katalogów, uprawnień OS i mountów jest częścią modelu wdrożenia.

Kontrakt API powinien jednoznacznie określać dekodowanie i walidację ścieżek; odrzucać niejednoznaczne wejście, zamiast interpretować tę samą nazwę inaczej w autoryzacji, storage i ZIP. System plików, w tym case folding i Unicode, wyznacza realne kolizje nazw.

## Sesje i tożsamość

Losowy sekret sesji; w bazie jego hash, użytkownik, terminy ważności i stan odwołania. Cookie ustawiane przez serwer z HttpOnly, Secure i SameSite. Jawny POST logout, unieważnienie sesji przy resecie hasła i blokadzie konta. Ponowne uwierzytelnienie przy wrażliwych zmianach konta. Limit bezczynności i maksymalny czas życia egzekwuje serwer.

Cookie wymaga ochrony CSRF dla operacji zmieniających stan: kontrola Origin i token CSRF, brak mutacji w GET. Ograniczyć koszt logowania, wielkość wejścia, liczbę prób i równoległe kosztowne operacje hashowania; nie polegać wyłącznie na adresie IP z niezaufanego nagłówka.

Konto posiada stabilny identyfikator niezależny od loginu. Prywatny katalog może wynikać z ID, nigdy z potencjalnie kolidującej normalizacji nazwy użytkownika. Brak uprawnień domyślnych do wspólnego root. Rejestracja i automatyczny provisioning to osobne funkcje.

## Jeden model uploadu

Stany: utworzony → przesyłany → gotowy do zatwierdzenia → zatwierdzony; osobno anulowany/wygasły. Rekord ma właściciela, przestrzeń, cel, rozmiar, offset, termin ważności i losowy identyfikator. Zarówno prosty upload, jak i wznawianie używają tego samego modelu.

Najpierw walidacja i autoryzacja, później staging w prywatnym katalogu na tym samym filesystemie co cel, niedostępnym przez eksportowane API. Limit na plik, użytkownika/przestrzeń, aktywne transfery i całkowity staging. Zadeklarowany rozmiar nie zastępuje liczenia rzeczywistych bajtów.

Blokada uploadu obejmuje odczyt offsetu, zapis i aktualizację stanu; blokada celu rozstrzyga konkurujące zatwierdzenia. Uprawnienia sprawdzić ponownie przy finalizacji. Dla „nie nadpisuj” potrzebna jest atomowa semantyka no-replace, nie samo Exists+Rename. Zapis tymczasowy + rename może dać atomową widoczność pojedynczego pliku na obsługiwanym filesystemie; nie daje automatycznie transakcji baza+dysk ani atomowości całego drzewa.

Cleanup usuwa tylko należący do operacji plik staging. Restart wymaga odzyskania stanu i idempotentnego sprzątania. Cross-filesystem move to osobna operacja copy+commit+delete z możliwością częściowego wykonania. Dla SMB/NFS trzeba najpierw zweryfikować semantykę docelowego środowiska.

## Udostępnianie i zewnętrzne zmiany

Najtrudniejsza decyzja: co identyfikuje link? Ścieżka, konkretny obiekt czy zamrożona wersja danych?

- Dla plików zarządzanych wyłącznie przez aplikację: ID zasobu + generacja, unieważnienie udziałów po delete/replace oraz jawna polityka rename. Sama baza nie zapewnia atomowości z dyskiem; potrzebny dziennik operacji i odtwarzanie po awarii.
- Dla dowolnych katalogów zmienianych przez SMB/inne procesy: watcher, inode i mtime nie są wystarczającym dowodem niezmienności. Bezpiecznym wariantem pierwszych publicznych linków są kopie/snapshoty w storage zarządzanym przez Filedeck, z limitami miejsca. Kopiowanie źródła zmienianego w trakcie nie gwarantuje spójnego snapshotu — to osobny warunek do rozwiązania.
- „Żywy link do folderu” może być świadomą funkcją ujawniającą także nowe pliki. Wymaga jednoznacznej informacji w UI i osobnego modelu dostępu; nie może udawać linku do niezmiennego obiektu.

Link ma losowy sekret o wysokiej entropii, wygasanie i odwołanie. Hasło udziału nie powinno generować drugiego trwałego URL omijającego ochronę hasłem; po odblokowaniu krótka sesja odbiorcy. Sekrety udziałów usuwać z logów i refererów. Sprawdzać aktualny stan właściciela, udziału i praw przy każdym dostępie. Cache treści nie omija autoryzacji.

## Podglądy i limity

HTML/SVG/EPUB traktować jako aktywną treść. Najprostsza pierwsza wersja może oferować pobranie; późniejszy podgląd wymaga sandboxu lub osobnego origin bez sesji aplikacji. Markdown sanitizować, a surowy HTML ograniczyć. Lista wspieranych formatów ma wynikać z testów i potrzeby użytkownika.

Limity dotyczą bajtów faktycznie odczytanych, rozmiaru wyjścia, pikseli po dekodowaniu, głębokości drzewa, liczby wyników, czasu i równoległości. Sam Context nie zatrzyma każdej biblioteki ani blokującego syscalla; nieprzerywalne i ryzykowne konwersje mogą wymagać osobnego procesu z limitami OS.

## Konfiguracja i eksploatacja

Jeden jawny model konfiguracji z walidacją przed startem. Sekrety poza katalogami udostępnianymi. Start bez domyślnego publicznego konta administratora; jednorazowy bootstrap. Proces bez roota, minimalne mounty, backup metadanych i danych, log zdarzeń bez sekretów. Migracje nie importują aktywnych sesji, starych sekretów udziałów ani reguł, których nie da się odwzorować bez poszerzenia dostępu.
