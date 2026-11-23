"""刮削：图片尺寸解析、两个来源的候选解析、以及「只补不清空」的写盘规矩。"""

import struct

import pytest

from kiralrc import scrape


# ---------- 造素材 ----------

def png_bytes(w: int, h: int) -> bytes:
    """一张真能解开的纯色 PNG（CRC、IDAT、IEND 都齐）：封面要过完整性校验。"""
    import zlib

    def chunk(kind: bytes, body: bytes) -> bytes:
        return (struct.pack(">I", len(body)) + kind + body
                + struct.pack(">I", zlib.crc32(kind + body) & 0xFFFFFFFF))

    ihdr = struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)
    row = b"\x00" + b"\x20\x40\x60" * w       # 过滤字节 0 + RGB
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
            + chunk(b"IDAT", zlib.compress(row * h)) + chunk(b"IEND", b""))


def jpeg_bytes(w: int, h: int) -> bytes:
    app0 = (b"\xff\xe0" + struct.pack(">H", 16)
            + b"JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
    sof = (b"\xff\xc0" + struct.pack(">H", 17) + b"\x08"
           + struct.pack(">HH", h, w) + b"\x03"
           + b"\x01\x11\x00\x02\x11\x01\x03\x11\x01")
    return b"\xff\xd8" + app0 + sof + b"\xff\xd9"


def minimal_flac() -> bytes:
    """只有 STREAMINFO 的极简 FLAC，mutagen 能读写。"""
    info = struct.pack(">HH", 4096, 4096) + b"\x00" * 6
    packed = (44100 << 44) | (1 << 41) | (15 << 36) | 44100
    info += packed.to_bytes(8, "big") + b"\x00" * 16
    return b"fLaC" + bytes([0x80, 0, 0, len(info)]) + info


MPEG_FRAME = b"\xff\xfb\x90\x00" + b"\x00" * 413


def make_flac(path, title="", cover=b"", cover_type=3, extra=None):
    from mutagen.flac import FLAC, Picture

    path.write_bytes(minimal_flac())
    audio = FLAC(str(path))
    if title:
        audio["title"] = title
    for key, value in (extra or {}).items():
        audio[key] = value
    if cover:
        pic = Picture()
        pic.type = cover_type
        pic.mime = scrape._cover_mime(cover)
        pic.desc = "Cover"
        pic.data = cover
        audio.add_picture(pic)
    audio.save()
    return path


def make_mp3(path, title="", cover=b""):
    from mutagen.id3 import APIC, ID3, TIT2, TXXX

    tag = ID3()
    if title:
        tag.add(TIT2(encoding=3, text=title))
    tag.add(TXXX(encoding=3, desc="163 key(Don't modify)", text="encrypted-blob"))
    if cover:
        tag.add(APIC(encoding=3, mime="image/jpeg", type=3, desc="Cover", data=cover))
    tag.save(str(path))
    with open(path, "ab") as f:      # 补一帧真 MPEG，mutagen 才认得出是 mp3
        f.write(MPEG_FRAME * 4)
    return path


def candidate(**kw) -> dict:
    base = {"source": "网易云", "title": "", "artist": "", "album": "",
            "year": "", "duration": 0, "song_id": 0,
            "thumb_url": "", "cover_url": "", "cover_hint": "1200×1200"}
    base.update(kw)
    return base


ALL_FIELDS = {"cover": True, "year": True, "netease_id": True,
              "album": True, "title_artist": True}


def tags_of(path) -> dict:
    from mutagen import File as MutagenFile
    audio = MutagenFile(str(path))
    tags = getattr(audio, "tags", None) or {}
    return {str(k).lower(): tags[k] for k in tags.keys()}


def has_163_key(path) -> bool:
    return any("163 key" in k for k in tags_of(path))


# ---------- 图片尺寸 ----------

def test_png_size_from_ihdr():
    assert scrape.image_size(png_bytes(1200, 1180)) == (1200, 1180)


def test_jpeg_size_from_sof0():
    assert scrape.image_size(jpeg_bytes(640, 480)) == (640, 480)


def test_unknown_bytes_size_is_zero():
    assert scrape.image_size(b"RIFF....WAVE") == (0, 0)
    assert scrape.image_size(b"") == (0, 0)


def test_jpeg_sof2_is_understood():
    data = bytearray(jpeg_bytes(300, 300))
    data[data.index(b"\xff\xc0") + 1] = 0xC2     # 渐进式 JPEG 的 SOF2
    assert scrape.image_size(bytes(data)) == (300, 300)


# ---------- inspect ----------

def test_inspect_reads_year_id_and_cover_size(tmp_path):
    cover = png_bytes(900, 900)
    p = make_flac(tmp_path / "a.flac", title="晴天",
                  cover=cover, extra={"date": "2003-07-31", "NETEASE_ID": "186016"})
    info = scrape.inspect_track(p)
    assert info["writable"] and info["format"] == "flac"
    assert info["title"] == "晴天"
    assert info["year"] == "2003"
    assert info["netease_id"] == 186016
    assert (info["cover_w"], info["cover_h"]) == (900, 900)


def test_inspect_marks_wav_readonly(tmp_path):
    p = tmp_path / "a.wav"
    p.write_bytes(b"RIFF" + b"\x00" * 64)
    info = scrape.inspect_track(p)
    assert info["writable"] is False
    assert (info["cover_w"], info["cover_h"]) == (0, 0)


# ---------- 候选解析 ----------

def test_itunes_candidates_are_parsed_and_upscaled():
    payload = {"results": [{
        "trackName": "晴る", "artistName": "ヨルシカ",
        "collectionName": "晴る - Single", "releaseDate": "2023-07-10T07:00:00Z",
        "trackTimeMillis": 261000,
        "artworkUrl100": "https://is1.mzstatic.com/image/thumb/x/100x100bb.jpg",
    }]}

    def fake_json(url):
        assert "country=jp" in url          # 假名走日区
        assert "itunes.apple.com" in url
        return payload

    cands = scrape.search_candidates("晴る", itunes_json=fake_json,
                                     netease_search=lambda *a, **k: [],
                                     netease_json=lambda *a, **k: {})
    assert len(cands) == 1
    c = cands[0]
    assert c["source"] == "iTunes"
    assert c["title"] == "晴る" and c["artist"] == "ヨルシカ"
    assert c["year"] == "2023" and c["duration"] == 261
    assert c["song_id"] == 0
    assert c["thumb_url"].endswith("240x240bb.jpg")
    assert c["cover_url"].endswith("6000x6000bb.jpg")   # 要尽可能大的原图，尺寸由前端实测
    assert c["cover_hint"] == ""


def test_netease_candidates_get_album_and_year():
    def fake_search(keyword, limit=5):
        return [{"id": 11, "name": "晴天", "artist": "周杰伦", "duration": 269}]

    def fake_json(url):
        assert "song/detail" in url and "11" in url
        return {"songs": [{
            "id": 11, "name": "晴天", "duration": 269000,
            "artists": [{"name": "周杰伦"}],
            "album": {"name": "叶惠美", "picUrl": "https://p1.music.126.net/a.jpg",
                      "publishTime": 1059552000000},
        }]}

    cands = scrape.search_candidates("晴天", netease_search=fake_search,
                                     netease_json=fake_json,
                                     itunes_json=lambda url: {"results": []})
    assert len(cands) == 1
    c = cands[0]
    assert c["source"] == "网易云" and c["album"] == "叶惠美"
    assert c["year"] == "2003" and c["song_id"] == 11
    assert "param=240y240" in c["thumb_url"]
    assert c["cover_url"] == "https://p1.music.126.net/a.jpg"   # 不带 param = 原图


def test_netease_by_id_comes_first():
    def fake_search(keyword, limit=5):
        return [{"id": 22, "name": "别的", "artist": "谁", "duration": 200}]

    def fake_json(url):
        return {"songs": [{"id": 11, "name": "晴天", "duration": 269000,
                           "artists": [{"name": "周杰伦"}],
                           "album": {"name": "叶惠美", "picUrl": "https://x/a.jpg"}}]}

    cands = scrape.search_candidates("晴天", netease_id=11,
                                     netease_search=fake_search,
                                     netease_json=fake_json,
                                     itunes_json=lambda url: {"results": []})
    assert cands[0]["song_id"] == 11


def test_rate_limited_netease_does_not_block_itunes():
    from kiralrc.ratelimit import RateLimited

    def boom(*a, **k):
        raise RateLimited("网易云限速")

    payload = {"results": [{"trackName": "A", "artistName": "B",
                            "collectionName": "C", "releaseDate": "2020",
                            "trackTimeMillis": 1000,
                            "artworkUrl100": "https://x/100x100bb.jpg"}]}
    cands = scrape.search_candidates("a", netease_search=boom, netease_json=boom,
                                     itunes_json=lambda url: payload)
    assert [c["source"] for c in cands] == ["iTunes"]


# ---------- 写回 ----------

def test_flac_fill_keeps_163_key_and_back_cover(tmp_path, monkeypatch):
    front = png_bytes(300, 300)
    back = png_bytes(200, 200)
    p = make_flac(tmp_path / "a.flac", title="旧标题", cover=front,
                  extra={"date": "2001", "163 key(Don't modify)": "blob",
                         "NETEASE_ID": "12345"})
    # 再加一张封底
    from mutagen.flac import FLAC, Picture
    audio = FLAC(str(p))
    pic = Picture()
    pic.type = 4
    pic.mime = "image/png"
    pic.data = back
    audio.add_picture(pic)
    audio.save()

    big = png_bytes(900, 900)
    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: big)
    item = {"path": str(p), "candidate": candidate(song_id=999, year="2018",
                                                   title="新标题", album="新专辑",
                                                   cover_url="https://x/big.png"),
            "fields": ALL_FIELDS, "mode": "fill"}
    res = scrape.apply_one(item)

    assert res["ok"], res
    types = [pic.type for pic in FLAC(str(p)).pictures]
    assert 3 in types and 4 in types            # 正面换了，封底还在
    assert has_163_key(p)                       # 163 key 没被清掉
    info = scrape.inspect_track(p)
    assert info["title"] == "旧标题"             # fill 模式不动已有标题
    assert info["year"] == "2001"               # 已有的年份也不动
    assert info["album"] == "新专辑"             # 缺的专辑才补
    assert info["netease_id"] == 12345          # 已有 ID 不被候选改写
    assert (info["cover_w"], info["cover_h"]) == (900, 900)   # 300px 低清被换掉


