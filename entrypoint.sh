#!/bin/sh
# Apply PUID/PGID if set, otherwise keep defaults (911/911).
PUID=${PUID:-911}
PGID=${PGID:-911}

# Update the spendrop group/user to match requested IDs
groupmod -o -g "$PGID" spendrop 2>/dev/null || true
usermod -o -u "$PUID" spendrop 2>/dev/null || true

# Ensure the data directory is writable by the spendrop user.
# Docker volumes are created root-owned; a recursive chown is needed ONLY on
# first boot (or after a PUID/PGID change). Re-running `chown -R` every boot is
# O(files) and grows with backups/migration-snapshots, so guard on the actual
# owner of the top-level dir: if it already matches PUID, the tree is correct
# and we skip the sweep. busybox `stat -c` is present in alpine. If `stat` is
# ever unavailable the `|| echo -1` makes current_uid != PUID, so it fails SAFE
# (the recurse runs, same as before this guard).
current_uid=$(stat -c '%u' /app/data 2>/dev/null || echo -1)
if [ "$current_uid" != "$PUID" ]; then
  chown -R spendrop:spendrop /app/data
fi
exec su-exec spendrop "$@"
