# Changelog

Changes to Filedeck described from the user's point of view: what you will see, what you can use, what changes in your work. Technical details and tests: [docs/PROGRESS.md](docs/PROGRESS.md), security: [docs/SECURITY.md](docs/SECURITY.md).

## 0.1.0

Filedeck is a web file browser written from scratch as a successor to File Browser — built so that you cannot accidentally lose data or get around permissions.

### New Features

- **First start in the browser** — on the first start, the page asks for a one-time code from the server log and lets you create the administrator account without a command line.
- **Multiple spaces** — next to "My files" you see folders shared by the server (e.g. network drives), each as a separate space. Read-only spaces are marked and show no buttons for changes.
- **Accounts and per-space permissions** — the administrator creates accounts and decides, separately for each space, who can browse it, download files, add new ones and change existing ones.
- **Upload files and whole folders** — with a button or by dragging onto the window or onto a specific folder. The subfolder structure is kept, and an interrupted upload of a large file can be resumed.
- **Preview** of photos, videos, music, PDFs and text files, with arrow keys to move to the previous and next file.
- **Text file editor** (e.g. txt, md, yaml) — Ctrl+S saves, unsaved changes are visible, and the previous version goes to the trash. If someone changed the file in the meantime (e.g. over a network share), your changes will not overwrite their version — you can save them as a new file.
- **New text file and new folder** with one click.
- **Copy and move** files and folders, also between spaces. Runs in the background, shows progress and can be cancelled.
- **Trash** — deleted items can be restored, also under a different path. They disappear on their own after the retention period; only an administrator can delete them permanently.
- **Rename** files and folders.
- **Select multiple items** — checkboxes, Ctrl+click, Shift+click (range), Esc clears the selection. Selected items can be copied, moved, trashed or downloaded at once.
- **Search by name** in the current folder and its subfolders. Clicking a result opens the folder the file is in, and its preview.
- **Sorting** by name, size and modification date (your choice is remembered), plus a modification date column.
- **Public links** — share a file or a whole folder with someone who has no account: choose how long the link lasts (from an hour to a year) and whether it needs a password, then copy it with one click. The recipient sees a simple page with a download button or the folder's file list. Nothing can be changed through a link, and it cannot reach outside the shared folder.
- **List of shared links** — see all your links in one place: until when they are valid, which have a password and which have stopped working, and revoke any of them instantly. The administrator sees every user's links.
- **Bell with history** of recent operations (uploads, copies, trash, renames, saves) with their status and progress.
- **Light, dark or automatic theme** (following the system), remembered.
- **English (default) and Polish** with a language switch, remembered.
- **Change your own password**; the administrator can also set a new password for someone else.
- **Delete users** — the administrator can remove an account from the Users panel. The person is signed out at once and their public links stop working; files they uploaded stay where they are. Your own account and the last administrator cannot be deleted.
- **New logo and an installable app** — Filedeck has its own logo (a stacked "F" of file cards) in the browser tab, in the header and on the home screen. It can be installed as an app from Chrome, Edge or Android, and added to the home screen on iPhone and iPad with its own icon.
- **Two-factor authentication** — everyone can protect their account with a code from an authenticator app on their phone (Google Authenticator, Microsoft Authenticator, 2FAS, Aegis, Bitwarden and others). You turn it on yourself in the menu under "Two-factor authentication" by scanning a QR code, and you can set it up again on a new phone, get new recovery codes or turn it off whenever you like. Signing in then asks for the code after your password. You also get ten one-time recovery codes for when your phone is lost. If someone loses both, the administrator can turn it off for them. With an optional secret key in the settings, the codes are stored encrypted, so even a stolen backup does not reveal them.
- **Check a network drive before use** — the `selftest` command tells you whether a mounted share (SMB/NFS) supports everything Filedeck needs, before you start writing to it.
- **Installation with Docker Compose** with its own HTTPS certificate, access from the local network and an automatic health check ("healthy" status).
- **Ready-made images to download** — every release goes to the image registry in a separate channel: stable (`latest`, version number) and development (`dev_latest`, `dev` + number). Updating a server means pulling a new image instead of building it on the spot. Images run on regular servers (x86-64) as well as on ARM (e.g. Raspberry Pi 4/5, ARM servers).

