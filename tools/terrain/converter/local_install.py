"""Convert the user's installed game; never download game data."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import shutil
import sys


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--game', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--references', type=Path, required=True)
    parser.add_argument('--geo-references', type=Path, required=True)
    parser.add_argument('--resolution', type=int, default=4096)
    parser.add_argument('--preflight', action='store_true')
    args = parser.parse_args(argv)
    if not 2 <= args.resolution <= 8192:
        parser.error('--resolution must be between 2 and 8192')

    # Install the hard aggregate Job limit before native dependency imports or
    # parsing game archives. Worker subprocesses retain their own 2 GiB limit.
    from memory_guard import MemoryGuard
    guard = MemoryGuard()
    from targets import load_reference_ids
    from game_configs import extract_level_configs
    from catalog import build_catalog
    from batch import atomic_write_json

    game = args.game.resolve(strict=True)
    output = args.output.resolve()
    for path, label in [(output, 'cache'), (args.references.resolve(), 'reference file')]:
        if path == game or game in path.parents or path in game.parents:
            raise ValueError(f'{label} must be outside the game installation')
    ids = load_reference_ids(args.references)
    missing = [map_id for map_id in ids if not (game/'levels'/f'{map_id}.bin').is_file()]
    if missing:
        raise ValueError('Game is missing required levels (complete/update the client): '+', '.join(missing))
    print(f'Game root: {game}\nRead-only game input; {len(ids)} maps; 2 GiB Job limit', flush=True)
    output.mkdir(parents=True, exist_ok=True)
    configs = extract_level_configs(game, ids, output/'local-config')
    entries = build_catalog(configs, game, ids)
    by_id = {entry['id']: entry for entry in entries}
    entries = [by_id[map_id] for map_id in ids]
    for entry in entries:
        if not entry['config_exists'] or not entry['exists'] or entry.get('config_error') or entry.get('header_error'):
            raise ValueError(f"{entry['id']}: invalid or missing local level/config")
        if entry['map_coord0'] is None or entry['map_coord1'] is None or entry['water_level'] is None:
            raise ValueError(f"{entry['id']}: local game has no reliable full-map bounds or water level")
        entry['config_source'] = 'local_game_aces_vromfs'
    atomic_write_json(output/'catalog.json', entries)
    for source, name in [(args.references,'AtlasReferences.json'), (args.geo_references,'GeoReferences.json')]:
        shutil.copyfile(source, output/name)
    print(f'Validated {len(entries)} local game levels and configurations.', flush=True)
    if args.preflight:
        print('Preflight complete; no map conversion or Tacview destination writes.', flush=True)
        return 0

    import batch
    result = batch.main(['--catalog',str(output/'catalog.json'),'--references',str(output/'AtlasReferences.json'),
                         '--expected-targets',str(len(ids)),'--output',str(output),
                         '--resolution',str(args.resolution),'--timeout','1200','--resume'])
    if result:
        print('Conversion incomplete; cache retained for the next run. Nothing installed.', file=sys.stderr)
        return result
    import verify_outputs
    result = verify_outputs.main(['--root',str(output),'--catalog',str(output/'catalog.json')])
    if result:
        return result
    from finalize import finalize
    report = finalize(output, output/'AtlasReferences.json', output/'catalog.json',len(ids),[])
    print(f"Complete: {report['map_count']} verified maps ready for Tacview installation.", flush=True)
    atomic_write_json(output/'local-conversion.json',dict(schema=1,status='complete',map_count=len(ids),
        game=str(game),resolution=args.resolution,memoryguard=guard.stats()))
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f'Local terrain conversion failed: {exc}', file=sys.stderr, flush=True)
        raise SystemExit(1)
