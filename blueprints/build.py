#!/usr/bin/env python3
"""Build and publish portable Blueprint OCI artifacts, independently of Kit."""
import argparse
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent
MANIFEST = 'application/vnd.oci.image.manifest.v1+json'
ARTIFACT = 'application/vnd.inframill.blueprint.v1'
LAYER = 'application/vnd.inframill.blueprint.layer.v1.tar+gzip'
EMPTY = 'application/vnd.oci.empty.v1+json'
NAME = r'[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*'
PRERELEASE = r'(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
VERSION = r'(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-' + PRERELEASE + r'(?:\.' + PRERELEASE + r')*)?'
DIGEST = r'sha256:[0-9a-f]{64}'


def require(condition, message):
    if not condition:
        raise ValueError(message)


def encode(value):
    return json.dumps(value, separators=(',', ':'), ensure_ascii=False, allow_nan=False).encode()


def digest(data):
    return 'sha256:' + hashlib.sha256(data).hexdigest()


def descriptor(media, data):
    return {'mediaType': media, 'digest': digest(data), 'size': len(data)}


def read_json(path):
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, 'duplicate JSON key: ' + key)
            result[key] = value
        return result
    return json.loads(Path(path).read_text(), object_pairs_hook=pairs)


def releases(path):
    config = read_json(path)
    require(config.get('schema') == 1, 'unsupported releases.json schema')
    entries = config.get('blueprints')
    require(isinstance(entries, list) and entries, 'release list is empty')
    seen = set()
    for entry in entries:
        name, version = entry['name'], entry['version']
        require(re.fullmatch(NAME, name) and re.fullmatch(VERSION, version), 'invalid name or exact version')
        require((name, version) not in seen, 'duplicate blueprint release')
        seen.add((name, version))
        require(entry.get('protocol') == 1, 'unsupported blueprint protocol')
        require(sorted(entry['platforms']) == ['linux/amd64', 'linux/arm64'] and
                sorted(entry['architectures']) == ['amd64', 'arm64'], 'blueprint must support AMD64 and ARM64')
        require(re.fullmatch(VERSION, entry['inframill_version']), 'invalid engine version')
        require(re.fullmatch(DIGEST, entry['source_sha256']), 'missing sealed source digest')
        drivers = entry['drivers']
        require(drivers and len({d['id'] for d in drivers}) == len(drivers), 'invalid or duplicate drivers')
        for driver in drivers:
            require(re.fullmatch(NAME, driver['id']) and driver['id'] != 'builtin' and
                    re.fullmatch(NAME, driver['name']) and re.fullmatch(VERSION, driver['version']),
                    'invalid driver requirement')
    return entries


def source_path(root, entry):
    path = root / entry['name'] / 'versions' / entry['version']
    require(not any(p.is_symlink() for p in [root, path.parent.parent, path.parent, path]),
            'source directories cannot be symlinks')
    return path


