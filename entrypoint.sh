#!/bin/sh
# Ensure the data directory is writable by the spendrop user.
# Docker volumes are created root-owned; this fixes ownership on first run.
chown -R spendrop:spendrop /app/data
exec su-exec spendrop "$@"
