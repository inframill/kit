#!/usr/bin/env python3
"""Acceptance checks using disposable daemons, OCI registries and local resources."""
import contextlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import uuid

# Make the Kit build and provider helpers available when invoked as a script.
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from blueprint_workflows import (BLUEPRINTS, local_paths, LOCAL_REFERENCE,
                                 local_flow, pv_flow, hello_world_flow)
from providers.tests.daemon_check import daemon_check
from engine_client import Engine
from query_check import query_check
from image_check import image_check
from build import runtime_settings, load_image, verify_image, ROOT, local_tools, configuration, bundle_output, bundle_lock, providers_dist, native_platform


def run(args, **kwargs):
    print('+', ' '.join(map(str, args)), file=sys.stderr, flush=True)
    return subprocess.run(list(map(str, args)), check=True, text=True, **kwargs)


def tools():
    tofu, ansible = local_tools()
    binaries = [providers_dist() / d['name'] / d['version'] / native_platform() / 'provider' for d in bundle_lock()['providers']]
    for tool in [tofu, ansible, ROOT / 'bin/inframill', *binaries]:
        if not tool.is_file():
            raise RuntimeError(f'Missing prerequisite {tool}; see README setup')
    return tofu, ansible


def engine(tmp, env=None, data=None, debug_only=False, paths=None):
    if debug_only:
        config = {'logger': {'type': 'slog', 'settings': {'level': 'info', 'format': 'json'}},
            'engine': {'dataDir': str(data or tmp / 'data'), 'providers': {
            'debug': {'path': str(ROOT / 'bin/inframill'), 'args': ['debug', 'provider']}}},
            'handlers': [{'type': 'connect-engine'}]}
    else:
        tofu, ansible = tools()
        config = configuration(providers_dist(), data or tmp / 'data',
                               {'opentofu': tofu, 'ansible': ansible}, target=native_platform())
    config['engine']['localBlueprintPaths'] = local_paths() if paths is None else paths
    return Engine(tmp, config, ROOT / 'bin/inframill', env)


def registry_image():
    image = json.loads((ROOT / 'tests/fixtures/registry.json').read_text())['image']
    if not re.fullmatch(r'.+@sha256:[0-9a-f]{64}', image):
        raise ValueError('Registry image must be digest pinned')
    return image


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
    # Matching ports let the native Docker CLI inspect the same image reference.
    push_address = None
    try:
        run(['docker', 'run', '-d', '--network', name, '--network-alias', 'registry',
             '--name', name, '-p', '127.0.0.1::5000', '-v', volume + ':/var/lib/registry',
             '-e', 'OTEL_TRACES_EXPORTER=none', registry_image()], stdout=subprocess.DEVNULL)
        started.append(name)
        address = run(['docker', 'port', name, '5000/tcp'], capture_output=True).stdout.strip()
        push_address = address
        if native_platform().startswith('darwin/'):
            run(['docker', 'run', '-d', '--network', 'host', '--name', name + '-push',
                 '-e', 'REGISTRY_HTTP_ADDR=' + push_address, '-e', 'REGISTRY_HTTP_DEBUG_ADDR=127.0.0.1:0',
                 '-e', 'OTEL_TRACES_EXPORTER=none',
                 '-v', volume + ':/var/lib/registry', registry_image()], stdout=subprocess.DEVNULL)
            started.append(name + '-push')
        yield address, name, push_address
    finally:
        # Remove only tags belonging to this temporary publication endpoint.
        tagged = run(['docker', 'image', 'ls', '--format', '{{.Repository}}:{{.Tag}}',
                      '--filter', 'reference=' + push_address + '/inframill/kit'],
                     capture_output=True).stdout.splitlines() if push_address else []
        if tagged:
            run(['docker', 'image', 'rm', *tagged], stdout=subprocess.DEVNULL)
        if started:
            run(['docker', 'rm', '-f', *started], stdout=subprocess.DEVNULL)
        run(['docker', 'volume', 'rm', volume], stdout=subprocess.DEVNULL)
        run(['docker', 'network', 'rm', name], stdout=subprocess.DEVNULL)


