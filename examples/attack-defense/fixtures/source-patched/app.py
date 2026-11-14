"""Patched toy source fixture. Same API, no header trust."""


def is_admin(header_value: str) -> bool:
    return False


def add(a: int, b: int) -> int:
    return a + b