def archive(source):
    """Snapshot source once; normalized tar headers and gzip time ignore host metadata."""
    require(source.is_dir() and not source.is_symlink(), 'missing source directory')
    output = io.BytesIO()
    entries = {}
    expanded = 0
    with tarfile.open(fileobj=output, mode='w', format=tarfile.PAX_FORMAT) as tar:
        for path in sorted(source.rglob('*')):
            name = path.relative_to(source).as_posix()
            require(name in ('blueprint.yaml', 'template') or name.startswith('template/'),
                    'blueprint contains files outside its source contract: ' + name)
            require('\\' not in name and '\x00' not in name, 'invalid source path')
            for part in path.relative_to(source).parts:
                require(part not in ('.terraform', '.inframill', 'runtime', '.git') and
                        not part.startswith(('.env', '.kit-')) and
                        not part.endswith(('.tfstate', '.tfplan', '.tfvars', '.tfvars.json', '.pem', '.key')),
                        'blueprint contains runtime or credential files: ' + name)
            mode = path.lstat().st_mode
            directory = stat.S_ISDIR(mode)
            require(directory or stat.S_ISREG(mode), 'links and special files are forbidden: ' + name)
            require(len(entries) < 10000, 'too many source entries')
            size = 0 if directory else path.stat().st_size
            require(size <= 64 << 20, 'source file exceeds size limit')
            expanded += 512 + ((size + 511) // 512) * 512
            require(expanded <= 250 << 20, 'source exceeds extracted limit')
            data = b'' if directory else path.read_bytes()
            require(len(data) == size, 'source changed during snapshot')
            header = tarfile.TarInfo(name)
            header.type = tarfile.DIRTYPE if directory else tarfile.REGTYPE
            header.mode = 0o755 if directory or mode & 0o111 else 0o644
            header.size = len(data)
            tar.addfile(header, None if directory else io.BytesIO(data))
            entries[name] = None if directory else data
    require(isinstance(entries.get('blueprint.yaml'), bytes) and 'template' in entries and
            entries['template'] is None, 'blueprint.yaml and template directory are required')
    compressed = io.BytesIO()
    with gzip.GzipFile(fileobj=compressed, mode='wb', filename='', mtime=0, compresslevel=9) as stream:
        stream.write(output.getvalue())
    layer = compressed.getvalue()
    require(len(layer) <= 64 << 20, 'compressed source exceeds consumer limit')
    return layer, entries


def validate_definition(entries, release):
    try:
        import yaml
        from jsonschema import Draft202012Validator, validators
        from jsonschema.exceptions import SchemaError
    except ImportError as error:
        raise ValueError('Install build dependencies: python3 -m pip install -r requirements.txt') from error
    data = entries['blueprint.yaml']
    require(len(data) <= 1 << 20, 'blueprint.yaml exceeds 1 MiB')
    try:
        require(not any(isinstance(event, yaml.AliasEvent) for event in yaml.parse(data)), 'YAML aliases are forbidden')
        node = yaml.compose(data)
    except yaml.YAMLError as error:
        raise ValueError('invalid blueprint.yaml: ' + str(error)) from error

    def check(node):
        if isinstance(node, yaml.MappingNode):
            require(node.tag == 'tag:yaml.org,2002:map', 'unsupported YAML tag')
            keys = set()
            for key, value in node.value:
                require(key.tag == 'tag:yaml.org,2002:str' and key.value not in keys, 'non-string or duplicate YAML key')
                keys.add(key.value)
                check(value)
        elif isinstance(node, yaml.SequenceNode):
            require(node.tag == 'tag:yaml.org,2002:seq', 'unsupported YAML tag')
            for value in node.value:
                check(value)
        else:
            require(node.tag in ['tag:yaml.org,2002:' + t for t in ('str', 'null', 'bool', 'int', 'float')],
                    'unsupported YAML scalar')
    require(node is not None, 'empty blueprint definition')
    check(node)
    definition = yaml.safe_load(data)
    encode(definition)  # Reject non-JSON scalar values, including NaN/Infinity.
    require(isinstance(definition, dict) and set(definition) ==
            {'api_version', 'description', 'values_schema', 'driver_settings'}, 'invalid blueprint fields')
    require(definition['api_version'] == 'inframill.dev/blueprint/v1', 'unsupported api_version')
    require(isinstance(definition['description'], str) and definition['description'].strip(), 'description is required')
    schema = definition['values_schema']
    require(isinstance(schema, dict), 'values_schema must be an object')
    validator = validators.validator_for(schema, default=Draft202012Validator)
    try:
        validator.check_schema(schema)
    except SchemaError as error:
        raise ValueError('invalid values_schema: ' + str(error)) from error

    def offline(value):
        if isinstance(value, dict):
            for key, child in value.items():
                if key in ('$ref', '$dynamicRef', '$recursiveRef'):
                    require(isinstance(child, str) and child.startswith('#'), 'external schema resources are forbidden')
                offline(child)
        elif isinstance(value, list):
            for child in value:
                offline(child)
    offline(schema)
    settings = definition['driver_settings']
    require(isinstance(settings, dict) and set(settings) == {d['id'] for d in release['drivers']},
            'blueprint settings and release drivers differ')
    for driver in release['drivers']:
        config = settings[driver['id']]
        require(isinstance(config, dict) and config.get('id') == driver['id'] and
                config.get('required_driver_version') == driver['version'], 'driver requirements disagree')
        prefix = 'template/' + driver['id']
        require(prefix in entries and entries[prefix] is None, 'missing driver template directory')
        if 'settings_file' in config:
            filename = config['settings_file']
            require(isinstance(filename, str) and filename not in ('', '.', '..') and
                    '/' not in filename and '\\' not in filename, 'invalid settings_file')
            require(any(isinstance(entries.get(prefix + '/' + filename + suffix), bytes)
                        for suffix in ('', '.gotmpl')), 'missing driver settings template')


def blob(layout, desc):
    require(re.fullmatch(DIGEST, desc['digest']), 'invalid blob digest')
    data = (layout / 'blobs/sha256' / desc['digest'][7:]).read_bytes()
    require(len(data) == desc['size'] and digest(data) == desc['digest'], 'blob checksum/size mismatch')
    return data


def verify(output, version, release=None):
    layout = output / 'oci'
    require(read_json(layout / 'oci-layout') == {'imageLayoutVersion': '1.0.0'}, 'invalid OCI layout')
    index = read_json(layout / 'index.json')
    require(len(index['manifests']) == 1, 'expected one architecture-independent manifest')
    desc = index['manifests'][0]
    require(desc.get('annotations', {}).get('org.opencontainers.image.ref.name') == version, 'built version unavailable')
    record = read_json(output / 'build.json')
    if release is not None:
        require(record['release'] == release, 'release metadata differs from build record; use a new version or explicitly remove development output')
    require(all(desc[k] == record['descriptor'][k] for k in ('digest', 'size', 'mediaType')), 'OCI root differs from build record')
    manifest = blob(layout, desc)
    m = json.loads(manifest)
    require(m['schemaVersion'] == 2 and m['mediaType'] == MANIFEST and m['artifactType'] == ARTIFACT and
            len(m['layers']) == 1 and m['layers'][0]['mediaType'] == LAYER and
            m['config']['mediaType'] == EMPTY and 'platform' not in desc, 'unsupported blueprint manifest')
    require(blob(layout, m['config']) == b'{}', 'blueprint config must be empty')
    blob(layout, m['layers'][0])
    return desc, manifest, m


def build(root, dist, entry):
    layer, entries = archive(source_path(root, entry))
    require(digest(layer) == entry['source_sha256'], 'source differs from sealed release; run source-digest and update releases.json')
    validate_definition(entries, entry)
    config = b'{}'
    manifest = encode({'schemaVersion': 2, 'mediaType': MANIFEST, 'artifactType': ARTIFACT,
                       'config': descriptor(EMPTY, config), 'layers': [descriptor(LAYER, layer)]})
    desc = descriptor(MANIFEST, manifest)
    output = dist / entry['name'] / entry['version']
    if output.exists():
        existing, _, _ = verify(output, entry['version'])
        require(existing['digest'] == desc['digest'], 'different output already exists; use a new version or explicitly remove development output')
        verify(output, entry['version'], entry)
        return desc
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.build-', dir=output.parent) as td:
        staging = Path(td) / 'package'
        layout = staging / 'oci'
        (layout / 'blobs/sha256').mkdir(parents=True)
        for data in (config, layer, manifest):
            (layout / 'blobs/sha256' / digest(data)[7:]).write_bytes(data)
        tagged = {**desc, 'annotations': {'org.opencontainers.image.ref.name': entry['version']}}
        (layout / 'index.json').write_bytes(encode({'schemaVersion': 2, 'manifests': [tagged]}))
        (layout / 'oci-layout').write_bytes(encode({'imageLayoutVersion': '1.0.0'}))
        (staging / 'build.json').write_bytes(encode({'release': entry, 'descriptor': desc}))
        staging.rename(output)
    return desc


def docker_credentials(registry):
    path = Path(os.environ.get('DOCKER_CONFIG', str(Path.home() / '.docker'))) / 'config.json'
    if not path.exists():
        return None
    config = read_json(path)
    helper = config.get('credHelpers', {}).get(registry, config.get('credsStore'))
    if helper:
        result = subprocess.run(['docker-credential-' + helper, 'get'], input=registry + '\n',
                                text=True, capture_output=True)
        if result.returncode:
            # Anonymous registries need no stored credential.
            require('credentials not found' in (result.stdout + result.stderr).lower(), 'Docker credential helper failed')
            return None
        credential = json.loads(result.stdout)
        return credential['Username'], credential['Secret']
    auths = config.get('auths', {})
    for key in (registry, 'https://' + registry, 'https://' + registry + '/v1/'):
        if auths.get(key, {}).get('identitytoken'):
            return '<token>', auths[key]['identitytoken']
        if auths.get(key, {}).get('auth'):
            return tuple(base64.b64decode(auths[key]['auth']).decode().split(':', 1))
    return None


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, url):
        # Never forward registry credentials to a different endpoint implicitly.
        return None


