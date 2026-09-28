# Filedeck branding

This folder holds the master copies of the Filedeck logo, icons and banners. The application uses copies of the icons in `internal/web/static/`; they must stay identical to the files here.

## Source logo

| File | Description |
|---|---|
| `ChatGPT Image Sep 28, 2026, 10_09_14 PM.png` | The logo on a navy background (mark and "Filedeck" wordmark), 1254×1254 — the source of every derived file |
| `ChatGPT Image Sep 28, 2026, 10_10_36 PM.png` | The same logo with a transparent background, 1254×1254 |

## Derived files

The mark and the wordmark were cropped from the navy version and blended into a matching navy background.

| File | Use |
|---|---|
| `filedeck-banner.png` | Banner 1500×500 (3:1): mark and wordmark side by side — README, sign-in screen, website |
| `filedeck-social.png` | 1280×640 (2:1) — GitHub social preview (Settings → Social preview) |
| `favicon.ico` | Favicon with 16, 32 and 48 px images (`/favicon.ico` in the app) |
| `favicon-16.png`, `favicon-32.png`, `favicon-48.png` | The same favicons as separate PNGs |
| `icon-64.png` | Logo in the application header |
| `icon-192.png`, `icon-512.png` | PWA icons (rounded tile) |
| `icon-maskable-512.png` | PWA maskable icon (full square, the mark inside the safe zone) |
| `apple-touch-icon.png` | iOS/iPadOS home-screen icon, 180×180 (square; the system rounds the corners) |

## In the application

`internal/web/static/` contains `filedeck-banner.png` (shown on the sign-in and first-start screens), `favicon.ico`, `icon-64.png`, `icon-192.png`, `icon-512.png`, `icon-maskable-512.png`, `apple-touch-icon.png` and `manifest.webmanifest`. With them Filedeck can be installed as an app (Chrome, Edge, Android) and added to the home screen on iOS/iPadOS. `TestIconsAndManifest` checks that the manifest is valid and every referenced icon is embedded.

When an icon changes, replace it here first, then copy it to `internal/web/static/` under the same name.
