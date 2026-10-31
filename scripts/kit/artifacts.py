"""Reject swapped or unsupported native binary formats before packaging."""
import struct

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

