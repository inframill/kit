#!/usr/bin/env python3
"""Acceptance checks using disposable daemons, OCI registries and local resources."""
import contextlib
import io
import json
import os
import pathlib
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import uuid

# Make the sibling blueprint test package available when invoked as a script.
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from blueprints.tests.integration import (BLUEPRINTS, fixture_cases, registry_image,
                                          blueprint_releases, publish, local_flow,
                                          pv_flow, verify)
from drivers.tests.daemon_check import daemon_check
from engine_client import Engine
from image_check import image_check
from release_config import ROOT, local_tools, configuration, bundle_output, bundle_lock, drivers_dist, native_platform


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def tools():
    tofu, ansible = local_tools()
    binaries = [drivers_dist() / d['name'] / d['version'] / native_platform() / 'driver' for d in bundle_lock()['drivers']]
    for tool in [tofu, ansible, ROOT / 'bin/inframill', *binaries]:
        if not tool.is_file():
            raise RuntimeError(f'Missing prerequisite {tool}; see README setup')
    return tofu, ansible


def engine(tmp, env=None, data=None):
    tofu, ansible = tools()
    config = configuration(drivers_dist(), data or tmp / 'data',
                           {'opentofu': tofu, 'ansible': ansible}, target=native_platform())
    return Engine(tmp, config, ROOT / 'bin/inframill', env)


@contextlib.contextmanager
def registry():
    name = 'kit-test-' + uuid.uuid4().hex[:12]
    run(['docker', 'network', 'create', name], stdout=subprocess.DEVNULL)
    started = []
    volume = name + '-data'
    run(['docker', 'volume', 'create', volume], stdout=subprocess.DEVNULL)
    # Docker Desktop's daemon cannot use the Mac's published loopback port.
    # Two disposable endpoints share storage: native/containers read through the
    # bridge endpoint, while docker push writes through the daemon's loopback.
    push_address = '127.0.0.1:' + str(49152 + int(uuid.uuid4().hex[:4], 16) % 16384)
    try:
        run(['docker', 'run', '-d', '--network', name, '--network-alias', 'registry',
             '--name', name, '-p', '127.0.0.1::5000', '-v', volume + ':/var/lib/registry',
             '-e', 'OTEL_TRACES_EXPORTER=none', registry_image()], stdout=subprocess.DEVNULL)
        started.append(name)
        run(['docker', 'run', '-d', '--network', 'host', '--name', name + '-push',
             '-e', 'REGISTRY_HTTP_ADDR=' + push_address, '-e', 'REGISTRY_HTTP_DEBUG_ADDR=127.0.0.1:0',
             '-e', 'OTEL_TRACES_EXPORTER=none',
             '-v', volume + ':/var/lib/registry', registry_image()], stdout=subprocess.DEVNULL)
        started.append(name + '-push')
        address = run(['docker', 'port', name, '5000/tcp'], capture_output=True).stdout.strip()
        yield address, name, push_address
    finally:
        # Remove only tags belonging to this temporary publication endpoint.
        tagged = run(['docker', 'image', 'ls', '--format', '{{.Repository}}:{{.Tag}}',
                      '--filter', 'reference=' + push_address + '/inframill/blueprints/*'],
                     capture_output=True).stdout.splitlines()
        if tagged:
            run(['docker', 'image', 'rm', *tagged], stdout=subprocess.DEVNULL)
        if started:
            run(['docker', 'rm', '-f', *started], stdout=subprocess.DEVNULL)
        run(['docker', 'volume', 'rm', volume], stdout=subprocess.DEVNULL)
        run(['docker', 'network', 'rm', name], stdout=subprocess.DEVNULL)


def registry_env(tmp, address, push_address):
    return {**os.environ, 'KIT_DIST_DIR': str(tmp / 'dist'), 'REGISTRY': push_address,
            'NAMESPACE': 'inframill', 'PLAIN_HTTP': 'true', 'INFRAMILL_HTTP_REGISTRIES': address}


