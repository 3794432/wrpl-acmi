"""Assemble source-backed runway catalogues; optionally sample converted terrain.

All height reads are bounded random reads from signed 16-bit metre rasters.
No game asset decoding, Tacview installation, or network access occurs here.
"""
import argparse
import hashlib
import json
import math
from pathlib import Path
import re
import struct

MAX_JSON = 32 * 1024 * 1024


def read_json(path):
    with Path(path).open('rb') as stream:
        raw = stream.read(MAX_JSON + 1)
    if len(raw) > MAX_JSON:
        raise ValueError(f'oversize catalogue/metadata: {path}')
    return json.loads(raw)


def known_ids():
    source = Path(__file__).resolve().parents[2] / 'internal/terrainmap/terrainmap.go'
    text = source.read_text(encoding='utf-8')
    match = re.search(r'var knownIDs = strings.Fields\(`([^`]+)`\)', text)
    if not match:
        raise ValueError('cannot read canonical map identifiers')
    return sorted(match[1].split())


def point(value):
    return (isinstance(value, list) and len(value) == 3
            and all(isinstance(v, (int, float)) and math.isfinite(v) and abs(v) < 1e7 for v in value))


def transform(matrix, local):
    if not isinstance(matrix, list) or len(matrix) != 4 or not all(point(row) for row in matrix) or not point(local):
        raise ValueError('invalid runway transform')
    return [matrix[3][axis] + sum(matrix[row][axis] * local[row] for row in range(3)) for axis in range(3)]


def valid_runway(start, end, width):
    return (point(start) and point(end) and isinstance(width,(int,float))
            and math.isfinite(width) and 0 < width <= 1000
            and 0 < math.hypot(end[0]-start[0],end[2]-start[2]) <= 50000)


class TerrainSampler:
    def __init__(self, root, map_id):
        folder = Path(root) / 'maps' / map_id
        metadata = read_json(folder / 'metadata.json')
        output = metadata['output']
        if metadata.get('id') != map_id or output.get('id') != map_id:
            raise ValueError('terrain map identifier mismatch')
        if output.get('output_type') != 'int16_le' or output.get('output_row_order') != 'north_to_south':
            raise ValueError('unsupported terrain raster encoding')
        self.width, self.height = output['width'], output['height']
        if not (2 <= self.width <= 16384 and 2 <= self.height <= 16384):
            raise ValueError('invalid terrain dimensions')
        self.bounds = output['sample_bounds_m']
        if len(self.bounds) != 4 or not all(math.isfinite(x) for x in self.bounds):
            raise ValueError('invalid terrain bounds')
        x0, z0, x1, z1 = self.bounds
        if x1 <= x0 or z1 <= z0:
            raise ValueError('inverted terrain bounds')
        path = (folder / output['data_file']).resolve()
        if not path.is_relative_to(folder.resolve()) or path.stat().st_size != self.width * self.height * 2:
            raise ValueError('terrain data size/path mismatch')
        # Verify each used raster once, without loading it into memory.
        digest = hashlib.sha256()
        with path.open('rb') as stream:
            while block := stream.read(1 << 20):
                digest.update(block)
        if digest.hexdigest() != output['data_sha256']:
            raise ValueError('terrain data hash mismatch')
        self.path = path
        self.provenance = {'data_sha256': output['data_sha256'],
                           'level_sha256': metadata.get('source', {}).get('sha256'),
                           'encoding': 'int16_le:north_to_south:metres', 'bounds': self.bounds}

    def sample(self, x, z):
        x0, z0, x1, z1 = self.bounds
        if not (x0 <= x <= x1 and z0 <= z <= z1):
            raise ValueError('runway point outside terrain raster')
        u = (x - x0) / (x1 - x0) * (self.width - 1)
        v = (z1 - z) / (z1 - z0) * (self.height - 1)
        left, top = min(int(u), self.width-2), min(int(v), self.height-2)
        fx, fy = u-left, v-top
        with self.path.open('rb') as stream:
            stream.seek((top*self.width+left)*2)
            a, b = struct.unpack('<hh', stream.read(4))
            stream.seek(((top+1)*self.width+left)*2)
            c, d = struct.unpack('<hh', stream.read(4))
        return (a*(1-fx)+b*fx)*(1-fy)+(c*(1-fx)+d*fx)*fy


