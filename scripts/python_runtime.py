"""Find a supported Python for the pinned Ansible environment."""
import shutil
import subprocess
import sys


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
