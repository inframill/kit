"""Install local test tools, or print native engine configuration."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
from runtime.tools import install_ansible, install_opentofu
from ..core.workspace import workspace
from .workspace import replace_outputs
from .project import get_context, runtime_settings, native_platform, local_tools, local_executable, configuration


def find_python(override=None, *, context=None):
    context = get_context(context)
    candidates = [override] if override else [sys.executable, 'python3.14', 'python3.13', 'python3.12', 'python3']
    search_path = os.pathsep.join(str(context.root / entry)
                                  for entry in context.env.get('PATH', os.defpath).split(os.pathsep))
    seen = set()
    for candidate in candidates:
        candidate = str(candidate)
        lookup = str(context.root / candidate) if os.path.dirname(candidate) else candidate
        # Preserve interpreter symlinks: resolving venv/bin/python bypasses the venv.
        executable = shutil.which(lookup, path=search_path)
        if not executable or executable in seen:
            continue
        seen.add(executable)
        try:
            version = context.output(
                [executable, '-c', 'import sys; print(".".join(map(str, sys.version_info[:3])))'],
                stderr=subprocess.DEVNULL, timeout=10,
            ).strip()
            parts = tuple(int(part) for part in version.split('.'))
        except (OSError, subprocess.SubprocessError, ValueError):
            continue
        if len(parts) == 3 and (3, 12) <= parts[:2] <= (3, 14):
            return executable, version
    if override:
        raise ValueError(f'PYTHON={override} must point to a working Python 3.12–3.14 interpreter.')
    raise ValueError(
        'Ansible requires Python 3.12–3.14; no supported interpreter was found. '
        'Install one alongside your system Python, then rerun task setup '
        'or set PYTHON=/path/to/python3.14.'
    )


def setup(*, context=None):
    context = get_context(context)
    settings = runtime_settings(context=context)
    python, version = find_python(context.env.get('PYTHON'), context=context)
    print(f'Using Python {version}: {python}')
    cache = context.root / '.cache'
    tofu = cache / 'tools/opentofu' / settings['OPENTOFU_VERSION']
    venv = cache / ('ansible-' + settings['ANSIBLE_VERSION'])
    with workspace(cache, prefix='setup-') as stage:
        prepared = stage / 'opentofu'
        install_opentofu(native_platform(), settings, prepared)
        backup = stage / 'backup'
        backup.mkdir()
        # Virtualenv launchers embed absolute paths, so install at the final path
        # and restore both tools if installation or pip's validation fails.
        with replace_outputs([tofu, venv], backup) as previous:
            prepared.rename(tofu)
            interpreter = Path(os.path.abspath(python))
            if interpreter.is_relative_to(venv):
                python = str(previous[venv] / interpreter.relative_to(venv))
            install_ansible(python, settings, venv, venv / 'python-packages.json',
                            env=dict(context.env), cwd=context.root)


def config(output=None, *, context=None):
    context = get_context(context)
    tofu, ansible = local_tools(context=context)
    document = configuration(local_executable(context=context), context.root / '.inframill',
                             {'opentofu': tofu, 'ansible': ansible}, context=context)
    document['engine']['localBlueprintPaths'] = [str(context.root / 'blueprints')]
    text = json.dumps(document, indent=2) + '\n'
    if output is not None:
        output = context.path(output)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(text)
        print('Native dev config: ' + str(output))
    else:
        print(text, end='')


if __name__ == '__main__':
    from ..cli import main
    raise SystemExit(main(['kit', 'runtime', *sys.argv[1:]]))
