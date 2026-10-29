#!/usr/bin/env python3
"""Build AAM/AGM aliases from pinned, verified Datamine weapons and English names.

Use --cache DIR containing tree.json (Git tree at COMMIT), then --fetch to
populate missing weapon/CSV files. Subsequent builds are offline. The selected
scope is every rocketguns definition in the pinned tree, not future versions.
Use --role AAM or AGM (default). Explicit projectile fields select the role;
AAM-typed ground-only seekers stay AGMs, and SAM-only types are excluded.
No raw game assets are included in the generated catalogue.
"""
import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import csv
import gzip
import hashlib
import io
import json
from pathlib import Path
import sys
import time
import urllib.request

COMMIT = 'f7952e4291e9fd7b6fe38e84020dddef925c3457'
BASE = f'https://raw.githubusercontent.com/gszabi99/War-Thunder-Datamine/{COMMIT}/'
LANG = 'lang.vromfs.bin_u/lang/units_weaponry.csv'

def blob_sha(raw):
    return hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()

def download(path, expected):
    last = None
    for attempt in range(3):
        url = BASE + path if attempt < 2 else 'https://api.github.com/repos/gszabi99/War-Thunder-Datamine/git/blobs/' + expected
        try:
            request = urllib.request.Request(url, headers={'User-Agent': 'wrpl-acmi-agm-catalog', 'Accept-Encoding': 'gzip'})
            with urllib.request.urlopen(request, timeout=20) as response:
                raw = response.read(16 * 1024 * 1024 + 1)
                if response.headers.get('Content-Encoding') == 'gzip':
                    with gzip.GzipFile(fileobj=io.BytesIO(raw)) as compressed:
                        raw = compressed.read(16 * 1024 * 1024 + 1)
            if attempt == 2:
                payload = json.loads(raw)
                if payload.get('sha') != expected or payload.get('encoding') != 'base64':
                    raise ValueError('unexpected Git blob response')
                raw = base64.b64decode(payload['content'])
            if len(raw) > 16 * 1024 * 1024 or blob_sha(raw) != expected:
                raise ValueError('asset size/hash mismatch: ' + path)
            return raw
        except Exception as error:
            last = error
            time.sleep(0.25)
    raise ValueError(f'Cannot verify {path}: {last}') from last

