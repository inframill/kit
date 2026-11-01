"""Synchronize Runmill and the engine runtime pin from a sibling checkout."""
from pathlib import Path
import json
import shutil
import tempfile

from .project import ROOT
from .engine import (MODULE, engine_checkout, prepare, artifact_items, install, runmill_digest)
LOCAL_RUNMILL = 'github.com/inframill/kit/pkg/runmill'


def stage_runmill(source, destination, stage):
    package = source / 'pkg/runmill'
    files = sorted(package.glob('*.go'))
    if not files or package.is_symlink() or any(path.is_symlink() or not path.is_file() for path in files):
        raise ValueError('Expected regular Go source files in the sibling pkg/runmill')
    if destination.is_symlink() or destination.parent.is_symlink():
        raise ValueError('Kit pkg/runmill must not be a symlink')
    if destination.exists():
        shutil.copytree(destination, stage, symlinks=True)
    else:
        stage.mkdir()
    # Keep Kit's README and any non-Go files; replace the entire Go source set,
    # including tests and platform files, so removed upstream files disappear.
    for path in stage.glob('*.go'):
        if path.is_symlink() or not path.is_file():
            raise ValueError('Kit Runmill Go files must be regular files')
        path.unlink()
    for path in files:
        data = path.read_bytes().replace((MODULE + '/pkg/runmill').encode(), LOCAL_RUNMILL.encode())
        (stage / path.name).write_bytes(data)


def sync(source):
    source = Path(source).resolve()
    destination = ROOT / 'pkg/runmill'
    settings_path = ROOT / 'runtime/dependencies.json'
    settings = json.loads(settings_path.read_text())
    parent = ROOT / '.build'
    parent.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='sync-', dir=parent) as temporary:
        temporary = Path(temporary)
        metadata = prepare(source, temporary)
        stage = temporary / 'pkg/runmill'
        stage.parent.mkdir()
        stage_runmill(source, destination, stage)
        if runmill_digest(stage) != metadata['runmill_sha256']:
            raise ValueError('Runmill source changed during import')
        settings['INFRAMILL'] = metadata
        prepared_settings = temporary / 'dependencies.json'
        prepared_settings.write_text(json.dumps(settings, indent=2) + '\n')
        backup = temporary / 'backup'
        backup.mkdir()
        install([(stage, destination), *artifact_items(temporary), (prepared_settings, settings_path)], backup)
    print('Synced pkg/runmill and runtime engine pin from ' + metadata['revision'])


if __name__ == '__main__':
    sync(engine_checkout())
