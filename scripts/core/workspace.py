"""Unique staging directories whose lifetime is limited to one operation."""
from contextlib import contextmanager
from pathlib import Path
import tempfile


@contextmanager
def workspace(parent, *, prefix):
    parent = Path(parent)
    parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=prefix, dir=parent) as temporary:
        yield Path(temporary)
