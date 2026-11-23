"""按网易云专辑 ID 批量补全：每张专辑查一次、封面下一次、版本唯一才写。"""

import base64
import json

import pytest

from kiralrc import ncm163, scrape
from test_scrape import candidate, make_flac, png_bytes


def blob163(song_id: int, album_id: int) -> str:
    """造一条网易云客户端风格的 163 key，用来测「专辑 ID 从哪来」。"""
    payload = json.dumps({"musicId": song_id, "albumId": album_id,
                          "musicName": "x"}, ensure_ascii=False).encode("utf-8")
    raw = ncm163.encrypt_ecb(b"music:" + payload)
    return "163 key(Don't modify):" + base64.b64encode(raw).decode()


def album_track(**kw) -> dict:
    base = {"source": "网易云", "title": "", "artist": "", "album": "专辑",
            "year": "2013", "duration": 0, "song_id": 0,
            "thumb_url": "", "cover_url": "https://p1.music.126.net/album.jpg"}
    base.update(kw)
    return base


# ---------- 专辑 ID 从哪来 ----------

def test_album_id_from_plain_tag(tmp_path):
    p = make_flac(tmp_path / "tag.flac", extra={"NETEASE_ALBUM_ID": "16953"})
    assert scrape.album_id_of(p) == 16953
    assert scrape.inspect_track(p)["album_id"] == 16953


def test_album_id_from_163_key(tmp_path):
    blob = blob163(167870, 2781646)
    p = make_flac(tmp_path / "blob.flac", extra={"163 key(Don't modify):": blob})
    assert scrape.album_id_of(p) == 2781646


def test_album_id_absent_is_zero(tmp_path):
    assert scrape.album_id_of(make_flac(tmp_path / "none.flac")) == 0


# ---------- 专辑详情 ----------

def test_album_detail_parses_tracks_and_cover():
    payload = {
        "code": 200,
        "album": {"name": "自定义", "publishTime": 1246464000000,
                  "picUrl": "https://p1.music.126.net/c.jpg"},
        "songs": [
            {"id": 167870, "name": "如果当时", "duration": 316000,
             "artists": [{"name": "许嵩"}]},
            {"id": 167873, "name": "多余的解释", "duration": 277000,
             "artists": [{"name": "许嵩"}]},
        ],
    }
    info = scrape.album_detail(16953, album_json=lambda url: payload)
    assert info["name"] == "自定义" and info["year"] == "2009"
    assert info["cover_url"] == "https://p1.music.126.net/c.jpg"
    assert [t["song_id"] for t in info["tracks"]] == [167870, 167873]
    assert info["tracks"][0]["album"] == "自定义"
    assert info["tracks"][0]["duration"] == 316


def test_album_detail_retries_anti_crawl(monkeypatch):
    """-462 是随机的反爬：同一张专辑再要一次就好，别直接当「没有」。"""
    calls = []
    ok = {"code": 200, "album": {"name": "A", "picUrl": "https://x/a.jpg"},
          "songs": [{"id": 1, "name": "t", "duration": 1000, "artists": [{"name": "a"}]}]}

    def get(url):
        calls.append(url)
        return {"code": -462, "msg": "Cheating"} if len(calls) == 1 else ok

    monkeypatch.setattr(scrape.time, "sleep", lambda s: None)
    info = scrape.album_detail(1, album_json=get)
    assert info["tracks"][0]["song_id"] == 1
    assert len(calls) == 2


def test_album_detail_reports_blocked_and_rate_limited(monkeypatch):
    from kiralrc.ratelimit import RateLimited

    state: dict = {}
    monkeypatch.setattr(scrape.time, "sleep", lambda s: None)
    assert scrape.album_detail(1, album_json=lambda url: {"code": -462}, state=state) == {}
    assert state.get("blocked")

    state = {}

    def boom(url):
        raise RateLimited("操作频繁")

    assert scrape.album_detail(1, album_json=boom, state=state) == {}
    assert state.get("rate_limited")


# ---------- 专辑内的匹配：版本 / 时长 ----------

def test_album_match_rejects_live_version():
    """同一张专辑里同名的 Live 版不能被当成录音室版写进去。"""
    local = {"title": "届かない恋", "artist": "米澤円",
             "album": "WHITE ALBUM2 VOCAL COLLECTION", "duration": 300}
    tracks = [
        album_track(song_id=28390033,
                    title="届かない恋 Live at Campus Fes(TV anime ver.)",
                    artist="米澤円", duration=300),
        album_track(song_id=28390036, title="After All ～綴る想い～ '13",
                    artist="上原れな", duration=290),
    ]
    cand, reason = scrape.find_unique_match(local, tracks, check_album=False)
    assert not cand and reason == "none"


