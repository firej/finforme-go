#!/usr/bin/env bash
set -euo pipefail
umask 027
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

base=/volume1/backups/finforme
config="$base/.config"
dest="$base/database"
exec 9>/run/lock/finforme-backup-pull.lock
flock -n 9 || exit 0
trap 'rc=$?; if ((rc)); then logger -t finforme-backup "FAILED: NAS pull (exit $rc)"; fi' EXIT

mountpoint -q /volume1 || { echo '/volume1 is not mounted' >&2; exit 1; }
test -d "$dest" && test -d "$config"

export RSYNC_RSH="ssh -F $config/ssh_config"
# No --delete: server retention must never remove NAS history.
rsync -rtpgo --ignore-existing --timeout=120 --chmod=F640,D750 --chown=root:admin \
  --include='finforme-*.sql.gz' --include='finforme-*.sql.gz.sha256' \
  --exclude='*' finforme-source:./ "$dest/"

cd "$dest"
shopt -s nullglob
files=(finforme-*.sql.gz)
((${#files[@]})) || { echo 'No database backups downloaded' >&2; exit 1; }
for file in "${files[@]}"; do
  [[ "$file" =~ ^finforme-[0-9]{8}T[0-9]{6}Z\.sql\.gz$ ]] || exit 1
  test -f "$file.sha256"
  # Validate the checksum format and filename before letting sha256sum read it.
  read -r checksum checksum_name < "$file.sha256"
  [[ "$checksum" =~ ^[a-f0-9]{64}$ && "$checksum_name" == "$file" ]] || exit 1
  actual=$(sha256sum -- "$file")
  [[ "${actual%% *}" == "$checksum" ]] || { echo "Checksum mismatch: $file" >&2; exit 1; }
  gzip -t "$file"
  chgrp admin "$file" "$file.sha256"
  chmod 0640 "$file" "$file.sha256"
done

latest=${files[-1]}
now=$(date -u +%s)
latest_epoch=$(date -u -d "${latest:9:4}-${latest:13:2}-${latest:15:2} ${latest:18:2}:${latest:20:2}:${latest:22:2}" +%s)
((latest_epoch <= now + 300 && now - latest_epoch < 172800)) || {
  echo "Newest backup is not fresh: $latest" >&2
  exit 1
}

# Retain 90 days, and never prune below 14 copies. Only prune after verification.
remaining=${#files[@]}
for file in "${files[@]}"; do
  ((remaining > 14)) || break
  if [[ -n "$(find "$file" -maxdepth 0 -type f -mtime +89 -print)" ]]; then
    rm -- "$file" "$file.sha256"
    remaining=$((remaining - 1))
  fi
done
status=$(mktemp "$base/.last-success.XXXXXXXX")
printf 'verified_at_utc=%s\nlatest=%s\nretained=%s\n' "$(date -u +%FT%TZ)" "$latest" "$remaining" > "$status"
chgrp admin "$status"
chmod 0640 "$status"
mv -T "$status" "$base/last-success.txt"
logger -t finforme-backup "OK: $latest; $remaining verified copies on NAS"
printf 'OK: %s; %s verified copies on NAS\n' "$latest" "$remaining"