class Registry:
    def __init__(self, registry, repository, plain=False):
        parsed = urllib.parse.urlsplit('https://' + registry)
        require(parsed.netloc == registry and not parsed.path and not parsed.username and not parsed.password,
                'registry must be a host[:port], without a scheme or path')
        require(not plain or parsed.hostname in ('localhost', '127.0.0.1', 'registry'), 'plain HTTP is only allowed for local test registries')
        require(all(re.fullmatch(r'[a-z0-9]+(?:[._-][a-z0-9]+)*', p) for p in repository.split('/')), 'invalid repository')
        self.origin = ('http://' if plain else 'https://') + registry
        self.base = self.origin + '/v2/' + repository + '/'
        self.registry = registry
        self.repository = repository
        self.authorization = None
        self.opener = urllib.request.build_opener(NoRedirect())

    def request(self, method, path, data=None, media=None, missing=False):
        url = urllib.parse.urljoin(self.base, path)
        require(urllib.parse.urlsplit(url)[:2] == urllib.parse.urlsplit(self.origin)[:2], 'cross-origin registry upload refused')
        for attempt in range(2):
            headers = {'Accept': MANIFEST}
            if media:
                headers['Content-Type'] = media
            if self.authorization:
                headers['Authorization'] = self.authorization
            try:
                with self.opener.open(urllib.request.Request(url, data=data, headers=headers, method=method), timeout=60) as response:
                    return response.headers, response.read()
            except urllib.error.HTTPError as error:
                if method == 'HEAD' and error.code in (302, 307, 308):
                    # Blob storage redirects mean the registry already has this digest.
                    headers = error.headers
                    error.close()
                    return headers, b''
                if error.code == 404 and missing:
                    error.close()
                    return None
                if error.code != 401 or attempt:
                    raise
                challenge = error.headers.get('WWW-Authenticate', '')
                error.close()
                self.authenticate(challenge)
        raise ValueError('registry authentication failed')

    def authenticate(self, challenge):
        credentials = docker_credentials(self.registry)
        basic = ('Basic ' + base64.b64encode((':'.join(credentials)).encode()).decode()) if credentials else None
        if challenge.lower().startswith('basic '):
            require(basic, 'registry requires authentication; run docker login ' + self.registry)
            self.authorization = basic
            return
        require(challenge.lower().startswith('bearer '), 'unsupported registry authentication')
        params = dict(re.findall(r'(\w+)="([^\"]*)"', challenge))
        realm = params['realm']
        parsed = urllib.parse.urlsplit(realm)
        require(parsed.scheme == 'https' or
                (self.origin.startswith('http://') and parsed.scheme == 'http' and
                 parsed.netloc == urllib.parse.urlsplit(self.origin).netloc), 'insecure token endpoint')
        query = urllib.parse.parse_qsl(parsed.query) + [('service', params.get('service', '')),
                ('scope', 'repository:' + self.repository + ':pull,push')]
        headers = {'Authorization': basic} if basic else {}
        body = None
        if credentials and credentials[0] == '<token>':
            headers = {'Content-Type': 'application/x-www-form-urlencoded'}
            body = urllib.parse.urlencode(query + [('grant_type', 'refresh_token'),
                ('refresh_token', credentials[1]), ('client_id', 'blueprint-builder')]).encode()
            url = realm
        else:
            url = urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))
        with self.opener.open(urllib.request.Request(url, data=body, headers=headers), timeout=60) as response:
            token = json.load(response)
        value = token.get('token') or token.get('access_token')
        require(value, 'registry returned no token')
        self.authorization = 'Bearer ' + value

    def manifest_digest(self, reference):
        response = self.request('GET', 'manifests/' + reference, missing=True)
        return digest(response[1]) if response else None