def test_mp3_increment_write_keeps_163_key(tmp_path):
    p = make_mp3(tmp_path / "a.mp3", title="旧标题", cover=jpeg_bytes(300, 300))
    item = {"path": str(p), "candidate": candidate(song_id=777, year="2019"),
            "fields": ALL_FIELDS, "mode": "overwrite"}
    res = scrape.apply_one(item)
    assert res["ok"], res
    assert has_163_key(p)
    info = scrape.inspect_track(p)
    assert info["year"] == "2019" and info["netease_id"] == 777
    assert info["title"] == "旧标题"             # 候选标题为空，就不该动它


def test_itunes_candidate_never_clears_existing_netease_id(tmp_path):
    p = make_flac(tmp_path / "a.flac", extra={"NETEASE_ID": "12345"})
    item = {"path": str(p),
            "candidate": candidate(source="iTunes", song_id=0, year="2020",
                                   album="专辑"),
            "fields": {"cover": False, "year": True, "netease_id": True,
                       "album": True, "title_artist": False},
            "mode": "overwrite"}
    res = scrape.apply_one(item)
    assert res["ok"], res
    assert scrape.inspect_track(p)["netease_id"] == 12345


def test_fill_and_overwrite_differ(tmp_path):
    p = make_flac(tmp_path / "a.flac", title="旧标题", extra={"album": "旧专辑"})
    cand = candidate(title="新标题", album="新专辑", year="2011")

    fill = scrape.apply_one({"path": str(p), "candidate": cand,
                             "fields": {"year": True, "album": True,
                                        "title_artist": True},
                             "mode": "fill"})
    assert fill["ok"]
    after = scrape.inspect_track(p)
    assert after["title"] == "旧标题" and after["album"] == "旧专辑"
    assert after["year"] == "2011"                # 只有缺的（年份）被补上

    over = scrape.apply_one({"path": str(p), "candidate": cand,
                             "fields": {"year": True, "album": True,
                                        "title_artist": True},
                             "mode": "overwrite"})
    assert over["ok"]
    after = scrape.inspect_track(p)
    assert after["title"] == "新标题" and after["album"] == "新专辑"
    assert after["artist"] == ""                  # 候选里没有歌手，别写成空


