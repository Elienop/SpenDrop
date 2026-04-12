#!/bin/sh
# Apply PUID/PGID if set, otherwise keep defaults (911/911).
PUID=${PUID:-911}
PGID=${PGID:-911}

# Update the spendrop group/user to match requested IDs
groupmod -o -g "$PGID" spendrop 2>/dev/null || true
usermod -o -u "$PUID" spendrop 2>/dev/null || true

# Ensure the data directory is writable by the spendrop user.
# Docker volumes are created root-owned; this fixes ownership on first run.
chown -R spendrop:spendrop /app/data
exec su-exec spendrop "$@"
