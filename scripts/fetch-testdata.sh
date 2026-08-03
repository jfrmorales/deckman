#!/usr/bin/env bash
# Trae de una Steam Deck los ficheros de configuracion reales que usan las
# pruebas. No se versionan: llevan identificadores de la cuenta de Steam.
set -euo pipefail
HOST="${1:?uso: $0 <ip-de-la-deck> [usuario]}"
USER="${2:-deck}"
cd "$(dirname "$0")/.."
mkdir -p testdata

S="$USER@$HOST"
R='$HOME/.local/share/Steam'
ssh "$S" "cat $R/userdata/*/config/shortcuts.vdf"        > testdata/shortcuts.vdf
ssh "$S" "cat $R/config/config.vdf"                      > testdata/config.vdf
ssh "$S" "cat $R/steamapps/libraryfolders.vdf"           > testdata/libraryfolders.vdf
ssh "$S" "cat \$(ls $R/steamapps/appmanifest_*.acf | head -1)" > testdata/appmanifest_252950.acf
ls -l testdata/