def registry_env(tmp, push_address):
    return {**os.environ, 'KIT_DIST_DIR': str(tmp / 'dist'), 'REGISTRY': push_address,
            'NAMESPACE': 'inframill'}


def blueprint_check(name):
    if name not in ('pv-cloud', 'hello-world', 'local-resource'):
        raise RuntimeError('Unknown local blueprint')
    with tempfile.TemporaryDirectory(prefix='kit-blueprint-') as td:
        tmp = pathlib.Path(td)
        reference = 'build.in/' + name
        with engine(tmp / 'engine', debug_only=name == 'hello-world') as client:
            client.create_namespace('test')
            if name == 'hello-world':
                hello_world_flow(client, reference)
            elif name == 'local-resource':
                local_flow(client, reference)
            else:
                pv_flow(client, reference, tools()[0])


def cache_check(tmp, env):
    reference = LOCAL_REFERENCE
    data = tmp / 'fresh-data'
    with engine(tmp / 'fetch', env, data) as client:
        client.create_namespace('test')
        client.create_namespace('sibling')
        info = client.fetch(reference, 'test')
        assert info['status'] == 'BLUEPRINT_STATUS_READY', info
        assert 'namespace' not in info, info
        assert info['repository'] == reference, info
        reference += '@' + info['digest']
        assert client.rpc('ListBlueprints', namespace='sibling').get('blueprints', []) == []
        sibling = client.fetch(reference, 'sibling')
        assert 'namespace' not in sibling, sibling
    offline = env
    with engine(tmp / 'offline', offline, data, paths=[]) as client:
        # Explicit init never fetches; local source access is disabled in this daemon.
        local_flow(client, reference, 'test/offline', fetch=False)
    cached_schema = next((data / 'test/blueprints').rglob('blueprint.yaml'))
    cached_schema.write_bytes(cached_schema.read_bytes() + b'\n')
    before = set((data / 'test/instances').iterdir())
    with engine(tmp / 'tampered', offline, data, paths=[]) as client:
        entries = client.rpc('ListBlueprints', namespace='test')['blueprints']
        assert len(entries) == 1 and entries[0]['status'] == 'BLUEPRINT_STATUS_FAILED', entries
        try:
            client.initialize('test', 'tampered', reference, {'architecture': 'arm64'})
        except urllib.error.HTTPError as error:
            failure = json.load(error)
            assert 'blueprint' in failure.get('message', '').lower(), failure
        else:
            raise RuntimeError('Corrupted cached source was accepted')
        assert set((data / 'test/instances').iterdir()) == before
        # Removal of a failed catalogue entry preserves existing Instance snapshots.
        client.rpc('DeleteBlueprint', {'reference': reference}, namespace='test')
        assert client.rpc('ListBlueprints', namespace='test').get('blueprints', []) == []
        sibling = client.rpc('GetBlueprint', {'reference': reference}, namespace='sibling')['blueprint']
        assert sibling['status'] == 'BLUEPRINT_STATUS_READY' and 'namespace' not in sibling, sibling
        assert len(client.rpc('ListInstances', namespace='test')['instances']) == 1
    print('Namespace cache isolation, offline reuse, failed catalogue entry and safe deletion passed', flush=True)


def container_checks(tmp, network, env):
    if not (bundle_output() / 'build.json').is_file():
        raise RuntimeError('Run task bundle before check-full container verification')
    verify_image()
    run([sys.executable, ROOT / 'build.py', 'push'], cwd=ROOT,
        env={**env, 'KIT_DIST_DIR': str(bundle_output().parent.parent)})
    image = 'docker.io/library/inframill-kit-test:' + uuid.uuid4().hex
    load_image(bundle_output(), image)
    try:
        python = '/opt/inframill/tools/ansible/' + runtime_settings()['ANSIBLE_VERSION'] + '/bin/python3'
        for arch in ['arm64', 'amd64']:
            image_check(tmp, image, arch, network)
            run(['docker', 'run', '--rm', '--pull=never', '--platform', 'linux/' + arch,
                 '--network', network,
                 '--mount', 'type=bind,src=' + str(ROOT / 'tests') + ',dst=/verify,readonly',
                 '--mount', 'type=bind,src=' + str(BLUEPRINTS) + ',dst=/blueprints,readonly',
                 '--entrypoint', python, image, '/verify/runtime_smoke.py'])
    finally:
        subprocess.run(['docker', 'image', 'rm', image], stdout=subprocess.DEVNULL, check=False)


