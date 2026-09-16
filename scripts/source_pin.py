"""Verify the public Go dependency used for a release build."""
import pathlib
import subprocess


def verify_source(root, expected):
    source = pathlib.Path(root).parent / 'inframill'
    revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip()
    changed = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=all', '--', 'cmd', 'internal', 'pkg', 'go.mod', 'go.sum', 'LICENSE*', 'NOTICE*'], cwd=source, text=True)
    if revision != expected or changed.strip():
        raise ValueError('Inframill source differs from the source revision pinned in the bundle lock')
    return revision
