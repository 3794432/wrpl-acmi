#!/usr/bin/env python3
"""Run air-map terrain exports serially through one worker process per map."""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
from typing import Any

from targets import TargetReferenceError, load_reference_ids


SCHEMA_VERSION = 1
HASH_CHUNK = 8 << 20
DEFAULT_TIMEOUT = 20 * 60
MAP_ID_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
SCRIPT_DIR = Path(__file__).resolve().parent
WORKER = SCRIPT_DIR / "worker.py"


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def atomic_write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    text = json.dumps(
        value, ensure_ascii=False, indent=2, allow_nan=False, sort_keys=False
    ) + "\n"
    temporary = path.with_name(path.name + f".tmp.{os.getpid()}")
    try:
        with temporary.open("w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            try:
                temporary.unlink()
            except OSError:
                pass


def stream_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while True:
            data = handle.read(HASH_CHUNK)
            if not data:
                break
            digest.update(data)
    return digest.hexdigest()


def short_error(value: Any, limit: int = 600) -> str:
    text = " ".join(str(value).split())
    return text[:limit]


def valid_id(value: Any) -> bool:
    return isinstance(value, str) and bool(MAP_ID_RE.fullmatch(value))


def load_catalog(path: Path) -> list[dict[str, Any]]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"cannot read catalog {path}: {exc}") from exc
    if isinstance(value, dict) and isinstance(value.get("entries"), list):
        value = value["entries"]
    if not isinstance(value, list):
        raise ValueError("catalog must be a JSON array of entries")
    entries: list[dict[str, Any]] = []
    seen: set[str] = set()
    for index, item in enumerate(value):
        if not isinstance(item, dict):
            raise ValueError(f"catalog item {index} is not an object")
        map_id = item.get("id")
        if not valid_id(map_id):
            raise ValueError(f"catalog item {index} has an invalid id: {map_id!r}")
        if map_id in seen:
            raise ValueError(f"catalog has duplicate id: {map_id}")
        seen.add(map_id)
        entries.append(item)
    return entries


def flatten_ids(values: list[str] | None) -> list[str] | None:
    if values is None:
        return None
    result: list[str] = []
    seen: set[str] = set()
    for value in values:
        for part in value.split(","):
            item = part.strip()
            if item and item not in seen:
                seen.add(item)
                result.append(item)
    return result


def result_paths(root: Path, map_id: str) -> tuple[Path, Path, Path, Path]:
    folder = root / "maps" / map_id
    return (
        folder,
        folder / f"{map_id}.data",
        folder / "metadata.json",
        folder / "CustomHeightmapList.xml",
    )


def write_simple_result(root: Path, map_id: str, status: str, error: str, **extra: Any) -> None:
    folder, _, _, _ = result_paths(root, map_id)
    value = {
        "schema_version": SCHEMA_VERSION,
        "id": map_id,
        "status": status,
        "error": error,
        "finished_utc": utc_now(),
    }
    value.update(extra)
    atomic_write_json(folder / "result.json", value)


def _canonical_json(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, allow_nan=False, sort_keys=True, separators=(",", ":"))


