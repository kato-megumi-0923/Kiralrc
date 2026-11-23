"""网易云身份与限速冷却。

两件事放在这里，因为所有网易云请求都要经过：

1. 登录态（可选）。Cloudy 登录后会把 `MUSIC_U=…; __csrf=…` 存进 Windows
   凭据管理器（target 是「应用标识/netease-session」），带上它走的是登录态
   配额，不会被匿名接口的「操作频繁，请稍候再试」卡住。但这是一份账号凭据，
   **默认不借**：要 `KIRALRC_CLOUDY_AUTH=1`（或界面里打开「借用 Cloudy 登录态」）
   才启用；`KIRALRC_NETEASE_COOKIE` 手动指定优先，`KIRALRC_NO_CLOUDY_AUTH=1`
   可随时关掉。

   发出去的范围也收紧了三处：只认 HTTPS 白名单主机、每次请求现构造一份 header
   （不动共用的 dict）、跨主机重定向时把 Cookie 摘掉（urllib 默认会带着走）。

2. 限速冷却。单曲搜索、按 ID 补全、按网易云补全共用一个冷却：第一次吃到
   405/418/429 就记下来，之后谁再想发请求先问 `cooling_for()`；有 Retry-After
   就按它等，没有则默认两分钟。冷却存在进程内，跨进程由调用方（Go 端）接住。

认证状态按**实际请求结果**算：真的带着 Cookie 发出去且没被 301 打回来，才算
「Cloudy 登录态」；被拒过就如实写「匿名（登录态失效，已退回）」。
"""

from __future__ import annotations

import os
import sys
import time
import urllib.parse
import urllib.request

# Cloudy 的 mygo 标识；开发版会带 .dev 后缀，另外留一个历史名兜底
TARGETS = (
    "io.github.nya-owo.cloud-nya/netease-session",
    "io.github.nya-owo.cloud-nya.dev/netease-session",
    "Cloudy/netease-session",
)

# Cookie 只发给这一个主机：代码里所有网易云请求都走它，别的域一律不带
ALLOWED_HOSTS = frozenset({"music.163.com"})

DEFAULT_COOLDOWN = 120.0   # 没有 Retry-After 时按两分钟等
_MAX_COOLDOWN = 1800.0

_CRED_TYPE_GENERIC = 1

_ENV_COOKIE = "KIRALRC_NETEASE_COOKIE"
_ENV_ENABLE = "KIRALRC_CLOUDY_AUTH"
_ENV_DISABLE = "KIRALRC_NO_CLOUDY_AUTH"

_cookie: str | None = None
_source = ""
_state = {"attached": False, "rejected": False}
_opener: urllib.request.OpenerDirector | None = None
_cool_until = 0.0


def _truthy(name: str) -> bool:
    return os.environ.get(name, "").strip().lower() in {"1", "true", "yes", "on"}


# ---------- 主机白名单 ----------

def _host(url: str) -> str:
    try:
        return (urllib.parse.urlsplit(url).hostname or "").lower()
    except Exception:
        return ""


def _allowed(url: str) -> bool:
    """只放行白名单里的 HTTPS 主机。"""
    try:
        scheme = urllib.parse.urlsplit(url).scheme.lower()
    except Exception:
        return False
    return scheme == "https" and _host(url) in ALLOWED_HOSTS


# ---------- 登录态 ----------

def _read_credential_windows(target: str) -> str:
    """读一条 Credential Manager 的 generic 凭据（CredReadW）。"""
    import ctypes
    import ctypes.wintypes as wt

    class CREDENTIALW(ctypes.Structure):
        _fields_ = [
            ("Flags", wt.DWORD),
            ("Type", wt.DWORD),
            ("TargetName", wt.LPWSTR),
            ("Comment", wt.LPWSTR),
            ("LastWritten", wt.FILETIME),
            ("CredentialBlobSize", wt.DWORD),
            ("CredentialBlob", ctypes.POINTER(ctypes.c_byte)),
            ("Persist", wt.DWORD),
            ("AttributeCount", wt.DWORD),
            ("Attributes", ctypes.c_void_p),
            ("TargetAlias", wt.LPWSTR),
            ("UserName", wt.LPWSTR),
        ]

    advapi32 = ctypes.WinDLL("advapi32", use_last_error=True)
    advapi32.CredReadW.argtypes = [wt.LPCWSTR, wt.DWORD, wt.DWORD,
                                   ctypes.POINTER(ctypes.POINTER(CREDENTIALW))]
    advapi32.CredReadW.restype = wt.BOOL

    ptr = ctypes.POINTER(CREDENTIALW)()
    if not advapi32.CredReadW(target, _CRED_TYPE_GENERIC, 0, ctypes.byref(ptr)):
        return ""
    try:
        cred = ptr.contents
        raw = ctypes.string_at(cred.CredentialBlob, cred.CredentialBlobSize)
    finally:
        advapi32.CredFree(ptr)
    return raw.decode("utf-8", "ignore").strip()


