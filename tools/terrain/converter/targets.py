"""Load the installer's authoritative map-ID set.

AtlasReferences.json is the source of truth for package membership.  The
catalog can discover additional local levels, but only IDs in this document
are converted.
"""

from __future__ import annotations

import json
import math
import re
from pathlib import Path
from typing import Any


MAP_ID_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")


class TargetReferenceError(ValueError):
    """The authoritative target document is malformed or inconsistent."""


def load_reference_ids(path: Path, expected_count: int | None = None) -> list[str]:
    path = Path(path)
    try:
        document: Any = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise TargetReferenceError(f"cannot read target references {path}: {exc}") from exc
    if not isinstance(document, dict) or not document:
        raise TargetReferenceError(
            f"{path} must be a nonempty JSON object mapping map IDs to references"
        )
    result: list[str] = []
    seen: set[str] = set()
    for map_id, value in document.items():
        if not isinstance(map_id, str) or not MAP_ID_RE.fullmatch(map_id):
            raise TargetReferenceError(f"invalid map ID in {path}: {map_id!r}")
        if map_id in seen:
            raise TargetReferenceError(f"duplicate map ID in {path}: {map_id}")
        if not isinstance(value, dict):
            raise TargetReferenceError(f"reference for {map_id} must be a JSON object")
        latitude = value.get("latitude")
        longitude = value.get("longitude")
        if (
            not isinstance(latitude, (int, float))
            or isinstance(latitude, bool)
            or not isinstance(longitude, (int, float))
            or isinstance(longitude, bool)
            or not math.isfinite(latitude)
            or not math.isfinite(longitude)
            or not -85 <= latitude <= 85
            or not -180 <= longitude <= 180
        ):
            raise TargetReferenceError(
                f"reference for {map_id} needs finite latitude [-85,85] and longitude [-180,180]"
            )
        seen.add(map_id)
        result.append(map_id)
    if expected_count is not None and len(result) != expected_count:
        raise TargetReferenceError(
            f"{path} contains {len(result)} map IDs; expected exactly {expected_count}"
        )
    return result