def blueprint_check(name, version):
    if name != 'local-resource' and (name, version) != ('pv-cloud', '0.2.0'):
        raise RuntimeError('No declared integration fixture for this blueprint version')
    with registry() as (address, _, push_address), tempfile.TemporaryDirectory(prefix='kit-blueprint-') as td:
        tmp = pathlib.Path(td)
        env = registry_env(tmp, address, push_address)
        publish(name, version, env)
        reference = f'{address}/inframill/blueprints/{name}:{version}'
        with engine(tmp / 'engine', env) as client:
            if name == 'local-resource':
                local_flow(client, version, reference)
            else:
                pv_flow(client, reference, tools()[0])


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
        env={**env, 'REGISTRY': env['INFRAMILL_HTTP_REGISTRIES'],
             'KIT_DIST_DIR': str(bundle_output().parent.parent)})
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
                 '--mount', 'type=bind,src=' + str(ROOT / 'scripts') + ',dst=/verify,readonly',
                 '--mount', 'type=bind,src=' + str(BLUEPRINTS) + ',dst=/blueprints,readonly',
                 '--entrypoint', python, image, '/verify/runtime-smoke.py'])
    finally:
        subprocess.run(['docker', 'image', 'rm', image], stdout=subprocess.DEVNULL, check=False)


def registry_check():
    tools()
    with registry() as (address, network, push_address), tempfile.TemporaryDirectory(prefix='kit-registry-') as td:
        tmp = pathlib.Path(td)
        env = registry_env(tmp, address, push_address)
        cases = fixture_cases() + [{'name': b['name'], 'version': b['version']} for b in blueprint_releases()]
        for case in cases:
            publish(case['name'], case['version'], env)
        with engine(tmp / 'workflows', env) as client:
            for case in fixture_cases():
                local_flow(client, case['version'], f"{address}/inframill/blueprints/local-resource:{case['version']}")
            pv_flow(client, address + '/inframill/blueprints/pv-cloud:0.2.0', tools()[0])
        daemon_check(tmp, address, env)
        cache_check(tmp, address, env)
        container_checks(tmp, network, env)


def bundle_verify(reference):
    data = json.loads(run(['docker', 'buildx', 'imagetools', 'inspect', '--raw', reference], capture_output=True).stdout)
    platforms = {(m.get('platform', {}).get('os'), m.get('platform', {}).get('architecture')) for m in data.get('manifests', [])}
    if not {('linux', 'amd64'), ('linux', 'arm64')} <= platforms:
        raise RuntimeError('Missing runtime architecture')
    for arch in ['arm64', 'amd64']:
        name = 'kit-verify-' + uuid.uuid4().hex
        for driver in bundle_lock()['drivers']:
            executable = '/opt/inframill/drivers/' + driver['name'] + '/' + driver['version'] + '/driver'
            run(['docker', 'run', '--rm', '--platform', 'linux/' + arch, '--entrypoint', executable, reference, 'version'])
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
    if action == 'config':
        tofu, ansible = local_tools()
        print(json.dumps(configuration(drivers_dist(), os.environ.get('DATA_DIR', str(ROOT / '.inframill')),
                                       {'opentofu': tofu, 'ansible': ansible}, target=native_platform()), indent=2))
    elif action in ('registry', 'full'):
        registry_check()
    elif action == 'blueprint:check':
        blueprint_check(os.environ['BLUEPRINT'], os.environ.get('BLUEPRINT_VERSION', '0.2.0'))
    elif action == 'blueprint:verify':
        reference = os.environ['REFERENCE']
        if '@sha256:' not in reference:
            raise RuntimeError('REFERENCE must be digest-pinned')
        name, version = os.environ['BLUEPRINT'], os.environ.get('BLUEPRINT_VERSION', '0.2.0')
        with tempfile.TemporaryDirectory(prefix='kit-verify-') as td:
            with engine(pathlib.Path(td)) as client:
                verify(client, name, version, reference)
    elif action == 'bundle:verify':
        reference = os.environ['REFERENCE']
        if '@sha256:' not in reference:
            raise RuntimeError('REFERENCE must be digest-pinned')
        bundle_verify(reference)
    else:
        raise RuntimeError('Unknown check')


if __name__ == '__main__':
    main()