def test_fill_does_not_replace_a_big_cover(tmp_path, monkeypatch):
    p = make_flac(tmp_path / "a.flac", cover=png_bytes(1200, 1200))
    called = []
    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: called.append(url) or b"")
    res = scrape.apply_one({"path": str(p),
                            "candidate": candidate(cover_url="https://x/big.jpg"),
                            "fields": {"cover": True}, "mode": "fill"})
    assert not res["ok"] and not called           # 已经够高清，不该再拉图


def test_fill_fetches_hires_when_local_cover_is_small(tmp_path, monkeypatch):
    p = make_flac(tmp_path / "a.flac", cover=jpeg_bytes(300, 300))
    big = jpeg_bytes(1200, 1200)
    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: big)
    res = scrape.apply_one({"path": str(p),
                            "candidate": candidate(cover_url="https://x/big.jpg"),
                            "fields": {"cover": True}, "mode": "fill"})
    assert res["ok"] and "cover" in res["changed"]
    assert scrape.inspect_track(p)["cover_w"] == 1200


def test_readonly_format_is_reported_not_written(tmp_path):
    p = tmp_path / "a.wav"
    p.write_bytes(b"RIFF" + b"\x00" * 64)
    res = scrape.apply_one({"path": str(p), "candidate": candidate(title="x"),
                            "fields": ALL_FIELDS, "mode": "overwrite"})
    assert res["readonly"] and not res["ok"]
    assert p.read_bytes() == b"RIFF" + b"\x00" * 64


