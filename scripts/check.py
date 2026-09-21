#!/usr/bin/env python3
"""Acceptance checks using disposable daemons, OCI registries and local resources."""
import contextlib
import io
import json
import os
import pathlib
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import uuid

from daemon_check import daemon_check
from engine_client import Engine
from image_check import image_check
from release_config import (ROOT, FIXTURES, local_tools, configuration, blueprint_root,
                            driver_ids, fixture_cases, registry_image, bundle_output, bundle_lock)


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def tools():
    tofu, ansible = local_tools()
    for tool in [tofu, ansible, ROOT / 'bin/inframill-kit', ROOT / 'bin/inframill']:
        if not tool.is_file():
            raise RuntimeError(f'Missing prerequisite {tool}; see README setup')
    return tofu, ansible


def engine(tmp, env=None, data=None):
    tofu, ansible = tools()
    config = configuration(ROOT / 'bin/inframill-kit', data or tmp / 'data',
                           {'opentofu': tofu, 'ansible': ansible})
    return Engine(tmp, config, ROOT / 'bin/inframill', env)


@contextlib.contextmanager
def registry():
    name = 'kit-test-' + uuid.uuid4().hex[:12]
    run(['docker', 'network', 'create', name], stdout=subprocess.DEVNULL)
    started = False
    try:
        run(['docker', 'run', '-d', '--rm', '--network', name, '--network-alias', 'registry',
             '--name', name, '-p', '127.0.0.1::5000', registry_image()], stdout=subprocess.DEVNULL)
        started = True
        address = run(['docker', 'port', name, '5000/tcp'], capture_output=True).stdout.strip()
        yield address, name
    finally:
        if started:
            run(['docker', 'stop', name], stdout=subprocess.DEVNULL)
        run(['docker', 'network', 'rm', name], stdout=subprocess.DEVNULL)


def registry_env(tmp, address):
    return {**os.environ, 'KIT_DIST_DIR': str(tmp / 'dist'), 'REGISTRY': address,
            'NAMESPACE': 'inframill', 'PLAIN_HTTP': 'true', 'INFRAMILL_HTTP_REGISTRIES': address}


def publish(name, version, env):
    selection = {**env, 'BLUEPRINT_ROOT': str(blueprint_root(name)),
                 'BLUEPRINT': name, 'BLUEPRINT_VERSION': version}
    for action in ['blueprint:package', 'blueprint:push', 'blueprint:push']:
        run(['go', 'run', './cmd/kit-build', action], cwd=ROOT, env=selection)


def local_flow(client, version, reference, name=None, fetch=True):
    data = json.loads((FIXTURES / f'examples/local-resource-{version}.json').read_text())
    data['architecture'] = 'arm64' if platform.machine() in ('arm64', 'aarch64') else 'amd64'
    if fetch:
        client.fetch(reference)
    iid = client.initialize(name or 'test/local-' + version.replace('.', '-'), reference, data)
    ids = driver_ids('local-resource', version)
    prefix, ansible = ids['opentofu'], ids['ansible']

    def do(command, args=None):
        return client.execute(iid, command, args)
    do(prefix + '.init')
    do(prefix + '.validate')
    plan = do(prefix + '.plan')
    do(prefix + '.apply', {'plan_id': plan['plan_id']})
    do(ansible + '.inventory')
    do(ansible + '.syntax-check', {'playbook': 'site'})
    do(ansible + '.check', {'playbook': 'site'})
    do(ansible + '.apply', {'playbook': 'site'})
    destroy = do(prefix + '.plan', {'destroy': True})
    do(prefix + '.destroy', {'plan_id': destroy['plan_id']})


def pv_flow(client, reference):
    tofu, _ = tools()
    client.fetch(reference)
    ids = driver_ids('pv-cloud', '0.2.0')
    instances = []
    for suffix in ['arm64', 'amd64', '0.2.0']:
        values = json.loads((ROOT / f'examples/pv-cloud-{suffix}.json').read_text())
        iid = client.initialize('test/pv-' + suffix.replace('.', '-'), reference, values)
        instances.append(iid)
        client.execute(iid, ids['ansible'] + '.syntax-check', {'playbook': 'site'})
    data = pathlib.Path(client.config['engine']['dataDir'])
    workspaces = [data / 'instances' / iid / 'workspace' / ids['opentofu'] for iid in instances]
    assert all((work / 'main.tf').is_file() for work in workspaces)
    for args in [['init', '-backend=false', '-input=false', '-lockfile=readonly', '-no-color'],
                 ['validate', '-no-color'], ['test', '-no-color']]:
        run([tofu, *args], cwd=workspaces[0], env={**os.environ, 'HCLOUD_TOKEN': 'mock-only'})


def blueprint_check(name, version):
    if name != 'local-resource' and (name, version) != ('pv-cloud', '0.2.0'):
        raise RuntimeError('No declared integration fixture for this blueprint version')
    with registry() as (address, _), tempfile.TemporaryDirectory(prefix='kit-blueprint-') as td:
        tmp = pathlib.Path(td)
        env = registry_env(tmp, address)
        publish(name, version, env)
        reference = f'{address}/inframill/blueprints/{name}:{version}'
        with engine(tmp / 'engine', env) as client:
            if name == 'local-resource':
                local_flow(client, version, reference)
            else:
                pv_flow(client, reference)