### Improvements

- **Protection compared to File Browser** — all 62 known vulnerabilities of the original were reviewed. Those that concern features present in Filedeck are blocked and guarded by tests. Filedeck does not run system commands and does not have the features some of the old vulnerabilities came from.
- **Nothing is overwritten silently** — when uploading, copying, moving, renaming or restoring from the trash, an existing file with the same name stays untouched and you get a message.
- **Files appear only when complete** — neither other users nor programs using the same folder will ever see an unfinished file. A server crash leaves no half-written files behind.
- **The disk never fills up completely** — Filedeck keeps a reserve of free space and refuses an upload or copy rather than filling the disk for other programs.
- **Links do not survive changes** — a link stops working when the file is moved, renamed or deleted (also through a network drive or another program) and when its owner loses access to that place. A new file with the same name will not be shared by accident through an old link.
- **Account changes take effect immediately** — changing a password, disabling an account or changing permissions ends all of that person's sessions at once.
- **Notifications instead of sections under the file list** — operations no longer take up space below the table. Short notifications disappear on their own after a few seconds, at most 3 at a time, so they do not cover buttons; the rest is under the bell.
- **Ready to be published on the internet behind a reverse proxy** — someone guessing passwords no longer blocks signing in for everyone else: limits now apply to each visitor separately, also behind a proxy. The browser is told to always use HTTPS for Filedeck.
- **Security log** — sign-ins, failed attempts, account changes and shared links are recorded in the server log with the visitor's address (never passwords or codes), so you can see who tries to get in and let tools such as fail2ban or CrowdSec block them.
- **"My files" always first** — among the space tabs, "My files" is always on the left, set apart by a separator; the other spaces follow in alphabetical order.
- **Long file names no longer break the list** — very long names are cut with "…" (the full name appears when you point at it), so the size, date and action icons always stay in one tidy row, and the list uses more of the screen width.
- **A welcoming sign-in screen** — the Filedeck banner sits on top of the sign-in and first-start forms, in both the light and the dark theme and on phones.
- **A tidy top bar on smaller windows** — on tablets and narrower laptop windows the account links fold into the "☰" menu instead of wrapping over several rows.
- **Comfortable on phones** — the top bar fits in one row (logo, language, theme, notifications and a menu with the rest), the file tools take two compact rows of icons, and each file has a single "⋯" button that opens all its actions at the bottom of the screen. Trash and shared links show as easy-to-read cards.
- **Clearer row actions** — icons with a tooltip on hover; rename has its own recognisable icon.
- **Polished light theme** with a new colour palette.
- **Works without internet** — icons and all page elements are built into the application.
- **Specific error messages** — they say what happened and what to do (e.g. no space left, file changed in the meantime, no permission, wrong address in the configuration).

### Bug Fixes

- A wrong administrator password when creating an account or changing a password looked like a logout. Now a "Wrong password" message appears and the session continues.
- Uploading a folder was refused — it now works both from the button and by dragging.
- A file dragged onto the window opened in the browser instead of being uploaded.
- A new installation with a container user set (`FILEDECK_USER`) failed to start with "permission denied".
- The page could not be reached from other computers on the network, and the container kept restarting before an administrator was created. Now it is enough to set the address in `.env`, and the first start calmly waits for the account to be created in the browser.
- After reloading the page, operations finished earlier no longer pop up again as new notifications.
- Downloads of large files broke off after about a minute on slower connections. Now a download lasts as long as data keeps flowing.
- Rows in the trash had misaligned table lines.
- Trying to demote, disable or delete the last administrator showed a misleading "name already exists" message. It now says clearly that the last administrator must stay.
- On phones, tapping the sign-in fields zoomed the page in, and it had to be zoomed out by hand. The page now stays as it is.
- On touch screens, a tooltip stayed on the screen after tapping a button.
- Tooltips of the buttons in the top bar went off-screen.
- After signing in, the alphabetically first space opened, even when it was read-only. Now the one you can work in opens.
