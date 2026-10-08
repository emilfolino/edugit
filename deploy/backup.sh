#!/bin/sh
# Consistent backup of the data directory while edugit runs.
# Usage: backup.sh [data-dir] [backup-dir]   (run as the edugit user)
set -eu
data=${1:-/var/lib/edugit}
dest=${2:-/var/backups/edugit}/$(date +%F-%H%M)
mkdir -p "$dest"
sqlite3 "$data/edugit.db" "VACUUM INTO '$dest/edugit.db'"
# Repos are plain git directories; rsync is safe, a snapshot is safer still.
rsync -a --delete "$data/repos/" "$dest/repos/"
rsync -a "$data/saml-sp.key" "$data/saml-sp.crt" "$dest/" 2>/dev/null || true
# Keep the last 14 backups.
ls -1d "$(dirname "$dest")"/*/ | head -n -14 | xargs -r rm -rf
