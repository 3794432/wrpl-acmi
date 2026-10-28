# Windows single-file build and release

[简体中文](standalone-build.zh-CN.md) · [Project README](../README.md) · [Terrain guide](../tools/terrain/README.md)

The root `build.ps1` builds two independently usable executables. The terrain installer bundles its conversion runtime, with no pre-generated `.data` files or other game-data payload.

| Executable | Purpose | Runtime requirements |
| --- | --- | --- |
| `wrpl-acmi.exe` | Converts a client replay or a server replay shard directory to ACMI; normal lookup tables are embedded. | Windows. No game, terrain installation, Python, Go, Git, or Tacview is needed to export. Tacview is needed to view the result. |
| `wrpl-terrain-installer.exe` | Converts the user's local game assets and installs terrain in Tacview. | Windows with system Windows PowerShell and local War Thunder assets, or a manual existing-converted-source override. Portable CPython and four pinned packages are bundled; no user-installed Python, Go, Git, network, or game-data download is needed at runtime. |

## Build the programs

Use Windows PowerShell and Go 1.25.6 or newer, from the repository root:

```powershell
.\build.ps1
```

The default output directory is `dist\wrpl-acmi`. This default build creates the self-contained installer; no game installation, Datamine checkout, or terrain conversion is needed on the build machine. Building does not install terrain into Tacview.

At **build time**, the script fetches hash-pinned open-source CPython runtime and wheel artifacts. Exact runtime and package versions and artifact hashes are governed by `tools/terrain/runtime-lock.json`. These downloads contain the conversion environment, not game data. Runtime execution uses the bundled environment offline.

| Build option | Purpose |
| --- | --- |
| `-OutputRoot` | Package directory; defaults to `dist\wrpl-acmi`. |
| `-PythonRuntime <directory>` | Select a prepared portable runtime directory. It must satisfy the pinned runtime contract. |
| `-RuntimeCache <directory>` | Select the build cache for runtime artifacts. This is separate from the installer's converted-map cache. |

```powershell
.\build.ps1 -PythonRuntime "C:\Build\terrain-runtime" -RuntimeCache "C:\Build\runtime-cache"
```

The former `-WithTerrain` and `-TerrainSource` options are rejected with a migration error. Do not embed a converted game-data directory in a public installer. For developer conversion, invoke `tools/terrain/build-terrain.ps1` directly as described in the [terrain guide](../tools/terrain/README.md#build-from-scratch); that manual Python/Datamine pipeline is separate from the end-user automatic local reader. To install an already converted directory, use installer `-source` at runtime.

The package includes the two EXEs, documentation, licenses, and supporting files. Each EXE can run independently of adjacent package folders. `build-manifest.json` records build information and executable hashes. `BUILD-INCOMPLETE.txt` remains during a build or after failure and is removed only when the package is complete.

## Convert and install local terrain

Place `wrpl-terrain-installer.exe` in the War Thunder game root containing `levels/` and `aces.vromfs.bin`, then run it:

```powershell
.\wrpl-terrain-installer.exe
```

The default game root is the **directory containing the EXE**, not the current working directory. An explicit game path allows running the EXE from elsewhere:

```powershell
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -resolution 4096
```

The installer reads local `levels/*.bin` terrain and `aces.vromfs.bin` configuration. Game assets remain read-only by default. It converts the 71 known maps into a local cache and installs to `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`. It adds no runway geometry.

| Installer option | Meaning |
| --- | --- |
| `-game <directory>` | Local game root; defaults to the EXE directory. |
| `-cache <directory>` | Converted-map cache and logs; defaults to `%LOCALAPPDATA%\wrpl-acmi\terrain`. |
| `-resolution 4096` | Conversion resolution; 4096 is the default. |
| `-destination <directory>` | Override the Tacview installation directory. |
| `-profile auto` | Default: detect Tacview and select height encoding; fall back to 1.9.4 if Tacview cannot be found. |
| `-profile 1.9.4` | Use 1.9.4-compatible height encoding. |
| `-profile 1.9.5` | Use the native signed-height layout for 1.9.5. |
| `-validate-only` | Check inputs without changing the destination. Does not convert all maps or prove full conversion succeeds. |
| `-convert-only` | Generate the converted cache without installing into Tacview. |
| `-source <directory>` | Manual existing-converted-source override; skips automatic game reading. |
| `-atlas` | Install the technical Atlas layout; pair with exporter `-terrain-layout atlas`. |

```powershell
# Check local inputs without installing or converting the entire catalogue
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -validate-only

# Convert into a selected cache without installing
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -cache "D:\TerrainCache" -convert-only

# Install a manually prepared converted directory without reading game assets
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

Map conversion caches and logs are retained. At completion the installer prints the cache path and asks users to clean it manually when no longer needed. Deleting a cache requires conversion again on a later installation. Rerunning checks source hashes before reusing cached results, including after game updates. Keep the reported logs when diagnosing a failure. The build-time runtime cache and runtime converted-map cache serve different purposes.

The installer verifies converted-source metadata and SHA-256 before installation. With `-source`, supply both `AtlasReferences.json` and `GeoReferences.json` alongside the complete converted map set. `-validate-only` in this mode checks the supplied source without touching the destination. Validation does not establish that Tacview has loaded or rendered the terrain.

## Coordinate layout and compatibility

Both programs default to geographic placement: 14 reviewed WGS84 estimates and 57 Atlas fallbacks. Atlas fallback is a technical display location. To choose pure Atlas, select it in both programs:

```powershell
.\wrpl-terrain-installer.exe -game "C:\Games\War Thunder" -atlas
.\wrpl-acmi.exe -terrain-layout atlas -in "replay.wrpl" -out "replay-atlas.acmi"
```

Different layouts or reference configurations cause tracks and terrain to diverge. Fully quit and restart Tacview after installation, then re-export existing ACMI from the original WRPL. Exporter `-alt` changes height only.

The 71-map catalogue includes Japanese Port (`avn_japan`) and Red Desert (`avg_red_desert`). This installer change does not expand runway geometry. The installer supports upgrading older terrain installations, including the 1.9.4 format. A failed format-selection step does not complete installation; retain its reported log.

## Distribution and licenses

Release the newly built runtime-bearing installer. Historical installers containing 69- or 71-map terrain payloads, and workflows that copy those assets, do not produce this local-game installer.

Game assets and pre-generated terrain are not included in the new EXE. Conversion occurs on the user's machine. The extracted runtime includes a `LICENSES/` directory containing CPython and bundled dependency license notices; preserve these notices when preparing or distributing a runtime.

Project code is AGPL-3.0; dependencies retain their own licenses. See the [terrain licensing notes](../tools/terrain/README.md#input-sources-and-licensing) and [converter notices](../tools/terrain/converter/THIRD-PARTY-NOTICES.md). Game assets belong to Gaijin Entertainment. Reducing game-data redistribution does not establish permission to extract, convert, use, or redistribute those assets.
