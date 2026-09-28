# Changelog

Zmiany w Filedeck opisane z perspektywy użytkownika: co zobaczysz, z czego skorzystasz, co się zmieni w pracy. Szczegóły techniczne i testy: [docs/PROGRESS.md](docs/PROGRESS.md), bezpieczeństwo: [docs/SECURITY.md](docs/SECURITY.md).

## [Niewydane] — pierwsza wersja (wrzesień 2026)

Filedeck to przeglądarka plików przez WWW napisana od zera jako następca File Browser — z naciskiem na to, żeby nie dało się przypadkiem stracić danych ani obejść uprawnień.

### New Features

- **Pierwsze uruchomienie w przeglądarce** — przy pierwszym starcie strona prosi o jednorazowy kod z logów serwera i pozwala założyć konto administratora bez wiersza poleceń.
- **Wiele przestrzeni** — obok „Moich plików” widzisz foldery udostępnione przez serwer (np. dyski sieciowe), każdy jako osobną przestrzeń. Przestrzenie tylko do odczytu są oznaczone i nie pokazują przycisków zmian.
- **Konta i uprawnienia per przestrzeń** — administrator tworzy konta i dla każdej przestrzeni osobno decyduje, kto może ją przeglądać, pobierać pliki, dodawać nowe i zmieniać istniejące.
- **Wysyłanie plików i całych folderów** — przyciskiem albo przeciągając na okno lub na konkretny folder. Struktura podfolderów zostaje zachowana, a przerwane wysyłanie dużego pliku da się wznowić.
- **Podgląd** zdjęć, filmów, muzyki, PDF-ów i plików tekstowych, z przechodzeniem strzałkami do poprzedniego i następnego pliku.
- **Edytor plików tekstowych** (np. txt, md, yaml) — Ctrl+S zapisuje, widać niezapisane zmiany, a poprzednia wersja trafia do kosza. Jeśli ktoś w międzyczasie zmienił plik (np. przez udział sieciowy), Twoje zmiany nie nadpiszą jego wersji — możesz zapisać je jako nowy plik.
- **Nowy plik tekstowy i nowy folder** jednym kliknięciem.
- **Kopiowanie i przenoszenie** plików i folderów, także między przestrzeniami. Działa w tle, pokazuje postęp i można je anulować.
- **Kosz** — usunięte rzeczy można przywrócić, także pod inną ścieżkę. Znikają same po okresie przechowywania; usunąć je na stałe może tylko administrator.
- **Zmiana nazwy** plików i folderów.
- **Zaznaczanie wielu pozycji** — pola wyboru, Ctrl+klik, Shift+klik (zakres), Esc czyści zaznaczenie. Zaznaczone pozycje możesz naraz skopiować, przenieść, wrzucić do kosza albo pobrać.
- **Wyszukiwanie po nazwie** w bieżącym folderze i jego podfolderach. Kliknięcie wyniku otwiera folder, w którym leży plik, i jego podgląd.
- **Sortowanie** po nazwie, rozmiarze i dacie modyfikacji (wybór jest zapamiętywany) oraz kolumna z datą modyfikacji.
- **Dzwonek z historią** ostatnich operacji (wysyłanie, kopiowanie, kosz, zmiany nazw, zapisy) wraz z ich stanem i postępem.
- **Motyw jasny, ciemny lub automatyczny** (jak w systemie), zapamiętywany.
- **Język angielski (domyślny) i polski** z przełącznikiem, zapamiętywanym.
- **Zmiana własnego hasła**; administrator może też ustawić nowe hasło innej osobie.
- **Sprawdzenie dysku sieciowego przed użyciem** — polecenie `selftest` mówi, czy podłączony udział (SMB/NFS) obsługuje wszystko, czego Filedeck potrzebuje, zanim zaczniesz na nim zapisywać.
- **Instalacja przez Docker Compose** z własnym certyfikatem HTTPS, dostępem z sieci lokalnej i automatyczną kontrolą stanu (status „healthy”).

### Improvements

- **Zabezpieczenia względem File Browser** — sprawdzone wszystkie 62 znane podatności oryginału. Te, które dotyczą funkcji obecnych w Filedeck, są zablokowane i pilnowane testami. Filedeck nie uruchamia poleceń systemowych i nie ma funkcji, z których wzięła się część dawnych podatności.
- **Nic nie jest nadpisywane po cichu** — przy wysyłaniu, kopiowaniu, przenoszeniu, zmianie nazwy i przywracaniu z kosza istniejący plik o tej samej nazwie zostaje nietknięty, a Ty dostajesz komunikat.
- **Pliki pojawiają się dopiero w całości** — ani inni użytkownicy, ani programy korzystające z tego samego folderu nie zobaczą niedokończonego pliku. Po awarii serwera nie zostają połówki plików.
- **Dysk nie zapełni się do zera** — Filedeck zostawia zapas wolnego miejsca i odmawia wysyłania lub kopiowania, zamiast zapchać dysk innym programom.
- **Zmiany kont działają natychmiast** — zmiana hasła, blokada konta lub zmiana uprawnień od razu kończy wszystkie sesje tej osoby.
- **Komunikaty zamiast sekcji pod listą plików** — operacje nie zajmują już miejsca pod tabelą. Krótkie powiadomienia znikają same po kilku sekundach, najwyżej 3 naraz, więc nie zasłaniają przycisków; resztę znajdziesz pod dzwonkiem.
- **Czytelniejsze akcje w wierszach** — ikony z podpowiedzią po najechaniu; zmiana nazwy ma własną, rozpoznawalną ikonę.
- **Dopracowany jasny motyw** z nową paletą kolorów.
- **Działa bez internetu** — ikony i wszystkie elementy strony są wbudowane w aplikację.
- **Konkretne komunikaty błędów** — mówią, co się stało i co zrobić (np. brak miejsca, plik zmieniony w międzyczasie, brak uprawnień, zły adres w konfiguracji).

### Bug Fixes

- Błędne hasło administratora przy tworzeniu konta lub zmianie hasła wyglądało jak wylogowanie. Teraz pojawia się komunikat „Wrong password”, a sesja trwa dalej.
- Wysyłanie folderu było odrzucane — teraz działa zarówno z przycisku, jak i przez przeciągnięcie.
- Plik przeciągnięty na okno otwierał się w przeglądarce zamiast się wysłać.
- Nowa instalacja z ustawionym użytkownikiem kontenera (`FILEDECK_USER`) nie startowała z błędem „permission denied”.
- Strona była nieosiągalna z innych komputerów w sieci, a kontener przed założeniem administratora restartował się w kółko. Teraz wystarczy ustawić adres w `.env`, a pierwszy start spokojnie czeka na założenie konta w przeglądarce.
- Po przeładowaniu strony zakończone wcześniej operacje nie wyskakują już ponownie jako nowe powiadomienia.
- Podpowiedzi przycisków w górnym pasku wychodziły poza ekran.
- Po zalogowaniu otwierała się pierwsza alfabetycznie przestrzeń, nawet jeśli była tylko do odczytu. Teraz otwiera się ta, w której możesz pracować.
