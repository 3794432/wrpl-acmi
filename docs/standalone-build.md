# Windows single-file build and release

[简体中文](standalone-build.zh-CN.md) · [Project README](../README.md)

The root `build.ps1` script produces an exporter and a terrain installer. Each executable can be copied and distributed independently; the build options determine whether the installer includes terrain data.

| Executable | Purpose | Runtime requirements |
| --- | --- | --- |
| `wrpl-acmi.exe` | Converts one client replay or one server replay shard directory to ACMI. All normal replay lookup data, including `ecshashes`, is compiled into the executable. | Windows. No adjacent `data/` directory, converted or installed terrain, War Thunder client, Python, Go, Git, or Tacview is required to export. Tacview is needed only to view the ACMI. |
| `wrpl-terrain-installer.exe` | Installs a converted War Thunder terrain set into Tacview. The terrain payload may be appended at build time. | Windows PowerShell (`powershell.exe`). A complete offline installer also needs several gigabytes of free disk space for temporary extraction and installation. The game, Python, Go, Git, and converter scripts are not required at install time. |

Without terrain input, the installer contains only installation logic and requires an external converted directory at installation time. Tacview is needed to view the results, while terrain installation is optional for replay export.

## Choose a build method

The source build requires Windows PowerShell and Go 1.25.6 or newer. Run it from the repository root. The default output directory is `dist\wrpl-acmi`.

### Build the programs without terrain

```powershell
.\build.ps1
```

This builds `wrpl-acmi.exe` and `wrpl-terrain-installer.exe`. The exporter is complete as a single file. The terrain installer has no embedded terrain and cannot install War Thunder heights until it receives a converted source with `-source`, or until the build is rerun with `-TerrainSource` or `-WithTerrain`.

### Package an existing converted terrain set

```powershell
.\build.ps1 -TerrainSource ".\converted-terrain"
```

`-TerrainSource` accepts an already converted terrain directory, such as the result of the 69-map conversion. The build appends that payload to `wrpl-terrain-installer.exe`. Preparing or packaging an existing payload with this option does not require Python, Git, a War Thunder client, or Tacview on the build machine. The source tree and Go toolchain are still required to build the executables.

`-TerrainSource` does not convert, reshape, or relocate the supplied maps. It does not change the converter's default `-out` behavior either; a converter run without `-out` continues to write to the current working directory.

### Convert and embed during the build

```powershell
.\build.ps1 -WithTerrain `
  -GameRoot "C:\Path\To\War Thunder" `
  -DatamineRoot ".\War-Thunder-Datamine" `
  -Python ".\.venv\Scripts\python.exe"
```

This converts the 69 maps from the local game and pinned Datamine checkout, then embeds the result in the same installer executable. It requires the converter prerequisites described in the [terrain guide](../tools/terrain/README.md). Use `-ResumeTerrain` with the same arguments to resume an interrupted conversion. `-TerrainSource` and `-WithTerrain` are mutually exclusive.

### Build options and completion

| Option | Purpose |
| --- | --- |
| `-OutputRoot` | Package directory; defaults to `dist\wrpl-acmi`. |
| `-TerrainSource` | Package an existing converted directory. |
| `-WithTerrain` | Convert and embed terrain during the build. |
| `-ResumeTerrain` | Resume conversion; requires `-WithTerrain`. |
| `-GameRoot` | Local War Thunder installation. |
| `-DatamineRoot` | Pinned Datamine checkout. |
| `-Python` | Conversion interpreter; defaults to `python`. |

Without `-ResumeTerrain`, conversion refuses an existing nonempty terrain output directory. Building does not install terrain into Tacview.

`build-manifest.json` records executable hashes and whether the installer contains data. `BUILD-INCOMPLETE.txt` remains during a build or after failure, and is removed only when the package is complete.

## Install terrain

With an embedded payload, the default install uses the included 69-map set:

```powershell
.\wrpl-terrain-installer.exe
```

Use an external converted directory instead:

```powershell
.\wrpl-terrain-installer.exe -source "C:\Terrain\converted-terrain"
```

The supported options are:

| Option | Meaning |
| --- | --- |
| `-source <directory>` | Optional converted terrain directory. Overrides the embedded payload. Required when `wrpl-terrain-installer.exe` was built without terrain data. |
| `-destination <directory>` | Install directory. Default: `%APPDATA%\Tacview\Data\Terrain\Custom\WarThunderMaps`. |
| `-profile auto` | Detect the installed Tacview version and apply its matching height encoding. This is the default. |
| `-profile 1.9.4` | Create the 1.9.4-compatible height encoding. |
| `-profile 1.9.5` | Install the native signed-height layout used by 1.9.5. |
| `-validate-only` | Validate the source and write nothing to the destination. |
| `-atlas` | Install the technical Atlas coordinate layout. Pair it with exporter `wrpl-acmi.exe -terrain-layout atlas`. |

`-profile auto` looks for the Tacview executable and selects the matching encoding. If it cannot find Tacview, it falls back to the 1.9.4 profile. Running the installer again can reuse its cache. For the full 69-map set at the default 4096 resolution, allow at least 8 GB of free disk space for temporary extraction, original rasters and the 1.9.4 height copies.

The installer verifies source data against its SHA-256 metadata before writing the target. Validation confirms the supplied data and supported encoding path; it does not establish that a Tacview GUI loaded or rendered the terrain correctly.

An old 1.9.4 aggregate index can remain after its cache or format state has been removed. Matching War Thunder entries are adopted and their missing height copies rebuilt. Entries with another file path, different geometry or duplicate IDs remain protected and produce an error instead of being overwritten. A failed format-selection step does not complete the 1.9.4 installation; retain the reported `install.log` when diagnosing it.

## Coordinate layout

Both programs default to geographic placement, combining reviewed georeferences with Atlas fallback. To use technical Atlas coordinates, select the layout in both programs:

```powershell
.\wrpl-terrain-installer.exe -atlas
.\wrpl-acmi.exe -terrain-layout atlas -in "replay.wrpl" -out "replay-atlas.acmi"
```

Different layouts or reference configurations will cause tracks and terrain to diverge. Restart Tacview after installation and re-export existing ACMI from the original WRPL. The exporter's `-alt` option changes height only and cannot correct horizontal alignment.

## Distribution limits

Game height data is not committed to Git. A payload-bearing installer contains the converted data in its appended package. The source tree stores the converter, installer and configuration; build the data-bearing executable locally from converted game terrain.

The embedded replay defaults make the exporter independent of package folders. `wrpl-acmi.exe -ecshashes` defaults to `embedded`; an explicit JSON path overrides the table, and an empty value disables it. If no War Thunder terrain is installed, Tacview can show its default ocean or background terrain, while the exported aircraft, ground objects, missiles, bombs, and countermeasures remain in the ACMI. Terrain installation changes the displayed landscape, not the replay object export.
