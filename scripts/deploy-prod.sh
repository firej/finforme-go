#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
host=${DEPLOY_HOST:-firej@firej.org}
mode=${1:-web}
case "$mode" in
  web|bot) ;;
  *) echo 'Usage: bash scripts/deploy-prod.sh [web|bot]' >&2; exit 2 ;;
esac

release=$(mktemp -d)
trap 'rm -rf -- "$release"' EXIT
mkdir "$release/release-bin"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-w -s' \
  -o "$release/release-bin/" ./cmd/server ./cmd/import-rates ./cmd/import-rates-history \
  ./cmd/import-blue-rates ./cmd/telegram-bot
mv "$release/release-bin/server" "$release/release-bin/finforme"
cp Dockerfile.release docker-compose.prod.yml "$release/"
cp -R static templates "$release/"

ssh -o BatchMode=yes "$host" 'set -eu; sudo -n install -d -m 0755 /opt/finforme; sudo -n install -d -m 0700 -o "$(id -un)" -g "$(id -gn)" /opt/finforme/releases'
remote_release=$(ssh -o BatchMode=yes "$host" 'mktemp -d /opt/finforme/releases/release.XXXXXXXX')
if [[ ! "$remote_release" =~ ^/opt/finforme/releases/release\.[A-Za-z0-9]+$ ]]; then
  echo 'Unexpected release directory' >&2
  exit 1
fi
COPYFILE_DISABLE=1 tar --no-xattrs -czf - -C "$release" . | ssh -o BatchMode=yes "$host" "tar -xzf - -C '$remote_release'"

ssh -o BatchMode=yes "$host" "bash -s -- '$remote_release' '$mode'" <<'REMOTE'
set -euo pipefail
release=$1
mode=$2
trap 'rm -rf -- "$release"' EXIT

# Serialize changes to the live Compose project.
exec 9>/opt/finforme/releases/.deploy.lock
flock 9
sudo -n docker network inspect traefik_default >/dev/null
sudo -n docker build -f "$release/Dockerfile.release" -t finforme:latest "$release"

sudo -n bash -s <<'ENV'
set -euo pipefail
cd /opt/finforme
if [[ ! -e .env ]]; then
  # Keep these values across deploys. MariaDB only reads its passwords on initialization.
  umask 077
  env_file=$(mktemp .env.XXXXXXXX)
  trap 'rm -f -- "$env_file"' EXIT
  printf 'FINFORME_DB_PASSWORD=%s\nFINFORME_DB_ROOT_PASSWORD=%s\nSESSION_SECRET=%s\n' \
    "$(openssl rand -hex 32)" "$(openssl rand -hex 32)" "$(openssl rand -hex 32)" > "$env_file"
  mv -T "$env_file" .env
fi
ENV

compose=(sudo -n docker compose --env-file /opt/finforme/.env)
"${compose[@]}" -f "$release/docker-compose.prod.yml" config -q
if [[ "$mode" == bot ]]; then
  sudo -n awk -F= '$1 == "TELEGRAM_BOT_TOKEN" && $2 ~ /^[0-9]+:.+/ { found=1 } END { exit !found }' /opt/finforme/.env || {
    echo 'Set TELEGRAM_BOT_TOKEN in /opt/finforme/.env first' >&2
    exit 1
  }
fi
sudo -n install -m 0644 "$release/docker-compose.prod.yml" /opt/finforme/docker-compose.yml
compose+=(-f /opt/finforme/docker-compose.yml)
services=(finforme)
if [[ "$mode" == bot ]]; then
  services+=(finforme-bot)
fi
"${compose[@]}" up -d --wait --wait-timeout 120 "${services[@]}"
for ((i = 0; i < 24; i++)); do
  status=$(curl -s --max-time 5 --resolve finfor.me:443:127.0.0.1 -o /dev/null -w '%{http_code}' https://finfor.me/ || true)
  if [[ "$status" == 200 ]]; then
    echo 'https://finfor.me/ returned HTTP 200 over HTTPS'
    exit 0
  fi
  sleep 3
done
echo 'HTTPS health check failed' >&2
"${compose[@]}" ps
exit 1
REMOTE
