"""Tacview native custom elevation resources, independent of map textures."""
import hashlib
import math
import os
import xml.etree.ElementTree as ET
from pathlib import Path


def location(x, z, lat=0., lon=0., center_x=0., center_z=0.):
    # Match cmd/acmi.localToLatLon: cos uses the converted latitude.
    latitude = lat + (z - center_z) / 111320.
    if abs(latitude) >= 85:
        raise ValueError('terrain reference too near a pole')
    longitude = lon + (x - center_x) / (111320. * math.cos(math.radians(latitude)))
    if not -180 <= longitude <= 180:
        raise ValueError('terrain crosses the antimeridian; choose another reference')
    return longitude, latitude


def write_xml(folder: Path, meta: dict, lat=0., lon=0., center_x=0., center_z=0.):
    folder = Path(folder)
    root = ET.Element('Resources')
    listing = ET.SubElement(root, 'CustomHeightmapList')
    node = ET.SubElement(listing, 'CustomHeightmap', Layer='Real World', ID='WarThunder.' + meta['id'])
    for name, value in [('File', meta['data_file']), ('Visible', 'true'),
                        ('BigEndian', '0'), ('Signed', 'true'),
                        ('Width', meta['width']), ('Height', meta['height']),
                        ('AltitudeFactor', '1'), ('AltitudeOffset', '0'), ('Projection', 'Quad')]:
        ET.SubElement(node, name).text = str(value)
    x0, z0, x1, z1 = meta['sample_bounds_m']
    for name, x, z in [('BottomLeft', x0, z0), ('BottomRight', x1, z0),
                        ('TopRight', x1, z1), ('TopLeft', x0, z1)]:
        longitude, latitude = location(x, z, lat, lon, center_x, center_z)
        corner = ET.SubElement(node, name)
        ET.SubElement(corner, 'Longitude').text = format(longitude, '.12f')
        ET.SubElement(corner, 'Latitude').text = format(latitude, '.12f')
    ET.indent(root, space='  ')
    path = folder / 'CustomHeightmapList.xml'
    tmp = path.with_suffix('.xml.tmp')
    ET.ElementTree(root).write(tmp, encoding='utf-8', xml_declaration=True)
    os.replace(tmp, path)
    return path


def write_data(folder: Path, map_id: str, heights, geometry: dict):
    import numpy as np
    folder = Path(folder)
    folder.mkdir(parents=True, exist_ok=True)
    h, width = heights.shape
    if h < 2 or width < 2 or h * width > 64 * 1024**2:
        raise ValueError('invalid output raster dimensions')
    name = map_id + '.data'
    path = folder / name
    tmp = path.with_suffix('.data.tmp')
    digest = hashlib.sha256()
    ymin = math.inf
    ymax = -math.inf
    # North-to-south rows, west-to-east samples, signed little-endian meters.
    with tmp.open('wb') as f:
        for start in range(h, 0, -64):
            rows = np.asarray(heights[max(0, start - 64):start][::-1], dtype=np.float32)
            if not np.isfinite(rows).all():
                raise ValueError('nonfinite terrain height')
            ymin = min(ymin, float(rows.min()))
            ymax = max(ymax, float(rows.max()))
            rounded = np.rint(rows)
            if (rounded < -32767).any() or (rounded > 32767).any():
                raise ValueError('height exceeds Tacview signed 16-bit meter range')
            raw = rounded.astype('<i2').tobytes()
            f.write(raw)
            digest.update(raw)
    os.replace(tmp, path)
    return dict(geometry, id=map_id, width=width, height=h, data_file=name,
                data_bytes=path.stat().st_size, data_sha256=digest.hexdigest(),
                minimum_m=ymin, maximum_m=ymax, output_type='int16_le',
                output_row_order='north_to_south', output_unit='meter',
                altitude_factor=1, altitude_offset=0, quantization_max_error_m=0.5)
