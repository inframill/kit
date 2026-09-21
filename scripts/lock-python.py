#!/usr/bin/env python3
"""Update Python pins in the selected release from an explicitly resolved pip report."""
import json
import pathlib
import sys
import urllib.request

from release_config import ROOT, project_version, python_requirements

path = ROOT / f'releases/{project_version()}.lock.json'
lock = json.loads(path.read_text())
report = json.loads(pathlib.Path(sys.argv[1]).read_text())
packages = []
for item in sorted(report['install'], key=lambda x: x['metadata']['name'].lower()):
    name, version = item['metadata']['name'], item['metadata']['version']
    with urllib.request.urlopen(f'https://pypi.org/pypi/{name}/{version}/json', timeout=60) as response:
        metadata = json.load(response)
    source = next(f for f in metadata['urls'] if f['packagetype'] == 'sdist')
    packages.append({
        'name': name, 'version': version,
        'hashes': sorted({f['digests']['sha256'] for f in metadata['urls']}),
        'source': {'url': source['url'], 'sha256': source['digests']['sha256']},
    })
lock['python_dependencies'] = packages
python_requirements(lock)
path.write_text(json.dumps(lock, indent=2) + '\n')
