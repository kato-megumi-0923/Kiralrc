"""平台让我们慢一点时抛这个，让调用方暂停队列而不是继续硬撞。"""


class RateLimited(RuntimeError):
    pass