def test_album_match_rejects_bracketed_live_title():
    """「同上歌名 + (Live)」这种折叠后同名的情况，靠版本标记挡住。"""
    local = {"title": "晴天", "artist": "周杰伦", "album": "叶惠美", "duration": 269}
    tracks = [album_track(song_id=1, title="晴天 (Live)", artist="周杰伦", duration=269)]
    cand, reason = scrape.find_unique_match(local, tracks, check_album=False)
    assert not cand and reason == "none"

    # 两边都是 Live 才算同一版
    live = {"title": "晴天 (Live)", "artist": "周杰伦", "album": "叶惠美", "duration": 269}
    cand, _ = scrape.find_unique_match(live, tracks, check_album=False)
    assert cand["song_id"] == 1


def test_album_match_compares_duration():
    local = {"title": "届かない恋", "artist": "米澤円", "album": "X", "duration": 300}
    tracks = [album_track(song_id=1, title="届かない恋", artist="米澤円", duration=361)]
    cand, reason = scrape.find_unique_match(local, tracks, check_album=False)
    assert not cand and reason == "none"


def test_album_match_ignores_album_name_inside_the_album():
    """同一张专辑里，本地标签与网易云正式名不一致不该挡路。"""
    local = {"title": "ありがとう", "artist": "KOKIA", "album": "songbird", "duration": 249}
    tracks = [album_track(song_id=7, title="ありがとう", artist="KOKIA",
                          album="songbird 10th anniversary")]   # 时长未知就不比
    cand, _ = scrape.find_unique_match(local, tracks, check_album=False)
    assert cand["song_id"] == 7


# ---------- 整套流程 ----------

def test_album_mode_writes_unique_match_once_per_album(tmp_path, monkeypatch):
    """整张专辑：取一次曲目表、下一张封面，命中的两首写入。"""
    a = make_flac(tmp_path / "a.flac", title="如果当时",
                  extra={"artist": "许嵩", "album": "自定义"})
    b = make_flac(tmp_path / "b.flac", title="多余的解释",
                  extra={"artist": "许嵩", "album": "自定义"})
    c = make_flac(tmp_path / "c.flac", title="不在这张专辑里",
                  extra={"artist": "许嵩", "album": "自定义"})
    info = {
        "id": 16953, "name": "自定义", "year": "2009",
        "cover_url": "https://p1.music.126.net/cover.jpg",
        # 真实 album_detail 会用专辑的 publishTime 给每条曲目算年份
        "tracks": [
            album_track(song_id=167870, title="如果当时", artist="许嵩", year="2009"),
            album_track(song_id=167873, title="多余的解释", artist="许嵩", year="2009"),
        ],
    }
    asked = []
    monkeypatch.setattr(scrape, "album_detail", lambda aid, **kw: asked.append(aid) or info)
    downloads = []
    monkeypatch.setattr(scrape, "_download",
                        lambda url, timeout=30: downloads.append(url) or png_bytes(1200, 1200))

    plan = [{"path": str(p), "album_id": 16953, "resolve": "album",
             "fields": {"cover": True, "netease_id": True, "year": True},
             "mode": "fill"} for p in (a, b, c)]
    summary = scrape.apply_plan({"items": plan})

    assert asked == [16953]                       # 一张专辑只查一次
    assert summary["ok"] == 2 and summary["skipped"] == 1
    assert scrape.inspect_track(a)["netease_id"] == 167870
    assert scrape.inspect_track(b)["netease_id"] == 167873
    assert scrape.inspect_track(c)["netease_id"] == 0   # 专辑里没有这首，不硬塞
    assert scrape.inspect_track(a)["year"] == "2009"
    assert len(downloads) == 1                    # 同一张封面只下一次
    assert scrape.inspect_track(a)["cover_w"] == 1200


def test_album_mode_ambiguous_leaves_for_pick(tmp_path, monkeypatch):
    p = make_flac(tmp_path / "a.flac", title="届かない恋",
                  extra={"artist": "米澤円", "album": "WA2"})
    info = {"id": 1, "name": "WA2", "year": "2013", "cover_url": "",
            "tracks": [album_track(song_id=11, title="届かない恋", artist="米澤円"),
                       album_track(song_id=12, title="届かない恋", artist="米澤円")]}
    monkeypatch.setattr(scrape, "album_detail", lambda aid, **kw: info)

    res = scrape.apply_one({"path": str(p), "album_id": 1, "resolve": "album",
                            "fields": {"netease_id": True}, "mode": "fill"},
                           albums={1: info})
    assert res["skipped"] and res["needs_pick"]
    assert scrape.inspect_track(p)["netease_id"] == 0