def assemble(prefabs, missions, terrain_root=None):
    if prefabs.get('commit') != missions.get('commit') or not prefabs.get('commit'):
        raise ValueError('airport source commits differ')
    by_prefab = {p['id'].casefold(): p for p in prefabs['prefabs']}
    if len(by_prefab) != len(prefabs['prefabs']):
        raise ValueError('duplicate prefab identifiers')
    entries = {m['map_id']: m for m in missions['maps']}
    if len(entries) != len(missions['maps']):
        raise ValueError('duplicate map identifiers')
    maps, terrain_sources, coverage = [], {}, {}
    for map_id in known_ids():
        entry = entries.get(map_id, {'map_id':map_id, 'missions':[], 'reason':'No source mission found'})
        sampler = None
        height_error = ''
        if terrain_root:
            try:
                sampler = TerrainSampler(terrain_root, map_id)
                terrain_sources[map_id] = sampler.provenance
            except (OSError, ValueError, KeyError) as exc:
                height_error = str(exc)
        configured = 0
        source_count = 0
        for mission in entry.get('missions', []):
            available = 0
            for placement in mission.get('placements', []):
                prefab = by_prefab.get(str(placement.get('prefab','')).casefold())
                if not prefab:
                    continue
                placement['prefab'] = prefab['id']
                start, end = (transform(placement['tm'], prefab[key]) for key in ('start','end'))
                if not valid_runway(start,end,prefab['width']):
                    placement['geometry_unavailable'] = 'Degenerate or invalid source runway dimensions'
                    continue
                available += 1
                if sampler:
                    try:
                        heights = [sampler.sample(start[0]+(end[0]-start[0])*t,
                                                  start[2]+(end[2]-start[2])*t) for t in (.25,.5,.75)]
                        placement['ground_height'] = round(sum(heights)/len(heights),3)
                        placement['ground_height_source'] = 'TerrainHeightmapQuarterSamplesApproximation'
                    except ValueError as exc:
                        placement['height_unavailable'] = str(exc)
            for runway in mission.get('direct_runways', []):
                unknown_width = (runway.get('width_known') is False and runway.get('width') == 0
                                 and runway.get('geometry_kind') == 'NamedRunwayEndpointAreas')
                if not valid_runway(runway.get('start'),runway.get('end'),1 if unknown_width else runway.get('width',0)):
                    continue
                available += 1
                if sampler:
                    start, end = runway['start'], runway['end']
                    try:
                        runway['ground_height'] = round(sampler.sample((start[0]+end[0])/2,(start[2]+end[2])/2),3)
                        runway['ground_height_source'] = 'TerrainHeightmapMidpointApproximation'
                    except ValueError as exc:
                        runway['height_unavailable'] = str(exc)
            if available:
                configured += 1
                source_count += available
        entry['status'] = 'Configured' if configured else 'NoSourcedLandRunway'
        if height_error:
            entry['height_unavailable'] = height_error
        coverage[map_id] = {'missions':len(entry.get('missions',[])), 'missions_with_runways':configured,
                            'source_runway_candidates':source_count, 'status':entry['status']}
        maps.append(entry)
    return {'schema':2, 'commit':prefabs['commit'], 'prefabs':prefabs['prefabs'], 'maps':maps,
            'coverage':coverage, 'prefab_exclusions':prefabs.get('exclusions',[]),
            'terrain_sources':terrain_sources}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prefabs', type=Path, required=True)
    parser.add_argument('--missions', type=Path, required=True)
    parser.add_argument('--terrain-root', type=Path)
    parser.add_argument('--output', type=Path, default=Path(__file__).resolve().parents[2]/'cmd/acmi/data/airfields_all.json')
    args = parser.parse_args()
    result = assemble(read_json(args.prefabs),read_json(args.missions),args.terrain_root)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result,ensure_ascii=False,sort_keys=True,indent=2,allow_nan=False)+'\n',encoding='utf-8')
    configured = sum(v['status']=='Configured' for v in result['coverage'].values())
    print(json.dumps({'maps':len(result['maps']), 'maps_with_sourced_runways':configured,
                      'prefabs':len(result['prefabs']), 'output':str(args.output)}))


if __name__ == '__main__':
    main()
