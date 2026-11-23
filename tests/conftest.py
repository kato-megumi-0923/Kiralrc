"""所有刮削相关用例共用：每个用例都从「没登录态、没冷却」开始。"""

import pytest


@pytest.fixture(autouse=True)
def _fresh_netauth():
    from kiralrc import netauth

    netauth.reset()
    netauth.clear_cooldown()
    yield
    netauth.reset()
    netauth.clear_cooldown()