def resume_status(
    root: Path, entry: dict[str, Any], resolution: int
) -> tuple[bool, str]:
    map_id = entry["id"]
    folder, data_path, metadata_path, xml_path = result_paths(root, map_id)
    if not metadata_path.is_file():
        return False, "metadata.json is absent"
    if not data_path.is_file():
        return False, "data file is absent"
    if not xml_path.is_file():
        return False, "CustomHeightmapList.xml is absent"
    try:
        metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        return False, f"metadata.json is invalid: {exc}"
    if not isinstance(metadata, dict) or metadata.get("status") != "ok":
        return False, "metadata status is not ok"
    if metadata.get("id") != map_id:
        return False, "metadata id differs"
    request = metadata.get("request")
    if not isinstance(request, dict) or request.get("resolution") != resolution:
        return False, "requested resolution differs"
    source_meta = metadata.get("source")
    source_value = entry.get("source_path")
    if not isinstance(source_meta, dict) or not isinstance(source_value, str):
        return False, "metadata source is absent"
    source = Path(source_value)
    if not source.is_file():
        return False, "source file is absent"
    if source_meta.get("path") != str(source):
        return False, "metadata source path differs"
    try:
        source_hash = stream_sha256(source)
    except OSError as exc:
        return False, f"cannot hash source: {exc}"
    if source_meta.get("sha256") != source_hash:
        return False, "source SHA256 differs"
    catalog_meta = metadata.get("catalog")
    if not isinstance(catalog_meta, dict):
        return False, "metadata catalog parameters are absent"
    for key in (
        "map_coord0",
        "map_coord1",
        "water_level",
        "heightmap_format",
        "has_hm2",
        "has_lmap",
        "has_frt",
        "explicit_target",
    ):
        if _canonical_json(catalog_meta.get(key)) != _canonical_json(entry.get(key)):
            return False, f"catalog {key} differs"
    config_value = entry.get("config_path")
    if not isinstance(config_value, str) or not config_value:
        return False, "catalog config_path is absent"
    config_path = Path(config_value)
    if not config_path.is_file():
        return False, "catalog level config is absent"
    if config_path.stat().st_size > 2 * 1024**2:
        return False, "catalog level config exceeds 2 MiB"
    if catalog_meta.get("config_path") != str(config_path):
        return False, "catalog config_path differs"
    try:
        config_hash = stream_sha256(config_path)
    except OSError as exc:
        return False, f"cannot hash catalog level config: {exc}"
    if catalog_meta.get("config_sha256") != config_hash:
        return False, "catalog level config SHA256 differs"
    if catalog_meta.get("config_size") != config_path.stat().st_size:
        return False, "catalog level config size differs"
    output = metadata.get("output")
    if not isinstance(output, dict):
        return False, "metadata output is absent"
    if output.get("data_file") != data_path.name:
        return False, "metadata data filename differs"
    recorded_hash = output.get("data_sha256")
    if not isinstance(recorded_hash, str) or not re.fullmatch(r"[0-9a-f]{64}", recorded_hash):
        return False, "metadata data SHA256 is invalid"
    try:
        actual_hash = stream_sha256(data_path)
    except OSError as exc:
        return False, f"cannot hash data: {exc}"
    if actual_hash != recorded_hash:
        return False, "data SHA256 differs"
    return True, "data/source/config hashes and terrain parameters match"


