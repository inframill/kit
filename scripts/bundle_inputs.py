"""Read existing Inframill release binaries. Never build or download them."""
import hashlib
import pathlib
import re
import struct


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
