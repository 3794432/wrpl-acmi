"""Rebuild the small reviewed airfield catalogue from pinned Datamine files.

Python standard library only. The exporter embeds the result and needs no
network or game assets at runtime. --source-root uses an existing checkout.
"""
import argparse
import hashlib
import json
from pathlib import Path
from urllib.request import urlopen

COMMIT = 'f7952e4291e9fd7b6fe38e84020dddef925c3457'
PREFIX = f'https://raw.githubusercontent.com/gszabi99/War-Thunder-Datamine/{COMMIT}/'
BASE = 'mis.vromfs.bin_u/gamedata/missions/cta/planes/historical/'
FILES = {
    'mission': BASE+'bfd/air_falklands_bfd_norespawn.blkx',
    'include': BASE+'bfd/air_falklands/airfields_part.blkx',
    'top': BASE+'bfd/air_falklands/airfields/template_airfields_top_ranks.blkx',
    'high': BASE+'bfd/air_falklands/airfields/template_airfields_high_ranks.blkx',
    'prefab': 'aces.vromfs.bin_u/gamedata/objectgroups/dynaf_pg_1line_3000_universal.blkx',
    'main': 'aces.vromfs.bin_u/gamedata/units/structures/unit_airfield_main_radar_char.blkx',
    'flight': 'aces.vromfs.bin_u/gamedata/units/structures/unit_airfield_flight_radar_char.blkx',
    'decor': 'mis.vromfs.bin_u/gamedata/missions/templates/units_sets/airfields_decor/air_radar_decor.blkx',
}
EXPECTED = {
    'mission':'0e95ce4075649dc1d13fced118d661837c9a2d2db37bcea2df343ae760e5ef5d',
    'include':'ebffc8a880bd4a99f88f21b8daf48783b68fc4f8994e2bb7cc9989941db0089b',
    'top':'a59b3f73acac5400ef3caf52c9b09fc356a80f3f32a52aaf0512fa779b992c1f',
    'high':'aab3fc7ed5d97a805d3c177859948faf5b53408f42cf4ab5565887781ec0900a',
    'prefab':'9dfd162a92dcf43e1604fc40cdee0b56669645fac1fa8eb8649c40fed81a0579',
    'main':'7029c56aeb511ef94839ee3343b02f4d1f749a336ca46e8f494ac2f26766c5f1',
    'flight':'f52fad72350fcd0786e2d1f3032328e44dd3233313656151ca24ff3ec08b4198',
    'decor':'6799d3ec0bb7c9da368635310e6baca5889fb780691a4288575ad05a724d63ec',
}

def build(source_root=None):
    data, sources = {}, []
    for key, path in FILES.items():
        if source_root:
            with (Path(source_root)/path).open('rb') as source:
                raw = source.read(2*1024*1024+1)
        else:
            with urlopen(PREFIX+path, timeout=40) as response:
                raw = response.read(2*1024*1024+1)
        if len(raw) > 2*1024*1024:
            raise ValueError('Oversize Datamine file: '+path)
        data[key] = json.loads(raw)
        # Normalize platform newlines before hashing the source text.
        digest = hashlib.sha256(raw.replace(b'\r\n', b'\n')).hexdigest()
        if digest != EXPECTED[key]:
            raise ValueError('Source does not match the pinned commit: '+path)
        sources.append(dict(path=path, sha256_lf=digest))
    assert data['mission']['mission_settings']['mission']['level'] == 'levels/air_falklands.bin'
    imports = data['mission']['imports']['import_record']
    assert any(r['file'].lower().endswith('air_falklands/airfields_part.blk') for r in imports)
    includes = data['include']['imports']['import_record']
    for rank in ('top','high'):
        assert any(r['file'].lower().endswith(f'template_airfields_{rank}_ranks.blk') for r in includes)
    assert data['main']['toGround'] and data['flight']['toGround']
    spawns=data['decor']['triggers']['airfield_spawn_decor']['actions']['unitSpawnOnObjectGroup']
    assert any(s['type']=='air_main_radar' for s in spawns)
    assert sum(s['type']=='air_flight_radar' for s in spawns)==4
    prefab=data['prefab']
    assert prefab['classname']=='composit'
    prefab_id=Path(FILES['prefab']).stem
    anchors=[]
    for kind in ('air_main_radar','air_flight_radar'):
        matching=[n for n in prefab['node'] if n.get('type')==kind]
        if not matching:
            matching=[n for n in prefab['node'] if n.get('name')==kind]
        anchors.extend(matching)
    # Select by explicit node type; fail rather than silently changing the
    # ordering/schema if the pinned source structure differs.
    if len(anchors)!=3:
        raise ValueError('Expected one main and two flight radar anchors')
    radar=[n['tm'][3] for n in anchors]
    placements=[]
    for rank in ('top','high'):
        for group in data[rank]['units']['objectGroups']:
            assert group['unit_class']==prefab_id
            placements.append(dict(name=group['name'],team=group['props']['army'],tm=group['tm'],source=FILES[rank]))
    return dict(schema=1,commit=COMMIT,version='2.59.0.74',sources=sources,
                map_id='air_falklands',mission='air_falklands_BfD_norespawn',
                prefab=prefab_id,geometry_source=FILES['prefab'],
                radars=radar,start=prefab['airfield']['start'],end=prefab['airfield']['end'],
                width=prefab['airfield']['width'],placements=placements)

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root',type=Path)
    parser.add_argument('--output',type=Path,default=Path(__file__).resolve().parents[2]/'cmd/acmi/data/airfields.json')
    args=parser.parse_args()
    result=build(args.source_root)
    args.output.parent.mkdir(parents=True,exist_ok=True)
    args.output.write_text(json.dumps(result,indent=2,ensure_ascii=False,allow_nan=False)+'\n',encoding='utf-8')
    print(f'Wrote {len(result["placements"])} reviewed placements to {args.output}')