def test_m4a_writer_maps_keys(tmp_path, monkeypatch):
    """m4a 容器造不出来（要先有真文件），这里用替身核对键名映射。"""
    import mutagen.mp4 as mp4

    class FakeMP4:
        last = None

        def __init__(self, path):
            self.tags = {}
            FakeMP4.last = self

        def add_tags(self):
            self.tags = {}

        def save(self):
            pass

    monkeypatch.setattr(mp4, "MP4", FakeMP4)
    cover = jpeg_bytes(1200, 1200)
    scrape._write_m4a(tmp_path / "a.m4a",
                      {"year": "2022", "netease_id": 4242}, cover)

    tags = FakeMP4.last.tags
    assert tags["\xa9day"] == ["2022"]
    assert bytes(tags["----:com.apple.iTunes:NETEASE_ID"][0]) == b"4242"
    assert bytes(tags["covr"][0]) == cover


def test_apply_plan_streams_every_item_and_summarizes(tmp_path):
    a = make_flac(tmp_path / "a.flac")
    b = tmp_path / "b.wav"
    b.write_bytes(b"RIFF" + b"\x00" * 64)
    seen = []
    summary = scrape.apply_plan({"items": [
        {"path": str(a), "candidate": candidate(year="2020"),
         "fields": {"year": True}, "mode": "fill"},
        {"path": str(b), "candidate": candidate(year="2020"),
         "fields": {"year": True}, "mode": "fill"},
    ]}, lambda i, total, res: seen.append((i, total, res["path"])))

    assert [s[0] for s in seen] == [1, 2]
    assert all(s[1] == 2 for s in seen)
    assert summary["total"] == 2 and summary["ok"] == 1
    assert summary["skipped"] == 1 and summary["failed"] == 0   # wav 是跳过，不算失败


# ---------- 按 ID 批量补全 ----------

def test_apply_by_id_fetches_song_detail_once(tmp_path, monkeypatch):
    """只给 netease_id 的计划：一次请求补齐后按 ID 的信息写，封面按 ID 拉高清。"""
    p = make_flac(tmp_path / "a.flac")
    song = {"id": 555, "name": "晴天", "duration": 269000,
            "artists": [{"name": "周杰伦"}],
            "album": {"name": "叶惠美", "picUrl": "https://x/a.jpg",
                      "publishTime": 1059552000000}}
    asked = []
    monkeypatch.setattr(scrape, "netease_details",
                        lambda ids, netease_json=None, state=None:
                        asked.append(list(ids)) or [song])
    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: png_bytes(1200, 1200))

    summary = scrape.apply_plan({"items": [{
        "path": str(p), "netease_id": 555,
        "fields": {"cover": True, "year": True, "netease_id": True},
        "mode": "fill",
    }]})

    assert asked == [[555]]                     # 只请求一次，不是一首一次
    assert summary["ok"] == 1 and summary["failed"] == 0
    info = scrape.inspect_track(p)
    assert info["netease_id"] == 555
    assert info["year"] == "2003"
    assert (info["cover_w"], info["cover_h"]) == (1200, 1200)


def test_apply_by_id_skips_when_detail_is_missing(tmp_path, monkeypatch):
    p = make_flac(tmp_path / "a.flac")
    monkeypatch.setattr(scrape, "netease_details", lambda ids, netease_json=None: [])
    res = scrape.apply_one({"path": str(p), "netease_id": 999,
                            "fields": {"year": True}, "mode": "fill"}, {})
    assert res["skipped"] and not res["ok"]
    assert "ID" in res["note"]
    assert scrape.inspect_track(p)["year"] == ""   # 什么都没写进去


