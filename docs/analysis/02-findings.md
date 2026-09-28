# Ustalenia z kodu referencyjnego

Status „mechanizm obecny” oznacza analizę statyczną badanego HEAD, nie wykonany PoC. Numery GHSA odsyłają do pełnego [rejestru](05-advisories.md). Ryzyka poniżej są pogrupowane według przyczyny; nie należy sumować ich jako niezależnych podatności.

## Mechanizmy wymagające przeprojektowania

| Obszar | Dowód w kodzie | Ocena i warunki | Konsekwencja dla Filedeck |
|---|---|---|---|
| Sprzątanie po uploadzie | `http/resource.go:133`, błąd `writeFile` prowadzi do `RemoveAll` w linii 183 | Mechanizm GHSA-c4fr-5f24-4wrj obecny. Upload na istniejący katalog z override; potrzebne Create i Modify, brak sprawdzenia Delete w cleanup | Usuwamy wyłącznie staging należący do uploadu, nigdy żądany cel |
| Polityka ścieżek a symlinki | `http/data.go` sprawdza tekst ścieżki; `files/scoped.go` dopuszcza linki pozostające w scope | Mechanizm GHSA-7w29-q235-57m9 obecny. Alias w scope może wskazywać cel zakazany regułą | Jednorodna polityka przestrzeni, domyślny zakaz przechodzenia symlinków |
| Równoległe TUS PATCH | `http/tus_handlers.go`, `tusPatchUpload`: stat, kontrola offsetu, otwarcie z append, zapis | GHSA-4r8p-gqj2-mwgm: brak serializacji całego cyklu; limit per request nie jest limitem uploadu | Blokada uploadu obejmująca kontrolę offsetu, zapis i aktualizację stanu |
| WebSocket poleceń | `http/commands.go`: Upgrade i ReadMessage przed EnableExec i Execute, brak SetReadLimit | GHSA-39cx-23x9-5c8p: mechanizm obecny. Wyłączenie exec nie usuwa kosztu buforowania dla zalogowanego klienta | Nie rejestrować nieaktywnych funkcji; autoryzować przed upgrade/read |
| Konwersja napisów | `http/subtitle.go`, `subtitleFileHandler`: ReadAll, parser, wynik w bytes.Buffer | GHSA-448h-jr2h-3vhp: mechanizm obecny; uprawniony odczyt dużego pliku może kosztować wiele kopii w RAM | Ograniczyć wejście, wynik, czas i liczbę konwersji |
| Pliki specjalne w ZIP | `http/raw.go`, `rawHandler` ma kontrolę named pipe, `getFiles` nie filtruje typów przed Open | GHSA-8q5j-8wcr-8v2v: mechanizm obecny. FIFO może pochodzić z lokalnego storage | Tylko zwykłe pliki i katalogi; bez blokującego otwierania FIFO |
| Udziały po usunięciu | `http/resource.go:110`, `storage/bolt/share.go`, filtr userID | GHSA-r6pg-pg54-rcr5: cleanup ograniczony do udziałów kasującego, mimo wspólnego pliku | Unieważnienie udziałów zasobu niezależne od właściciela udziału |
| Udziały po rename | `http/resource.go`, `patchAction` usuwa cache, przenosi plik, nie odwołuje udziałów | GHSA-m8v4-4w34-rrvf: stara ścieżka może później ujawnić nową treść | Jawna semantyka tożsamości i generacji udostępnienia |
| Sesje | `http/auth.go`, `frontend/src/utils/auth.ts:118`: wylogowanie usuwa stan klienta | GHSA-7xwp-2cpp-p8r7 i GHSA-v7vv-5wj2-gfcj: brak serwerowego odwołania tokenu; odczyt aktualnych praw z bazy nie unieważnia uwierzytelnienia | Sesja z losowym sekretem, rekord serwerowy, logout/reset odwołują sesje |
| Token dostępny dla JS | `frontend/src/utils/auth.ts:13`: cookie z JS i localStorage | Zwiększa skutki XSS; nie jest samodzielnym dowodem XSS | Cookie HttpOnly, Secure, SameSite; osobna ochrona CSRF |
| Runner i hooki | `runner/parser.go`, `runner/runner.go`, `http/commands.go` | Shell, podstawianie zmiennych, podkomendy i dostęp UID procesu; zależy od konfiguracji. Allowlista nazw nie daje izolacji | Usunąć wykonawcę z rdzenia lub wydzielić z rzeczywistą izolacją |
| Proxy auth | `auth/proxy.go`, `ProxyAuth.Auth` ufa nagłówkowi | GHSA-xqp3-jq6g-x3qm: bezpośredni dostęp klienta do tego trybu przekracza granicę zaufania | Preferować OIDC; proxy tylko jawnie zaufany peer, kontrola po stronie backendu |
| Domyślny zakres nowych kont | `http/auth.go`, `settings/dir.go`, `settings/defaults.go` | GHSA-6759-996p-gpj6: przy wyłączonym CreateUserDir defaults mogą nadal nadać wspólny root | Brak automatycznego dziedziczenia zakresu administratora |

