#!/usr/bin/env python3
"""Export Bybit Unified/Funding ledgers. Python 3.9+, standard library only."""
import argparse
import csv
from datetime import datetime, timedelta, timezone
import getpass
import hashlib
import hmac
import json
import os
from pathlib import Path
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

UTC = timezone.utc
SOURCES = {
    'unified': ('/v5/account/transaction-log', 'startTime', 'endTime', 1000, 50),
    'funding': ('/v5/asset/fundinghistory', 'createTimeFrom', 'createTimeTo', 1, 100),
}


def date_arg(value):
    try:
        dt = datetime.fromisoformat(value.replace('Z', '+00:00'))
        return dt.replace(tzinfo=UTC) if dt.tzinfo is None else dt.astimezone(UTC)
    except ValueError as exc:
        raise argparse.ArgumentTypeError('Use YYYY-MM-DD or ISO 8601') from exc


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Client:
    def __init__(self, key, secret, base_url):
        self.key, self.secret, self.base_url = key, secret, base_url
        self.opener = urllib.request.build_opener(NoRedirect)

    def get(self, path, params):
        query = urllib.parse.urlencode(params)
        for attempt in range(6):
            time.sleep(0.25)
            timestamp = str(int(time.time() * 1000))
            signature = hmac.new(self.secret.encode(),
                (timestamp + self.key + '10000' + query).encode(), hashlib.sha256).hexdigest()
            req = urllib.request.Request(self.base_url + path + '?' + query, headers={
                'X-BAPI-API-KEY': self.key, 'X-BAPI-SIGN': signature,
                'X-BAPI-TIMESTAMP': timestamp, 'X-BAPI-RECV-WINDOW': '10000',
            })
            try:
                with self.opener.open(req, timeout=30) as response:
                    data = json.load(response)
                code = data.get('retCode')
                if code == 0:
                    result = data.get('result')
                    if not isinstance(result, dict) or not isinstance(result.get('list'), list):
                        raise RuntimeError('Unexpected API response structure')
                    return result
                if code not in (10006, 10000, 10016):
                    # Do not print retMsg: auth errors can echo signing material.
                    raise RuntimeError(f'Bybit error {code}; check key permissions, API host and system clock')
            except urllib.error.HTTPError as exc:
                if exc.code != 429 and not 500 <= exc.code < 600:
                    raise RuntimeError(f'HTTP {exc.code}; check API host and regional access') from None
            except (urllib.error.URLError, TimeoutError, ConnectionError):
                pass
            if attempt < 5:
                time.sleep(min(2 ** (attempt + 1), 30))
        raise RuntimeError('API unavailable or rate limited after 6 attempts; rerun the export')


def records(client, source, start, end):
    path, start_key, end_key, scale, limit = SOURCES[source]
    lo, stop = int(start.timestamp() * scale), int(end.timestamp() * scale)
    while lo < stop:
        hi = min(lo + 7 * 86400 * scale, stop) - 1
        params = {start_key: lo, end_key: hi, 'limit': limit}
        if source == 'unified':
            params['accountType'] = 'UNIFIED'
        cursors = set()
        while True:
            result = client.get(path, params)
            yield from result['list']
            cursor = result.get('nextPageCursor')
            if not cursor:
                break
            if cursor in cursors:
                raise RuntimeError(f'{source}: repeated pagination cursor')
            cursors.add(cursor)
            params['cursor'] = cursor
        print(f'{source}: through {datetime.fromtimestamp(hi / scale, UTC).isoformat()}', file=sys.stderr)
        lo = hi + 1


def export(client, source, start, end, directory):
    # Spool first: preserve all API fields without holding the export in RAM.
    raw = directory / f'{source}.partial.jsonl'
    partial_csv = directory / f'{source}.partial.csv'
    target = directory / f'{source}.csv'
    fields = {'source', 'time_utc'}
    count = 0
    with raw.open('x', encoding='utf-8') as spool:
        for record in records(client, source, start, end):
            row = dict(record)
            row['source'] = source
            stamp = row.get('transactionTime') if source == 'unified' else row.get('createTime')
            row['time_utc'] = datetime.fromtimestamp(int(stamp) / SOURCES[source][3], UTC).isoformat() if stamp else ''
            fields.update(row)
            spool.write(json.dumps(row, ensure_ascii=False) + '\n')
            spool.flush()
            count += 1
    columns = ['source', 'time_utc'] + sorted(fields - {'source', 'time_utc'})
    with raw.open(encoding='utf-8') as spool, partial_csv.open('x', encoding='utf-8-sig', newline='') as output:
        writer = csv.DictWriter(output, fieldnames=columns)
        writer.writeheader()
        for line in spool:
            row = json.loads(line)
            writer.writerow({k: json.dumps(v, ensure_ascii=False) if isinstance(v, (dict, list)) else v for k, v in row.items()})
    partial_csv.replace(target)
    raw.unlink()
    print(f'{target}: {count} rows', file=sys.stderr)
    return count


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--start', type=date_arg, help='Inclusive UTC date/time; default: 729 days ago')
    parser.add_argument('--end', type=date_arg, help='Exclusive UTC date/time; default: now')
    parser.add_argument('--source', choices=['all', *SOURCES], default='all')
    parser.add_argument('--output', type=Path, help='New output directory (must not exist)')
    parser.add_argument('--base-url', default='https://api.bybit.com', help='Official regional Bybit API URL')
    args = parser.parse_args()
    now = datetime.now(UTC)
    start, end = args.start or now - timedelta(days=729), args.end or now
    if start >= end or end > now:
        parser.error('Require start < end <= now')
    url = urllib.parse.urlsplit(args.base_url)
    if url.scheme != 'https' or not url.hostname or url.username or url.password or url.query or url.fragment or url.path not in ('', '/'):
        parser.error('--base-url must be an HTTPS origin')
    sources = list(SOURCES) if args.source == 'all' else [args.source]
    if 'unified' in sources and start < now - timedelta(days=730):
        print('WARNING: Unified only retains 2 years; older history may be unavailable.', file=sys.stderr)
    key = os.getenv('BYBIT_API_KEY') or getpass.getpass('Bybit API key (hidden): ')
    secret = os.getenv('BYBIT_API_SECRET') or getpass.getpass('Bybit API secret (hidden): ')
    if not key.strip() or not secret.strip():
        parser.error('API key and secret are required')
    directory = args.output or Path('bybit-export-' + now.strftime('%Y%m%d-%H%M%S'))
    directory.mkdir(parents=True, exist_ok=False)
    manifest = {'start_inclusive': start.isoformat(), 'end_exclusive': end.isoformat(), 'sources': {}, 'complete': False}
    manifest_path = directory / 'manifest.json'
    def save_manifest():
        manifest_path.write_text(json.dumps(manifest, indent=2) + '\n', encoding='utf-8')
    save_manifest()
    client = Client(key.strip(), secret.strip(), args.base_url.rstrip('/'))
    failed = False
    for source in sources:
        try:
            count = export(client, source, start, end, directory)
            manifest['sources'][source] = {'status': 'complete', 'rows': count}
        except (RuntimeError, OSError, ValueError) as exc:
            failed = True
            manifest['sources'][source] = {'status': 'failed', 'error': str(exc)}
            print(f'{source}: {exc}; partial files are NOT a complete export', file=sys.stderr)
        save_manifest()
    manifest['complete'] = not failed
    save_manifest()
    return 1 if failed else 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, EOFError) as exc:
        print(f'Export failed: {exc}', file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print('\nInterrupted; partial files are NOT a complete export.', file=sys.stderr)
        sys.exit(130)
