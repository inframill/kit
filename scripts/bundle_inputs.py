"""Read existing Inframill release binaries. Never build or download them."""
import hashlib
import json
import pathlib
import re
import struct


def read_driver(dist, selection, target, lock):
    """Accept only an existing, complete driver build; never compile a fallback."""
    name, version = selection['name'], selection['version']
    if not re.fullmatch(r'[a-z][a-z0-9-]*', name) or not re.fullmatch(r'[0-9A-Za-z.-]+', version) or version in ('.', '..'):
        raise ValueError('Invalid driver selection')
    folder = pathlib.Path(dist) / name / version / target
    path = folder / 'build.json'
    if not path.is_file():
        raise ValueError(f'Missing prebuilt driver {name}:{version} for {target}; run task build in drivers/')
    record = json.loads(path.read_text())
    release = record['release']
    if record['schema'] != 1 or (release['name'], release['version']) != (name, version) or record['platform'] != target:
        raise ValueError('Driver build identity mismatch')
    expected_engine = {k: lock['inframill'][k] for k in ('version', 'revision')}
    if record['inframill'] != expected_engine:
        raise ValueError('Driver and image target different Inframill releases')
    tool_version = {'opentofu': lock['opentofu']['version'], 'ansible': lock['ansible_core']}.get(name)
    if release['tool_version'] != tool_version or release['protocol'] != 1 or set(release['platforms']) != set(lock['platforms']):
        raise ValueError('Driver tool/platform/protocol differs from the image release')
    actual = set()
    for file in folder.rglob('*'):
        if file.is_symlink():
            raise ValueError('Driver output contains a link')
        if file.is_file() and file != path:
            actual.add(str(file.relative_to(folder)))
    if actual != set(record['files']) or 'driver' not in actual or not any(p.startswith('sources/') for p in actual):
        raise ValueError('Driver output is incomplete or contains unrecorded files')
    for name, digest in record['files'].items():
        relative = pathlib.PurePosixPath(name)
        if relative.is_absolute() or '..' in relative.parts or not re.fullmatch(r'[0-9a-f]{64}', digest):
            raise ValueError('Invalid driver output record')
        if hashlib.sha256((folder / name).read_bytes()).hexdigest() != digest:
            raise ValueError('Driver output checksum mismatch: ' + name)
    for name, digest in record['source_files'].items():
        if record['files'].get('sources/' + name) != digest:
            raise ValueError('Driver source snapshot differs from the build record')
    data = read_binary(folder, {'path': 'driver', 'sha256': record['files']['driver']}, target.split('/')[1])
    return folder, record, data


def read_binary(dist, entry, arch):
    relative = pathlib.PurePosixPath(entry['path'])
    if relative.is_absolute() or '..' in relative.parts or not relative.parts:
        raise ValueError('Inframill binary path must be relative to its dist directory')
    expected = entry['sha256']
    if not re.fullmatch(r'[0-9a-f]{64}', expected):
        raise ValueError('Inframill binary requires a pinned SHA-256 checksum')
    path = pathlib.Path(dist) / relative
    if not path.is_file():
        raise ValueError(f'Missing prebuilt Inframill binary: {path}; provide both Linux releases in the sibling dist directory')
    with path.open('rb') as stream:
        data = stream.read((128 << 20) + 1)
    if len(data) > 128 << 20:
        raise ValueError('Inframill binary exceeds size limit')
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError(f'Inframill binary checksum mismatch: {path}')
    # Linux Go executables use the System V ELF ABI; macOS binaries are rejected.
    if len(data) < 64 or data[:6] != b'\x7fELF\x02\x01' or data[7] not in (0, 3):
        raise ValueError(f'Expected a Linux 64-bit little-endian ELF binary: {path}')
    machine = struct.unpack_from('<H', data, 18)[0]
    if machine != {'amd64': 62, 'arm64': 183}[arch]:
        raise ValueError(f'Inframill binary architecture mismatch: expected {arch}: {path}')
    return data