def cache_check(tmp, address, env):
    record = json.loads((tmp / 'dist/blueprints/local-resource/0.1.0/build.json').read_text())
    reference = address + '/inframill/blueprints/local-resource@' + record['descriptor']['digest']
    data = tmp / 'fresh-data'
    with engine(tmp / 'fetch', env, data) as client:
        info = client.fetch(reference)
        assert info['status'] == 'BLUEPRINT_STATUS_READY', info
        assert info['repository'] == address + '/inframill/blueprints/local-resource', info
    offline = {**env, 'INFRAMILL_HTTP_REGISTRIES': ''}
    with engine(tmp / 'offline', offline, data) as client:
        # Explicit init never fetches; the registry isn't authorized in this daemon.
        local_flow(client, '0.1.0', reference, 'test/offline', fetch=False)
    cached_schema = next((data / 'blueprints').rglob('blueprint.yaml'))
    cached_schema.write_bytes(cached_schema.read_bytes() + b'\n')
    before = set((data / 'instances').iterdir())
    with engine(tmp / 'tampered', offline, data) as client:
        entries = client.rpc('ListBlueprints')['blueprints']
        assert len(entries) == 1 and entries[0]['status'] == 'BLUEPRINT_STATUS_FAILED', entries
        try:
            client.initialize('test/tampered', reference, {'architecture': 'arm64'})
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert 'blueprint' in failure.get('message', '').lower(), failure
        else:
            raise RuntimeError('Corrupted cached source was accepted')
        assert set((data / 'instances').iterdir()) == before
        # Removal of a failed catalogue entry preserves existing Instance snapshots.
        client.rpc('DeleteBlueprint', {'reference': reference})
        assert client.rpc('ListBlueprints').get('blueprints', []) == []
        assert len(client.rpc('ListInstances')['instances']) == 1
    print('Offline cache reuse, failed catalogue entry and safe deletion passed', flush=True)


def container_checks(tmp, network, env):
    if not (bundle_output() / 'build.json').is_file():
        raise RuntimeError('Run task bundle before check-full container verification')
    run(['go', 'run', './cmd/kit-build', 'bundle:push'], cwd=ROOT,
        env={**env, 'KIT_DIST_DIR': str(bundle_output().parent.parent)})
    image = 'docker.io/library/inframill-kit-test:' + uuid.uuid4().hex
    layout = bundle_output() / 'oci'
    index = json.loads((layout / 'index.json').read_text())
    for manifest in index['manifests']:
        manifest.setdefault('annotations', {})['org.opencontainers.image.ref.name'] = image.rsplit(':', 1)[1]
        manifest['annotations']['io.containerd.image.name'] = image
    archive = tmp / 'runtime.tar'
    with tarfile.open(archive, 'w') as tar:
        for path in sorted(layout.rglob('*')):
            if path.name != 'index.json':
                tar.add(path, arcname=str(path.relative_to(layout)), recursive=False)
        data = json.dumps(index).encode()
        header = tarfile.TarInfo('index.json')
        header.size = len(data)
        tar.addfile(header, io.BytesIO(data))
    run(['docker', 'load', '-i', archive])
    try:
        python = '/opt/inframill/tools/ansible/' + bundle_lock()['ansible_core'] + '/bin/python3'
        for arch in ['arm64', 'amd64']:
            image_check(tmp, image, arch, network)
            run(['docker', 'run', '--rm', '--pull=never', '--platform', 'linux/' + arch,
                 '--network', network, '-e', 'KIT_TEST_REGISTRY=registry:5000',
                 '-e', 'KIT_TEST_FIXTURES=' + json.dumps([
                     {**case, 'drivers': driver_ids(case['name'], case['version'])} for case in fixture_cases()]),
                 '--mount', 'type=bind,src=' + str(ROOT / 'scripts') + ',dst=/verify,readonly',
                 '--mount', 'type=bind,src=' + str(ROOT / 'examples/pv-cloud-0.2.0.json') + ',dst=/pv-example.json,readonly',
                 '--entrypoint', python, image, '/verify/runtime-smoke.py'])
    finally:
        subprocess.run(['docker', 'image', 'rm', image], stdout=subprocess.DEVNULL, check=False)


