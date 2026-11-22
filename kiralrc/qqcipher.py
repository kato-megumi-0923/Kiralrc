"""QQ 音乐歌词用的 3DES。

标准库解不开：S 盒里有笔误、PC-2 偏移不同、只跑 15 轮半、末轮不交换左右。
这份从 Cloudy 的 Go 实现（已验证与 LDDC 逐字符一致）搬过来，S 盒用脚本生成。
"""

from __future__ import annotations

# QQ 的八个替换盒（与标准 DES 不同，不要"修正"）
SBOX = (
    (
        14, 4, 13, 1, 2, 15, 11, 8, 3, 10, 6, 12, 5, 9, 0, 7,
        0, 15, 7, 4, 14, 2, 13, 1, 10, 6, 12, 11, 9, 5, 3, 8,
        4, 1, 14, 8, 13, 6, 2, 11, 15, 12, 9, 7, 3, 10, 5, 0,
        15, 12, 8, 2, 4, 9, 1, 7, 5, 11, 3, 14, 10, 0, 6, 13,
    ),
    (
        15, 1, 8, 14, 6, 11, 3, 4, 9, 7, 2, 13, 12, 0, 5, 10,
        3, 13, 4, 7, 15, 2, 8, 15, 12, 0, 1, 10, 6, 9, 11, 5,
        0, 14, 7, 11, 10, 4, 13, 1, 5, 8, 12, 6, 9, 3, 2, 15,
        13, 8, 10, 1, 3, 15, 4, 2, 11, 6, 7, 12, 0, 5, 14, 9,
    ),
    (
        10, 0, 9, 14, 6, 3, 15, 5, 1, 13, 12, 7, 11, 4, 2, 8,
        13, 7, 0, 9, 3, 4, 6, 10, 2, 8, 5, 14, 12, 11, 15, 1,
        13, 6, 4, 9, 8, 15, 3, 0, 11, 1, 2, 12, 5, 10, 14, 7,
        1, 10, 13, 0, 6, 9, 8, 7, 4, 15, 14, 3, 11, 5, 2, 12,
    ),
    (
        7, 13, 14, 3, 0, 6, 9, 10, 1, 2, 8, 5, 11, 12, 4, 15,
        13, 8, 11, 5, 6, 15, 0, 3, 4, 7, 2, 12, 1, 10, 14, 9,
        10, 6, 9, 0, 12, 11, 7, 13, 15, 1, 3, 14, 5, 2, 8, 4,
        3, 15, 0, 6, 10, 10, 13, 8, 9, 4, 5, 11, 12, 7, 2, 14,
    ),
    (
        2, 12, 4, 1, 7, 10, 11, 6, 8, 5, 3, 15, 13, 0, 14, 9,
        14, 11, 2, 12, 4, 7, 13, 1, 5, 0, 15, 10, 3, 9, 8, 6,
        4, 2, 1, 11, 10, 13, 7, 8, 15, 9, 12, 5, 6, 3, 0, 14,
        11, 8, 12, 7, 1, 14, 2, 13, 6, 15, 0, 9, 10, 4, 5, 3,
    ),
    (
        12, 1, 10, 15, 9, 2, 6, 8, 0, 13, 3, 4, 14, 7, 5, 11,
        10, 15, 4, 2, 7, 12, 9, 5, 6, 1, 13, 14, 0, 11, 3, 8,
        9, 14, 15, 5, 2, 8, 12, 3, 7, 0, 4, 10, 1, 13, 11, 6,
        4, 3, 2, 12, 9, 5, 15, 10, 11, 14, 1, 7, 6, 0, 8, 13,
    ),
    (
        4, 11, 2, 14, 15, 0, 8, 13, 3, 12, 9, 7, 5, 10, 6, 1,
        13, 0, 11, 7, 4, 9, 1, 10, 14, 3, 5, 12, 2, 15, 8, 6,
        1, 4, 11, 13, 12, 3, 7, 14, 10, 15, 6, 8, 0, 5, 9, 2,
        6, 11, 13, 8, 1, 4, 10, 7, 9, 5, 0, 15, 14, 2, 3, 12,
    ),
    (
        13, 2, 8, 4, 6, 15, 11, 1, 10, 9, 3, 14, 5, 0, 12, 7,
        1, 15, 13, 8, 10, 3, 7, 4, 12, 5, 6, 11, 0, 14, 9, 2,
        7, 11, 4, 1, 9, 12, 14, 2, 0, 6, 10, 13, 15, 3, 5, 8,
        2, 1, 14, 7, 4, 10, 8, 13, 15, 12, 9, 0, 3, 5, 6, 11,
    ),
)

