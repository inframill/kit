"""Synchronize Runmill and the engine runtime pin from a sibling checkout."""
import json
import shutil

from ..core.workspace import workspace
from .workspace import install
from .project import get_context
from .engine import (MODULE, prepare, artifact_items, runmill_digest)
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


def sync(source, *, context=None):
    context = get_context(context)
    source = context.path(source)
    destination = context.root / 'pkg/runmill'
    settings_path = context.root / 'runtime/dependencies.json'
    settings = json.loads(settings_path.read_text())
    with workspace(context.root / '.build', prefix='sync-') as temporary:
        metadata = prepare(source, temporary, context=context)
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
        install([(stage, destination), *artifact_items(temporary, context=context), (prepared_settings, settings_path)], backup)
    print('Synced pkg/runmill and runtime engine pin from ' + metadata['revision'])


if __name__ == '__main__':
    import sys
    from ..cli import main
    raise SystemExit(main(['kit', 'dev', 'sync', *sys.argv[1:]]))