def test_a_smaller_source_cover_does_not_downgrade(tmp_path, monkeypatch):
    """网易云的 ?param=1200 只是请求尺寸：源图 390 时不能把本地 800 换成 390。"""
    p = make_flac(tmp_path / "a.flac", cover=png_bytes(800, 800))
    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: png_bytes(390, 390))
    res = scrape.apply_one({"path": str(p),
                            "candidate": candidate(cover_url="https://x/small.png"),
                            "fields": {"cover": True}, "mode": "overwrite"})
    assert res["cover_kept"] and not res["ok"]
    assert "比本地" in res["note"]
    assert scrape.inspect_track(p)["cover_w"] == 800


# ---------- 封面档位：原图 / 1200 ----------

def test_resize_cover_url_handles_both_cdns():
    netease = "https://p1.music.126.net/a.jpg"
    assert scrape.resize_cover_url(netease, 1200) == netease + "?param=1200y1200"
    assert scrape.resize_cover_url(netease, 0) == netease          # 0 = 原图
    # 已经带了参数的地址不重复拼
    assert scrape.resize_cover_url(netease + "?param=240y240", 1200).endswith("param=240y240")

    apple = "https://is1-ssl.mzstatic.com/image/thumb/Music/x/100x100bb.jpg"
    assert scrape.resize_cover_url(apple, 1200).endswith("/1200x1200bb.jpg")
    assert scrape.resize_cover_url(apple, 0) == apple


def test_apply_fetches_at_the_chosen_size(tmp_path, monkeypatch):
    p = make_flac(tmp_path / "a.flac")
    seen = []
    monkeypatch.setattr(scrape, "_download",
                        lambda url, timeout=30: seen.append(url) or png_bytes(1200, 1200))
    # 原图候选 + 1200 档 → 实际请求要带 param
    scrape.apply_one({"path": str(p),
                      "candidate": candidate(song_id=1, cover_url="https://p1.music.126.net/a.jpg"),
                      "fields": {"cover": True}, "mode": "overwrite", "cover_size": 1200})
    assert "param=1200y1200" in seen[0]

    # 原图档 → 请求保持裸地址
    p2 = make_flac(tmp_path / "b.flac")
    seen.clear()
    scrape.apply_one({"path": str(p2),
                      "candidate": candidate(song_id=1, cover_url="https://p1.music.126.net/b.jpg"),
                      "fields": {"cover": True}, "mode": "overwrite", "cover_size": 0})
    assert seen[0] == "https://p1.music.126.net/b.jpg"


def test_cover_only_overwrite_keeps_every_other_field(tmp_path, monkeypatch):
    """只勾「高清封面」+「覆盖选中」：其余字段一个都不许动。"""
    p = make_flac(tmp_path / "a.flac", title="晴天", cover=png_bytes(300, 300),
                  extra={"artist": "周杰伦", "album": "叶惠美", "date": "2003",
                         "NETEASE_ID": "186016"})
    before = scrape.inspect_track(p)
    assert before["netease_id"] == 186016 and before["year"] == "2003"

    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: png_bytes(1600, 1600))
    res = scrape.apply_one({
        "path": str(p),
        "candidate": candidate(title="别的标题", artist="别人", album="别的专辑",
                               year="1999", song_id=999, cover_url="https://x/big.png"),
        "fields": {"cover": True}, "mode": "overwrite",
    })

    assert res["changed"] == ["cover"]
    after = scrape.inspect_track(p)
    for key in ("title", "artist", "album", "year", "netease_id"):
        assert after[key] == before[key], f"{key} 被改了：{before[key]} → {after[key]}"
    assert (after["cover_w"], after["cover_h"]) == (1600, 1600)


def test_cover_only_overwrite_keeps_every_other_field(tmp_path, monkeypatch):
    """只勾「高清封面」+「覆盖选中」：其余字段一个都不许动。"""
    p = make_flac(tmp_path / "a.flac", title="晴天", cover=png_bytes(300, 300),
                  extra={"artist": "周杰伦", "album": "叶惠美", "date": "2003",
                         "NETEASE_ID": "186016"})
    before = scrape.inspect_track(p)
    assert before["netease_id"] == 186016 and before["year"] == "2003"

    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: png_bytes(1600, 1600))
    res = scrape.apply_one({
        "path": str(p),
        "candidate": candidate(title="别的标题", artist="别人", album="别的专辑",
                               year="1999", song_id=999, cover_url="https://x/big.png"),
        "fields": {"cover": True}, "mode": "overwrite",
    })

    assert res["changed"] == ["cover"]
    after = scrape.inspect_track(p)
    for key in ("title", "artist", "album", "year", "netease_id"):
        assert after[key] == before[key], f"{key} 被改了：{before[key]} → {after[key]}"
    assert (after["cover_w"], after["cover_h"]) == (1600, 1600)




