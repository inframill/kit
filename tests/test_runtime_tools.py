import hashlib
import io
import json
import pathlib
import tempfile
import unittest
import zipfile
from unittest.mock import patch

from build import ROOT, local_tools, runtime_settings
from runtime.tools import ansible_requirements, fetch_sources, install_opentofu


class RuntimeToolTests(unittest.TestCase):
    def test_native_setup_matches_provider_requirements(self):
        settings = runtime_settings()
        tofu, ansible = local_tools()
        for name, key, path in [('opentofu', 'OPENTOFU_VERSION', tofu), ('ansible', 'ANSIBLE_VERSION', ansible)]:
            release = json.loads((ROOT / 'providers/roots' / name / '0.3.0/release.json').read_text())
            self.assertEqual(settings[key], release['tool_version'])
            self.assertIn(settings[key], str(path))
        self.assertIn('ansible-core==' + settings['ANSIBLE_VERSION'], ansible_requirements(settings))

    def test_opentofu_checks_the_selected_platform_before_installing(self):
        archive = io.BytesIO()
        with zipfile.ZipFile(archive, 'w') as z:
            z.writestr('tofu', b'tool')
            z.writestr('LICENSE', b'license')
        data = archive.getvalue()
        settings = {'OPENTOFU_VERSION': '1.11.5', 'OPENTOFU_SHA256_LINUX_ARM64': hashlib.sha256(data).hexdigest()}
        with tempfile.TemporaryDirectory() as td:
            with patch('urllib.request.urlopen', return_value=io.BytesIO(data)) as fetch:
                install_opentofu('linux/arm64', settings, td)
                self.assertTrue(fetch.call_args.args[0].endswith('/tofu_1.11.5_linux_arm64.zip'))
                self.assertEqual((pathlib.Path(td) / 'tofu').read_bytes(), b'tool')
                self.assertEqual((pathlib.Path(td) / 'LICENSE').read_bytes(), b'license')
            settings['OPENTOFU_SHA256_LINUX_ARM64'] = '0' * 64
            with patch('urllib.request.urlopen', return_value=io.BytesIO(data)), self.assertRaisesRegex(ValueError, 'Checksum mismatch'):
                install_opentofu('linux/arm64', settings, pathlib.Path(td) / 'rejected')
            self.assertFalse((pathlib.Path(td) / 'rejected/tofu').exists())

    def test_sources_follow_installed_package_report(self):
        source = {'packagetype': 'sdist', 'url': 'https://example.invalid/source.tar.gz',
                  'filename': 'source.tar.gz', 'digests': {'sha256': 'a' * 64}}
        settings = {'OPENTOFU_VERSION': '1.11.5', 'OPENTOFU_SOURCE_SHA256': 'b' * 64}
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            report = root / 'report.json'
            report.write_text(json.dumps({'install': [{'metadata': {'name': 'ansible-core', 'version': '2.20.1'}}]}))
            with patch('urllib.request.urlopen', return_value=io.BytesIO(json.dumps({'urls': [source]}).encode())) as fetch, \
                 patch('runtime.tools.download') as download:
                fetch_sources(settings, report, root)
                self.assertEqual(fetch.call_args.args[0], 'https://pypi.org/pypi/ansible-core/2.20.1/json')
                download.assert_called_with(source['url'], 'a' * 64, root / 'source.tar.gz')
            records = json.loads((root / 'python-sources.json').read_text())
            self.assertEqual(records[0]['version'], '2.20.1')
            self.assertEqual(records[0]['sha256'], 'a' * 64)
