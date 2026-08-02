#!/bin/sh
# Apply PUID/PGID if set, otherwise keep defaults (911/911).
PUID=${PUID:-911}
PGID=${PGID:-911}

# Update the spendrop group/user to match requested IDs
groupmod -o -g "$PGID" spendrop 2>/dev/null || true
usermod -o -u "$PUID" spendrop 2>/dev/null || true

# Ensure the data directory AND the database files inside it are writable by
# the spendrop user.
#
# Docker volumes are created root-owned, so a recursive chown is needed on first
# boot and after a PUID/PGID change. Re-running `chown -R` every boot is
# O(files) and grows with backups/migration-snapshots, so it is guarded.
#
# The guard must inspect the DATABASE FILES, not only the directory holding
# them. A restore is done with the container stopped, by a root shell or a root
# helper container writing straight into the volume, so the restored
# spendrop.db lands root-owned inside an /app/data whose ownership is already
# correct. A guard that stats only the top-level directory sees a match, skips
# the sweep, and the server then cannot open its own database — on exactly the
# boot a restore is trying to fix, where the operator is least able to debug it.
#
# The probe list is the set of paths the server must own to function, and it is
# O(1): the volume root, the database file's own directory, the two directories
# the server writes into, and the SQLite triplet. It does NOT walk backups/. A
# file left root-owned *inside* backups/ is not probed and is not a startup
# failure — removing and rewriting backups needs write permission on the
# directory, not on the individual files.
#
# The two derived directories are computed the way the SERVER computes them, not
# hardcoded, so overriding an env var cannot leave the guard probing a path
# nothing writes while skipping one it does:
#   - backups:            $BACKUP_DIR verbatim, including the relative default
#     ("backups"), which the server resolves against its CWD. This script execs
#     the server in place, so both resolve against the same /app WORKDIR.
#   - migration snapshots: always the sibling of the database file. It is
#     deliberately not its own env var (see cmd/spendrop/main.go).
#
# Missing paths are skipped rather than treated as a mismatch: -wal and -shm
# only exist while the database is open, and BACKUP_DIR may be disabled.
#
# Both uid and gid are compared — checking only uid would miss a PGID-only
# change (group flipped, owner unchanged). busybox `stat -c` is present in
# alpine. If `stat` is ever unavailable the `|| echo -1` makes the observed id
# != PUID/PGID, so it fails SAFE (the sweep runs, same as before this guard).
DB_FILE=${DB_PATH:-/app/data/spendrop.db}
DB_DIR=$(dirname "$DB_FILE")
BACKUPS_DIR=${BACKUP_DIR:-backups}
SNAPSHOTS_DIR="$DB_DIR/migration-snapshots"

needs_chown=no
for probe in /app/data "$DB_DIR" "$BACKUPS_DIR" "$SNAPSHOTS_DIR" \
             "$DB_FILE" "$DB_FILE-wal" "$DB_FILE-shm"; do
  [ -e "$probe" ] || continue
  probe_uid=$(stat -c '%u' "$probe" 2>/dev/null || echo -1)
  probe_gid=$(stat -c '%g' "$probe" 2>/dev/null || echo -1)
  if [ "$probe_uid" != "$PUID" ] || [ "$probe_gid" != "$PGID" ]; then
    echo "entrypoint: $probe is owned by ${probe_uid}:${probe_gid}, expected ${PUID}:${PGID} — correcting ownership"
    needs_chown=yes
    break
  fi
done
if [ "$needs_chown" = yes ]; then
  chown -R spendrop:spendrop /app/data
  # DB_PATH and BACKUP_DIR can be pointed outside /app/data, which the sweep
  # above does not reach. Correct those by name too, NON-recursively so this
  # stays O(1) and never sweeps an unrelated tree.
  #
  # The containing DIRECTORIES are as load-bearing as the files: SQLite creates
  # -wal/-shm and renames during checkpointing, and both need write permission
  # on the directory, not on the database file. Chowning only the files would
  # leave a database that opens read-only and fails on first write.
  for f in "$DB_DIR" "$BACKUPS_DIR" "$SNAPSHOTS_DIR" \
           "$DB_FILE" "$DB_FILE-wal" "$DB_FILE-shm"; do
    [ -e "$f" ] || continue
    chown spendrop:spendrop "$f"
  done
fi
exec su-exec spendrop "$@"
