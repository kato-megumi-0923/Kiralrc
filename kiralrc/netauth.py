"""借 Cloudy 的网易云登录态。

Cloudy 登录后会把 `MUSIC_U=…; __csrf=…` 存进 Windows 凭据管理器
（target 是「应用标识/netease-session」）。带上这个 Cookie 走的是登录态配额，
就不会再被匿名接口的「操作频繁，请稍候再试」卡住。

读不到（没装 Cloudy、没登录、不是 Windows）就安静退回匿名，一切照旧。
环境变量 KIRALRC_NETEASE_COOKIE 可以手动指定，优先级最高。
"""

from __future__ import annotations

import os
import sys

# Cloudy 的 mygo 标识；开发版会带 .dev 后缀，另外留一个历史名兜底
TARGETS = (
    "io.github.nya-owo.cloud-nya/netease-session",
    "io.github.nya-owo.cloud-nya.dev/netease-session",
    "Cloudy/netease-session",
)

_CRED_TYPE_GENERIC = 1

_cookie: str | None = None


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


def cloudy_cookie() -> str:
    """Cloudy 存的那串 Cookie；没有就空串。

    设了 KIRALRC_NO_CLOUDY_AUTH=1 就不去读（不想被借用时用）。
    """
    if sys.platform != "win32" or os.environ.get("KIRALRC_NO_CLOUDY_AUTH"):
        return ""
    for target in TARGETS:
        try:
            cookie = _read_credential_windows(target)
        except Exception:
            continue
        if "MUSIC_U=" in cookie:
            return cookie
    return ""


def netease_cookie() -> str:
    """环境变量优先，其次借 Cloudy 的登录态；结果在进程内记住。"""
    global _cookie
    if _cookie is None:
        _cookie = (os.environ.get("KIRALRC_NETEASE_COOKIE", "").strip()
                   or cloudy_cookie())
    return _cookie


def describe() -> str:
    """给界面用的一句话：现在用的是哪种身份。"""
    if os.environ.get("KIRALRC_NETEASE_COOKIE", "").strip():
        return "环境变量 Cookie"
    if cloudy_cookie():
        return "Cloudy 登录态"
    return "匿名"


def ensure(headers: dict) -> bool:
    """给请求头补上 Cookie；本来就有或者拿不到就什么都不做。"""
    if "Cookie" in headers:
        return True
    cookie = netease_cookie()
    if cookie:
        headers["Cookie"] = cookie
        return True
    return False


def forget(headers: dict) -> None:
    """登录态过期时摘掉，退回匿名。"""
    global _cookie
    headers.pop("Cookie", None)
    _cookie = ""
