# Single-file release build

[简体中文](standalone-build.zh-CN.md) · [Project README](../README.md)

`build.ps1` produces two Windows executables. They can be copied and used separately:

| Executable | Purpose | Runtime requirements |
| --- | --- | --- |
| `wrpl-acmi.exe` | Converts one client replay or one server replay shard directory to ACMI. All normal replay lookup data, including `ecshashes`, is compiled into the executable. | Windows. No adjacent `data/` directory, converted or installed terrain, War Thunder client, Python, Go, Git, or Tacview is required to export. Tacview is needed only to view the ACMI. |
| `wrpl-terrain-installer.exe` | Installs a converted War Thunder terrain set into Tacview. The terrain payload may be appended at build time. | Windows PowerShell (`powershell.exe`). A complete offline installer also needs several gigabytes of free disk space for temporary extraction and installation. The game, Python, Go, Git, and converter scripts are not required at install time. |

The two files have separate jobs and can be distributed independently. A build without terrain input still produces the terrain installer, but that copy contains installer logic only. It is not a complete offline terrain package.

## Build

The source build requires Windows PowerShell and Go 1.25.6 or newer. Run it from the repository root. The default output directory is `dist\wrpl-acmi`.

### Exporter and logic-only installer

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

Builds write `build-manifest.json` with the output hashes and whether the terrain installer includes data. A `BUILD-INCOMPLETE.txt` marker is present while a build is running or after it fails and is removed only after the package is complete.

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

## Coordinate layout

Geographic placement is the default. It combines the reviewed georeferences with the Atlas fallback. Choose installer `-atlas` for technical Atlas coordinates only when the matching ACMI export uses:

```powershell
.\wrpl-acmi.exe -terrain-layout atlas ...
```

Keep the installer `-atlas` option and exporter `-terrain-layout` setting consistent. A terrain install and an ACMI export in different layouts will not align. Geographic remains the default on both sides.

## Distribution limits

Game height data is not committed to Git. A payload-bearing installer contains the converted data in its appended package. The source tree stores the converter, installer and configuration; build the data-bearing executable locally from converted game terrain.

The embedded replay defaults make the exporter independent of package folders. `wrpl-acmi.exe -ecshashes` defaults to `embedded`; an explicit JSON path overrides the table, and an empty value disables it. If no War Thunder terrain is installed, Tacview can show its default ocean or background terrain, while the exported aircraft, ground objects, missiles, bombs, and countermeasures remain in the ACMI. Terrain installation changes the displayed landscape, not the replay object export.