# ---------- 严格匹配：版本、完整艺人、时长 ----------

@pytest.fixture(autouse=True)
def _fresh_netauth():
    """每个用例都从「没登录态、没冷却」开始，别互相带。"""
    from kiralrc import netauth

    netauth.reset()
    netauth.clear_cooldown()
    yield
    netauth.reset()
    netauth.clear_cooldown()


def test_strict_match_keeps_version_tags():
    local = {"title": "晴天", "artist": "周杰伦", "album": "叶惠美", "duration": 269}
    assert scrape.strict_match(local, {"title": "晴天", "artist": "周杰伦",
                                       "album": "叶惠美", "duration": 270})
    # (Live) 不能被当成原版
    assert not scrape.strict_match(local, {"title": "晴天 (Live)", "artist": "周杰伦",
                                           "album": "叶惠美", "duration": 269})
    # 本地是 Live、候选是原版，同样不行
    live = dict(local, title="晴天 (Live)")
    assert not scrape.strict_match(live, {"title": "晴天", "artist": "周杰伦",
                                          "album": "叶惠美", "duration": 269})
    # 伴奏 / instrumental 也算不同录音
    assert not scrape.strict_match(local, {"title": "晴天 (Instrumental)",
                                           "artist": "周杰伦", "album": "叶惠美"})


def test_strict_match_needs_full_artist_and_album():
    local = {"title": "晴天", "artist": "周杰伦", "album": "叶惠美"}
    # 一方包含另一方不算对上：完整艺人必须一致
    assert not scrape.strict_match(local, {"title": "晴天", "artist": "周杰伦/袁咏琳",
                                           "album": "叶惠美"})
    # 多歌手只是顺序不同，算同一批人
    duet = {"title": "晴天", "artist": "袁咏琳/周杰伦", "album": "叶惠美"}
    assert scrape.strict_match({"title": "晴天", "artist": "周杰伦、袁咏琳",
                                "album": "叶惠美"}, duet)
    # 候选缺专辑：信息不全就不猜
    assert not scrape.strict_match(local, {"title": "晴天", "artist": "周杰伦", "album": ""})


def test_strict_match_compares_duration_when_known():
    local = {"title": "晴天", "artist": "周杰伦", "album": "叶惠美", "duration": 269}
    assert not scrape.strict_match(local, {"title": "晴天", "artist": "周杰伦",
                                           "album": "叶惠美", "duration": 320})
    # 一边没有时长就不比
    assert scrape.strict_match({"title": "晴天", "artist": "周杰伦", "album": "叶惠美"},
                               {"title": "晴天", "artist": "周杰伦", "album": "叶惠美",
                                "duration": 320})


def test_ambiguous_candidates_are_left_for_manual_pick(tmp_path, monkeypatch):
    p = make_flac(tmp_path / "a.flac", title="晴天",
                  extra={"artist": "周杰伦", "album": "叶惠美"})
    one = candidate(song_id=11, title="晴天", artist="周杰伦", album="叶惠美", year="2003")
    two = candidate(song_id=22, title="晴天", artist="周杰伦", album="叶惠美", year="2003")
    monkeypatch.setattr(scrape, "_netease_candidates", lambda *a, **k: [one, two])

    res = scrape.apply_one({"path": str(p), "resolve": "netease",
                            "fields": {"year": True, "netease_id": True}, "mode": "fill"})
    assert res["skipped"] and res["needs_pick"] and not res["ok"]
    assert "手动" in res["note"]
    assert scrape.inspect_track(p)["netease_id"] == 0     # 一个字都没写


def test_rate_limited_resolution_is_not_reported_as_missing(tmp_path, monkeypatch):
    from kiralrc.ratelimit import RateLimited

    p = make_flac(tmp_path / "a.flac", title="晴天")
    monkeypatch.setattr(scrape, "_netease_candidates",
                        lambda *a, **k: (_ for _ in ()).throw(RateLimited("操作频繁")))
    res = scrape.apply_one({"path": str(p), "resolve": "netease",
                            "fields": {"year": True}, "mode": "fill"})
    assert res["skipped"] and "限速" in res["note"]
    assert "没搜到" not in res["note"]


