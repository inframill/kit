#!/usr/bin/env python3
"""Run daemon workflows inside each runtime using disposable built-in resources."""
import hashlib
import json
import pathlib
import platform
import subprocess
import tempfile

import yaml
from blueprint_workflows import runtime_flows, local_paths, LOCAL_REFERENCE
from engine_client import Engine
from query_check import query_check

arch = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
lock = json.loads(pathlib.Path('/usr/share/inframill/bundle.lock.json').read_text())
assert hashlib.sha256(pathlib.Path('/usr/local/bin/inframill').read_bytes()).hexdigest() == lock['inframill']['binaries']['linux/'+arch]['sha256']
for release in lock['providers']:
    folder = pathlib.Path('/opt/inframill/providers') / release['name'] / release['version']
    record = json.loads((folder / 'build.json').read_text())
    assert hashlib.sha256((folder / 'provider').read_bytes()).hexdigest() == record['files']['provider']
    metadata = json.loads(subprocess.check_output([str(folder / 'provider'), 'version']))
    assert (metadata['name'], metadata['version']) == (release['name'], release['version'])
with tempfile.TemporaryDirectory(prefix='kit-runtime-') as td:
    tmp = pathlib.Path(td)
    config = yaml.safe_load(pathlib.Path('/etc/inframill/inframill.yaml').read_text())
    config['engine']['dataDir'] = str(tmp / 'data')
    config['engine']['localBlueprintPaths'] = local_paths()
    with Engine(tmp, config, '/usr/local/bin/inframill') as client:
        assert client.rpc('GetServerVersion') == {'version': lock['inframill']['version']}
        query_check(client, LOCAL_REFERENCE, arch)
        tofu = config['engine']['providers']['opentofu']['args'][1]
        runtime_flows(client, arch, tofu)
print('Runtime workflow and provider mocks passed on ' + arch)