def registry_check():
    tools()
    with registry() as (address, network), tempfile.TemporaryDirectory(prefix='kit-registry-') as td:
        tmp = pathlib.Path(td)
        env = registry_env(tmp, address)
        cases = fixture_cases() + json.loads((ROOT / 'releases/sets/default.yaml').read_text())['blueprints']
        for case in cases:
            publish(case['name'], case['version'], env)
        with engine(tmp / 'workflows', env) as client:
            for case in fixture_cases():
                local_flow(client, case['version'], f"{address}/inframill/blueprints/local-resource:{case['version']}")
            pv_flow(client, address + '/inframill/blueprints/pv-cloud:0.2.0')
        daemon_check(tmp, address, env)
        conflict = tmp / 'conflict/blueprints/local-resource/0.1.0'
        shutil.copytree(tmp / 'dist/blueprints/local-resource/0.2.0', conflict)
        index_path = conflict / 'oci/index.json'
        index = json.loads(index_path.read_text())
        for descriptor in index['manifests']:
            descriptor.setdefault('annotations', {})['org.opencontainers.image.ref.name'] = '0.1.0'
        index_path.write_text(json.dumps(index))
        rejected = subprocess.run(['go', 'run', './cmd/kit-build', 'blueprint:push'], cwd=ROOT,
                                  env={**env, 'KIT_DIST_DIR': str(tmp / 'conflict'),
                                       'BLUEPRINT': 'local-resource', 'BLUEPRINT_VERSION': '0.1.0'},
                                  text=True, capture_output=True)
        if rejected.returncode == 0 or 'release tag already points to different content' not in rejected.stderr:
            raise RuntimeError('Conflicting release tag was not rejected: ' + rejected.stderr)
        cache_check(tmp, address, env)
        container_checks(tmp, network, env)


def bundle_verify(reference):
    data = json.loads(run(['docker', 'buildx', 'imagetools', 'inspect', '--raw', reference], capture_output=True).stdout)
    platforms = {(m.get('platform', {}).get('os'), m.get('platform', {}).get('architecture')) for m in data.get('manifests', [])}
    if not {('linux', 'amd64'), ('linux', 'arm64')} <= platforms:
        raise RuntimeError('Missing runtime architecture')
    for arch in ['arm64', 'amd64']:
        name = 'kit-verify-' + uuid.uuid4().hex
        run(['docker', 'run', '--rm', '--platform', 'linux/' + arch, '--entrypoint', 'inframill-kit', reference, 'version'])
        run(['docker', 'run', '-d', '--rm', '--platform', 'linux/' + arch, '--name', name, reference])
        try:
            def ready():
                state = run(['docker', 'inspect', '--format', '{{.State.Running}}', name], capture_output=True)
                if state.stdout.strip() != 'true':
                    raise RuntimeError('Published image exited')
                response = subprocess.run(['docker', 'exec', name, 'inframill', 'server', 'driver', 'list', '-o', 'json'], capture_output=True, text=True)
                return json.loads(response.stdout) if response.returncode == 0 else None
            drivers = Engine.poll(ready, 45)['drivers']
            expected = {d['name'] + '-' + d['version'].replace('.', '-') for d in bundle_lock()['drivers']}
            assert {d['id'] for d in drivers} == expected, drivers
            assert all(d['status'] == 'DRIVER_STATUS_AVAILABLE' for d in drivers), drivers
        finally:
            run(['docker', 'stop', name], stdout=subprocess.DEVNULL)


def main():
    action = sys.argv[1]
    if action in ('registry', 'full'):
        registry_check()
    elif action == 'blueprint:check':
        blueprint_check(os.environ['BLUEPRINT'], os.environ.get('BLUEPRINT_VERSION', '0.2.0'))
    elif action == 'driver:check':
        name, version = os.environ['DRIVER'], os.environ.get('DRIVER_VERSION', '0.2.0')
        run([ROOT / 'bin/inframill-kit', 'driver', name, '--driver-version', version],
            input=json.dumps({'protocol': 1, 'method': 'info', 'params': {}}))
        run(['go', 'test', '-vet=off', './internal/drivers/' + name])
    elif action == 'blueprint:verify':
        reference = os.environ['REFERENCE']
        if '@sha256:' not in reference:
            raise RuntimeError('REFERENCE must be digest-pinned')
        name, version = os.environ['BLUEPRINT'], os.environ.get('BLUEPRINT_VERSION', '0.2.0')
        values = json.loads((blueprint_root(name) / f'examples/{name}-{version}.json').read_text())
        release = json.loads(run(['go', 'run', './cmd/kit-build', 'blueprint:requirements'],
                                 cwd=ROOT, capture_output=True,
                                 env={**os.environ, 'BLUEPRINT_ROOT': str(blueprint_root(name))}).stdout)
        candidates = [d['id'] for d in release['drivers'] if d['name'] == 'ansible']
        if len(candidates) != 1:
            raise RuntimeError('Verification requires a fixture selecting one Ansible driver')
        with tempfile.TemporaryDirectory(prefix='kit-verify-') as td:
            with engine(pathlib.Path(td)) as client:
                client.fetch(reference)
                iid = client.initialize('verify/blueprint', reference, values)
                client.execute(iid, candidates[0] + '.syntax-check', {'playbook': 'site'})
    elif action == 'bundle:verify':
        reference = os.environ['REFERENCE']
        if '@sha256:' not in reference:
            raise RuntimeError('REFERENCE must be digest-pinned')
        bundle_verify(reference)
    else:
        raise RuntimeError('Unknown check')


if __name__ == '__main__':
    main()
