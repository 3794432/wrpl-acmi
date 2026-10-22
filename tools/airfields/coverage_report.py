"""Write bilingual, per-map runway coverage from the embedded source catalogue."""
import argparse
import json
from pathlib import Path


def write_reports(catalog, directory):
    entries={item['map_id']:item for item in catalog['maps']}
    coverage=catalog['coverage']
    configured=sum(item['status']=='Configured' for item in coverage.values())
    missions=sum(len(item.get('missions',[])) for item in entries.values())
    for chinese in (False,True):
        if chinese:
            lines=['# 跑道逐地图覆盖', '', '[English](ACMI_AIRFIELDS_COVERAGE.md)', '',
                   f'目录含 {len(entries)} 张地图，{configured} 张取得可用源定义，共 {missions} 个任务配置。', '',
                   '定义数包含不同任务、等级和条件分支，不代表同时出现的机场数。条件布局还需要回放证据确认；来源缺失时不补造跑道。', '',
                   '| 地图 | 任务数 | 可用源定义数 | 状态 |','|---|---:|---:|---|']
        else:
            lines=['# Runway coverage by map', '', '[中文](ACMI_AIRFIELDS_COVERAGE.zh-CN.md)', '',
                   f'The catalogue contains {len(entries)} maps, {configured} with usable source definitions, and {missions} mission entries.', '',
                   'Definition counts include mission, rank and conditional alternatives, not simultaneously active airports. Conditional layouts require replay evidence. Missing source geometry is not fabricated.', '',
                   '| Map | Missions | Usable source definitions | Status |','|---|---:|---:|---|']
        for map_id in sorted(entries):
            row=coverage[map_id]
            status=('源定义已配置' if chinese else 'Source configured') if row['status']=='Configured' else ('见下方说明' if chinese else 'See notes below')
            lines.append(f'| `{map_id}` | {row["missions"]} | {row["source_runway_candidates"]} | {status} |')
        lines.extend(['', '## 未配置原因' if chinese else '## Unconfigured sources', ''])
        for map_id in sorted(entries):
            if coverage[map_id]['status']=='Configured': continue
            reason=entries[map_id].get('reason','No sourced geometry')
            if chinese:
                if map_id=='air_race_phiphi_islands': reason='已核对竞速任务：为空中出发，未定义陆地跑道。'
                elif map_id=='caribbean_islands': reason='当前有界来源扫描尚未找到可用陆地跑道定义；不据此断言地图没有跑道。'
            lines.append(f'- `{map_id}`: {reason}')
        lines.extend(['', ('每个机场的实际选择和高度来源见 ACMI 的 `WT_Airfield*` 字段。' if chinese else 'Actual selection and elevation provenance are recorded in the ACMI `WT_Airfield*` fields.'), ''])
        name='ACMI_AIRFIELDS_COVERAGE'+('.zh-CN' if chinese else '')+'.md'
        (directory/name).write_text('\n'.join(lines),encoding='utf-8')


if __name__=='__main__':
    repo=Path(__file__).resolve().parents[2]
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--catalog',type=Path,default=repo/'cmd/acmi/data/airfields_all.json')
    parser.add_argument('--output-dir',type=Path,default=repo/'docs')
    args=parser.parse_args()
    args.output_dir.mkdir(parents=True,exist_ok=True)
    write_reports(json.loads(args.catalog.read_text(encoding='utf-8-sig')),args.output_dir)
