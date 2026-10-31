"""Build the resolved engine module with Toolai into disposable staging."""
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import tempfile

from .project import ROOT, engine_module, run


def build():
    source, metadata = engine_module()
    parent = ROOT / '.build'
    parent.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='engine-', dir=parent) as temporary:
        stage = Path(temporary)
        tree = stage / 'source'
        # The module cache already contains only module sources. Workspaces copy
        # tracked working files, preserving edits without copying local secrets.
        if (source / '.git').exists():
            names = subprocess.check_output(['git', 'ls-files', '-z'], cwd=source).decode().split('\0')
            for name in filter(None, names):
                path = source / name
                if not path.is_file() or path.is_symlink():
                    continue
                target = tree / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(path, target)
            metadata['revision'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip()
            metadata['dirty'] = bool(subprocess.check_output(['git', 'status', '--porcelain'], cwd=source, text=True).strip())
        else:
            shutil.copytree(source, tree)
            # A module pseudo-version identifies the exact source revision.
            metadata['revision'] = metadata['module_version'].rsplit('-', 1)[-1]
            metadata['dirty'] = False
        pinned = re.search(r'-[0-9]{14}-([0-9a-f]{12})$', metadata['module_version'])
        expected_revision = pinned.group(1) if pinned else subprocess.check_output(
            ['git', 'rev-parse', metadata['module_version'] + '^{commit}'], cwd=source, text=True).strip() if (source / '.git').exists() else metadata['revision']
        if metadata['dirty'] or not metadata['revision'].startswith(expected_revision):
            raise ValueError('Engine source must match the SDK module pin; update go.mod or use the pinned checkout')
        # Module cache directories are read-only; staging owns its build outputs.
        for directory in [tree, *(p for p in tree.rglob('*') if p.is_dir())]:
            directory.chmod(directory.stat().st_mode | 0o200)
        env = {**os.environ, 'GOWORK': 'off'}
        toolai = os.environ.get('TOOLAI', 'toolai')
        run([toolai, 'go', 'build', tree], env=env)
        run([toolai, 'go', 'build', tree, '--native', '--output', 'bin/inframill'], env=env)
        version = subprocess.check_output([tree / 'bin/inframill', '--version'], text=True).strip()
        if version != metadata['version']:
            raise ValueError('Engine binary version differs from module metadata')
        (stage / 'engine.json').write_text(json.dumps(metadata, indent=2) + '\n')
        destination = parent / 'engine'
        if destination.exists():
            shutil.rmtree(destination)
        shutil.move(stage, destination)
    binary = ROOT / 'bin/inframill'
    shutil.copy2(destination / 'source/bin/inframill', binary)


if __name__ == '__main__':
    build()
