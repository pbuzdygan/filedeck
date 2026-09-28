# Kolejność prac i warunki odbioru

## A. Domknąć analizę i kontrakty

Przed kodem aplikacji ustalić platformy, model zmian zewnętrznych, role, obsługiwane operacje i semantykę udziałów. Dla każdego advisory z rejestru nadać ostateczny status: mechanizm obecny / poprawiony w badanym kodzie / zależny od konfiguracji / nie dotyczy / wymaga dowodu. Obecny rejestr celowo nie udaje ukończonej weryfikacji.

Oczekiwane artefakty: zaakceptowana mapa funkcji, model zagrożeń, macierz praw, kontrakty błędów i limity, decyzje o wspieranych filesystemach. Uwzględnić przegląd niewymienionych w advisory powierzchni: admin, import, frontend, zależności, CI i wydania.

## B. Prototyp rdzenia bezpieczeństwa

Mały prototyp operacji filesystemu i uploadu, bez rozbudowanego UI. To właściwy kolejny krok po decyzjach, nie przepisywanie komponentów wizualnych.

| Test kontraktu | Wynik wymagany |
|---|---|
| `..`, podwójne kodowanie, slash/backslash, ścieżka absolutna | Odrzucenie lub jednoznaczna interpretacja zgodna z kontraktem; zero wyjścia poza przestrzeń |
| Symlink wewnętrzny/zewnętrzny/dangling, równoległa podmiana katalogu | Brak odczytu/zapisu poza dozwoloną granicą i brak obejścia polityki symlinków |
| FIFO/socket/device w odczycie, podglądzie i ZIP | Szybkie odrzucenie bez zawieszenia workera |
| Nieprawidłowy upload na istniejący plik/katalog | Oryginalne dane niezmienione; cleanup dotyczy wyłącznie staging |
| Dwa PATCH z tym samym offsetem, dwa commity na ten sam cel | Serializacja lub konflikt; brak podwojonych danych i zdarzeń finalizacji |
| Utrata sieci, brak miejsca, restart w każdym stanie uploadu | Stan odzyskiwalny; brak usunięcia cudzych danych |
| Odebranie praw podczas uploadu | Finalizacja odmówiona zgodnie z ustaloną semantyką |
| Zewnętrzna zmiana pliku podczas edycji | Konflikt lub jawny kontrakt ograniczeń; bez obietnicy CAS opartej jedynie na stat |

Testy muszą sprawdzać stan danych i efekt uboczny, nie tylko status HTTP. Kontrolowane wyścigi z barierami są bardziej miarodajne niż samo wielokrotne uruchomienie testu. `go test -race` wykrywa wyścigi pamięci, nie dowodzi braku TOCTOU filesystemu.

## C. Działająca podstawa

Konta lokalne, sesje, przestrzenie, listowanie, pobieranie i upload. Przed odbiorem:

- Stary token/cookie odrzucony po logout, resecie hasła i blokadzie konta.
- Żądania CSRF i obce identyfikatory nie zmieniają danych.
- Wszystkie drogi odczytu respektują tę samą macierz praw.
- Limity utrzymane przy wielu użytkownikach, także po przerwaniu transferu.
- Backup i odtworzenie przećwiczone na danych testowych.

## D. Pełna praca na plikach

Kopiowanie, przenoszenie, usuwanie, edycja, wyszukiwanie, ZIP i wznowienia. Kryteria: jawne zachowanie konfliktów i częściowych operacji, testy anulowania, poprawne nazwy wpisów ZIP (także odbiorca Windows), ograniczony koszt dużych katalogów. Paginacja nie gwarantuje taniego sortowania wielkiego katalogu — potrzebny pomiar albo indeks.

## E. Udziały i podglądy

Dodać po zatwierdzeniu semantyki zasobów. Obowiązkowe scenariusze: udział użytkownika A, delete/rename/replace przez B; ponowne utworzenie dawnej ścieżki; zmiana poza aplikacją; wygaśnięcie i odwołanie; odebranie właścicielowi praw; próba wyjścia poza udostępniony katalog. XSS sprawdzić w prawdziwej przeglądarce; parsery testować z dużymi i uszkodzonymi plikami.

## F. Migracja i wydanie

Import w trybie dry-run na kopii starej bazy, raport wszystkich nieprzeniesionych reguł i konfliktów. Żadnego automatycznego poszerzania praw dla „zgodności”. Stare sesje i linki domyślnie wygasają; migracja haseł wymaga osobnej oceny zgodności. Wycofanie wdrożenia musi uwzględniać zmiany plików, nie tylko przywrócenie bazy.

CI: testy kontraktów i integracyjne na wspieranych platformach, fuzzing ścieżek i parserów, race detector, kontrola zależności i sekretów, powtarzalny build z lockfile. Testy starych podatności są źródłem scenariuszy; nowe testy piszemy pod kontrakty Filedeck. Przed publicznym wydaniem niezależny przegląd bezpieczeństwa krytycznego rdzenia i dokumentacja ograniczeń.

## Reguła zamknięcia advisory dla Filedeck

Każde GHSA musi mieć wskazany mechanizm, decyzję projektową oraz test z wynikiem albo uzasadnienie „funkcja nie występuje”. Status „nie dotyczy” ponownie otwieramy, gdy funkcja zostaje dodana. Sam upgrade biblioteki, wyłącznik w UI lub zmiana nazwy projektu nie zamyka zagrożenia.