# ---------- 封面必须真能解开 ----------

def test_html_error_page_is_not_written_as_cover(tmp_path, monkeypatch):
    p = make_mp3(tmp_path / "a.mp3", title="晴天")
    before = p.read_bytes()
    html = b"<!DOCTYPE html><html><body>502 Bad Gateway</body></html>" * 40
    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: html)

    res = scrape.apply_one({"path": str(p),
                            "candidate": candidate(cover_url="https://x/a.jpg"),
                            "fields": {"cover": True}, "mode": "overwrite"})
    assert not res["ok"] and not res["changed"]
    assert "图片" in res["note"] and res["cover_px"] == ""
    assert p.read_bytes() == before          # 文件一点没动
    assert scrape.inspect_track(p)["cover_w"] == 0


def test_truncated_png_is_not_written_as_cover(tmp_path, monkeypatch):
    p = make_flac(tmp_path / "a.flac")
    cut = png_bytes(1200, 1200)[:200]        # 掐掉 IDAT 与 IEND
    monkeypatch.setattr(scrape, "_download", lambda url, timeout=30: cut)
    res = scrape.apply_one({"path": str(p),
                            "candidate": candidate(cover_url="https://x/a.png"),
                            "fields": {"cover": True}, "mode": "overwrite"})
    assert not res["ok"] and "残缺" in res["note"]
    assert scrape.inspect_track(p)["cover_w"] == 0


def test_cover_problem_accepts_valid_images():
    assert scrape.cover_problem(png_bytes(600, 600)) == ""
    assert scrape.cover_problem(jpeg_bytes(600, 600)) == ""
    assert scrape.cover_problem(b"") != ""
    assert scrape.cover_problem(b"GIF89a" + b"\x00" * 600) != ""


# ---------- 限速冷却：所有入口共用 ----------

def test_apply_plan_pauses_on_rate_limit_and_reports_resume_point(tmp_path, monkeypatch):
    from kiralrc import netauth
    from kiralrc.ratelimit import RateLimited

    a = make_flac(tmp_path / "a.flac", title="A")
    b = make_flac(tmp_path / "b.flac", title="B")
    calls = []

    def boom(*args, **kwargs):
        calls.append(args)
        netauth.record_rate_limit(30)
        raise RateLimited("操作频繁")

    monkeypatch.setattr(scrape, "_netease_candidates", boom)

    seen = []
    summary = scrape.apply_plan({"items": [
        {"path": str(a), "resolve": "netease", "fields": {"year": True}, "mode": "fill"},
        {"path": str(b), "resolve": "netease", "fields": {"year": True}, "mode": "fill"},
    ]}, on_item=lambda i, t, r: seen.append((i, r["skipped"])))

    assert summary["paused"] and summary["retry_after"] >= 5
    assert summary["resume_from"] == 2                 # 第二首没跑
    assert len(calls) == 1                             # 限速后再没发请求
    assert [i for i, _ in seen] == [1, 2]              # 两首都报了结果，进度不缺
    assert all(skip for _, skip in seen)


def test_apply_plan_skips_network_while_cooling(tmp_path, monkeypatch):
    from kiralrc import netauth

    p = make_flac(tmp_path / "a.flac", title="A")
    netauth.record_rate_limit(60)
    hit = []
    monkeypatch.setattr(scrape, "_netease_candidates",
                        lambda *a, **k: hit.append(1) or [])
    summary = scrape.apply_plan({"items": [
        {"path": str(p), "resolve": "netease", "fields": {"year": True}, "mode": "fill"},
    ]})
    assert summary["paused"] and summary["resume_from"] == 1
    assert hit == []


def test_search_does_not_hit_network_while_cooling(monkeypatch):
    from kiralrc import netauth

    netauth.record_rate_limit(45)
    hit = []
    res = scrape.search_with_note("晴天", netease_search=lambda *a, **k: hit.append(1) or [],
                                  netease_json=lambda *a, **k: hit.append(1) or {},
                                  itunes_json=lambda *a, **k: {"results": []})
    assert hit == []
    assert res["items"] == [] and "冷却" in res["warning"]


def test_by_id_batch_stops_when_detail_is_rate_limited(tmp_path, monkeypatch):
    from kiralrc import netauth

    p = make_flac(tmp_path / "a.flac")

    def detail(ids, netease_json=None, state=None):
        if state is not None:
            state["rate_limited"] = True
        netauth.record_rate_limit(20)
        return []

    monkeypatch.setattr(scrape, "netease_details", detail)
    summary = scrape.apply_plan({"items": [
        {"path": str(p), "netease_id": 42, "fields": {"year": True}, "mode": "fill"},
    ]})
    assert summary["paused"] and summary["ok"] == 0
    assert summary["skipped"] == 1


