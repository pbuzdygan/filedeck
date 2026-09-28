# Tabler Icons

Selected outline icons from [Tabler Icons](https://tabler.io/icons) (package `@tabler/icons` 3.48.0, MIT, see LICENSE),
copied unmodified to `internal/web/static/ti-<name>.svg` and embedded in the binary — nothing is loaded from the internet.

Icons: alert-circle, arrow-back-up, arrow-left, arrows-move, bell, checkbox, chevron-left, chevron-right, circle-check, copy, device-desktop, device-floppy, dots, download, drag-drop, external-link, eye, file-code, file-plus, file-text, file-type-pdf, file-zip, file, folder-plus, folder-up, folder, forms, home, key, language, link-off, link, list-check, loader-2, lock, logout, menu-2, moon, movie, music, pencil, photo, player-stop, refresh, search, server-2, share, shield-lock, sun, trash-x, trash, upload, users, x.

To add one: copy `icons/outline/<name>.svg` from the same package version as `internal/web/static/ti-<name>.svg`
and add an `.ic-<name>` rule in `app.css`.
