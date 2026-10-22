"""Rebuild the embedded all-map runway catalogue from pinned Datamine sources.

Python standard library only. Normal Go builds use the checked-in catalogue
and need no network. --source-root selects an offline Datamine checkout;
--terrain-root optionally adds heights sampled from converted game terrain.
"""
import argparse
import json
from pathlib import Path

import assemble_catalog
import build_missions
import build_prefabs


def build(source_root=None, terrain_root=None, cache_dir=None, mission_cap=720):
    repo = Path(__file__).resolve().parents[2]
    cache = Path(cache_dir) if cache_dir else repo.parent/'airfields-all/build-cache'
    prefabs = build_prefabs.build(source_root, cache/'prefabs')
    missions = build_missions.build(repo, cache/'missions.json', cache/'missions', source_root, 4, mission_cap)
    return assemble_catalog.assemble(prefabs, missions, terrain_root)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root', type=Path)
    parser.add_argument('--terrain-root', type=Path)
    parser.add_argument('--cache-dir', type=Path)
    parser.add_argument('--max-missions', type=int, default=720)
    parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parents[2]/'cmd/acmi/data/airfields_all.json')
    args = parser.parse_args()
    if not 69 <= args.max_missions <= 1600:
        parser.error('--max-missions must be between 69 and 1600')
    result = build(args.source_root, args.terrain_root, args.cache_dir, args.max_missions)
    if not any(item['source_runway_candidates'] for item in result['coverage'].values()):
        parser.error('source inventory produced no runway geometry')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2,sort_keys=True,ensure_ascii=False,allow_nan=False)+'\n',encoding='utf-8')
    configured = sum(item['status']=='Configured' for item in result['coverage'].values())
    print(f'Wrote {len(result["maps"])} maps ({configured} with sourced runway candidates), '
          f'{len(result["prefabs"])} prefabs to {args.output}')


if __name__ == '__main__':
    main()
