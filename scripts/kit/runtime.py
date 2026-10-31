"""Install local test tools, or print native engine configuration."""
import argparse
import os
import shutil
import subprocess
import sys
from runtime.tools import install_ansible, install_opentofu
from .project import ROOT, runtime_settings, native_platform, local_tools, local_executable, configuration
import json

def find_python(override=None):
    candidates = [override] if override else [sys.executable, 'python3.14', 'python3.13', 'python3.12', 'python3']
    seen = set()
    for candidate in candidates:
        executable = shutil.which(candidate)
        if not executable or executable in seen:
            continue
        seen.add(executable)
        try:
            version = subprocess.check_output(
                [executable, '-c', 'import sys; print(".".join(map(str, sys.version_info[:3])))'],
                text=True, stderr=subprocess.DEVNULL, timeout=10,
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


def setup():
    settings = runtime_settings()
    python, version = find_python(os.environ.get('PYTHON'))
    print(f'Using Python {version}: {python}')
    install_opentofu(native_platform(), settings, ROOT / '.cache/tools/opentofu' / settings['OPENTOFU_VERSION'])
    venv = ROOT / '.cache' / ('ansible-' + settings['ANSIBLE_VERSION'])
    install_ansible(python, settings, venv, venv / 'python-packages.json')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['setup', 'config'])
    args = parser.parse_args()
    if args.action == 'setup': setup()
    else:
        tofu, ansible = local_tools()
        config = configuration(local_executable(), ROOT / '.inframill', {'opentofu': tofu, 'ansible': ansible})
        config['engine']['localBlueprintPaths'] = [str(ROOT / 'blueprints')]
        print(json.dumps(config, indent=2))
