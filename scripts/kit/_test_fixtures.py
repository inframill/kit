"""Small metadata-only checkouts; no sibling, installed tools or Docker."""
import json
from pathlib import Path

from scripts.core.context import Context


def project(root, *, version='1.2.3', env=None):
    root = Path(root)
    root.mkdir(parents=True, exist_ok=True)
    (root / 'toolai.json').write_text(json.dumps({'version': version}))
    (root / 'runtime').mkdir()
    settings = {'OPENTOFU_VERSION': '1.0.0', 'ANSIBLE_VERSION': '2.0.0',
                'INFRAMILL': {'version': '3.0.0'}, 'SOURCE_DATE_EPOCH': '1'}
    (root / 'runtime/dependencies.json').write_text(json.dumps(settings))
    for name in ('opentofu', 'ansible', 'dispatch'):
        folder = root / 'internal/providers' / name
        folder.mkdir(parents=True)
        release = {'name': name, 'version': '0.1.0', 'protocol': 1,
                   'platforms': ['linux/amd64', 'linux/arm64']}
        (folder / 'release.json').write_text(json.dumps(release))
    return Context(root, env=env if env is not None else {})
