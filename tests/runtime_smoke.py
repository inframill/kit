#!/usr/bin/env python3
"""Run daemon workflows inside each runtime using disposable built-in resources."""
import hashlib
import json
import os
import pathlib
import platform
import subprocess
import sys
import tempfile

import yaml
sys.path.insert(0, '/')  # The harness mounts the blueprint subproject at /blueprints.
from blueprints.tests.integration import runtime_flows
from engine_client import Engine

registry = os.environ['KIT_TEST_REGISTRY']
os.environ['INFRAMILL_HTTP_REGISTRIES'] = registry
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
    with Engine(tmp, config, '/usr/local/bin/inframill') as client:
        assert client.rpc('GetServerVersion') == {'version': lock['inframill']['version']}
        tofu = '/opt/inframill/tools/opentofu/' + lock['opentofu']['version'] + '/tofu'
        runtime_flows(client, registry, arch, tofu)
print('Runtime workflow and provider mocks passed on ' + arch)
