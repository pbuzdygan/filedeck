# Filedeck — analiza przed nową implementacją

Stan analizy: 2026-09-28. Poniższa analiza poprzedza implementację. Powstał już [pierwszy prototyp rdzenia](06-implementation.md); jego testy i ograniczenia opisano osobno.

## Wniosek

Warto odtworzyć funkcje File Browser na nowym rdzeniu. Największą korzyść daje uproszczenie modelu dostępu, cyklu uploadu, sesji i udostępniania. Zmiana języka albo frameworka sama nie usuwa przyczyn wykrytych błędów.

Nowa implementacja ma zachować potrzeby użytkownika: pracę na katalogach, upload, pobieranie, edycję, wyszukiwanie, podgląd, konta i udostępnianie. Nie zakładamy zgodności starego API, bazy ani wszystkich opcji konfiguracji. Usunięcie lub odroczenie funkcji w poniższym planie to rekomendacja, nie zaakceptowana przez właściciela decyzja.

## Materiał i ograniczenia

- Repo referencyjne: `../../reference`, origin `git@github.com:pbuzdygan/filebrowser.git`.
- Badany HEAD: `833d908884d5c801f30f5c098d7977177eb3a36b`, data commita 2026-07-28, `docs: update post link`.
- Drzewo referencyjne było czyste. `git describe --tags --always` zwraca sam hash; nie przypisujemy automatycznie numeru wydania. Nie porównano HEAD z aktualnym zdalnym masterem upstreamu.
- Źródła: backend Go, trasy HTTP, auth, filesystem, upload, share, reguły, wybrane testy, frontend auth i edytor, manifesty zależności, Dockerfile i CI.
- Pobrano wszystkie 62 publiczne advisory zwrócone przez API upstreamu: 5 critical, 27 high, 24 medium, 6 low. 19 nie ma wskazanej wersji poprawionej w polu `patched_versions`. **To nie oznacza 19 potwierdzonych podatności tego checkoutu**: metadane, konfiguracja i kod wymagają osobnej interpretacji.
- Nie uruchamiano aplikacji, PoC, testów ani skanerów zależności. Potwierdzenie mechanizmu w kodzie jest odróżnione od demonstracji ataku. Nie jest to zakończony audyt bezpieczeństwa ani gwarancja kompletności.
- Publiczne advisory nie obejmują zgłoszeń prywatnych ani nieujawnionych podatności.

## Dokumenty

1. [Funkcje i propozycja zakresu](01-functions.md).
2. [Ustalenia z kodu](02-findings.md).
3. [Architektura i model zagrożeń Filedeck](03-design.md).
4. [Kolejność realizacji i warunki odbioru](04-roadmap.md).
5. [Rejestr wszystkich 62 advisory](05-advisories.md).
6. [Surowy zapis API, z opisami i metadanymi](sources/upstream-advisories.json).

## Decyzje wpływające na dalszą implementację

Użytkownik potwierdził: **Linux i Docker**, **istniejące katalogi zmieniane również przez inne aplikacje lub SMB/NFS**. To wymagania projektu. Trzeba jeszcze rozróżnić lokalny filesystem eksportowany przez SMB/NFS od mountu sieciowego po stronie Filedeck i ustalić testowane konfiguracje; nie deklarujemy automatycznie wsparcia zapisu na każdym SMB/NFS.

Do ustalenia dalej: liczba użytkowników i wzajemne zaufanie; dostęp z internetu; lokalne konta czy OIDC; potrzeba anonimowych linków; maksymalne wielkości plików i katalogów; wymagania migracji. Nie blokuje to analizy mechanizmów bezpieczeństwa, ale wpływa na szczegóły kontraktów i testów.
