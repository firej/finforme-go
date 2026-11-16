#!/usr/bin/env bash
set -euo pipefail
umask 077

backup_dir=/var/backups/finforme
install -d -m 0700 "$backup_dir"
# The NAS account can only download this directory through restricted rsync.
if getent group finforme-backup >/dev/null; then
  chgrp finforme-backup "$backup_dir"
  chmod 0750 "$backup_dir"
fi
exec 9>"$backup_dir/.lock"
flock -n 9

tmp=$(mktemp "$backup_dir/.finforme.XXXXXXXX")
trap 'rm -f -- "$tmp"' EXIT
docker compose -f /opt/finforme/docker-compose.yml --env-file /opt/finforme/.env \
  exec -T mariadb sh -eu -c 'MYSQL_PWD="$MARIADB_ROOT_PASSWORD" exec mariadb-dump --user=root --single-transaction --quick --databases finforme' \
  | gzip > "$tmp"
gzip -t "$tmp"
name="finforme-$(date -u +%Y%m%dT%H%M%SZ).sql.gz"
if getent group finforme-backup >/dev/null; then
  chgrp finforme-backup "$tmp"
  chmod 0640 "$tmp"
fi
mv -T "$tmp" "$backup_dir/$name"
# Publish checksums atomically; a pull during this short gap retries next hour.
tmp=$(mktemp "$backup_dir/.finforme.XXXXXXXX")
(cd "$backup_dir" && sha256sum "$name") > "$tmp"
if getent group finforme-backup >/dev/null; then
  chgrp finforme-backup "$tmp"
  chmod 0640 "$tmp"
fi
mv -T "$tmp" "$backup_dir/$name.sha256"
find "$backup_dir" -maxdepth 1 -type f -name 'finforme-*.sql.gz' -mtime +13 -delete
find "$backup_dir" -maxdepth 1 -type f -name 'finforme-*.sql.gz.sha256' -mtime +13 -delete