def cloudy_enabled() -> bool:
    """用户有没有显式允许借 Cloudy 的登录态（默认不允许）。"""
    return _truthy(_ENV_ENABLE) and not _truthy(_ENV_DISABLE)


def cloudy_cookie() -> str:
    """Cloudy 存的那串 Cookie；没开启、没装、没登录都返回空串。"""
    if sys.platform != "win32" or not cloudy_enabled():
        return ""
    for target in TARGETS:
        try:
            cookie = _read_credential_windows(target)
        except Exception:
            continue
        if "MUSIC_U=" in cookie:
            return cookie
    return ""


def _resolve() -> None:
    """定下这次用哪种身份（只解析一次）。"""
    global _cookie, _source
    if _cookie is not None:
        return
    env = os.environ.get(_ENV_COOKIE, "").strip()
    if env:
        _cookie, _source = env, "环境变量 Cookie"
        return
    borrowed = cloudy_cookie() if cloudy_enabled() else ""
    if borrowed:
        _cookie, _source = borrowed, "Cloudy 登录态"
        return
    _cookie, _source = "", ""


def cookie() -> str:
    _resolve()
    return _cookie or ""


def headers_for(url: str, base: dict | None = None) -> dict:
    """给这次请求现配一份 header；不在白名单里就原样复制，绝不带 Cookie。"""
    out = dict(base or {})
    if not _allowed(url):
        return out
    value = cookie()
    if value:
        out["Cookie"] = value
    return out


class _SafeRedirect(urllib.request.HTTPRedirectHandler):
    """跨主机重定向时把凭据摘掉——urllib 默认会原样带过去。"""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        new = super().redirect_request(req, fp, code, msg, headers, newurl)
        if new is None:
            return None
        if _host(newurl) != _host(req.full_url) or not _allowed(newurl):
            for name in ("Cookie", "Authorization"):
                new.remove_header(name)
        return new


def _get_opener() -> urllib.request.OpenerDirector:
    global _opener
    if _opener is None:
        _opener = urllib.request.build_opener(_SafeRedirect())
    return _opener


def open_url(url: str, base: dict | None = None, timeout: int = 20):
    """带正确身份地发一个 GET，返回 response（调用方负责关闭）。"""
    req = urllib.request.Request(url, headers=headers_for(url, base))
    resp = _get_opener().open(req, timeout=timeout)
    if "Cookie" in req.headers:
        _state["attached"] = True   # 真的带着 Cookie 发出去了
    return resp


def note_rejected() -> bool:
    """接口回 301（需要登录）：摘掉 Cookie 退回匿名。返回之前是否带了 Cookie。"""
    global _cookie, _source
    had = bool(_cookie)
    _cookie, _source = "", ""
    _state["attached"] = False
    if had:
        _state["rejected"] = True
    return had


def reset() -> None:
    """清掉进程内缓存（测试与切换设置后用）。"""
    global _cookie, _source
    _cookie, _source = None, ""
    _state["attached"] = False
    _state["rejected"] = False


def describe() -> str:
    """给界面/日志看的一句话：这次实际用的是哪种身份。"""
    if _state["rejected"]:
        return "匿名（登录态失效，已退回）"
    if _state["attached"] and _source:
        return _source
    if cookie():
        return "匿名（登录态未生效）"
    return "匿名"


# ---------- 限速冷却 ----------

def parse_retry_after(value) -> float | None:
    """HTTP 的 Retry-After 可能是秒数，也可能是 HTTP 日期。"""
    if value is None:
        return None
    text = str(value).strip()
    if not text:
        return None
    try:
        return max(0.0, float(text))
    except ValueError:
        pass
    try:
        import datetime as _dt
        from email.utils import parsedate_to_datetime

        when = parsedate_to_datetime(text)
        if when is None:
            return None
        now = _dt.datetime.now(when.tzinfo) if when.tzinfo else _dt.datetime.now()
        return max(0.0, (when - now).total_seconds())
    except Exception:
        return None


def record_rate_limit(retry_after: float | None = None) -> float:
    """记一次限速，返回要等多少秒。"""
    global _cool_until
    wait = retry_after if retry_after and retry_after > 0 else DEFAULT_COOLDOWN
    wait = min(max(float(wait), 5.0), _MAX_COOLDOWN)
    until = time.monotonic() + wait
    if until > _cool_until:
        _cool_until = until
    return wait


def cooling_for() -> float:
    """还要等多少秒；0 表示没在冷却。"""
    return max(0.0, _cool_until - time.monotonic())


def clear_cooldown() -> None:
    global _cool_until
    _cool_until = 0.0
