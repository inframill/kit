"""Temporary staging and replacement of deliberate outputs with rollback."""
from contextlib import contextmanager
from pathlib import Path
import shutil
import tempfile


@contextmanager
def workspace(parent, *, prefix):
    parent = Path(parent)
    parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=prefix, dir=parent) as temporary:
        yield Path(temporary)


@contextmanager
def replace_outputs(targets, backup):
    """Keep previous outputs in an empty backup directory until replacement succeeds.

    Callers own the backup's lifetime, normally through a workspace. Jobs such as
    virtualenv setup can write at their final paths so embedded paths stay valid.
    Yield the mapping from output paths to their temporary backup paths.
    """
    previous = []
    try:
        for index, target in enumerate(targets):
            if target.is_symlink():
                raise ValueError('Output target must not be a symlink: ' + str(target))
            target.parent.mkdir(parents=True, exist_ok=True)
            saved = backup / str(index)
            if target.exists():
                target.rename(saved)
            previous.append((target, saved))
        yield dict(previous)
    except BaseException:
        for target, saved in reversed(previous):
            if target.is_symlink():
                target.unlink()
            elif target.is_dir():
                shutil.rmtree(target)
            elif target.exists():
                target.unlink()
            if saved.exists():
                saved.rename(target)
        raise


def install(items, backup):
    """Install validated staged outputs together, restoring them on failure."""
    items = list(items)
    with replace_outputs([target for _, target in items], backup):
        for prepared, target in items:
            prepared.rename(target)