# ---------- Cookie 只发给白名单主机 ----------

def test_cookie_only_goes_to_allowed_hosts(monkeypatch):
    from kiralrc import netauth

    monkeypatch.setenv("KIRALRC_NETEASE_COOKIE", "MUSIC_U=abc")
    base = {"User-Agent": "x", "Referer": "https://music.163.com/"}

    allowed = netauth.headers_for("https://music.163.com/api/song/detail?ids=[1]", base)
    assert allowed["Cookie"] == "MUSIC_U=abc"
    assert base == {"User-Agent": "x", "Referer": "https://music.163.com/"}   # 共用 dict 没被改

    for url in ("https://interface.music.163.com/x", "http://music.163.com/api",
                "https://evil.example.com/?music.163.com",
                "https://music.163.com.evil.com/x",
                "https://p1.music.126.net/a.jpg"):
        assert "Cookie" not in netauth.headers_for(url, base), url


def test_redirect_to_another_host_strips_cookie(monkeypatch):
    import urllib.request

    from kiralrc import netauth

    monkeypatch.setenv("KIRALRC_NETEASE_COOKIE", "MUSIC_U=abc")
    handler = netauth._SafeRedirect()
    req = urllib.request.Request("https://music.163.com/api/x",
                                 headers={"Cookie": "MUSIC_U=abc"})
    same = handler.redirect_request(req, None, 302, "Found", {},
                                    "https://music.163.com/api/y")
    assert same.headers.get("Cookie") == "MUSIC_U=abc"

    other = handler.redirect_request(req, None, 302, "Found", {},
                                     "https://evil.example.com/y")
    assert "Cookie" not in other.headers


def test_cloudy_auth_needs_explicit_opt_in(monkeypatch):
    from kiralrc import netauth

    monkeypatch.delenv("KIRALRC_NETEASE_COOKIE", raising=False)
    monkeypatch.setattr(netauth, "cloudy_cookie", lambda: "MUSIC_U=borrowed")
    monkeypatch.delenv("KIRALRC_CLOUDY_AUTH", raising=False)
    assert not netauth.cloudy_enabled()
    assert netauth.cookie() == ""            # 默认不借

    monkeypatch.setenv("KIRALRC_CLOUDY_AUTH", "1")
    netauth.reset()
    assert netauth.describe() == "匿名（登录态未生效）"
    assert netauth.cookie() == "MUSIC_U=borrowed"
    assert netauth.describe() == "匿名（登录态未生效）"
    netauth.note_rejected()                  # 301：退回匿名
    assert netauth.cookie() == ""
    assert "失效" in netauth.describe()


def test_auth_status_follows_real_requests(monkeypatch):
    from kiralrc import netauth

    monkeypatch.setenv("KIRALRC_NETEASE_COOKIE", "MUSIC_U=abc")
    assert netauth.describe() == "匿名（登录态未生效）"   # 还没真的发出去
    saw = {}

    def fake_open(req, timeout=0):
        saw["cookie"] = req.headers.get("Cookie")

        class R:
            headers = {}
            def read(self):
                return b'{"code": 200}'
            def __enter__(self):
                return self
            def __exit__(self, *a):
                return False
        return R()

    monkeypatch.setattr(netauth, "_get_opener",
                        lambda: type("O", (), {"open": staticmethod(fake_open)})())
    from kiralrc import text as txt

    txt._get_json("https://music.163.com/api/x")
    assert saw["cookie"] == "MUSIC_U=abc"
    assert netauth.describe() == "环境变量 Cookie"        # 真的用上了才这么报


def test_rate_limited_search_is_reported_as_rate_limit(tmp_path, monkeypatch):
    """限速让候选变成空列表时，不能记成「没搜到」。"""
    from kiralrc import text as txt
    from kiralrc.ratelimit import RateLimited

    p = make_flac(tmp_path / "a.flac", title="晴天", extra={"artist": "周杰伦"})

    def boom(*a, **k):
        raise RateLimited("操作频繁")

    monkeypatch.setattr(txt, "netease_search", boom)
    res = scrape.apply_one({"path": str(p), "resolve": "netease",
                            "fields": {"year": True}, "mode": "fill"})
    assert res["skipped"] and "限速" in res["note"]
