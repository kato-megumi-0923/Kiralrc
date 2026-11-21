"""从网易云抓取参考歌词文本（匿名请求 + 限速，避免 405）。"""
import json
import sys
import time
import urllib.parse
import urllib.request

UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120 Safari/537.36"
HDR = {"User-Agent": UA, "Referer": "https://music.163.com/"}


def get(url, tries=3):
    for i in range(tries):
        req = urllib.request.Request(url, headers=HDR)
        try:
            with urllib.request.urlopen(req, timeout=20) as r:
                return json.loads(r.read().decode("utf-8"))
        except Exception as e:
            print("   retry", type(e).__name__, e)
            time.sleep(3)
    return None


def search(kw, limit=5):
    u = "https://music.163.com/api/search/get/web?s=%s&type=1&limit=%d" % (urllib.parse.quote(kw), limit)
    d = get(u)
    if not d or d.get("code") != 200:
        return []
    return (d.get("result") or {}).get("songs") or []


def lyric(sid):
    u = "https://music.163.com/api/song/lyric?id=%s&lv=1&kv=1&tv=-1" % sid
    d = get(u)
    if not d:
        return None, None
    lrc = ((d.get("lrc") or {}).get("lyric") or "").strip()
    tlrc = ((d.get("tlyric") or {}).get("lyric") or "").strip()
    return lrc, tlrc


if __name__ == "__main__":
    queries = [
        ("t1", "ずっと見ていたい 芹澤優", 124),
        ("t2", "白狐 艾秩曼", 271),
        ("t3", "走在乡间的小路上 卓依婷", 186),
        ("t4", "烟雨濛濛", 242),
    ]
    out = {}
    for tag, kw, dur in queries:
        print("==", tag, kw)
        songs = search(kw)
        time.sleep(4)
        cands = []
        for s in songs:
            d = round((s.get("duration") or 0) / 1000)
            cands.append({"id": s["id"], "name": s["name"],
                          "artist": ",".join(a["name"] for a in s.get("artists", [])),
                          "dur": d, "delta": abs(d - dur)})
        cands.sort(key=lambda c: c["delta"])
        for c in cands[:3]:
            print(f"   {c['id']:>10} {c['name']} | {c['artist']} | {c['dur']}s Δ{c['delta']}")
        picked = None
        for c in cands:
            lrc, tlrc = lyric(c["id"])
            time.sleep(4)
            n = len([x for x in (lrc or "").splitlines() if x.strip() and "[" in x])
            print(f"      -> id={c['id']} lyric_lines={n}")
            if n >= 10:
                picked = {**c, "lrc": lrc, "tlrc": tlrc}
                break
        out[tag] = picked
    with open("out/netease_raw.json", "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=1)
    print("saved out/netease_raw.json")
