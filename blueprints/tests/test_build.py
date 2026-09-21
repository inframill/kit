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
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('blueprint_build', Path(__file__).resolve().parents[1] / 'build.py')
build = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build)
FIXTURES = Path(__file__).parent / 'fixtures'


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

    def test_build_from_detached_project_and_unrelated_working_directory(self):
        shutil.copy(build.ROOT / 'build.py', self.root)
        # The detached script still uses only its project files and the Docker CLI.
        docker_bin = self.root / 'bin'
        docker_bin.mkdir()
        docker = docker_bin / 'docker'
        docker.write_text('#!/bin/sh\n[ "$1" = load ] || exit 1\ncat >/dev/null\n')
        docker.chmod(0o755)
        result = subprocess.run([sys.executable, str(self.root / 'build.py'), 'build'],
                                cwd=self.root.parent, capture_output=True, text=True,
                                env={**os.environ, 'PATH': str(docker_bin) + os.pathsep + os.environ['PATH']})
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

    def test_publish_uses_only_docker_load_then_push_for_every_release(self):
        calls = []
        def docker(command, **kwargs):
            self.assertTrue(kwargs['check'])
            calls.append(command)
            if command == ['docker', 'load']:
                with tarfile.open(fileobj=kwargs['stdin'], mode='r:') as tar:
                    index = json.load(tar.extractfile('index.json'))
                    desc = index['manifests'][0]
                    self.assertNotIn('platform', desc)
                    reference = desc['annotations']['io.containerd.image.name']
                    version = reference.rsplit(':', 1)[1]
                    original, _, _ = build.verify(self.dist / 'local-resource' / version, version)
                    self.assertEqual(desc['digest'], original['digest'])
                    manifest = json.load(tar.extractfile('blobs/sha256/' + desc['digest'][7:]))
                    self.assertEqual(manifest['artifactType'], build.ARTIFACT)
            return subprocess.CompletedProcess(command, 0)
        with patch.object(build.subprocess, 'run', side_effect=docker):
            build.main(['publish', '--root', str(self.root), '--registry', 'localhost:5000'])
        self.assertEqual(calls, [['docker', 'load'], ['docker', 'load'],
            ['docker', 'push', 'localhost:5000/inframill/blueprints/local-resource:0.1.0'],
            ['docker', 'push', 'localhost:5000/inframill/blueprints/local-resource:0.2.0']])

    def test_push_only_invokes_docker_push_and_propagates_failure(self):
        for entry in self.entries:
            build.build(self.root, self.dist, entry)
        denied = subprocess.CalledProcessError(1, ['docker', 'push'])
        with patch.object(build.subprocess, 'run', side_effect=denied) as docker:
            with self.assertRaises(subprocess.CalledProcessError):
                build.main(['push', '--root', str(self.root)])
        docker.assert_called_once_with(['docker', 'push',
            'ghcr.io/inframill/blueprints/local-resource:0.1.0'], check=True)

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



if __name__ == '__main__':
    unittest.main()