## Poprawki już widoczne — nie przedstawiać ich jako brakujących

- `withUser` pobiera aktualnego użytkownika z bazy, więc nie polega wyłącznie na kopii uprawnień zapisanej w JWT.
- `renewableErr` wymaga potwierdzenia tożsamości proxy dla wygasłego JWT; istnieje `TestExpiredTokenNeedsProxyAssertion`. GHSA-v3jv-rmh2-635j ma wskazaną poprawkę upstreamu.
- `checkDescendants` i `TestRecursiveOperationsEnforceDescendantRules` adresują GHSA-77x8-73f4-5485 na normalnych ścieżkach copy/rename/delete. Nie obejmuje to cleanup uploadu.
- TUS sprawdza ujemny Upload-Length i limituje bajty pojedynczego PATCH. To nie dowodzi odporności na równoległe PATCH.
- Signup usuwa Admin i Execute; provisioning ma kontrolę kolizji zakresów. Nie usuwa to problemu wspólnego root przy innej konfiguracji.
- `ScopedFs` sprawdza także dangling symlinks; publiczne udziały mają odrębne ograniczenie katalogu i testy symlinków.
- `shareResponse` nie serializuje hasha hasła i bypass tokenu. Osobno pozostaje mechanizm tokenu w URL w `public.go`.
- `files/file.go:264` ma limit 10 MiB dla klasyfikacji pliku jako tekst. Stary raport o dowolnie dużym tekście nie jest automatycznie aktualny; pozostaje pytanie o zmianę rozmiaru między stat i ReadFile oraz liczbę równoległych odczytów.
- Markdown używa DOMPurify. Obecność `v-html` nie jest sama dowodem XSS.
- Dockerfile ustawia nieuprzywilejowanego użytkownika; CI zawiera `go test --race`. Te zabezpieczenia należy zachować jako wymagania, nie uznawać za brakujące.

## Dodatkowe obserwacje wymagające osobnych testów

1. **TOCTOU w ScopedFs.** `guard()` wykonuje EvalSymlinks, po czym osobne `base.Open/OpenFile/...` ponownie rozwiązuje ścieżkę. Przy równoległej zmianie drzewa istnieje okno wyścigu. Potrzebny kontrolowany test z procesem podmieniającym link/katalog; nie zgłaszamy tu nowego potwierdzonego exploita. Mechanizm tej klasy opisuje [Go: traversal-resistant file APIs](https://go.dev/blog/osroot).
2. **Efekt przed walidacją TUS POST.** `tusPostHandler` otwiera cel i może wykonać O_TRUNC przed `getUploadLength`. Statycznie widać możliwość zmiany istniejącego pliku przed odrzuceniem błędnego nagłówka. Test ma sprawdzić niezmienność danych dla błędnego i ujemnego rozmiaru. Nie przypisujemy tego automatycznie istniejącemu GHSA.
3. **Limity zbiorcze.** Rekursywne listowanie zbiera wyniki w tablicy; sprawdzanie Context pomaga przy anulowaniu, ale nie ogranicza maksymalnego wyniku. Należy zmierzyć duże drzewa i równoległe żądania.

## Co nadal wymaga audytu

Pełna analiza frontendowych parserów, wszystkich handlerów administracyjnych, zależności transytywnych i lockfile, konfiguracji proxy, migracji/importu, cache Redis, logów, sekretów i łańcucha publikacji. Rejestr advisory jest kompletnym pobraniem publicznych metadanych, nie kompletnym potwierdzeniem każdego scenariusza na lokalnym kodzie.