def local_revision_check(tmp):
    source_root = tmp / 'sources'
    repository = source_root / 'hello-world'
    shutil.copytree(BLUEPRINTS / 'hello-world', repository)
    latest = repository / 'latest'
    shutil.copytree(latest, repository / '0.1.0')
    reference = 'build.in/hello-world'
    tagged = reference + ':0.1.0'
    with engine(tmp / 'revisions', debug_only=True, paths=[str(source_root)]) as client:
        client.create_namespace('test')
        first = client.fetch(reference, 'test')
        assert first['repository'] == reference and first['tags'] == ['latest'], first
        assert client.fetch(reference + ':latest', 'test')['digest'] == first['digest']
        shared = client.fetch(tagged, 'test')
        assert shared['digest'] == first['digest'] and shared['tags'] == ['0.1.0', 'latest'], shared
        values = {'greeting': 'Hello', 'name': 'world'}
        original = client.initialize('test', 'original', reference, values)
        template = latest / 'templates/debug/message.txt.gotmpl'
        template.write_text(template.read_text() + 'edited latest\n')
        second = client.fetch(reference, 'test')
        assert second['digest'] != first['digest'] and second['tags'] == ['latest'], second
        tagged_info = client.rpc('GetBlueprint', {'reference': tagged}, namespace='test')['blueprint']
        assert tagged_info['digest'] == first['digest'] and tagged_info['tags'] == ['0.1.0'], tagged_info
        # Fetching another tag must not change the bare repository's latest mapping.
        client.fetch(tagged, 'test')
        changed = client.initialize('test', 'changed', reference, values)
        versioned = client.initialize('test', 'versioned', tagged, values)
        pinned = client.initialize('test', 'pinned', reference + '@' + first['digest'], values)
        try:
            client.fetch(reference + ':missing', 'test')
        except urllib.error.HTTPError:
            pass
        else:
            raise AssertionError('Missing tag unexpectedly fell back to latest')
        assert client.rpc('GetBlueprint', {'reference': reference}, namespace='test')['blueprint']['digest'] == second['digest']
        instances = pathlib.Path(client.config['engine']['dataDir']) / 'test/instances'
        rendered = lambda iid: (instances / iid / 'workspace/debug/message.txt').read_text()
        assert rendered(original) == rendered(pinned) == rendered(versioned) == 'Hello, world!\n'
        assert rendered(changed) == 'Hello, world!\nedited latest\n'
    print('Local tags, latest mapping, changed revisions and immutable Instance snapshots passed', flush=True)


def native_checks(tmp, env):
    tools()
    local_revision_check(tmp)
    with engine(tmp / 'hello-world', env, debug_only=True) as client:
        client.create_namespace('test')
        hello_world_flow(client, 'build.in/hello-world')
    with engine(tmp / 'workflows', env) as client:
        query_check(client, LOCAL_REFERENCE, native_platform().split('/')[1])
        client.create_namespace('test')
        local_flow(client)
        pv_flow(client, 'build.in/pv-cloud', tools()[0])
    daemon_check(tmp, env)
    cache_check(tmp, env)


def registry_check():
    with tempfile.TemporaryDirectory(prefix='kit-check-') as td:
        tmp = pathlib.Path(td)
        native_checks(tmp, os.environ.copy())
        with registry() as (address, network, push_address):
            container_checks(tmp, network, registry_env(tmp, push_address))


def main():
    action = sys.argv[1]
    if action in ('registry', 'full'):
        registry_check()
    elif action == 'native':
        with tempfile.TemporaryDirectory(prefix='kit-native-') as td:
            native_checks(pathlib.Path(td), os.environ.copy())
    elif action == 'blueprint:check':
        blueprint_check(os.environ['BLUEPRINT'])
    else:
        raise RuntimeError('Unknown check')


if __name__ == '__main__':
    main()
