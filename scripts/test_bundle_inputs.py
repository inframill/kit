import hashlib
import pathlib
import struct
import tempfile
import unittest

from bundle_inputs import read_binary


class BinaryInputTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.dist = pathlib.Path(self.tmp.name)

    def binary(self, machine=62, magic=b'\x7fELF\x02\x01'):
        data = bytearray(64)
        data[:6] = magic
        struct.pack_into('<H', data, 18, machine)
        path = self.dist / 'release/inframill'
        path.parent.mkdir(exist_ok=True)
        path.write_bytes(data)
        return {'path': 'release/inframill', 'sha256': hashlib.sha256(data).hexdigest()}, bytes(data)

    def test_accepts_exact_bytes_for_both_platforms(self):
        for arch, machine in [('amd64', 62), ('arm64', 183)]:
            entry, data = self.binary(machine)
            self.assertEqual(read_binary(self.dist, entry, arch), data)

    def test_rejects_changed_bytes(self):
        entry, _ = self.binary()
        (self.dist / entry['path']).write_bytes(b'changed')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            read_binary(self.dist, entry, 'amd64')

    def test_rejects_wrong_architecture(self):
        entry, _ = self.binary()
        with self.assertRaisesRegex(ValueError, 'architecture mismatch'):
            read_binary(self.dist, entry, 'arm64')

    def test_rejects_non_linux_binary(self):
        entry, _ = self.binary(magic=b'\xcf\xfa\xed\xfe\x00\x00')
        with self.assertRaisesRegex(ValueError, 'ELF binary'):
            read_binary(self.dist, entry, 'amd64')

    def test_missing_binary_is_an_error(self):
        with self.assertRaisesRegex(ValueError, 'Missing prebuilt'):
            read_binary(self.dist, {'path': 'missing', 'sha256': '0' * 64}, 'amd64')

    def test_paths_stay_relative_to_dist(self):
        for path in ['../inframill', '/tmp/inframill']:
            with self.assertRaisesRegex(ValueError, 'relative'):
                read_binary(self.dist, {'path': path, 'sha256': '0' * 64}, 'amd64')


if __name__ == '__main__':
    unittest.main()
