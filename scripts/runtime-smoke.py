#!/usr/bin/env python3
"""Run daemon workflows inside each runtime using disposable built-in resources."""
import hashlib
import json
import os
import pathlib
import platform
import subprocess
import tempfile

import yaml
from engine_client import Engine

registry = os.environ['KIT_TEST_REGISTRY']
os.environ['INFRAMILL_HTTP_REGISTRIES'] = registry
arch = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
lock = json.loads(pathlib.Path('/usr/share/inframill/bundle.lock.json').read_text())
assert hashlib.sha256(pathlib.Path('/usr/local/bin/inframill').read_bytes()).hexdigest() == lock['inframill']['binaries'][arch]['sha256']
assert json.loads(subprocess.check_output(['inframill-kit', 'version']))['version'] == lock['kit_version'] == lock['version']
with tempfile.TemporaryDirectory(prefix='kit-runtime-') as td:
    tmp = pathlib.Path(td)
    config = yaml.safe_load(pathlib.Path('/etc/inframill/inframill.yaml').read_text())
    config['engine']['dataDir'] = str(tmp / 'data')
    with Engine(tmp, config, '/usr/local/bin/inframill') as client:
        assert client.rpc('GetServerVersion') == {'version': lock['inframill']['version']}
        for case in json.loads(os.environ['KIT_TEST_FIXTURES']):
            version = case['version']
            reference = f'{registry}/inframill/blueprints/local-resource:{version}'
            client.fetch(reference)
            iid = client.initialize('smoke/v' + version.replace('.', '-'), reference, {'architecture': arch})
            prefix, ansible = case['drivers']['opentofu'], case['drivers']['ansible']
            client.execute(iid, prefix + '.init')
            plan = client.execute(iid, prefix + '.plan')
            client.execute(iid, prefix + '.apply', {'plan_id': plan['plan_id']})
            client.execute(iid, ansible + '.apply', {'playbook': 'site'})
            plan = client.execute(iid, prefix + '.plan', {'destroy': True})
            client.execute(iid, prefix + '.destroy', {'plan_id': plan['plan_id']})
        reference = registry + '/inframill/blueprints/pv-cloud:0.2.0'
        values = json.loads(pathlib.Path('/pv-example.json').read_text())
        client.fetch(reference)
        iid = client.initialize('smoke/pv-cloud', reference, values)
        release = json.loads(pathlib.Path('/usr/share/inframill/sources/kit/releases/blueprints/pv-cloud/0.2.0.yaml').read_text())
        ids = {d['name']: d['id'] for d in release['drivers']}
        client.execute(iid, ids['ansible'] + '.syntax-check', {'playbook': 'site'})
        work = tmp / 'data/instances' / iid / 'workspace' / ids['opentofu']
        tofu = '/opt/inframill/tools/opentofu/' + lock['opentofu']['version'] + '/tofu'
        for args in [['init', '-backend=false', '-input=false', '-lockfile=readonly', '-no-color'],
                     ['validate', '-no-color'], ['test', '-no-color']]:
            subprocess.run([tofu, *args], cwd=work, env={**os.environ, 'HCLOUD_TOKEN': 'mock-only'}, check=True)
print('Runtime workflow and provider mocks passed on ' + arch)
