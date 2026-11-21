"""歌词文本：网易云抓取、参考文本读取、汉字→拼音。"""
from __future__ import annotations

import base64
import json
import re
import time
import urllib.parse
import urllib.request
from pathlib import Path

HAN = re.compile(r"[\u4e00-\u9fff]")
KANA = re.compile(r"[\u3040-\u30ff]")
UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
      "(KHTML, like Gecko) Chrome/120 Safari/537.36")
HEADERS = {"User-Agent": UA, "Referer": "https://music.163.com/"}
META_RE = re.compile(r"^\[(ti|ar|al|by|tool|offset|length):", re.I)
TRACK_RE = re.compile(r"^\s*\d{1,3}\s*[-._]\s*")
BRACKET_RE = re.compile(r"[\(\[（【][^\)\]）】]*[\)\]）】]")
DASH_RE = re.compile(r"\s*[-–—]{1,}\s*")
CREDIT_RE = re.compile(r"^\s*(作词|作曲|编曲|制作|出品|监制|混音|母带|演唱|OP|SP)\s*[:：]")


def _get_json(url: str, tries: int = 3, timeout: int = 20):
    for _ in range(tries):
        try:
            req = urllib.request.Request(url, headers=HEADERS)
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return json.loads(r.read().decode("utf-8"))
        except Exception:
            time.sleep(3)
    return None


def netease_search(keyword: str, limit: int = 5) -> list[dict]:
    url = ("https://music.163.com/api/search/get/web?s=%s&type=1&limit=%d"
           % (urllib.parse.quote(keyword), limit))
    data = _get_json(url)
    if not data or data.get("code") != 200:
        return []
    songs = (data.get("result") or {}).get("songs") or []
    return [{"id": s["id"], "name": s["name"],
             "artist": ",".join(a["name"] for a in s.get("artists", [])),
             "duration": round((s.get("duration") or 0) / 1000)}
            for s in songs]


def netease_lyric(song_id: int) -> tuple[str, str]:
    """返回 (原文 LRC, 翻译 LRC)。匿名请求注意限速，否则会 405。"""
    url = f"https://music.163.com/api/song/lyric?id={song_id}&lv=1&kv=1&tv=-1"
    data = _get_json(url) or {}
    return ((data.get("lrc") or {}).get("lyric") or "",
            (data.get("tlyric") or {}).get("lyric") or "")


def netease_match(keyword: str, duration: float | None = None, interval: float = 4.5) -> dict | None:
    """网易云音乐：按曲名搜索并挑时长最接近的条目。"""
    songs = netease_search(keyword)
    if not songs:
        return None
    if duration:
        songs.sort(key=lambda s: abs(s["duration"] - duration))
    else:
        songs.sort(key=lambda s: s["duration"])
    time.sleep(min(interval, 1.5))
    for cand in songs[:3]:
        lrc, tlrc = netease_lyric(cand["id"])
        time.sleep(min(interval, 1.5))
        n = len([x for x in lrc.splitlines() if x.strip()])
        if n >= 8:
            return {**cand, "source": "网易云", "lrc": lrc, "tlyric": tlrc, "lines": n}
    return None


