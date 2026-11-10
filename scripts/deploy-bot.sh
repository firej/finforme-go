#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
host=${BOT_DEPLOY_HOST:-firej@45.144.233.69}
prepare_only=${BOT_PREPARE_ONLY:-0}
case "$prepare_only" in
  0|1) ;;
  *) echo 'BOT_PREPARE_ONLY must be 0 or 1' >&2; exit 2 ;;
esac

release=$(mktemp -d)
trap 'rm -rf -- "$release"' EXIT
mkdir "$release/release-bin"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-w -s' \
  -o "$release/release-bin/telegram-bot" ./cmd/telegram-bot
cp Dockerfile.bot docker-compose.bot.yml "$release/"

ssh -o BatchMode=yes "$host" 'set -eu; sudo -n install -d -m 0755 /opt/finforme-bot; sudo -n install -d -m 0700 -o "$(id -un)" -g "$(id -gn)" /opt/finforme-bot/releases'
remote_release=$(ssh -o BatchMode=yes "$host" 'mktemp -d /opt/finforme-bot/releases/release.XXXXXXXX')
if [[ ! "$remote_release" =~ ^/opt/finforme-bot/releases/release\.[A-Za-z0-9]+$ ]]; then
  echo 'Unexpected release directory' >&2
  exit 1
fi
COPYFILE_DISABLE=1 tar --no-xattrs -czf - -C "$release" . | ssh -o BatchMode=yes "$host" "tar -xzf - -C '$remote_release'"

ssh -o BatchMode=yes "$host" "bash -s -- '$remote_release' '$prepare_only'" <<'REMOTE'
set -euo pipefail
release=$1
prepare_only=$2
trap 'rm -rf -- "$release"' EXIT
exec 9>/opt/finforme-bot/releases/.deploy.lock
flock 9
sudo -n test -s /opt/finforme-bot/.env || {
  echo 'Install TELEGRAM_BOT_TOKEN in /opt/finforme-bot/.env (0600) first' >&2
  exit 1
}
compose=(sudo -n docker compose --env-file /opt/finforme-bot/.env)
"${compose[@]}" -f "$release/docker-compose.bot.yml" config -q
sudo -n docker build -f "$release/Dockerfile.bot" -t finforme-bot:latest "$release"
sudo -n install -m 0644 "$release/docker-compose.bot.yml" /opt/finforme-bot/docker-compose.yml
compose+=(-f /opt/finforme-bot/docker-compose.yml)
# Checks both APIs without consuming Telegram updates or sending any messages.
"${compose[@]}" run --rm --no-deps -T finforme-bot -check </dev/null
if [[ "$prepare_only" == 1 ]]; then
  echo 'Bot prepared and checked; polling has not been started'
  exit 0
fi
"${compose[@]}" up -d --wait --wait-timeout 90 finforme-bot
REMOTE