def walk(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk(child)

def weapon_rows(path, data, names, role='AGM'):
    """Launcher aliases require all guided bullet identities to agree."""
    rows = []
    for obj in walk(data):
        rocket = obj.get('rocket')
        if not isinstance(rocket, dict):
            continue
        guidance = rocket.get('guidanceType')
        if not guidance and isinstance(rocket.get('controlSensitivity'), (int, float)) and rocket['controlSensitivity'] > 0:
            guidance = 'manual'
        if not isinstance(guidance, str) or not guidance:
            continue
        bullet_type = rocket.get('bulletType', obj.get('bulletType'))
        types = bullet_type if isinstance(bullet_type, list) else [bullet_type]
        seeker_block = rocket.get('guidance', {})
        ground_only = any(isinstance(seeker, dict) and seeker.get('groundVehiclesAsTarget') is True
                          and seeker.get('aircraftAsTarget') is False
                          for key, seeker in seeker_block.items() if key.endswith('Seeker')) if isinstance(seeker_block, dict) else False
        is_aam = 'aam' in types and not ground_only
        if 'sam_tank' in types or (role == 'AAM') != is_aam:
            continue
        bullet = rocket.get('bulletName', obj.get('bulletName'))
        if not isinstance(bullet, str) or not bullet:
            continue
        key = next((key for key in ('weapons/' + bullet + '/short', bullet, 'weapons/' + bullet) if names.get(key)), '')
        rows.append((bullet, names.get(key, ''), guidance, key))
    rows = sorted(set(rows))
    # Some assets use a historical/different bulletName but localize the
    # launcher itself. Only a single guided identity may borrow that label.
    if len(rows) == 1 and not rows[0][1]:
        bullet, _, guidance, _ = rows[0]
        stem = Path(path).stem
        key = next((key for key in ('weapons/' + stem + '/short', stem, 'weapons/' + stem) if names.get(key)), '')
        rows = [(bullet, names.get(key, ''), guidance, key)]
    return rows

def aam_radar_source(data):
    seekers = [o['radarSeeker'] for o in walk(data) if isinstance(o.get('radarSeeker'), dict)]
    if seekers and all(s.get('active') is True for s in seekers):
        return ', radarSeeker.active=true'
    # The established AAM asset convention defaults an omitted active bool
    # to false. Do not apply this rule to AGM passive radar seekers, missing
    # seeker blocks, mixed variants, or explicit non-boolean values.
    if seekers and all(s.get('active', False) is False for s in seekers):
        return ', radarSeeker.active=false/default (SARH)'
    return ''

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--cache', type=Path, required=True)
    ap.add_argument('--fetch', action='store_true')
    ap.add_argument('--out', type=Path, required=True)
    ap.add_argument('--role', choices=['AAM', 'AGM'], default='AGM')
    args = ap.parse_args()
    tree = json.loads((args.cache / 'tree.json').read_text(encoding='utf-8'))
    if tree['sha'] != COMMIT:
        raise ValueError('tree revision differs from pinned commit')
    entries = {e['path']: e['sha'] for e in tree['tree'] if e['type'] == 'blob'}
    def read(path):
        expected = entries[path]
        local = args.cache / path.replace('/', '__')
        if local.exists():
            raw = local.read_bytes()
        elif args.fetch:
            raw = download(path, expected)
            local.write_bytes(raw)
        else:
            raise ValueError('missing cached asset: ' + path)
        if blob_sha(raw) != expected:
            raise ValueError('cached blob differs from pinned tree: ' + path)
        return raw
    raw = read(LANG)
    reader = csv.reader(io.StringIO(raw.decode('utf-8-sig')), delimiter=';')
    header = next(reader)
    english = header.index('<English>')
    names = {r[0]: r[english].strip() for r in reader if len(r) > english and r[english].strip()}
    paths = sorted(p for p in entries if '/weapons/rocketguns/' in p and p.endswith('.blkx'))
    print(f'Inspecting {len(paths)} pinned weapon files', file=sys.stderr, flush=True)
    def inspect(path):
        data = json.loads(read(path))
        return path, weapon_rows(path, data, names, args.role), data
    with ThreadPoolExecutor(max_workers=8) as pool:
        inspected = list(pool.map(inspect, paths))
    aliases = {}
    def add(key, bullet, model, guidance, path, language_key, data):
        key = key.lower()
        guidance_source = 'manual controlSensitivity>0' if guidance == 'manual' else 'guidanceType=' + guidance
        if args.role == 'AAM' and guidance == 'radar':
            guidance_source += aam_radar_source(data)
        label_source = f'{LANG} blob={entries[LANG]} key={language_key}' if language_key else 'untranslated internal identifier (no English localization)'
        entry = {'model': model, 'internal_name': bullet,
                 'confidence': 'confirmed',
                 'source': f'Datamine {COMMIT} {path} blob={entries[path]}; {guidance_source}; {label_source}'}
        if args.role == 'AGM':
            entry['role'] = 'AGM'
        aliases.setdefault(key, []).append(entry)
    for path, rows, data in inspected:
        for bullet, model, guidance, language_key in rows:
            add(bullet, bullet, model or bullet, guidance, path, language_key, data)
        if len(rows) == 1:
            bullet, model, guidance, language_key = rows[0]
            for alias in (Path(path).stem, path, path.removeprefix('aces.vromfs.bin_u/').removesuffix('x')):
                add(alias, bullet, model or bullet, guidance, path, language_key, data)
    # Never make a conflict disappear by taking the last asset in enumeration.
    result = {}
    for key, values in sorted(aliases.items()):
        identities = {(v['model'], v['internal_name']) for v in values}
        if len(identities) == 1:
            result[key] = sorted(values, key=lambda v: v['source'])[0]
    out = {'schema': 1, 'generated_from': [f'War-Thunder-Datamine@{COMMIT}',
            f'tools/agm/build_catalog.py --role {args.role}; all pinned rocketguns; explicit guided fields and English CSV'],
           'aliases': result, 'replays': {}}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(out, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(f'{len(result)} aliases, {len({e["model"] for e in result.values()})} models', file=sys.stderr)

if __name__ == '__main__':
    main()
