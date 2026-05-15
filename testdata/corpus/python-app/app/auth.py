"""Auth module — exercises Class + Method + Function extraction."""


class Session:
    """A user session.

    Exercises ClassDef + FunctionDef-inside-class (= Method kind)."""

    def __init__(self, user: str) -> None:
        self.user = user

    def user_name(self) -> str:
        """Method that returns the user name."""
        return self.user


def open_session(user: str) -> Session:
    """Module-level function that calls into the Class."""
    return Session(user)
