"""Create a compact index without decoding the source levels again."""
import argparse
from collections import Counter
import json
from pathlib import Path


def summarize(root: Path, catalog: Path):
    entries = json.loads(catalog.read_text(encoding='utf-8'))
    targets = [e for e in entries if e.get('is_air')]
    rows = []
    missing = []
    pending = []
    for entry in targets:
        name = entry['id']
        path = root / 'maps' / name / 'metadata.json'
        if not entry.get('exists'):
            missing.append(name)
        if not path.is_file():
            if entry.get('exists'):
                pending.append(name)
            continue
        m = json.loads(path.read_text(encoding='utf-8'))
        output = m['output']
        rows.append(dict(id=name, **{k: output[k] for k in
                    ('width', 'height', 'data_bytes', 'data_sha256', 'minimum_m', 'maximum_m')},
                    method=m['format_decoder_info']['heightmap_mode'],
                    peak_commit_bytes=m['memoryguard']['peak_commit_bytes'],
                    elapsed_seconds=m['timing']['elapsed_seconds'], warnings=m.get('warnings', [])))
    summary = dict(target_count=len(targets), source_available=sum(bool(e.get('exists')) for e in targets),
                   converted=len(rows), source_missing=missing, errors_or_pending=pending,
                   total_data_bytes=sum(r['data_bytes'] for r in rows),
                   maximum_worker_peak_commit_bytes=max((r['peak_commit_bytes'] for r in rows), default=0),
                   worker_memory_limit_bytes=2 * 1024**3,
                   methods=dict(Counter(r['method'] for r in rows)),
                   all_available_sources_converted=not pending,
                   all_catalog_targets_converted=not missing and not pending,
                   data=rows)
    (root / 'summary.json').write_text(json.dumps(summary, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')
    lines = ['# 高程文件索引', '', f'目标 {len(targets)}；已转换 {len(rows)}；缺源 {len(missing)}。', '',
             '| 地图 ID | 高度网格 | 高度范围（m） | 方法 |', '|---|---|---|---|']
    for r in rows:
        name = r['id']
        lines.append(f"| [{name}](maps/{name}/{name}.data) | {r['width']}×{r['height']} | "
                     f"{r['minimum_m']:.1f}…{r['maximum_m']:.1f} | {r['method']} |")
    lines.extend(['', '缺少源资产：' + (', '.join(missing) or '无'),
                  '', '方法：hm2_native 为原始高度图；lmap 为地形碰撞三角网格；render_mesh 为地形渲染三角网格。'])
    (root / 'MAPS.md').write_text('\n'.join(lines) + '\n', encoding='utf-8')
    print(json.dumps({k: v for k, v in summary.items() if k != 'data'}, ensure_ascii=False))


if __name__ == '__main__':
    p = argparse.ArgumentParser()
    p.add_argument('--root', type=Path, required=True)
    p.add_argument('--catalog', type=Path, required=True)
    a = p.parse_args()
    summarize(a.root, a.catalog)
