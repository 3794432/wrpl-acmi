# Recorded radar and post-launch seeker states

[简体中文](ACMI_SENSOR_STATES.zh-CN.md) · [Export guide](acmi-export.md)

`-missile-seeker` and `-radar-modes` default to true. They are independent of
`-radar-locks`, which exports aircraft target designations. Disable either new
feature with `-missile-seeker=false` or `-radar-modes=false`. ACMI 2.1 remains the
default; no add-on is required to read the native properties or labels.

## Native properties and display

| Data | Native ACMI representation | Limit |
| --- | --- | --- |
| Recorded radar on/off and pointing | `RadarMode=0/1`, `RadarAzimuth`, `RadarElevation` | Unknown state clears the display with 0; `WT_RadarMode=unknown` distinguishes this from observed off. |
| Game sensor mode | `Label`, such as `TWS MED` | Display names are mapped separately; `WT_RadarPattern` retains the exact original ID. No invented Tacview numeric value for TWS, STT, or PD. |
| Radar seeker tracking | `LockedTargetMode=1`, supported `LockedTargetRange` | Distance is the seeker's reported estimate, not a geometric ground-truth distance. |
| Explicit ARH seeker search/tracking | `RadarMode=1` on the missile | A radar seeker block alone does not prove active-radar guidance. |
| Detailed seeker state | Native `Label`: `SRC` (search), `TRK` (track), `N/A` (unknown/unavailable), appended to the existing INS label | IR blocks have no established lock bit; zero timer is not treated as proof of lock. |
| Confirmed missile identity | Native `Name` and related existing naming properties | Ambiguous identities remain unresolved. |

The native properties and `WT_*` provenance fields passed a Tacview 1.9.4.200
batch import and save test in ACMI 2.1. Numeric empty assignments were normalized
to zero by Tacview, so provenance is essential when distinguishing unavailable
data from observed off/no lock. This checks file ingestion and persistence,
not every visual panel. A local 1.9.5 executable did not produce a saved file
in the attempted batch run; its runtime import is not claimed as verified.

## Evidence and interpretation

The projectile parser retains bounded raw seeker blocks. The supported layouts
are 607-bit radar search/track, 639-bit acquisition transition, and 283-bit IR.
Unknown lengths remain opaque. Finite coordinates and plausible unit line-of-sight
vectors are required before values are exported. No target object is guessed
from proximity, and no body-relative missile angles are fabricated from its course.

Aircraft mode interpretation currently targets replay version `0x00018C1C`.
The pinned sensor catalogue covers 1,311 model names/aliases and 298 sensor
definitions. Sensor slots retain RWR placeholders; transceiver, scan-pattern,
and signal arrays retain original asset order. Eight ambiguous model aliases
are excluded. IR/optical/passive transceivers are not reported as active radar.
One supported radar is selected deterministically when several are present.
Unknown state, a changed slot, and unavailable records clear previous values.
No scan range is invented, and optional beamwidth properties require explicit
data; the embedded table currently does not supply native beamwidths.

The legacy runtime wire layout is preserved. Captured tracking samples establish
the antenna angles as `Unk4`/`Unk5`, with right-positive azimuth and up-positive
elevation. In 87 paired F-2A ADTW samples from `#2026.09.17 13.50.59.wrpl`, median
differences from independently computed target geometry were approximately
0.063 and 0.048 degrees. This is sample validation, not universal format coverage.

## Model association

The exporter can use the exact parent entity's ammunition snapshot and consistent
seeker-kind evidence to narrow explicit model aliases. A unique high-confidence
candidate is required; unrecognized positive-count stores prevent false uniqueness.
Mixed candidates, conflicting evidence, and weak aliases do not assign a model.
Existing identities are retained. Same-replay propagation is restricted by exact
owner, generation, weapon ID, template, and weapon kind; no launch order, nearest
object, or global numeric weapon-ID mapping is used. The current alias catalogue
supports AAMs; the new association stage does not name AGMs.

## Sources and rebuilding

Edit [`cmd/acmi/data/radar_mode_labels.json`](../cmd/acmi/data/radar_mode_labels.json)
to change radar mode display names, then rebuild the EXE and re-export. This
105-entry display table is independent of the sensor catalogue. Unmapped names
fall back to their original ID. Labels omit the `Radar` prefix and `(mprf)`/`(hprf)` transceiver suffixes;
the original transceiver remains available in `WT_RadarTransceiver`.

- [WT Knowledge Docs: per-tick state](https://furro404.github.io/WT_KNOWLEDGE_DOCS/03-replays/#24-per-tick-unit-state)
- [Tacview official ACMI specification](https://raia-software-inc.gitbook.io/tacview/technical-documentation/acmi-telemetry-file-format)
- [War-Thunder-Datamine, pinned source](https://github.com/gszabi99/War-Thunder-Datamine/tree/f7952e4291e9fd7b6fe38e84020dddef925c3457)

The static catalogue is embedded, so normal builds and runtime need no network
or Datamine checkout. To explicitly regenerate it, run from the source repository root:

```powershell
python tools/radar-modes/build_radar_modes.py
python -m unittest discover -s tools/radar-modes -p "test_*.py"
```

Regeneration downloads the pinned inputs and preserves their ordering. A changed
game asset version can change index meanings; successful bounds checks alone
do not prove that a different asset revision is compatible.