def push(dist, entry, registry, namespace, plain):
    output = dist / entry['name'] / entry['version']
    # Serialize publishers sharing this local package. Registry tags still need release coordination across hosts.
    lock = output / '.push-lock'
    with lock.open('x'):
        try:
            desc, manifest, document = verify(output, entry['version'], entry)
            repository = namespace + '/blueprints/' + entry['name']
            remote = Registry(registry, repository, plain)
            prior = remote.manifest_digest(entry['version'])
            require(prior is None or prior == desc['digest'], 'release tag already points to different content')
            if prior is None:
                for child in [document['config'], *document['layers']]:
                    if remote.request('HEAD', 'blobs/' + child['digest'], missing=True) is None:
                        headers, _ = remote.request('POST', 'blobs/uploads/', data=b'')
                        location = urllib.parse.urljoin(remote.base, headers['Location'])
                        location += ('&' if '?' in location else '?') + urllib.parse.urlencode({'digest': child['digest']})
                        remote.request('PUT', location, blob(output / 'oci', child), 'application/octet-stream')
                remote.request('PUT', 'manifests/' + desc['digest'], manifest, MANIFEST)
                prior = remote.manifest_digest(entry['version'])
                require(prior is None or prior == desc['digest'], 'concurrent conflicting publication')
                remote.request('PUT', 'manifests/' + entry['version'], manifest, MANIFEST)
            require(remote.manifest_digest(entry['version']) == desc['digest'], 'published digest mismatch')
            return registry + '/' + repository + '@' + desc['digest']
        finally:
            lock.unlink()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['build', 'push', 'publish', 'list', 'source-digest'])
    parser.add_argument('--root', type=Path, default=ROOT, help='standalone blueprint project directory')
    parser.add_argument('--config', type=Path, help='release manifest (default: ROOT/releases.json)')
    parser.add_argument('--dist', type=Path, help='output directory (default: ROOT/dist)')
    parser.add_argument('--name', help='select one blueprint (default: all releases)')
    parser.add_argument('--version', help='select one version (requires --name)')
    parser.add_argument('--registry', default=os.environ.get('REGISTRY', 'ghcr.io'))
    parser.add_argument('--namespace', default=os.environ.get('NAMESPACE', 'inframill'))
    parser.add_argument('--plain-http', action='store_true', default=os.environ.get('PLAIN_HTTP') == 'true')
    args = parser.parse_args(argv)
    root = args.root.resolve()
    dist = (args.dist or root / 'dist').resolve()
    require(not args.version or args.name, '--version requires --name')
    if args.command == 'source-digest':
        require(args.name and args.version and re.fullmatch(NAME, args.name) and re.fullmatch(VERSION, args.version),
                'source-digest requires --name and --version')
        print(digest(archive(source_path(root, {'name': args.name, 'version': args.version}))[0]))
        return
    entries = [entry for entry in releases(args.config or root / 'releases.json')
               if (not args.name or entry['name'] == args.name) and (not args.version or entry['version'] == args.version)]
    require(entries, 'no matching blueprint releases')
    if args.command == 'list':
        print(json.dumps(entries, indent=2))
        return
    # Complete all builds before publishing anything in the combined command.
    if args.command in ('build', 'publish'):
        for entry in entries:
            result = build(root, dist, entry)
            print(entry['name'] + ':' + entry['version'] + ' ' + result['digest'], flush=True)
    if args.command in ('push', 'publish'):
        for entry in entries:
            verify(dist / entry['name'] / entry['version'], entry['version'], entry)
        for entry in entries:
            print(push(dist, entry, args.registry, args.namespace, args.plain_http), flush=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print('error: ' + str(error), file=sys.stderr)
        sys.exit(1)