def write_resumed_result(root: Path, entry: dict[str, Any]) -> None:
    map_id = entry["id"]
    folder, data_path, metadata_path, xml_path = result_paths(root, map_id)
    try:
        metadata = json.loads(metadata_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        metadata = {}
    output = metadata.get("output") if isinstance(metadata, dict) else None
    output = output if isinstance(output, dict) else {}
    decoder = metadata.get("format_decoder_info") if isinstance(metadata, dict) else None
    decoder = decoder if isinstance(decoder, dict) else {}
    atomic_write_json(
        folder / "result.json",
        {
            "schema_version": SCHEMA_VERSION,
            "id": map_id,
            "status": "ok",
            "resumed": True,
            "finished_utc": utc_now(),
            "width": output.get("width"),
            "height": output.get("height"),
            "data_file": str(Path("maps") / map_id / data_path.name),
            "xml_file": str(Path("maps") / map_id / xml_path.name),
            "metadata_file": str(Path("maps") / map_id / metadata_path.name),
            "data_sha256": output.get("data_sha256"),
            "mode": decoder.get("heightmap_mode"),
            "sample_bounds_m": output.get("sample_bounds_m"),
        },
    )


def manifest_counts(entries: dict[str, dict[str, Any]]) -> dict[str, int]:
    counts = {
        "selected": len(entries),
        "ok": 0,
        "skipped": 0,
        "missing": 0,
        "error": 0,
        "unknown_id": 0,
    }
    for record in entries.values():
        status = record.get("status")
        if status in counts:
            counts[status] += 1
    counts["pending"] = counts["selected"] - (
        counts["ok"]
        + counts["skipped"]
        + counts["missing"]
        + counts["error"]
        + counts["unknown_id"]
    )
    return counts


def read_result(folder: Path) -> dict[str, Any] | None:
    path = folder / "result.json"
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="output root")
    parser.add_argument(
        "--catalog", type=Path, required=True
    )
    parser.add_argument(
        "--references",
        type=Path,
        help="authoritative target JSON, normally tools/terrain/AtlasReferences.json",
    )
    parser.add_argument("--expected-targets", type=int)
    parser.add_argument("--resolution", type=int, default=4096)
    parser.add_argument(
        "--ids",
        nargs="*",
        default=None,
        help="optional ids, separated by spaces and/or commas",
    )
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    args = parser.parse_args(argv)
    if args.resolution < 2:
        parser.error("--resolution must be at least 2")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")
    if args.references is not None and args.ids is not None:
        parser.error("--references and explicit --ids are mutually exclusive")
    if args.expected_targets is not None and args.references is None:
        parser.error("--expected-targets requires --references")
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    output = args.output.resolve()
    catalog_path = args.catalog.resolve()
    output.mkdir(parents=True, exist_ok=True)
    (output / "maps").mkdir(parents=True, exist_ok=True)
    (output / "tmp").mkdir(parents=True, exist_ok=True)
    try:
        catalog = load_catalog(catalog_path)
    except ValueError as exc:
        print(f"batch: {short_error(exc)}", file=sys.stderr)
        return 2
    by_id = {entry["id"]: entry for entry in catalog}
    requested_ids = flatten_ids(args.ids)
    reference_ids: list[str] | None = None
    if args.references is not None:
        try:
            reference_ids = load_reference_ids(
                args.references.resolve(), args.expected_targets
            )
        except TargetReferenceError as exc:
            print(f"batch: {short_error(exc)}", file=sys.stderr)
            return 2
        requested_ids = reference_ids
    air_entries = [entry for entry in catalog if bool(entry.get("is_air"))]
    if requested_ids is None:
        selected = air_entries
    else:
        selected = [entry for entry in air_entries if entry["id"] in requested_ids]

    started = utc_now()
    started_clock = time.perf_counter()
    manifest_path = output / "manifest.json"
    manifest: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "started_utc": started,
        "finished_utc": None,
        "status": "running",
        "catalog": str(catalog_path),
        "output": str(output),
        "resolution": args.resolution,
        "timeout_seconds": args.timeout,
        "resume": bool(args.resume),
        "requested_ids": requested_ids,
        "references": str(args.references.resolve()) if args.references else None,
        "entries": {},
    }
    records = manifest["entries"]

    unknown = []
    if requested_ids is not None:
        known_air = {entry["id"] for entry in air_entries}
        for map_id in requested_ids:
            if map_id not in known_air:
                unknown.append(map_id)
                records[map_id] = {
                    "id": map_id,
                    "status": "unknown_id",
                    "error": "id is absent from catalog or is_air is false",
                    "finished_utc": utc_now(),
                    "elapsed_seconds": 0.0,
                }
    total = len(selected) + len(unknown)
    manifest["counts"] = manifest_counts(records)
    manifest["counts"]["pending"] = total
    atomic_write_json(manifest_path, manifest)
    print(
        f"batch: selected {len(selected)} air maps"
        + (f", {len(unknown)} unknown ids" if unknown else "")
        + f", resolution={args.resolution}, output={output}",
        flush=True,
    )
    for map_id in unknown:
        print(f"[unknown] {map_id}: not an air catalog id", flush=True)

    processed = 0
    for position, entry in enumerate(selected, start=1):
        map_id = entry["id"]
        record: dict[str, Any] = {
            "id": map_id,
            "catalog_index": catalog.index(entry),
            "status": "pending",
            "started_utc": utc_now(),
        }
        records[map_id] = record
        print(f"[{position}/{total}] {map_id}: running", flush=True)
        item_started = time.perf_counter()
        source_value = entry.get("source_path")
        source = Path(source_value) if isinstance(source_value, str) else None
        if entry.get("exists") is False or source is None or not source.is_file():
            error = f"source file is missing: {source_value}"
            record.update(
                {
                    "status": "missing",
                    "finished_utc": utc_now(),
                    "elapsed_seconds": round(time.perf_counter() - item_started, 6),
                    "error": error,
                }
            )
            try:
                write_simple_result(output, map_id, "missing", error)
            except OSError as exc:
                record["result_write_error"] = short_error(exc)
        else:
            if args.resume:
                valid, reason = resume_status(output, entry, args.resolution)
                if valid:
                    try:
                        write_resumed_result(output, entry)
                    except OSError as exc:
                        record["result_write_error"] = short_error(exc)
                    record.update(
                        {
                            "status": "skipped",
                            "finished_utc": utc_now(),
                            "elapsed_seconds": round(time.perf_counter() - item_started, 6),
                            "reason": reason,
                        }
                    )
                    print(
                        f"[{position}/{total}] {map_id}: skipped (hashes match)",
                        flush=True,
                    )
                else:
                    record["resume_rejected"] = reason
            if record["status"] != "skipped":
                task_dir = Path(
                    tempfile.mkdtemp(prefix=f"{map_id}.", dir=output / "tmp")
                )
                entry_path = (task_dir / "entry.json").resolve()
                try:
                    atomic_write_json(entry_path, entry)
                    environment = os.environ.copy()
                    environment.update(
                        {
                            "OMP_NUM_THREADS": "1",
                            "OPENBLAS_NUM_THREADS": "1",
                            "MKL_NUM_THREADS": "1",
                            "NUMEXPR_NUM_THREADS": "1",
                            "TEMP": str(task_dir),
                            "TMP": str(task_dir),
                            "TMPDIR": str(task_dir),
                            "PYTHONUNBUFFERED": "1",
                            "PYTHONIOENCODING": "utf-8",
                        }
                    )
                    command = [
                        sys.executable,
                        "-B",
                        str(WORKER.resolve()),
                        "--entry",
                        str(entry_path),
                        "--output",
                        str(output),
                        "--resolution",
                        str(args.resolution),
                    ]
                    returncode: int | None = None
                    stdout = ""
                    stderr = ""
                    timed_out = False
                    try:
                        completed = subprocess.run(
                            command,
                            cwd=SCRIPT_DIR,
                            env=environment,
                            capture_output=True,
                            text=True,
                            encoding="utf-8",
                            errors="replace",
                            timeout=args.timeout,
                            check=False,
                        )
                        returncode = completed.returncode
                        stdout = completed.stdout or ""
                        stderr = completed.stderr or ""
                    except subprocess.TimeoutExpired as exc:
                        timed_out = True
                        stdout = (
                            exc.stdout.decode("utf-8", "replace")
                            if isinstance(exc.stdout, bytes)
                            else (exc.stdout or "")
                        )
                        stderr = (
                            exc.stderr.decode("utf-8", "replace")
                            if isinstance(exc.stderr, bytes)
                            else (exc.stderr or "")
                        )
                        error = f"timeout after {args.timeout} seconds"
                        try:
                            write_simple_result(
                                output, map_id, "error", error, timeout=True
                            )
                        except OSError as write_exc:
                            record["result_write_error"] = short_error(write_exc)
                        record.update(
                            {
                                "status": "error",
                                "error": error,
                                "finished_utc": utc_now(),
                                "elapsed_seconds": round(
                                    time.perf_counter() - item_started, 6
                                ),
                                "worker_stdout_tail": short_error(stdout),
                                "worker_stderr_tail": short_error(stderr),
                            }
                        )

                    if not timed_out:
                        folder, data_path, metadata_path, xml_path = result_paths(
                            output, map_id
                        )
                        result = read_result(folder)
                        status = result.get("status") if result else None
                        if returncode == 0 and status == "ok":
                            missing_paths = [
                                path.name
                                for path in (data_path, metadata_path, xml_path)
                                if not path.is_file()
                            ]
                            if missing_paths:
                                error = "worker succeeded but missing " + ", ".join(
                                    missing_paths
                                )
                                record.update(
                                    {
                                        "status": "error",
                                        "error": error,
                                        "returncode": returncode,
                                        "finished_utc": utc_now(),
                                        "elapsed_seconds": round(
                                            time.perf_counter() - item_started, 6
                                        ),
                                        "worker_stdout_tail": short_error(stdout),
                                        "worker_stderr_tail": short_error(stderr),
                                    }
                                )
                                try:
                                    write_simple_result(
                                        output, map_id, "error", error
                                    )
                                except OSError as exc:
                                    record["result_write_error"] = short_error(exc)
                            else:
                                record.update(
                                    {
                                        "status": "ok",
                                        "mode": result.get("mode"),
                                        "width": result.get("width"),
                                        "height": result.get("height"),
                                        "source_sha256": result.get("source_sha256"),
                                        "data_file": result.get("data_file"),
                                        "metadata_file": result.get("metadata_file"),
                                        "xml_file": result.get("xml_file"),
                                        "returncode": returncode,
                                        "finished_utc": utc_now(),
                                        "elapsed_seconds": round(
                                            time.perf_counter() - item_started, 6
                                        ),
                                        "worker_stdout_tail": short_error(stdout),
                                        "worker_stderr_tail": short_error(stderr),
                                    }
                                )
                        else:
                            if status == "missing":
                                final_status = "missing"
                            else:
                                final_status = "error"
                            if result is None and returncode == 0:
                                error = (
                                    f"worker exited 0 but result.json was not found in "
                                    f"{folder}"
                                )
                            else:
                                error = (
                                    result.get("error")
                                    if isinstance(result, dict)
                                    else None
                                ) or stderr or stdout or f"worker exited {returncode}"
                            record.update(
                                {
                                    "status": final_status,
                                    "error": short_error(error),
                                    "returncode": returncode,
                                    "finished_utc": utc_now(),
                                    "elapsed_seconds": round(
                                        time.perf_counter() - item_started, 6
                                    ),
                                    "worker_stdout_tail": short_error(stdout),
                                    "worker_stderr_tail": short_error(stderr),
                                }
                            )
                            if result is None:
                                try:
                                    write_simple_result(
                                        output,
                                        map_id,
                                        final_status,
                                        short_error(error),
                                        returncode=returncode,
                                    )
                                except OSError as exc:
                                    record["result_write_error"] = short_error(exc)
                except Exception as exc:
                    error = short_error(exc)
                    record.update(
                        {
                            "status": "error",
                            "error": error,
                            "finished_utc": utc_now(),
                            "elapsed_seconds": round(
                                time.perf_counter() - item_started, 6
                            ),
                        }
                    )
                    try:
                        write_simple_result(output, map_id, "error", error)
                    except OSError as write_exc:
                        record["result_write_error"] = short_error(write_exc)
                finally:
                    shutil.rmtree(task_dir, ignore_errors=True)
                print(
                    f"[{position}/{total}] {map_id}: {record['status']}"
                    + (f" {record['mode']}" if record.get("mode") else "")
                    + " "
                    f"{record.get('elapsed_seconds', 0):.2f}s"
                    + (
                        f" ({short_error(record.get('error'))})"
                        if record.get("error")
                        else ""
                    ),
                    flush=True,
                )

        processed += 1
        manifest["counts"] = manifest_counts(records)
        manifest["counts"]["pending"] = max(0, total - processed)
        atomic_write_json(manifest_path, manifest)

    counts = manifest_counts(records)
    counts["pending"] = 0
    manifest["counts"] = counts
    manifest["finished_utc"] = utc_now()
    manifest["elapsed_seconds"] = round(time.perf_counter() - started_clock, 6)
    failed = counts["error"] + counts["missing"] + counts["unknown_id"]
    manifest["status"] = "completed_with_errors" if failed else "complete"
    atomic_write_json(manifest_path, manifest)
    print(
        "batch: complete "
        f"ok={counts['ok']} skipped={counts['skipped']} "
        f"missing={counts['missing']} error={counts['error']} "
        f"elapsed={manifest['elapsed_seconds']:.2f}s",
        flush=True,
    )
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
