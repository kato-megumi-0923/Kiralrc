"""命令行入口：python -m kiralrc <命令>"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import traceback
from pathlib import Path

from . import config, format as fmt, pipeline, text as tx


def cmd_align(a) -> int:
    from . import pipeline

    src = Path(a.audio)
    if not src.exists():
        print(f"找不到音频: {src}")
        return 2

    def on_prog(step: int, total: int, stage: str, detail: str) -> None:
        if not getattr(a, "quiet", False):
            tail = f" ({detail})" if detail else ""
            print(f"[{step}/{total}] {stage}{tail}", flush=True)

    try:
        lrc, note = pipeline.make_lrc(
            src, ref=a.ref, lang=a.lang, query=a.query,
            ti=a.ti, ar=a.ar, al=a.al,
            force_vocal=getattr(a, "force_vocal", False),
            ignore_ref_times=getattr(a, "ignore_ref_times", False),
            ignore_ref_ends=getattr(a, "ignore_ref_ends", False),
            en_granularity=getattr(a, "en_granularity", None),
            netease_id=tx.parse_song_id(getattr(a, "netease_id", None)),
            asr_fallback=getattr(a, "asr_fallback", False),
            waveform_first=getattr(a, "waveform_first", False),
            on_progress=on_prog)
    except pipeline.NoVocalError as e:
        print(f"[skip] {src.name}: {e}")
        return 0
    except pipeline.NoLyricsError as e:
        print(f"[skip-nolyrics] {src.name}: {e}")
        return 0

    out = Path(a.out) if a.out else src.with_suffix(".lrc")
    if a.dry:
        print(lrc.render())
        print(f"\n[来源] {note}\n[dry-run] 未写入 {out}")
        return 0
    fmt.write(out, lrc)
    probs = fmt.validate_enhanced(out)
    print(f"[ok] {out}  {len(lrc.lines)} 行  | {note}")
    for p in probs:
        print("  -", p)
    return 0


def _align_worker(parser: argparse.ArgumentParser) -> int:
    """Read one alignment request per line and delimit its streamed output."""
    for request in sys.stdin:
        try:
            args = json.loads(request)
            if not isinstance(args, list) or not all(isinstance(arg, str) for arg in args):
                raise ValueError("expected a JSON array of alignment arguments")
            code = cmd_align(parser.parse_args(args))
        except SystemExit as exc:
            code = int(exc.code or 0)
        except Exception:
            traceback.print_exc()
            code = 1
        print(f"\n[kiralrc-worker-result] {code}", flush=True)
    return 0


def cmd_batch(a) -> int:
    from . import pipeline

    folder = Path(a.dir)
    if not folder.exists():
        print(f"找不到目录: {folder}")
        return 2

    def on_song(idx: int, total: int, audio: Path, status: str, note: str) -> None:
        tag = {
            "done": "✅ 完成",
            "skipped_existing": "⏭ 跳过",
            "skipped_novocal": "🔇 无主唱",
            "failed": "❌ 失败",
        }.get(status, status)
        print(f"[{idx}/{total}] {tag}  {audio.name}  | {note}", flush=True)

    res = pipeline.batch_align(
        folder, lang=a.lang, jobs=a.jobs,
        skip_existing=not a.overwrite,
        force_vocal=a.force_vocal,
        en_granularity=a.en_granularity,
        on_song=on_song,
    )
    print(
        f"\n=== 批量完成: 共 {res['total']} 首 | 生成 {res['done']} | "
        f"已有跳过 {res['skipped_existing']} | 无主唱跳过 {res['skipped_novocal']} | "
        f"失败 {res['failed']}"
    )
    return 1 if res["failed"] else 0


def cmd_fetch_lyrics(a) -> int:
    from . import download

    if not a.dir and not getattr(a, "paths_file", None):
        print("请给出要处理的目录，或用 --paths-file 指定文件清单")
        return 2
    paths = None
    if getattr(a, "paths_file", None):
        with open(a.paths_file, encoding="utf-8") as f:
            paths = [line.strip() for line in f if line.strip()]
    counts = download.run(
        a.dir, force=a.force, jobs=a.jobs, paths=paths,
        on_song=(None if a.quiet else (lambda i, n, r: print(
            f"[{i}/{n}] {r.status:<9} {r.audio.name[:44]}  {r.note}", flush=True))),
    )
    print(download.report(counts))
    return 0 if counts.failed == 0 else 1


def cmd_decrypt(a) -> int:
    from . import decrypt

    targets = []
    for raw in a.paths:
        p = Path(raw)
        if p.is_dir():
            targets += [q for q in sorted(p.rglob("*")) if q.is_file()]
        elif p.exists():
            targets.append(p)
        else:
            print(f"[skip] 找不到 {raw}")
    todo = [p for p in targets if decrypt.looks_encrypted(p)]
    if not todo:
        print("这些路径里没有 NCM / QQ 加密文件")
        return 0

    ok = bad = 0
    for p in todo:
        try:
            info = decrypt.decrypt(p, out_dir=a.out, write_tags=not a.no_tags,
                                   rename=getattr(a, "rename", False))
            ok += 1
            if not a.quiet:
                print(f"[ok] {p.name} → {info.path.name}  {info.title} / {info.artist}"
                      f"{'  id=' + str(info.song_id) if info.song_id else ''}")
                if info.tag_error:      # 音频出来了，但标签没写进去
                    print(f"  [标签未写入] {info.tag_error}")
        except decrypt.NotEncrypted as e:
            bad += 1
            print(f"[skip] {e}")
        except Exception as e:
            bad += 1
            print(f"[fail] {p.name}: {e}")
    print(f"解密 {ok} 个" + (f"，{bad} 个没成" if bad else ""))
    return 0 if bad == 0 else 1


def cmd_scrape(a) -> int:
    from . import scrape

    action = getattr(a, "action", "")
    if action == "inspect":
        paths = None
        if getattr(a, "paths_file", None):
            with open(a.paths_file, encoding="utf-8") as f:
                paths = [line.strip() for line in f if line.strip()]
        if not a.dir and not paths:
            print("请给出要检查的目录，或用 --paths-file 指定文件清单")
            return 2
        items = scrape.run_inspect(a.dir or None, paths)
        print(json.dumps(items, ensure_ascii=False), flush=True)
        return 0

    if action == "search":
        res = scrape.search_with_note(
            a.query or "", artist=a.artist or "", title=a.title or "",
            netease_id=tx.parse_song_id(a.netease_id), limit=a.limit)
        print(json.dumps(res, ensure_ascii=False), flush=True)
        return 0

    if action == "apply":
        try:
            plan = scrape.load_plan(a.plan)
        except Exception as e:
            print(f"读不了计划文件：{e}")
            return 2

        def on_item(i: int, total: int, res: dict) -> None:
            # 逐首的 ok / skipped 是布尔，汇总里是首数，换个键名免得类型撞车
            line = {"i": i, "total": total}
            line.update({k: v for k, v in res.items() if k not in ("ok", "skipped")})
            line["written"] = bool(res.get("ok"))
            line["noop"] = bool(res.get("skipped"))
            print(json.dumps(line, ensure_ascii=False), flush=True)

        summary = scrape.apply_plan(plan, on_item)
        print(json.dumps(summary, ensure_ascii=False), flush=True)
        return 0 if summary["failed"] == 0 else 1

    print("请指定 inspect / search / apply")
    return 2


def cmd_clean(a) -> int:
    from . import pipeline

    days = 0.0 if getattr(a, "all", False) else a.days
    res = pipeline.clean_cache(days=days, dry_run=a.dry)
    mb = res["freed_bytes"] / (1024 * 1024)
    prefix = "[dry-run] 将清理" if a.dry else "[ok] 已清理"
    print(f"{prefix} {res['removed']} 项缓存，释放 {mb:.2f} MB")
    for name in res["paths"]:
        print(f"  - {name}")
    return 0


def cmd_scan(a) -> int:
    from . import pipeline

    items = pipeline.scan_library(a.dir)
    if a.json:
        print(json.dumps(items, ensure_ascii=False))
        return 0
    counts = {"enhanced": 0, "line_only": 0, "needs_fix": 0, "missing": 0, "instrumental": 0}
    inst_sub = {"纯音乐": 0, "OST 原声": 0, "伴奏 / Off Vocal": 0}
    for it in items:
        st = it["status"]
        counts[st] = counts.get(st, 0) + 1
        if st == "instrumental":
            k = it.get("inst_kind") or "纯音乐"
            inst_sub[k] = inst_sub.get(k, 0) + 1
    vocal_total = len(items) - counts["instrumental"]
    print(
        f"总音频 {len(items)} 首 | 有效人声歌曲 {vocal_total} 首 "
        f"(逐字 {counts['enhanced']} · 仅整行可升级 {counts['line_only']} · "
        f"需修复 {counts['needs_fix']} · 缺失 {counts['missing']})\n"
        f"已排除纯音乐/OST/伴奏 {counts['instrumental']} 首 "
        f"(纯音乐 {inst_sub['纯音乐']} · OST 原声 {inst_sub['OST 原声']} · "
        f"伴奏/Off Vocal {inst_sub['伴奏 / Off Vocal']})"
    )
    return 0


def cmd_validate(a) -> int:
    from . import wavecheck

    audio = getattr(a, "audio", None)
    vocals = pipeline._cache_dir(Path(audio), create=False) / "vocals.wav" if audio else None
    bad = 0
    for f in a.files:
        probs = fmt.validate_enhanced(f, max_jump=a.max_jump)
        lrc = fmt.parse(f)
        if vocals is not None and vocals.is_file():
            probs.extend(wavecheck.check(lrc, vocals))
        print(f"=== {f}\n  逐字行 {sum(1 for l in lrc.lines if l.enhanced)} / {len(lrc.lines)}")
        if probs:
            bad += 1
            for p in probs:
                print("  -", p)
        else:
            print("  ✅ 通过")
    return 1 if bad else 0


def cmd_hearts(a) -> int:
    from . import hearts

    for f in a.files:
        n = hearts.wrap(f)
        print(f"[ok] {f}  加了 {n} 行 ❤️")
    return 0


def cmd_convert(a) -> int:
    for f in a.files:
        n = fmt.convert_decimals(f)
        print(f"[ok] {f}  转换 {n} 个时间戳")
    return 0


def cmd_separate(a) -> int:
    import json

    from . import audio as au
    from . import cache

    src = Path(a.audio)
    out_dir = Path(a.out) if a.out else cache.sep_dir(src)
    out_dir.mkdir(parents=True, exist_ok=True)
    prefix = out_dir / cache.safe_stem(src)

    seen = {"stage": "", "pct": -1}

    def report(stage: str, pct: int) -> None:
        if seen["stage"] == stage and seen["pct"] == pct:
            return
        seen["stage"], seen["pct"] = stage, pct
        if getattr(a, "json", False):
            print(json.dumps({"stage": stage, "pct": pct}), flush=True)
        elif not a.quiet:
            print(f"[{stage}] {pct}%", flush=True)

    cache.purge()                       # 顺手清掉过期的
    res = au.separate(src, prefix, device=a.device, on_progress=report, combine_others=True)
    for name, path in res.items():
        if getattr(a, "json", False):
            print(json.dumps({"stem": name, "path": str(path),
                              "size": path.stat().st_size,
                              "first": au.first_sound(path),   # 整轨无声时是 null
                              "duration": round(au.duration(path), 3)},
                             ensure_ascii=False), flush=True)
        else:
            print(f"[ok] {name}: {path}")
    return 0


def cmd_export_stem(a) -> int:
    from . import audio as au

    dst = au.transcode(a.src, a.dst)
    print(dst)
    return 0


def cmd_fetch(a) -> int:
    from . import text as tx

    hit = tx.best_match(a.query, a.duration)
    if not hit:
        print("没搜到")
        return 1
    print(f"{hit['artist']} - {hit['name']}  {hit['duration']}s  id={hit['id']}  行数={hit['lines']}")
    if a.out:
        Path(a.out).write_text(hit["lrc"], encoding="utf-8")
        print(f"[ok] 写入 {a.out}")
    else:
        print(hit["lrc"][:1200])
    return 0


def _detect_gpu_label() -> str:
    import subprocess
    try:
        p = subprocess.run(
            ["nvidia-smi", "--query-gpu=name", "--format=csv,noheader"],
            capture_output=True, text=True, timeout=2,
            creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0),
        )
        if p.returncode == 0 and p.stdout.strip():
            return "CUDA " + p.stdout.strip().splitlines()[0].strip()
    except Exception:
        pass
    return "CPU"


def _check_models() -> list[dict]:
    from . import hubertfa
    import importlib.util

    torch_ckpt = config.torch_home_dir() / "hub" / "checkpoints"
    hf_hub = config.hf_hub_dir()

    def _safe_cfg_path(key: str) -> tuple[str, bool]:
        try:
            p = config.path(key)
            return str(p), p.exists()
        except Exception:
            return "", False

    # 1) Demucs htdemucs（同时兼容 torch hub 缓存与 HuggingFace 缓存 models--adefossez--HTDemucs）
    demucs_hits = list(torch_ckpt.glob("955717e8*.th")) if torch_ckpt.exists() else []
    hf_demucs = hf_hub / "models--adefossez--HTDemucs"
    hf_demucs_ok = hf_demucs.exists() and any((hf_demucs / "snapshots").glob("*/*"))
    demucs_ok = bool(demucs_hits) or hf_demucs_ok
    if demucs_hits:
        demucs_path = str(demucs_hits[0])
    elif hf_demucs_ok:
        demucs_path = str(hf_demucs)
    else:
        demucs_path = str(torch_ckpt / "955717e8-8726e21a.th")

    # 2) faster-whisper large-v3
    wh_path, wh_ok = _safe_cfg_path("whisper_model")

    # 3) SOFA Mandarin ckpt & FireRedASR-AED-L（中文专精听写）
    sofa_ckpt_path, sofa_ckpt_ok = _safe_cfg_path("sofa_ckpt")
    fr_path, _ = _safe_cfg_path("firered_model")
    fr_ok = bool(fr_path and (Path(fr_path) / "model.pth.tar").exists())

    # 4) Japanese MMS Karaoke model (NextFire)
    ja_dir = hf_hub / "models--NextFire--mms-300m-ForcedAligner-karaoke-ja-Latn"
    ja_snaps = list((ja_dir / "snapshots").glob("*/model.safetensors")) if (ja_dir / "snapshots").exists() else []
    ja_bins = list((ja_dir / "snapshots").glob("*/pytorch_model.bin")) if (ja_dir / "snapshots").exists() else []
    ja_ok = bool(ja_snaps or ja_bins)

    # 5) Japanese MeCab/unidic reading analyzer
    ja_g2p_ok = bool(importlib.util.find_spec("fugashi") and importlib.util.find_spec("unidic_lite"))

    # 6) English Echora MMS Lyrics Forced Aligner
    en_dir = hf_hub / "models--hcX02--echora-mms-300m-multilingual-lyrics-forced-aligner"
    en_snaps = list((en_dir / "snapshots").glob("*/model.safetensors")) if (en_dir / "snapshots").exists() else []
    en_bins = list((en_dir / "snapshots").glob("*/pytorch_model.bin")) if (en_dir / "snapshots").exists() else []
    en_ok = bool(en_snaps or en_bins)

    return [
        {"lang": "通用", "name": "Demucs 人声分离（htdemucs）", "path": demucs_path, "exists": demucs_ok},
        {"lang": "通用", "name": "faster-whisper large-v3（ASR 句级定位）", "path": wh_path or "models/faster-whisper-large-v3", "exists": wh_ok},
        {"lang": "中文", "name": "HubertFA v0.0.7 中文歌声对齐（默认）", "path": str(hubertfa.model_dir() / "model.onnx"), "exists": hubertfa.available()},
        {"lang": "中文", "name": "SOFA 中文歌声对齐（可选）", "path": sofa_ckpt_path or "models/sofa/v1.0.0_mandarin_singing.ckpt", "exists": sofa_ckpt_ok},
        {"lang": "中文", "name": "FireRedASR-AED-L（中文专精听写）", "path": fr_path or "models/FireRedASR-AED-L", "exists": fr_ok},
        {"lang": "日文", "name": "MMS 日语卡拉 OK 歌声模型（NextFire）", "path": str(ja_dir), "exists": ja_ok},
        {"lang": "日文", "name": "fugashi + unidic-lite（日语词法读音）", "path": "Python 环境内置（MeCab / UniDic）", "exists": ja_g2p_ok},
        {"lang": "英文", "name": "MMS 英文/多语歌词模型（Echora-300M）", "path": str(en_dir), "exists": en_ok},
    ]


def cmd_cover(a) -> int:
    from . import pipeline

    p = pipeline.ensure_cover(a.audio)
    if p and p.exists():
        print(str(p))
        return 0
    return 1


def cmd_info(a) -> int:
    from . import pipeline

    st = pipeline.cache_stats()
    if getattr(a, "json", False):
        paths = {}
        for key in ("sofa_ckpt", "sofa_root", "sofa_dict",
                    "whisper_model", "work_dir"):
            try:
                p = config.path(key)
                paths[key] = {"path": str(p), "exists": p.exists()}
            except KeyError as e:
                paths[key] = {"path": "", "exists": False, "error": str(e)}
        print(json.dumps({
            "root": str(config.ROOT),
            "gpu": _detect_gpu_label(),
            "models": _check_models(),
            "paths": paths,
            "cache": {"count": st["count"], "bytes": st["bytes"]},
            "defaults": config.cfg()["defaults"],
        }, ensure_ascii=False))
        return 0

    print(f"项目根目录 : {config.ROOT}")
    for key in ("sofa_ckpt", "sofa_root", "sofa_dict",
                "whisper_model", "work_dir"):
        try:
            p = config.path(key)
            print(f"{key:<13}: {'存在' if p.exists() else '缺失'}  {p}")
        except KeyError as e:
            print(f"{key:<13}: 未配置 ({e})")
    mb = st["bytes"] / (1024 * 1024)
    print(f"缓存占用     : {st['count']} 项 ({mb:.1f} MB)")
    print(f"默认输出小数位: {config.opt('decimals', 3)}")
    return 0


def cmd_gui(a) -> int:
    exe = config.ROOT / "kiralrc-gui.exe"
    if exe.exists():
        return subprocess.call([str(exe)])
    print(f"请先构建或直接运行 {exe}")
    return 1


def main(argv: list[str] | None = None) -> int:
    argv = sys.argv[1:] if argv is None else argv
    ap = argparse.ArgumentParser(prog="kiralrc", description="歌词逐字对齐工具")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("align", help="音频 → 逐字 LRC")
    p.add_argument("audio")
    p.add_argument("--ref", help="参考歌词（LRC 或纯文本）")
    p.add_argument("--lang", default="zh")
    p.add_argument("--query", help="网易云搜索关键词，默认用文件名")
    p.add_argument("--netease-id",
                   help="网易云歌曲 ID 或链接（如 30967650 或 music.163.com/song?id=...），"
                        "给了就直接按 ID 取歌词，不靠搜索")
    p.add_argument("--asr-fallback", action="store_true",
                   help="全网未搜到歌词且无参考文件时，先用 Demucs 提纯人声再用本地 faster-whisper 听写兜底")
    p.add_argument("--ti", default="")
    p.add_argument("--ar", default="")
    p.add_argument("--al", default="")
    p.add_argument("--out")
    p.add_argument("--dry", action="store_true")
    p.add_argument("--force-vocal", action="store_true", help="跳过主唱检测，强制生成")
    p.add_argument("--ignore-ref-times", action="store_true",
                   help="忽略参考 LRC 自带时间戳，强制用 ASR 重新定位每句起止时间")
    p.add_argument("--ignore-ref-ends", action="store_true",
                   help="沿用参考 LRC 的逐句起点，重新对齐逐字时间和句尾")
    p.add_argument("--en-granularity", choices=["word", "char"], default=None,
                   help="英文/数字逐字粒度：word（按词）或 char（按字母均分）")
    p.add_argument("--waveform-first", action="store_true",
                   help="鬼畜 / 波形起音优先模式：先按人声轨波形起音与能量谷切分，SOFA 仅作参考")
    p.add_argument("--quiet", action="store_true")
    p.set_defaults(func=cmd_align)
    if argv == ["--align-worker"]:
        return _align_worker(p)

    p = sub.add_parser("batch", help="批量补齐目录下的逐字 LRC（KI-4）")
    p.add_argument("dir")
    p.add_argument("--lang", default="zh")
    p.add_argument("--jobs", type=int, default=1)
    p.add_argument("--overwrite", action="store_true", help="已存在逐字 LRC 时仍重新生成")
    p.add_argument("--force-vocal", action="store_true", help="跳过无主唱检测")
    p.add_argument("--en-granularity", choices=["word", "char"], default=None)
    p.set_defaults(func=cmd_batch)

    p = sub.add_parser("download", help="批量下载歌词（网易云 → QQ → AMLL → 酷狗，不跑模型）")
    p.add_argument("dir", nargs="?", default="", help="目录，可省略（用 --paths-file 给清单时）")
    p.add_argument("--paths-file", help="只处理清单里的文件（每行一个路径）")
    p.add_argument("--force", action="store_true", help="已有官方逐字也重新下载")
    p.add_argument("--jobs", type=int, default=2, help="并发数，默认 2（平台会限速）")
    p.add_argument("--quiet", action="store_true")
    p.set_defaults(func=cmd_fetch_lyrics)

    p = sub.add_parser("export-stem", help="把分出来的音轨转成 wav / flac / mp3")
    p.add_argument("src")
    p.add_argument("dst")
    p.set_defaults(func=cmd_export_stem)

    p = sub.add_parser("decrypt", help="解开网易云 NCM（保留歌名/歌手/封面/网易云 ID）")
    p.add_argument("paths", nargs="+", help="文件或目录，目录会递归")
    p.add_argument("--out", help="输出目录，默认放在原文件旁边")
    p.add_argument("--no-tags", action="store_true", help="只出音频，不写标签")
    p.add_argument("--rename", action="store_true",
                   help="按「歌名 - 歌手」改名（默认沿用 .ncm 的文件名，好跟同名 .lrc 配对）")
    p.add_argument("--quiet", action="store_true")
    p.set_defaults(func=cmd_decrypt)

    p = sub.add_parser("scrape", help="多候选标签与高清封面刮削（网易云 + iTunes，手动挑版本）")
    sp = p.add_subparsers(dest="action", required=True)
    pi = sp.add_parser("inspect", help="读目录或清单里每首歌的现状，输出 JSON")
    pi.add_argument("dir", nargs="?", default="", help="目录，可省略（用 --paths-file 给清单时）")
    pi.add_argument("--paths-file", help="只处理清单里的文件（每行一个路径）")
    pi.set_defaults(func=cmd_scrape)
    ps = sp.add_parser("search", help="按关键词同时搜网易云与 iTunes，输出 JSON 候选")
    ps.add_argument("query")
    ps.add_argument("--netease-id", help="已知网易云 ID：按 ID 取的那条排第一")
    ps.add_argument("--artist", default="")
    ps.add_argument("--title", default="")
    ps.add_argument("--limit", type=int, default=8)
    ps.set_defaults(func=cmd_scrape)
    pa = sp.add_parser("apply", help="按计划 JSON 逐首写回，边写边输出进度 JSON")
    pa.add_argument("plan")
    pa.set_defaults(func=cmd_scrape)

    p = sub.add_parser("clean", help="清理 work/ 中间产物缓存（默认仅清 30 天前，全清需加 --all）")
    p.add_argument("--days", type=float, default=30.0, help="仅清理超过 N 天的缓存（默认 30 天）")
    p.add_argument("--all", action="store_true", help="清理全部缓存（覆盖 --days）")
    p.add_argument("--dry", action="store_true")
    p.set_defaults(func=cmd_clean)

    p = sub.add_parser("scan", help="扫描曲库目录状态")
    p.add_argument("dir")
    p.add_argument("--json", action="store_true")
    p.set_defaults(func=cmd_scan)

    p = sub.add_parser("validate", help="校验逐字 LRC")
    p.add_argument("files", nargs="+")
    p.add_argument("--max-jump", type=float, default=3.0)
    p.add_argument("--audio", help="使用此音频已分离的人声轨，补充对齐校验")
    p.set_defaults(func=cmd_validate)

    p = sub.add_parser("hearts", help="双语歌词给翻译行加 ❤️")
    p.add_argument("files", nargs="+")
    p.set_defaults(func=cmd_hearts)

    p = sub.add_parser("convert", help="两位小数 → 三位小数")
    p.add_argument("files", nargs="+")
    p.set_defaults(func=cmd_convert)

    p = sub.add_parser("separate", help="demucs 人声分离（默认写到 work/sep，不碰曲库）")
    p.add_argument("audio")
    p.add_argument("--out", help="输出目录，默认 work/sep/<歌名>_<哈希>")
    p.add_argument("--device", help="cpu / cuda，默认自动")
    p.add_argument("--json", action="store_true", help="按行输出进度 JSON")
    p.add_argument("--quiet", action="store_true")
    p.set_defaults(func=cmd_separate)

    p = sub.add_parser("fetch", help="从网易云抓歌词")
    p.add_argument("query")
    p.add_argument("--duration", type=float)
    p.add_argument("--out")
    p.set_defaults(func=cmd_fetch)

    p = sub.add_parser("info", help="检查环境与路径")
    p.add_argument("--json", action="store_true")
    p.set_defaults(func=cmd_info)

    p = sub.add_parser("cover", help="提取或从网易云补全音频专辑封面并打印路径")
    p.add_argument("audio")
    p.set_defaults(func=cmd_cover)

    p = sub.add_parser("gui", help="启动 Cloudy 风格桌面界面")
    p.set_defaults(func=cmd_gui)

    a = ap.parse_args(argv)
    return a.func(a)


if __name__ == "__main__":
    sys.exit(main())
