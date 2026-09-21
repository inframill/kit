import base64
import http.server
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import threading
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('blueprint_build', Path(__file__).resolve().parents[1] / 'build.py')
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)
FIXTURES = Path(__file__).parent / 'fixtures'


class RegistryHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, code, data=b'', **headers):
        self.send_response(code)
        for key, value in headers.items():
            self.send_header(key.replace('_', '-'), value)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        if self.command != 'HEAD':
            self.wfile.write(data)

    def authenticate(self):
        if self.path.startswith('/token?'):
            self.server.token_requests += 1
            self.reply(200, b'{"token":"local-test-token"}')
            return False
        if self.headers.get('Authorization') != 'Bearer local-test-token':
            self.reply(401, WWW_Authenticate='Bearer realm="http://127.0.0.1:%s/token",service="test"' % self.server.server_port)
            return False
        return True

    def do_GET(self):
        if not self.authenticate():
            return
        if self.path in self.server.objects:
            self.reply(200, self.server.objects[self.path])
        else:
            self.reply(404)

    do_HEAD = do_GET

    def do_POST(self):
        if self.authenticate():
            self.reply(202, Location=self.path + 'upload-id?state=test')

    def do_PUT(self):
        if not self.authenticate():
            return
        data = self.rfile.read(int(self.headers.get('Content-Length', '0')))
        if '/blobs/uploads/' in self.path:
            query = build.urllib.parse.parse_qs(build.urllib.parse.urlsplit(self.path).query)
            if query['digest'][0] != build.digest(data):
                self.reply(400)
                return
            path = self.path.split('/blobs/')[0] + '/blobs/' + query['digest'][0]
        else:
            path = self.path
        self.server.objects[path] = data
        self.reply(201)


class BuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'project'
        shutil.copytree(FIXTURES, self.root)
        self.dist = self.root / 'dist'
        self.entries = build.releases(self.root / 'releases.json')
        self.entry = self.entries[0]
        self.source = build.source_path(self.root, self.entry)

    def seal(self, entry=None):
        entry = entry or self.entry
        entry['source_sha256'] = build.digest(build.archive(build.source_path(self.root, entry))[0])
        return entry

    def registry(self):
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), RegistryHandler)
        server.objects = {}
        server.token_requests = 0
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return server, '127.0.0.1:' + str(server.server_port)

    def test_build_from_detached_project_and_unrelated_working_directory(self):
        shutil.copy(build.ROOT / 'build.py', self.root)
        result = subprocess.run([sys.executable, str(self.root / 'build.py'), 'build'],
                                cwd=self.root.parent, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(result.stdout.splitlines()), 2)
        for entry in self.entries:
            desc, _, manifest = build.verify(self.dist / entry['name'] / entry['version'], entry['version'])
            self.assertNotIn('platform', desc)
            self.assertEqual(manifest['config']['mediaType'], build.EMPTY)
            self.assertEqual(len(manifest['layers']), 1)

    def test_reproducible_snapshot_preserves_modes_and_ignores_mtime(self):
        first = build.build(self.root, self.dist, self.entry)
        for path in self.source.rglob('*'):
            os.utime(path, (987654321, 987654321))
        self.assertEqual(build.build(self.root, self.dist, self.entry), first)
        layer, _ = build.archive(self.source)
        with tarfile.open(fileobj=io.BytesIO(layer), mode='r:gz') as tar:
            for member in tar:
                self.assertEqual(member.mtime, 0)
                self.assertEqual(member.uid, 0)
                self.assertEqual(member.mode, 0o755 if member.isdir() else 0o644)
        source_file = self.source / 'template/opentofu-0-2-0/main.tf.gotmpl'
        source_file.chmod(0o755)
        self.assertNotEqual(build.digest(build.archive(self.source)[0]), self.entry['source_sha256'])

    def test_changed_source_and_immutable_outputs(self):
        build.build(self.root, self.dist, self.entry)
        (self.source / 'template/extra.txt').write_text('changed')
        with self.assertRaisesRegex(ValueError, 'source differs'):
            build.build(self.root, self.dist, self.entry)
        self.seal()
        with self.assertRaisesRegex(ValueError, 'different output already exists'):
            build.build(self.root, self.dist, self.entry)

    def test_corrupt_manifest_and_layer_rejected(self):
        build.build(self.root, self.dist, self.entry)
        output = self.dist / self.entry['name'] / self.entry['version']
        desc, _, manifest = build.verify(output, self.entry['version'])
        for child in [desc, manifest['layers'][0]]:
            path = output / 'oci/blobs/sha256' / child['digest'][7:]
            original = path.read_bytes()
            path.write_bytes(b'x' * len(original))
            with self.assertRaisesRegex(ValueError, 'checksum/size'):
                build.verify(output, self.entry['version'])
            path.write_bytes(original)

    def test_rejects_unsafe_sources(self):
        for name in ['unexpected.txt', 'template/private.pem', 'template/state.tfstate', 'template/.env']:
            path = self.source / name
            path.write_text('private')
            with self.subTest(name=name), self.assertRaises(ValueError):
                build.archive(self.source)
            path.unlink()
        link = self.source / 'template/link'
        link.symlink_to('/etc/passwd')
        with self.assertRaisesRegex(ValueError, 'links'):
            build.archive(self.source)
        link.unlink()
        (self.source / 'template/link').symlink_to(self.source, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, 'links'):
            build.archive(self.source)

    def test_definition_validation(self):
        _, entries = build.archive(self.source)
        original = json.loads(entries['blueprint.yaml'])
        build.validate_definition(entries, self.entry)
        mutations = [dict(original, unknown=True), dict(original, values_schema=None),
                     dict(original, values_schema={'type': 42}),
                     dict(original, values_schema={'$ref': 'https://example.invalid/schema'}),
                     dict(original, driver_settings={}), dict(original, description=42)]
        for definition in mutations:
            with self.subTest(definition=definition), self.assertRaises(ValueError):
                build.validate_definition({**entries, 'blueprint.yaml': json.dumps(definition).encode()}, self.entry)
        for invalid in [b'a: 1\na: 2', b'a: &x {}\nb: *x', b'a: .nan', b'---\na: 1\n---\nb: 2']:
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                build.validate_definition({**entries, 'blueprint.yaml': invalid}, self.entry)

    def test_publish_all_authentication_idempotence_and_tag_conflict(self):
        server, address = self.registry()
        with patch.object(build, 'docker_credentials', return_value=None):
            build.main(['publish', '--root', str(self.root), '--registry', address, '--plain-http'])
            originals = dict(server.objects)
            build.main(['push', '--root', str(self.root), '--registry', address, '--plain-http'])
            self.assertEqual(server.objects, originals)
            self.assertGreater(server.token_requests, 0)
            tag = '/v2/inframill/blueprints/local-resource/manifests/0.1.0'
            server.objects[tag] = b'conflicting manifest'
            with self.assertRaisesRegex(ValueError, 'release tag already points to different content'):
                build.push(self.dist, self.entry, address, 'inframill', True)
            self.assertEqual(server.objects[tag], b'conflicting manifest')
            self.assertFalse((self.dist / 'local-resource/0.1.0/.push-lock').exists())

    def test_publish_builds_everything_before_upload(self):
        self.entries[1]['source_sha256'] = 'sha256:' + '0' * 64
        (self.root / 'releases.json').write_text(json.dumps({'schema': 1, 'blueprints': self.entries}))
        with patch.object(build, 'push') as push:
            with self.assertRaisesRegex(ValueError, 'source differs'):
                build.main(['publish', '--root', str(self.root)])
            push.assert_not_called()

    def test_push_preflights_all_packages_before_any_upload(self):
        for entry in self.entries:
            build.build(self.root, self.dist, entry)
        self.entries[1]['inframill_version'] = '0.4.0'
        (self.root / 'releases.json').write_text(json.dumps({'schema': 1, 'blueprints': self.entries}))
        with patch.object(build, 'push') as push:
            with self.assertRaisesRegex(ValueError, 'metadata differs'):
                build.main(['push', '--root', str(self.root)])
            push.assert_not_called()

    def test_registry_rejects_nonlocal_http(self):
        with self.assertRaisesRegex(ValueError, 'local test registries'):
            build.Registry('ghcr.io', 'inframill/blueprints/example', True)

    def test_registry_blob_redirect_does_not_forward_credentials(self):
        remote = build.Registry('ghcr.io', 'inframill/blueprints/example')
        error = build.urllib.error.HTTPError('https://ghcr.io/v2/blob', 307, 'redirect',
                                            {'Location': 'https://blob.example/download'}, io.BytesIO())
        with patch.object(remote.opener, 'open', side_effect=error) as opened:
            headers, data = remote.request('HEAD', 'blobs/sha256:' + 'a' * 64)
            self.assertEqual(data, b'')
            self.assertEqual(opened.call_count, 1)

    def test_identity_token_uses_refresh_exchange(self):
        remote = build.Registry('ghcr.io', 'inframill/blueprints/example')
        with patch.object(build, 'docker_credentials', return_value=('<token>', 'identity-secret')), \
             patch.object(remote.opener, 'open', return_value=io.BytesIO(b'{"access_token":"access"}')) as opened:
            remote.authenticate('Bearer realm="https://ghcr.io/token",service="ghcr.io"')
            request = opened.call_args.args[0]
            body = build.urllib.parse.parse_qs(request.data.decode())
            self.assertEqual(body['grant_type'], ['refresh_token'])
            self.assertEqual(body['refresh_token'], ['identity-secret'])
            self.assertIsNone(request.get_header('Authorization'))
            self.assertEqual(remote.authorization, 'Bearer access')

    def test_docker_credentials_support_inline_and_helper(self):
        config = self.root / 'docker'; config.mkdir()
        with patch.dict(os.environ, {'DOCKER_CONFIG': str(config)}):
            (config / 'config.json').write_text(json.dumps({'auths': {'ghcr.io': {
                'auth': base64.b64encode(b'user:password').decode()}}}))
            self.assertEqual(build.docker_credentials('ghcr.io'), ('user', 'password'))
            (config / 'config.json').write_text(json.dumps({'credsStore': 'osxkeychain'}))
            with patch.object(build.subprocess, 'run', return_value=subprocess.CompletedProcess(
                    [], 0, '{"Username":"user","Secret":"password"}', '')) as helper:
                self.assertEqual(build.docker_credentials('ghcr.io'), ('user', 'password'))
                self.assertEqual(helper.call_args.args[0], ['docker-credential-osxkeychain', 'get'])


if __name__ == '__main__':
    unittest.main()