def test_album_mode_pauses_at_the_current_album_when_rate_limited(tmp_path, monkeypatch):
    from kiralrc import netauth
    from kiralrc.ratelimit import RateLimited

    paths = [make_flac(tmp_path / f"{i}.flac", title=f"T{i}") for i in range(3)]

    def boom(aid, **kw):
        netauth.record_rate_limit(20)
        kw.get("state", {}).update({"rate_limited": True})
        return {}

    monkeypatch.setattr(scrape, "album_detail", boom)
    seen = []
    summary = scrape.apply_plan({"items": [
        {"path": str(p), "album_id": 100 + i, "resolve": "album",
         "fields": {"netease_id": True}, "mode": "fill"} for i, p in enumerate(paths)
    ]}, on_item=lambda i, t, r: seen.append(i))

    assert summary["paused"] and summary["resume_from"] == 1
    assert seen == [1, 2, 3]          # 三首都报了「待处理」，进度不缺
    assert scrape.inspect_track(paths[0])["netease_id"] == 0


def test_album_mode_resumes_from_the_failed_album(tmp_path, monkeypatch):
    """第一张专辑被限速：续跑从它自己开始，第二张专辑不受影响。"""
    from kiralrc import netauth
    from kiralrc.ratelimit import RateLimited

    paths = [make_flac(tmp_path / f"{i}.flac", title=f"T{i}") for i in range(2)]
    calls = []

    def detail(aid, **kw):
        calls.append(aid)
        if len(calls) == 1:
            netauth.record_rate_limit(5)
            kw.get("state", {}).update({"rate_limited": True})
            return {}
        return {"id": aid, "name": "A", "year": "2010", "cover_url": "",
                "tracks": [album_track(song_id=900 + aid, title=f"T{aid - 100}",
                                       artist="")]}

    monkeypatch.setattr(scrape, "album_detail", detail)
    plan = [{"path": str(p), "album_id": 100 + i, "resolve": "album",
             "fields": {"netease_id": True}, "mode": "fill"} for i, p in enumerate(paths)]

    first = scrape.apply_plan({"items": plan})
    assert first["paused"] and first["resume_from"] == 1

    netauth.clear_cooldown()
    second = scrape.apply_plan({"items": plan[first["resume_from"] - 1:]})
    assert not second.get("paused")
    assert second["ok"] == 2
    assert calls == [100, 100, 101]     # 第一张专辑重查了一次，第二张只查一次


def test_non_rate_limit_pause_waits_before_retrying(tmp_path, monkeypatch):
    """反爬挡住时没有冷却时间可等，也不能报 1 秒——否则续跑会连撞三次上限。"""
    from kiralrc import netauth

    p = make_flac(tmp_path / "a.flac", title="T")

    def blocked(aid, **kw):
        kw.get("state", {}).update({"blocked": True})
        return {}

    monkeypatch.setattr(scrape, "album_detail", blocked)
    summary = scrape.apply_plan({"items": [
        {"path": str(p), "album_id": 1, "resolve": "album",
         "fields": {"netease_id": True}, "mode": "fill"},
    ]})
    assert netauth.cooling_for() == 0          # 不是限速
    assert summary["paused"] and summary["retry_after"] >= 25
    assert summary["resume_from"] == 1


def test_non_rate_limit_pause_waits_before_retrying(tmp_path, monkeypatch):
    """反爬挡住时没有冷却时间可等，也不能报 1 秒——否则续跑会连撞三次上限。"""
    from kiralrc import netauth

    p = make_flac(tmp_path / "a.flac", title="T")

    def blocked(aid, **kw):
        kw.get("state", {}).update({"blocked": True})
        return {}

    monkeypatch.setattr(scrape, "album_detail", blocked)
    summary = scrape.apply_plan({"items": [
        {"path": str(p), "album_id": 1, "resolve": "album",
         "fields": {"netease_id": True}, "mode": "fill"},
    ]})
    assert netauth.cooling_for() == 0          # 不是限速
    assert summary["paused"] and summary["retry_after"] >= 25
    assert summary["resume_from"] == 1