def qq_match(keyword: str, duration: float | None = None) -> dict | None:
    """QQ 音乐：搜索并拉取原词 + 中文译文。"""
    try:
        payload = json.dumps({
            "req_1": {
                "method": "DoSearchForQQMusicDesktop",
                "module": "music.search.SearchCgiService",
                "param": {"query": keyword, "num_per_page": 5, "page_num": 1},
            }
        }).encode("utf-8")
        req = urllib.request.Request(
            "https://u.y.qq.com/cgi-bin/musicu.fcg",
            data=payload,
            headers={"User-Agent": UA, "Referer": "https://y.qq.com/", "Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=12) as r:
            data = json.loads(r.read().decode("utf-8", errors="replace"))
        songs = (((data.get("req_1") or {}).get("data") or {}).get("body") or {}).get("song", {}).get("list") or []
        if not songs:
            return None
        cands = []
        for s in songs:
            mid = s.get("mid") or ""
            if not mid:
                continue
            singer = ",".join(a.get("name", "") for a in (s.get("singer") or []))
            cands.append({
                "id": mid,
                "name": s.get("name") or "",
                "artist": singer,
                "duration": int(s.get("interval") or 0),
            })
        if not cands:
            return None
        if duration:
            cands.sort(key=lambda s: abs(s["duration"] - duration))
        for cand in cands[:3]:
            lurl = f"https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?songmid={cand['id']}&format=json&nobase64=0"
            lreq = urllib.request.Request(lurl, headers={"User-Agent": UA, "Referer": "https://y.qq.com/"})
            with urllib.request.urlopen(lreq, timeout=12) as r:
                ld = json.loads(r.read().decode("utf-8", errors="replace"))
            lrc = base64.b64decode(ld.get("lyric") or "").decode("utf-8", errors="replace") if ld.get("lyric") else ""
            tlrc = base64.b64decode(ld.get("trans") or "").decode("utf-8", errors="replace") if ld.get("trans") else ""
            n = len([x for x in lrc.splitlines() if x.strip()])
            if n >= 8:
                return {**cand, "source": "QQ音乐", "lrc": lrc, "tlyric": tlrc, "lines": n}
    except Exception:
        return None
    return None


def kugou_match(keyword: str, duration: float | None = None) -> dict | None:
    """酷狗音乐：搜歌取 hash 再拉取 LRC。"""
    try:
        kw = urllib.parse.quote(keyword)
        req = urllib.request.Request(
            f"http://mobilecdn.kugou.com/api/v3/search/song?format=json&keyword={kw}&page=1&pagesize=5",
            headers={"User-Agent": UA},
        )
        with urllib.request.urlopen(req, timeout=12) as r:
            info = ((json.loads(r.read().decode("utf-8", errors="replace")).get("data") or {}).get("info")) or []
        if not info:
            return None
        if duration:
            info.sort(key=lambda s: abs(int(s.get("duration") or 0) - duration))
        for s in info[:3]:
            h = s.get("hash") or ""
            dur_s = int(s.get("duration") or 0)
            if not h:
                continue
            sreq = urllib.request.Request(
                f"https://lyrics.kugou.com/search?ver=1&man=yes&client=pc&hash={h}&duration={dur_s * 1000}",
                headers={"User-Agent": UA},
            )
            with urllib.request.urlopen(sreq, timeout=12) as r:
                cands = json.loads(r.read().decode("utf-8", errors="replace")).get("candidates") or []
            if not cands:
                continue
            c0 = cands[0]
            dreq = urllib.request.Request(
                f"https://lyrics.kugou.com/download?ver=1&client=pc&id={c0['id']}&accesskey={c0['accesskey']}&fmt=lrc&charset=utf8",
                headers={"User-Agent": UA},
            )
            with urllib.request.urlopen(dreq, timeout=12) as r:
                dd = json.loads(r.read().decode("utf-8", errors="replace"))
            lrc = base64.b64decode(dd.get("content") or "").decode("utf-8", errors="replace") if dd.get("content") else ""
            n = len([x for x in lrc.splitlines() if x.strip()])
            if n >= 8:
                return {
                    "source": "酷狗音乐",
                    "id": h,
                    "name": s.get("songname") or "",
                    "artist": s.get("singername") or "",
                    "duration": dur_s,
                    "lrc": lrc,
                    "tlyric": "",
                    "lines": n,
                }
    except Exception:
        return None
    return None


def lrclib_match(keyword: str, duration: float | None = None) -> dict | None:
    """LRCLIB：免鉴权国际歌词库，适合欧美 / 日文 / 独立音乐兜底。"""
    try:
        kw = urllib.parse.quote(keyword)
        req = urllib.request.Request(f"https://lrclib.net/api/search?q={kw}", headers={"User-Agent": UA})
        with urllib.request.urlopen(req, timeout=12) as r:
            arr = json.loads(r.read().decode("utf-8", errors="replace"))
        synced = [x for x in (arr or []) if isinstance(x, dict) and (x.get("syncedLyrics") or "").strip()]
        if not synced:
            return None
        if duration:
            synced.sort(key=lambda s: abs(float(s.get("duration") or 0) - duration))
        s0 = synced[0]
        lrc = s0["syncedLyrics"]
        n = len([x for x in lrc.splitlines() if x.strip()])
        if n >= 8:
            return {
                "source": "LRCLIB",
                "id": s0.get("id") or 0,
                "name": s0.get("trackName") or "",
                "artist": s0.get("artistName") or "",
                "duration": round(float(s0.get("duration") or 0)),
                "lrc": lrc,
                "tlyric": "",
                "lines": n,
            }
    except Exception:
        return None
    return None


def best_match(keyword: str, duration: float | None = None, interval: float = 4.5) -> dict | None:
    """多源顺序兜底搜歌词：网易云 → QQ音乐 → 酷狗音乐 → LRCLIB。"""
    for fn in (
        lambda: netease_match(keyword, duration, interval=interval),
        lambda: qq_match(keyword, duration),
        lambda: kugou_match(keyword, duration),
        lambda: lrclib_match(keyword, duration),
    ):
        hit = fn()
        if hit:
            return hit
    return None


def ref_lines(path: str | Path) -> list[str]:
    """从 LRC 或纯文本里取歌词行（去掉时间戳与元信息）。"""
    text = open(path, encoding="utf-8-sig").read()
    out = []
    for raw in text.splitlines():
        t = raw.strip()
        if not t or t == "---":
            continue
        if t.startswith("[") and "]" in t:
            t = t.split("]", 1)[1].strip()
            if not t:
                continue
        if META_RE.match(t) or CREDIT_RE.match(t):
            continue
        out.append(t)
    return out


DEFAULT_POLYPHONES: dict[str, list[str]] = {
    "还得": ["hai", "dei"],
    "走在": ["zou", "zai"],
    "看着": ["kan", "zhe"],
    "笑着": ["xiao", "zhe"],
    "听着": ["ting", "zhe"],
    "吹着": ["chui", "zhe"],
    "睡着": ["shui", "zhao"],
    "觉得": ["jue", "de"],
    "记得": ["ji", "de"],
    "舍不得": ["she", "bu", "de"],
    "音乐": ["yin", "yue"],
    "快乐": ["kuai", "le"],
    "长大": ["zhang", "da"],
    "长夜": ["chang", "ye"],
    "重逢": ["chong", "feng"],
    "重新": ["chong", "xin"],
    "行囊": ["xing", "nang"],
    "一行泪": ["yi", "hang", "lei"],
    "宿命": ["su", "ming"],
    "温柔地": ["wen", "rou", "de"],
    "静静地": ["jing", "jing", "de"],
    "悄悄地": ["qiao", "qiao", "de"],
    "慢慢地": ["man", "man", "de"],
}

COMMON_POLYPHONE_CHARS: dict[str, list[str]] = {
    "了": ["le", "liao"],
    "着": ["zhe", "zhao", "zhuo"],
    "得": ["de", "dei"],
    "还": ["hai", "huan"],
    "长": ["chang", "zhang"],
    "行": ["xing", "hang"],
    "重": ["chong", "zhong"],
    "乐": ["le", "yue"],
    "觉": ["jue", "jiao"],
    "地": ["de", "di"],
}


def han_chars(text: str) -> list[str]:
    return [c for c in text if HAN.match(c)]


def load_polyphones(override_path: str | Path | None = None) -> dict[str, list[str]]:
    """加载多音字词表（内置白名单 + polyphone.toml 人工覆盖，KI-5）。"""
    import tomllib
    from . import config

    table: dict[str, list[str]] = {k: list(v) for k, v in DEFAULT_POLYPHONES.items()}
    if override_path is None:
        raw = config.opt("polyphone_file", "polyphone.toml")
        p = Path(raw) if Path(raw).is_absolute() else config.ROOT / raw
    else:
        p = Path(override_path)

    if p.exists():
        try:
            with open(p, "rb") as f:
                data = tomllib.load(f)
            phrases = data.get("phrases", data)
            for k, v in phrases.items():
                if not isinstance(k, str):
                    continue
                syls = v.split() if isinstance(v, str) else list(v)
                h_chars = han_chars(k)
                if h_chars and len(h_chars) == len(syls):
                    table["".join(h_chars)] = [s.lower() for s in syls]
        except Exception:
            pass
    return table


def pinyin_syllables(text: str,
                     overrides: dict[str, list[str]] | None = None) -> list[str]:
    """整句转无声调拼音（拼音数 == 汉字数），非汉字自动丢弃，并应用多音字词表覆盖（KI-5）。"""
    from pypinyin import Style, lazy_pinyin

    syls = list(lazy_pinyin(text, style=Style.NORMAL, errors=lambda x: None))
    chars = han_chars(text)
    if len(syls) != len(chars) or not chars:
        return syls

    table = overrides if overrides is not None else load_polyphones()
    if not table:
        return syls

    # 只在同一连续汉字片段内匹配词组，避免跨标点误配
    pos_to_han: dict[int, int] = {}
    h_idx = 0
    for idx, ch in enumerate(text):
        if HAN.match(ch):
            pos_to_han[idx] = h_idx
            h_idx += 1

    locked = [False] * len(chars)
    for phrase in sorted(table.keys(), key=len, reverse=True):
        if not phrase or any(not HAN.match(c) for c in phrase):
            continue
        p_syls = table[phrase]
        if len(p_syls) != len(phrase):
            continue
        start = 0
        while True:
            pos = text.find(phrase, start)
            if pos == -1:
                break
            h_indices = [pos_to_han[pos + k] for k in range(len(phrase)) if (pos + k) in pos_to_han]
            if len(h_indices) == len(phrase) and not any(locked[i] for i in h_indices):
                for i, s in zip(h_indices, p_syls):
                    syls[i] = s
                    locked[i] = True
            start = pos + 1

    return syls


def detect_polyphones(text: str,
                      overrides: dict[str, list[str]] | None = None) -> list[dict]:
    """检测句中常见多音字及其当前采用的读音，供 UI 或 CLI 提示与覆写（KI-5）。"""
    chars = han_chars(text)
    syls = pinyin_syllables(text, overrides=overrides)
    out: list[dict] = []
    for idx, (ch, sy) in enumerate(zip(chars, syls)):
        if ch in COMMON_POLYPHONE_CHARS:
            out.append({
                "index": idx,
                "char": ch,
                "pinyin": sy,
                "candidates": COMMON_POLYPHONE_CHARS[ch],
            })
    return out


def lines_with_translations(
    text: str, dup_tol: float = 0.06
) -> tuple[list[tuple[float, str]], list[list[str]]]:
    """从带时间戳的歌词里拆出原词和同时间戳的译文行。

    返回 ([(时间, 原词)], [[译文1, ...], ...])，两个列表等长。
    网易云 / 双语 LRC 中「原词 + 译文」共用同一个时间戳；对齐时只用原词，
    输出逐字 LRC 时再把译文按对齐后的行首时间戳贴回。
    """
    from . import format as fmt

    orig: list[tuple[float, str]] = []
    trans: list[list[str]] = []
    for ln in fmt.parse(text).lines:
        t = ln.text.strip()
        if not t or CREDIT_RE.match(t):
            continue
        if orig and abs(ln.start - orig[-1][0]) < dup_tol:
            trans[-1].append(t)
            continue
        orig.append((ln.start, t))
        trans.append([])
    return orig, trans


def lines_with_times(text: str, dup_tol: float = 0.06) -> list[tuple[float, str]]:
    """从带时间戳的歌词里取 [(时间, 行)]，同一时间戳的第二行按「译文」丢弃。"""
    orig, _ = lines_with_translations(text, dup_tol=dup_tol)
    return orig


def merge_tlyric(orig_lrc: str, tlyric: str, tol: float = 0.25) -> str:
    """把网易云分开返回的 lrc（原文）和 tlyric（中文译文）按时间戳合并成双语 LRC。"""
    if not tlyric or not tlyric.strip():
        return orig_lrc
    from . import format as fmt

    orig = fmt.parse(orig_lrc)
    tr_lines = [ln for ln in fmt.parse(tlyric).lines if ln.text.strip() and not CREDIT_RE.match(ln.text.strip())]
    if not orig.lines or not tr_lines:
        return orig_lrc

    used: set[int] = set()
    out_lines: list[fmt.Line] = []
    for ln in orig.lines:
        out_lines.append(ln)
        if not ln.text.strip() or CREDIT_RE.match(ln.text.strip()):
            continue
        best_i, best_d = -1, tol + 1.0
        for i, tr in enumerate(tr_lines):
            if i in used:
                continue
            d = abs(tr.start - ln.start)
            if d <= tol and d < best_d:
                best_i, best_d = i, d
        if best_i >= 0:
            used.add(best_i)
            out_lines.append(fmt.Line(ln.start, tr_lines[best_i].text.strip()))
    return fmt.Lrc(metas=orig.metas, lines=out_lines).render()


def query_from_filename(stem: str) -> str:
    """文件名 → 网易云搜索词。

    "02 - 好想好想 - 赵薇"        -> "好想好想 赵薇"
    "艾秩曼+-+白狐（TV+Version）" -> "艾秩曼 白狐"
    "[110316]ソラノシズク"        -> "ソラノシズク"
    """
    s = BRACKET_RE.sub(" ", stem)
    s = TRACK_RE.sub("", s)
    s = s.replace("_", " ").replace("+", " ").replace("\u3000", " ")
    s = DASH_RE.sub(" ", s)
    s = re.sub(r"\s{2,}", " ", s)
    return s.strip(" -._")
