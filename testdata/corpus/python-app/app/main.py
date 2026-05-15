"""Entry point — exercises cross-file IMPORTS + CALLS edges."""

from app.auth import open_session


def main() -> int:
    """Entry-point function."""
    session = open_session("demo")
    print(session.user_name())
    return 0


async def run_async() -> int:
    """Async entry-point — exercises AsyncFunctionDef extraction."""
    session = open_session("demo-async")
    return 0 if session.user_name() else 1
