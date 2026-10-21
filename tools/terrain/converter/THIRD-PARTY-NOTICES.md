# Provenance and third-party notices

Adapted on 2026-10-22 from this project's terrain-export development pipeline.
The recovered source is distributed under this repository's AGPL-3.0 license;
the full text is in `LICENSES/AGPL-3.0.txt`.

| Module | Responsibility | Provenance |
| --- | --- | --- |
| `catalog.py`, `targets.py`, `batch.py`, `worker.py`, `tacview.py` | cataloging, selection, worker orchestration, Tacview output | Project terrain-export pipeline; revised for portable paths and authoritative references |
| `container.py`, `hm2.py` | Dagor DBLD framing and compressed HM2 decoding | Project terrain-export pipeline; format behavior checked against Gaijin Dagor Engine `dagor_2024_01_02` |
| `lmap.py`, `render_mesh.py`, `frt.py` | LMp2/LTdump, render-mesh, and FRT triangle rasterization | Project terrain-export pipeline; format behavior checked against Dagor Engine serialization and scene-ray sources |
| `codec.py` | bounded LZMA, Zstandard, and Oodle stream adapters | Project terrain-export pipeline; uses package backends from `requirements.txt` |
| `memory_guard.py` | 2 GiB Windows Job object and POSIX address-space guard | Project terrain-export pipeline |
| `verify_outputs.py`, `summarize.py`, `finalize.py` | raster, XML, installer-contract, and package checks | Project terrain-export pipeline; finalizer added for exact 69-map publication |
| `tests/` | synthetic decoder, routing, hash, zero-origin, and strict-finalization regressions | Project terrain-export pipeline plus converter-specific regressions |

Format research references:

- Gaijin Entertainment, [DagorEngine](https://github.com/GaijinEntertainment/DagorEngine), tag `dagor_2024_01_02`: compressed heightmap, LMp2/LTdump, render-mesh, and scene-ray serialization behavior. No engine source is copied here.
- [War-Thunder-Datamine](https://github.com/gszabi99/War-Thunder-Datamine): level configuration field layout, `mapCoord0`, `mapCoord1`, and `water_level`. The caller supplies the checkout; configs are not redistributed.
- [quentin-dh/Dagor-Asset-Explorer](https://github.com/quentin-dh/Dagor-Asset-Explorer): community reference for the render-mesh `LandMesh` / `ShaderMesh` / `MatVData` / `CompressedData` layout followed by `render_mesh.py`.
- [majorlier/nuclear-thunder-interactive-map](https://github.com/majorlier/nuclear-thunder-interactive-map) and [FURRO404/WT_KNOWLEDGE_DOCS](https://github.com/FURRO404/WT_KNOWLEDGE_DOCS): community format references used during the original terrain research; this converter adds its own bounds checks, rasterization, and tests.
- [WT-MAP-ANALYZE](https://github.com/ShioUmiusi/WT-MAP-ANALYZE), pinned commit `c26c0069558079df6597a7bc147cb91391509f89`: geographic estimates referenced by the separate `GeoReferences.json`; attribution is retained under `../georeference/`.
- Tacview custom terrain documentation: XML, Quad projection, and signed elevation raster contract (https://raia-software-inc.gitbook.io/tacview/customization/terrain/terrain-elevation/elevation-maps).

Dependencies pinned by `requirements.txt`:

| Package | Version | License | Supplied license text |
| --- | --- | --- | --- |
| NumPy | 2.3.5 | BSD-3-Clause | `LICENSES/numpy-BSD-3-Clause.txt` |
| Pillow | 12.3.0 | HPND / MIT-CMU | `LICENSES/Pillow-HPND.txt` |
| pyooz | 0.0.8 | GPL-3.0-or-later | `LICENSES/pyooz-GPL-3.0-or-later.txt` |
| zstandard | 0.25.0 | BSD-3-Clause | `LICENSES/zstandard-BSD-3-Clause.txt` |

No compiled third-party extension, engine source archive, game asset, downloaded
level config, or generated catalog is included in this directory.
