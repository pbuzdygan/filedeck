# Pierwszy etap implementacji

Powstał nowy projekt w [filedeck](../../filedeck/README.md), z niezależnym modułem Go i kodem napisanym od nowa.

Zrealizowano prototyp storage, uprawnień i uploadu, zgodnie z kierunkiem etapu B planu. Rozstrzygnięcia na ten etap:

- Linux/Docker; otwieranie przez `openat2`, bez symlinków i zagnieżdżonych mountów.
- Jedna przestrzeń i instancja; brak bazy na tym etapie.
- Upload wyłącznie nowego pliku, z publikacją no-replace. Nadpisywanie wymaga dalszego projektu wobec zmian zewnętrznych.
- Prywatny staging obok katalogu danych, na tym samym mountcie.
- Restart usuwa porzucony staging zamiast udawać możliwość wznowienia.
- Lokalne CLI jako narzędzie testowania; HTTP, sesje i UI pozostają następnym etapem.

[Kontrakt i ograniczenia](../../filedeck/docs/CONTRACT.md) precyzują semantykę uchwytów, zmiany zewnętrzne i warunki wdrożenia. [Stan realizacji i weryfikacja](../../filedeck/docs/PROGRESS.md) zawierają listę scenariuszy testowych oraz mapowanie do klas GHSA. Pełny audyt i weryfikacja pozostałych advisory nadal pozostają otwarte.