_MASK = 0xFFFFFFFF


def _bit(data: bytes, b: int, c: int) -> int:
    """取 data 的第 b 位，左移 c 位。"""
    return ((data[(b // 32) * 4 + 3 - (b % 32) // 8] >> (7 - b % 8)) & 1) << c


def _bit_intr(a: int, b: int, c: int) -> int:
    """取 a 自顶部数第 b 位，左移 c 位。"""
    return ((a >> (31 - b)) & 1) << c


def _bit_intl(a: int, b: int, c: int) -> int:
    """取 a 自底部数第 b 位，右移 c 位。"""
    return (((a & _MASK) << b) & 0x80000000) >> c


def _sbox_bit(a: int) -> int:
    return (a & 32) | ((a & 31) >> 1) | ((a & 1) << 4)


def _initial_permutation(data: bytes) -> tuple[int, int]:
    s0 = (_bit(data, 57, 31) | _bit(data, 49, 30) | _bit(data, 41, 29) | _bit(data, 33, 28) |
          _bit(data, 25, 27) | _bit(data, 17, 26) | _bit(data, 9, 25) | _bit(data, 1, 24) |
          _bit(data, 59, 23) | _bit(data, 51, 22) | _bit(data, 43, 21) | _bit(data, 35, 20) |
          _bit(data, 27, 19) | _bit(data, 19, 18) | _bit(data, 11, 17) | _bit(data, 3, 16) |
          _bit(data, 61, 15) | _bit(data, 53, 14) | _bit(data, 45, 13) | _bit(data, 37, 12) |
          _bit(data, 29, 11) | _bit(data, 21, 10) | _bit(data, 13, 9) | _bit(data, 5, 8) |
          _bit(data, 63, 7) | _bit(data, 55, 6) | _bit(data, 47, 5) | _bit(data, 39, 4) |
          _bit(data, 31, 3) | _bit(data, 23, 2) | _bit(data, 15, 1) | _bit(data, 7, 0))
    s1 = (_bit(data, 56, 31) | _bit(data, 48, 30) | _bit(data, 40, 29) | _bit(data, 32, 28) |
          _bit(data, 24, 27) | _bit(data, 16, 26) | _bit(data, 8, 25) | _bit(data, 0, 24) |
          _bit(data, 58, 23) | _bit(data, 50, 22) | _bit(data, 42, 21) | _bit(data, 34, 20) |
          _bit(data, 26, 19) | _bit(data, 18, 18) | _bit(data, 10, 17) | _bit(data, 2, 16) |
          _bit(data, 60, 15) | _bit(data, 52, 14) | _bit(data, 44, 13) | _bit(data, 36, 12) |
          _bit(data, 28, 11) | _bit(data, 20, 10) | _bit(data, 12, 9) | _bit(data, 4, 8) |
          _bit(data, 62, 7) | _bit(data, 54, 6) | _bit(data, 46, 5) | _bit(data, 38, 4) |
          _bit(data, 30, 3) | _bit(data, 22, 2) | _bit(data, 14, 1) | _bit(data, 6, 0))
    return s0, s1


def _inverse_permutation(s0: int, s1: int) -> bytes:
    data = bytearray(8)
    data[3] = (_bit_intr(s1, 7, 7) | _bit_intr(s0, 7, 6) | _bit_intr(s1, 15, 5) |
               _bit_intr(s0, 15, 4) | _bit_intr(s1, 23, 3) | _bit_intr(s0, 23, 2) |
               _bit_intr(s1, 31, 1) | _bit_intr(s0, 31, 0))
    data[2] = (_bit_intr(s1, 6, 7) | _bit_intr(s0, 6, 6) | _bit_intr(s1, 14, 5) |
               _bit_intr(s0, 14, 4) | _bit_intr(s1, 22, 3) | _bit_intr(s0, 22, 2) |
               _bit_intr(s1, 30, 1) | _bit_intr(s0, 30, 0))
    data[1] = (_bit_intr(s1, 5, 7) | _bit_intr(s0, 5, 6) | _bit_intr(s1, 13, 5) |
               _bit_intr(s0, 13, 4) | _bit_intr(s1, 21, 3) | _bit_intr(s0, 21, 2) |
               _bit_intr(s1, 29, 1) | _bit_intr(s0, 29, 0))
    data[0] = (_bit_intr(s1, 4, 7) | _bit_intr(s0, 4, 6) | _bit_intr(s1, 12, 5) |
               _bit_intr(s0, 12, 4) | _bit_intr(s1, 20, 3) | _bit_intr(s0, 20, 2) |
               _bit_intr(s1, 28, 1) | _bit_intr(s0, 28, 0))
    data[7] = (_bit_intr(s1, 3, 7) | _bit_intr(s0, 3, 6) | _bit_intr(s1, 11, 5) |
               _bit_intr(s0, 11, 4) | _bit_intr(s1, 19, 3) | _bit_intr(s0, 19, 2) |
               _bit_intr(s1, 27, 1) | _bit_intr(s0, 27, 0))
    data[6] = (_bit_intr(s1, 2, 7) | _bit_intr(s0, 2, 6) | _bit_intr(s1, 10, 5) |
               _bit_intr(s0, 10, 4) | _bit_intr(s1, 18, 3) | _bit_intr(s0, 18, 2) |
               _bit_intr(s1, 26, 1) | _bit_intr(s0, 26, 0))
    data[5] = (_bit_intr(s1, 1, 7) | _bit_intr(s0, 1, 6) | _bit_intr(s1, 9, 5) |
               _bit_intr(s0, 9, 4) | _bit_intr(s1, 17, 3) | _bit_intr(s0, 17, 2) |
               _bit_intr(s1, 25, 1) | _bit_intr(s0, 25, 0))
    data[4] = (_bit_intr(s1, 0, 7) | _bit_intr(s0, 0, 6) | _bit_intr(s1, 8, 5) |
               _bit_intr(s0, 8, 4) | _bit_intr(s1, 16, 3) | _bit_intr(s0, 16, 2) |
               _bit_intr(s1, 24, 1) | _bit_intr(s0, 24, 0))
    return bytes(data)


def _f(state: int, key: list[int]) -> int:
    t1 = (_bit_intl(state, 31, 0) | ((state & 0xf0000000) >> 1) | _bit_intl(state, 4, 5) |
          _bit_intl(state, 3, 6) | ((state & 0x0f000000) >> 3) | _bit_intl(state, 8, 11) |
          _bit_intl(state, 7, 12) | ((state & 0x00f00000) >> 5) | _bit_intl(state, 12, 17) |
          _bit_intl(state, 11, 18) | ((state & 0x000f0000) >> 7) | _bit_intl(state, 16, 23))
    t2 = (_bit_intl(state, 15, 0) | ((state & 0x0000f000) << 15) | _bit_intl(state, 20, 5) |
          _bit_intl(state, 19, 6) | ((state & 0x00000f00) << 13) | _bit_intl(state, 24, 11) |
          _bit_intl(state, 23, 12) | ((state & 0x000000f0) << 11) | _bit_intl(state, 28, 17) |
          _bit_intl(state, 27, 18) | ((state & 0x0000000f) << 9) | _bit_intl(state, 0, 23))

    lg = [((t1 >> 24) & 0xFF), ((t1 >> 16) & 0xFF), ((t1 >> 8) & 0xFF),
          ((t2 >> 24) & 0xFF), ((t2 >> 16) & 0xFF), ((t2 >> 8) & 0xFF)]
    for i in range(6):
        lg[i] ^= key[i]

    out = ((SBOX[0][_sbox_bit(lg[0] >> 2)] << 28) |
           (SBOX[1][_sbox_bit(((lg[0] & 0x03) << 4) | (lg[1] >> 4))] << 24) |
           (SBOX[2][_sbox_bit(((lg[1] & 0x0F) << 2) | (lg[2] >> 6))] << 20) |
           (SBOX[3][_sbox_bit(lg[2] & 0x3F)] << 16) |
           (SBOX[4][_sbox_bit(lg[3] >> 2)] << 12) |
           (SBOX[5][_sbox_bit(((lg[3] & 0x03) << 4) | (lg[4] >> 4))] << 8) |
           (SBOX[6][_sbox_bit(((lg[4] & 0x0F) << 2) | (lg[5] >> 6))] << 4) |
           SBOX[7][_sbox_bit(lg[5] & 0x3F)])

    return (_bit_intl(out, 15, 0) | _bit_intl(out, 6, 1) | _bit_intl(out, 19, 2) |
            _bit_intl(out, 20, 3) | _bit_intl(out, 28, 4) | _bit_intl(out, 11, 5) |
            _bit_intl(out, 27, 6) | _bit_intl(out, 16, 7) | _bit_intl(out, 0, 8) |
            _bit_intl(out, 14, 9) | _bit_intl(out, 22, 10) | _bit_intl(out, 25, 11) |
            _bit_intl(out, 4, 12) | _bit_intl(out, 17, 13) | _bit_intl(out, 30, 14) |
            _bit_intl(out, 9, 15) | _bit_intl(out, 1, 16) | _bit_intl(out, 7, 17) |
            _bit_intl(out, 23, 18) | _bit_intl(out, 13, 19) | _bit_intl(out, 31, 20) |
            _bit_intl(out, 26, 21) | _bit_intl(out, 2, 22) | _bit_intl(out, 8, 23) |
            _bit_intl(out, 18, 24) | _bit_intl(out, 12, 25) | _bit_intl(out, 29, 26) |
            _bit_intl(out, 5, 27) | _bit_intl(out, 21, 28) | _bit_intl(out, 10, 29) |
            _bit_intl(out, 3, 30) | _bit_intl(out, 24, 31))


def _crypt(data: bytes, key: list[list[int]]) -> bytes:
    s0, s1 = _initial_permutation(data)
    for i in range(15):
        s1, s0 = _f(s1, key[i]) ^ s0, s1
    s0 = _f(s1, key[15]) ^ s0
    return _inverse_permutation(s0, s1)


def _key_schedule(key: bytes, decrypt: bool) -> list[list[int]]:
    schedule = [[0] * 6 for _ in range(16)]
    shifts = (1, 1, 2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 2, 2, 2, 1)
    perm_c = (56, 48, 40, 32, 24, 16, 8, 0, 57, 49, 41, 33, 25, 17, 9, 1,
              58, 50, 42, 34, 26, 18, 10, 2, 59, 51, 43, 35)
    perm_d = (62, 54, 46, 38, 30, 22, 14, 6, 61, 53, 45, 37, 29, 21, 13, 5,
              60, 52, 44, 36, 28, 20, 12, 4, 27, 19, 11, 3)
    compress = (13, 16, 10, 23, 0, 4, 2, 27, 14, 5, 20, 9, 22, 18, 11, 3,
                25, 7, 15, 6, 26, 19, 12, 1, 40, 51, 30, 36, 46, 54, 29, 39,
                50, 44, 32, 47, 43, 48, 38, 55, 33, 52, 45, 41, 49, 35, 28, 31)

    c = d = 0
    for i in range(28):
        c |= _bit(key, perm_c[i], 31 - i)
        d |= _bit(key, perm_d[i], 31 - i)
    for i in range(16):
        shift = shifts[i]
        c = ((c << shift) | (c >> (28 - shift))) & 0xFFFFFFF0
        d = ((d << shift) | (d >> (28 - shift))) & 0xFFFFFFF0
        at = 15 - i if decrypt else i
        for j in range(24):
            schedule[at][j // 8] |= _bit_intr(c, compress[j], 7 - (j % 8))
        for j in range(24, 48):
            schedule[at][j // 8] |= _bit_intr(d, compress[j] - 27, 7 - (j % 8))
    return schedule


def _keys(key: bytes, decrypt: bool) -> list[list[list[int]]]:
    if not decrypt:
        return [_key_schedule(key[0:8], False), _key_schedule(key[8:16], True),
                _key_schedule(key[16:24], False)]
    return [_key_schedule(key[16:24], True), _key_schedule(key[8:16], False),
            _key_schedule(key[0:8], True)]


def triple_decrypt(data: bytes, key: bytes) -> bytes:
    """按 8 字节一块做三遍，解出明文（zlib 流）。"""
    out = bytearray()
    schedules = _keys(key, True)
    for i in range(0, len(data) - len(data) % 8, 8):
        block = data[i:i + 8]
        for schedule in schedules:
            block = _crypt(block, schedule)
        out += block
    return bytes(out)
