"""Deliberately vulnerable toy source fixture.

This is not a runnable service and is never executed on a host. The trusted
checker container mounts it read-only as data.
"""


def is_admin(header_value: str) -> bool:
    # INTENTIONAL EXAMPLE BUG: trusts a client-controlled header.
    return header_value == "yes"


def add(a: int, b: int) -> int:
    return a + b