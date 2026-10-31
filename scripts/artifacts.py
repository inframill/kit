"""Shared release artifact checks for executable builds and image assembly."""
import hashlib
import platform
import re
import struct
from pathlib import Path

LINUX = ['linux/amd64', 'linux/arm64']
PLATFORMS = LINUX + ['darwin/amd64', 'darwin/arm64']


def digest(data):
    return hashlib.sha256(data).hexdigest()


def valid_version(value, metadata=False):
    pattern = r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?'
    if metadata:
        pattern += r'(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?'
    if not isinstance(value, str) or not re.fullmatch(pattern, value):
        return False
    pre = value.split('+', 1)[0].partition('-')[2]
    return all(not (part.isdigit() and len(part) > 1 and part.startswith('0')) for part in pre.split('.'))


def native_platform():
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64', 'amd64': 'amd64'}.get(platform.machine().lower())
    target = platform.system().lower() + '/' + str(arch)
    if target not in PLATFORMS:
        raise ValueError('Unsupported native platform: ' + target)
    return target


def verify_binary(data, target):
    system, arch = target.split('/')
    if system == 'linux':
        if len(data) < 64 or data[:6] != b'\x7fELF\x02\x01' or data[7] not in (0, 3):
            raise ValueError('Expected a Linux 64-bit little-endian ELF binary')
        machine = struct.unpack_from('<H', data, 18)[0]
        expected = {'amd64': 62, 'arm64': 183}[arch]
    elif system == 'darwin':
        if len(data) < 32 or data[:4] != b'\xcf\xfa\xed\xfe':
            raise ValueError('Expected a Darwin Mach-O binary')
        machine = struct.unpack_from('<I', data, 4)[0]
        expected = {'amd64': 0x1000007, 'arm64': 0x100000c}[arch]
    else:
        raise ValueError('Unsupported binary platform: ' + target)
    if machine != expected:
        raise ValueError('Binary architecture mismatch: ' + target)


def verify_files(folder, record):
    actual = set()
    for path in folder.rglob('*'):
        if path.is_symlink():
            raise ValueError('Executable output contains a link')
        if path.is_file() and path != folder / 'build.json':
            actual.add(str(path.relative_to(folder)))
    if actual != set(record['files']) or 'exec' not in actual or not record['source_files']:
        raise ValueError('Executable output is incomplete or contains unrecorded files')
    for name, checksum in record['files'].items():
        path = Path(name)
        if path.is_absolute() or '..' in path.parts or not re.fullmatch(r'[0-9a-f]{64}', checksum):
            raise ValueError('Invalid executable output record')
        if digest((folder / path).read_bytes()) != checksum:
            raise ValueError('Executable output checksum mismatch: ' + name)
    for name, checksum in record['source_files'].items():
        if record['files'].get('sources/' + name) != checksum:
            raise ValueError('Executable source snapshot differs from the build record')
    verify_binary((folder / 'exec').read_bytes(), record['platform'])
