# deckman

*[Español](README.md) · **English** · [Français](README.fr.md)*

Manage your Steam Deck's games from your computer: see what is installed, send
Windows games and ROMs, change artwork, move games between the internal SSD and
the microSD, and free up space.

![deckman's library: the games installed on the Deck, with their cover, their
real size and where they live](docs/capturas/biblioteca.webp)

It is **a single executable**. Double-click it and the interface opens in your
browser. Nothing to install, neither on the PC nor on the Deck.

- `deckman.exe` — Windows
- `deckman` — Linux (plain binary or **Flatpak**, see below)

**[Download the latest version](https://github.com/jfrmorales/deckman/releases/latest)**
— every release ships its binaries under *Releases*, with a `SHA256SUMS` to
check them:

```sh
sha256sum -c SHA256SUMS
chmod +x deckman-*-linux-amd64
```

Since 0.6.2 that `SHA256SUMS` is **signed**. The `sha256sum` above tells you the
download is not corrupt; the signature also tells you it was published by me and
not by someone who got hold of the release credentials:

```sh
cosign verify-blob --key cosign.pub --bundle SHA256SUMS.bundle SHA256SUMS
```

It has to say `Verified OK`. `cosign.pub` and `SHA256SUMS.bundle` ship with the
same release; the public key is always the same one, so if you keep it the
first time, you will notice if it ever changes. The signature is also recorded
in Sigstore's public transparency log, which is what timestamps it.

There is also a `deckman-<version>.cdx.json`: the inventory of what went into
the binary, in case you ever need to tell whether a security advisory affects
you.

The interface speaks **Spanish, English and French**. It follows your browser
by default, and there is a selector in the top bar.

---

## What it does

| | |
|---|---|
| **Library** | Every game, Steam and non-Steam, with its cover, its real size, where it lives and how much the Proton prefix and the shader cache take up. Clicking the cover opens the artwork gallery. |
| **Send game** | Copies a Windows game folder to `~/Games` and registers it in Steam, with the Proton version you choose. |
| **Autodetection** | When you pick the folder it works out which game it is, which `.exe` starts it and which Proton suits it. |
| **Artwork** | A SteamGridDB gallery to choose cover, hero, logo and icon for any game, with a preview. Applied instantly. |
| **Emulation** | Copies ROMs to the right EmuDeck system folder; lists what is already in each system — only the ones that actually have ROMs, not EmuDeck's 181 folders — so you can rename or delete it; and downloads ROMs by URL (the Deck does the downloading, not the PC) with an archive.org search narrowed to the chosen system. |
| **ROM artwork** | Looks up box art, title screen and screenshot for each ROM on libretro-thumbnails and puts them where ES-DE looks for them. No key, no sign-up. |
| **Move** | Moves a game between the internal drive and the microSD without downloading it again. Non-Steam games too: it takes the folder across and updates the shortcut. |
| **Clean up** | Uninstalls games and deletes the Proton prefix or the shader cache separately. |

---

## What it looks like

The first screen asks for the Deck's IP and the password, and only the first
time: from then on it gets in with its own SSH key.

![deckman's connection screen](docs/capturas/conexion.webp)

When you pick a Windows game folder, deckman works out which game it is, which
`.exe` starts it and which Proton version suits it:

![Sending a game, with the folder already analysed](docs/capturas/enviar-juego.webp)

Clicking the cover opens the SteamGridDB gallery — vertical cover, horizontal
cover, hero, logo and icon. Applied instantly, without restarting Steam:

![Choosing artwork in the SteamGridDB gallery](docs/capturas/anim-caratulas.webp)

On the emulation side you see what is already in each EmuDeck system — only the
systems that actually have ROMs — so you can rename or delete it:

![The ROM collection manager](docs/capturas/emulacion-gestionar.webp)

And there is an archive.org search narrowed to the chosen system. The Deck does
the downloading, not the PC:

![Searching archive.org for ROMs from deckman](docs/capturas/anim-emulacion.webp)

The library filters by name, and can show only Steam games or only non-Steam
ones:

![Filtering the library](docs/capturas/anim-biblioteca.webp)

The interface in the screenshots is in Spanish; it also speaks English and
French, and follows your browser by default.

---

## Before you start

On the Deck, just once:

1. Desktop mode.
2. **System Settings → Desktop → Sharing → SSH**, turn it on.
   (Or in a terminal: `sudo systemctl enable --now sshd`.)
3. If you have never set a password for the `deck` user, set one with `passwd`.
   SteamOS ships without one and SSH will not let you in that way.

## Use

Run the binary. The interface opens **in a window of its own**, like any
desktop application:

- **Windows**: native window (WebView2, the Edge engine, which comes with
  Windows 10 and 11). Closing the window quits deckman.
- **Linux**: an app window of whichever Chromium browser you have (Chrome,
  Chromium, Brave, Edge, Vivaldi; Flatpak installs too). Closing the window
  quits deckman — unless a transfer is running, which finishes in the
  background before shutting down.
- If none of that is available, it opens as a tab in your usual browser
  (`http://127.0.0.1:8777`) and deckman is closed with the **Quit** button.

The first screen is the connection one: the Deck's IP, the password, and under
**Advanced options** the user, the SSH port if they are not the defaults
(`deck` and `22`) and a name to tell Decks apart. That screen also walks you
through turning SSH on if it is your first time.

**There is no default password.** `deck` is the usual SteamOS user name, not a
password. On connecting, deckman generates its own SSH key and leaves it
installed on the Deck, which is why it never asks again. The password is **not
stored anywhere**. The key shows up as `deckman@<your-pc>` in the Deck's
`~/.ssh/authorized_keys`.

### Several Decks

The Decks you connect to are remembered and listed on the connection screen:
one click to enter any of them, and **+ Add another Deck** to register a new
one. They all share the same SSH key, which identifies this PC.

**Forget** does what it says: besides removing it from the list, it **withdraws
the SSH key from that Deck**, so this PC loses access. If the Deck is off it
cannot be withdrawn right then, and deckman says so instead of keeping quiet —
it tells you which line to delete by hand. Forgetting the last Deck also
deletes this PC's key.

```
deckman -port 9000      # another port
deckman -browser        # browser tab instead of its own window
deckman -no-browser     # do not open the interface (server only)
deckman -version        # (on Windows it prints nothing: the .exe has no console)
```

If deckman is opened twice, the second one finds the first and opens another
window against it, without duplicating the server.

Settings are stored in `~/.config/deckman` (Linux) or `%AppData%\deckman`
(Windows).

To quit, closing the window is enough; the **Quit** button in the top bar does
the same and is the only way when the interface runs in a tab.

---

## Flatpak (Linux)

On Linux deckman can also be installed as a Flatpak, with an icon in the
application menu. Grab the `.flatpak` from
**[the latest release](https://github.com/jfrmorales/deckman/releases/latest)**
and:

```sh
flatpak install deckman-0.2.3.flatpak
```

On installing, flatpak warns that the origin is not signed: that is normal for
a file downloaded by hand. The release's `SHA256SUMS` is there to check it is
the right one.

If you would rather build it yourself (podman or docker needed):

```sh
make flatpak
```

That script builds the binary (with `./build.sh`, in a container), builds the
package with `org.flatpak.Builder` (itself a Flatpak: nothing is installed on
the system) and leaves it installed for the user. Then:

```sh
flatpak run io.github.jfrmorales.deckman     # or from the menu: deckman
```

Sandbox details:

- **Config**: goes to `~/.var/app/io.github.jfrmorales.deckman/config/deckman`.
  The first time, if `~/.config/deckman` exists (from the plain binary), it is
  copied across: the connection, the SSH key and the SteamGridDB key are kept.
- **Disks**: the browser sees your home, `/run/media`, `/media` and `/mnt`
  read-only. If you keep games somewhere else, grant access with
  `flatpak override --user --filesystem=/your/path:ro io.github.jfrmorales.deckman`.
- **Window**: opened by the host's Chromium browser through `flatpak-spawn`
  (the manifest's `--talk-name=org.freedesktop.Flatpak` permission). With no
  compatible browser, it falls back to a tab through the portal.
- If launched twice, the second instance finds the first and points the browser
  at it instead of starting another server.

---

## Sending a game

You choose the folder and deckman tries to work out the rest on its own:

1. **The Steam id, from the folder itself.** Many repacks leave a
   `steam_appid.txt` or a `steam_emu.ini` with the real app id. That is exact
   and instant, and does not depend on guessing the name right.
2. **Failing that, by name.** It cleans up the folder name
   (`Resident.Evil.4.REPACK-FitGirl` → `Resident Evil 4`) and looks it up in
   the Steam store. If it guesses wrong, you pick another result.
3. **The executable.** It ranks the `.exe` files by size, similarity to the
   name and where they sit, and throws out the junk: uninstallers,
   `vc_redist`, crash reporters, trainers, language pickers. It needs the care:
   in Resident Evil 4 the second largest `.exe` is `CrashReport.exe`, at 151 MB.
4. **The Proton version**, from what ProtonDB reports. *Platinum* and *gold* go
   to Proton Experimental; *silver*, *bronze* and *borked* to GE-Proton if you
   have it installed.
5. **The artwork**, if you have set up SteamGridDB.

All of it is a proposal: the fields are filled in but editable. If a service
does not answer, the transfer goes ahead anyway.

**Transfers can be resumed**: if a copy is interrupted, start it again and it
skips whatever is already there.

---

## Artwork

It comes from **SteamGridDB**, which needs a free key: go to `steamgriddb.com`
→ your profile → *Preferences* → *API*, and paste it into **Send game →
Artwork settings**. It is only stored on this PC. Without it everything else
works the same, but the game shows a grey box.

Two ways to use it:

- **Automatic** when sending a new game: it takes the first of each type.
- **Choosing yourself**: click the **cover** on any library row. Tabs for
  vertical cover, hero, background, logo and icon, with a green dot on the ones
  that already have an image. A click opens a **large preview** with the
  dimensions, the author and the file size; from there you apply or cancel.

The library thumbnail is the one Steam shows: first the art you chose yourself,
otherwise the store cover. A non-Steam game with no art shows its initial in a
box.

It works with **any** game, Steam or not. For Steam games the match is exact;
for non-Steam ones it searches by name and you can correct it with the
drop-down at the top right.

Watch out for repeated titles: "Resident Evil 4" is two different games on
SteamGridDB, the 2005 one and the 2023 remake, and the search returns the old
one first. If you see little art or no animated ones, check that drop-down.

**Animated**: the *Include animated* checkbox brings them in (covers and
backgrounds; logos and icons have none) and they come first because they are a
minority. They are heavy: an animated 3840×1240 background is around 45 MB.

**They show up instantly, without restarting Steam.** The only exception is
icons, which Steam cannot change while running.

---

## The folder browser

Because the browser's own picker never hands over a folder's real path, deckman
brings its own. It works like any desktop one:

- **Sidebar** with your home folder, Downloads, Desktop, Documents, the last
  one used and the external drives that are actually mounted.
- **Clickable breadcrumbs**: jump to any part of the path.
- **Type or paste a path** with the ✎ button.
- **Filter** for folders with a lot in them.
- **One click selects, double click enters.** With nothing ticked, the button
  uses the folder you are in.
- **Keyboard**: ↑ ↓ to move, Enter to enter or pick, Backspace to go up, Escape
  to close, and any letter starts filtering.

When choosing the executable, `.exe` files are highlighted. When choosing a
folder, files are greyed out rather than hidden: that way you can tell an empty
folder from the one you were looking for.

---

## Things worth knowing

**The first connection to a Deck remembers its SSH key**, without asking
anything. From then on, if that address answers with a different key, deckman
**stops** and shows you both fingerprints instead of connecting. Reinstalling
SteamOS legitimately changes that key: in that case, accept the warning and
carry on. If you have not done anything like that, do not accept — whatever
answers there may not be your Deck, and connecting would hand it your password.
Remembered keys live in `known_hosts` inside deckman's config folder; forgetting
a Deck takes its key with it.

**Adding games with Steam open is safe**, but only because it goes through
Steam's API. Steam keeps the shortcut list **in memory** and rewrites
`shortcuts.vdf` on exit: editing that file behind its back while Steam runs is
not only lost, it can leave you with fewer games than you had. That is why, if
Steam is open but does not answer, deckman **refuses to go on** rather than
risk it; and before writing that file it checks that no game it was not
touching disappears.

**Steam has to be closed to move or uninstall Steam games.** It keeps its state
in memory and rewrites the manifests on exit: if the files are moved with Steam
open, it undoes the change and leaves the game marked as not installed. deckman
checks and refuses, rather than leaving it half done.

**Moving a non-Steam game does work with Steam open**, because there is no
manifest involved: the folder is copied to the other drive and Steam is asked
to point the shortcut at the new place. Two details:

- It is only offered for games that live in a `Games` folder (the ones deckman
  sends). A shortcut to an emulator or to Heroic launches something that lives
  elsewhere, and moving it would fix nothing.
- **The Proton prefix is not moved.** Steam always creates it in the main
  library, so taking it to the microSD would leave the saved games where Steam
  does not look for them.

**The Restart Steam button** is in the library bar. In game mode Steam runs
under `steam-launcher.service`, so that unit is restarted and it comes back on
its own in a few seconds. In desktop mode it can only be closed; you have to
reopen it by hand. Either way any running game is closed, which is why it asks
for confirmation.

**Before touching any Steam configuration file a copy is left** next to the
original, with the `.deckman.bak` extension.

**Internet lookups**: detection uses the Steam store, ProtonDB and SteamGridDB,
sending only the game's name or app id. Nothing else leaves the PC. **SteamDB
is not used**: it has no public API and its terms do not allow scraping.

---

## Building

You need podman or docker. **Go is not installed on the system**:

```sh
make setup    # once: checks requirements and gets the clone ready
make build    # both binaries in dist/
```

Plain `make` lists everything you can do.

### Tests

```sh
make check    # local ones
make deck     # + integration against your Steam Deck
make audit    # static analysis and known vulnerabilities
```

`make audit` is deliberately separate from `make check`: it checks against a
database that changes on its own, so it can go red without anyone touching
anything. That is fine for finding out — CI runs it on every push — but it
would be a terrible gate for publishing.

The IP and the password go in `deck.local.env` (created by `make setup` from
the example), along with the SteamGridDB key if you want to test the artwork.
That file is **not versioned** — this repository is public.

The integration ones **do not touch your real configuration**: they set up a
fake Steam tree in `~/deckman-selftest` on the Deck and delete it when done.
The few that have to touch Steam (live artwork) restore what was there.

The local tests use samples in `testdata/`, not versioned because they contain
Steam account identifiers. To generate them:

```sh
scripts/fetch-testdata.sh 192.168.1.50
```

Without them, those tests skip themselves.

### Versions

Changes are noted in **[CHANGELOG.md](CHANGELOG.md)** under *No publicado*, and
released with a single command:

```sh
make release V=0.2.0
```

It checks, moves the pending entries to the new version, updates the Flatpak
metainfo, creates the `v0.2.0` tag, pushes it to every remote verifying that it
lands on each one, and reinstalls the Flatpak — so it does not fall behind the
code again.

It is reversible while it can be: if something fails **before** publishing, it
undoes the commit and the tag and leaves you as you were. If it had already
reached a remote it does not rewrite published history; it tells you the state
of each one and what is missing.

The version comes from a single place, the tag, and can be checked at any time:

```sh
deckman --version                                  # plain binary
flatpak run io.github.jfrmorales.deckman --version # Flatpak
flatpak list --columns=application,version | grep deckman
```

The repository lives in two places and they are kept in sync:
[GitHub](https://github.com/jfrmorales/deckman) and a self-hosted Forgejo.

---

## Documentation

- **[docs/ARQUITECTURA.md](docs/ARQUITECTURA.md)** — how the code is laid out
  and why the decisions were taken (in Spanish).
- **[docs/HALLAZGOS-STEAM.md](docs/HALLAZGOS-STEAM.md)** — how Steam works
  inside. None of it is documented by Valve: it was worked out by hand against
  a real Deck, and these are the traps that made things fail (in Spanish).
- **[CHANGELOG.md](CHANGELOG.md)** — what changed in each version.
- **[CONTRIBUTING.md](CONTRIBUTING.md)** — how to build, test and send patches.

---

## Contributing

Bugs and patches are welcome: read **[CONTRIBUTING.md](CONTRIBUTING.md)**,
which explains how to build (podman or docker is all you need), how to run the
tests against a real Deck and the two rules that are never broken, because
breaking them deletes people's games.

Translations are welcome too. The catalogues are
`internal/i18n/catalogo.go` (server messages) and
`internal/server/web/i18n-catalogo.js` (interface). In both, **the key is the
Spanish text**, so adding a language means adding one block.

---

## Licence

**GPL-3.0-or-later** — see [LICENSE](LICENSE). You may use it, study it, modify
it and redistribute it; if you distribute a modified version, it has to come
with the source and under this same licence.
