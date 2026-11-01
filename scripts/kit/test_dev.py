"""Import only Runmill, verified sibling binaries, pins and dependency notices."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from scripts.core.context import Context
from scripts.kit import dev, engine


class DevSyncTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        self.source = root / 'inframill'
        self.kit = root / 'kit'
        self.upstream = self.source / 'pkg/runmill'
        self.local = self.kit / 'pkg/runmill'
        self.upstream.mkdir(parents=True)
        self.local.mkdir(parents=True)
        (self.source / 'go.mod').write_text('module ' + engine.MODULE + '\n')
        (self.source / 'toolai.json').write_text('{"version":"1.2.3"}\n')
        (self.source / '.gitignore').write_text('/bin/\n')
        (self.source / 'private-engine.go').write_text('engine implementation excluded from Kit\n')
        (self.upstream / 'runmill.go').write_text('package runmill\n')
        (self.upstream / 'signals_linux.go').write_text('package runmill\n// linux\n')
        (self.upstream / 'example_test.go').write_text('import "' + engine.MODULE + '/pkg/runmill"\n')
        (self.local / 'runmill.go').write_text('package runmill\n// previous\n')
        (self.local / 'removed.go').write_text('package runmill\n// obsolete\n')
        (self.local / 'README.md').write_text('Kit documentation\n')
        (self.kit / 'go.mod').write_text('module github.com/inframill/kit\n')
        (self.kit / 'runtime').mkdir()
        (self.kit / 'runtime/dependencies.json').write_text('{"OPENTOFU_VERSION":"1.11.5"}\n')
        self.git('init', '--quiet')
        self.git('add', '.')
        self.git('-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                 '-c', 'commit.gpgsign=false', 'commit', '--quiet', '-m', 'fixture')
        (self.source / 'bin').mkdir()
        for platform in engine.PLATFORMS:
            (self.source / 'bin' / engine.binary_name(platform)).write_text('fixture binary for ' + platform)
        self.context = Context(self.kit)
        self.start_patch(engine, 'native_platform', return_value='linux/amd64')
        self.start_patch(engine, 'verify_engine_binary', side_effect=lambda path, *args, **kwargs: hashlib.sha256(path.read_bytes()).hexdigest())
        self.start_patch(engine, 'collect_notices', side_effect=lambda source, target, revision, **kwargs: target.mkdir())
        original_output = Context.output
        def output(context, args, **kwargs):
            if args[-1] == '--version':
                return '1.2.3\n'
            return original_output(context, args, **kwargs)
        self.start_patch(Context, 'output', autospec=True, side_effect=output)

    def start_patch(self, target, name, *args, **kwargs):
        patched = patch.object(target, name, *args, **kwargs)
        patched.start()
        self.addCleanup(patched.stop)

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.source, text=True).strip()

    def snapshot(self):
        return {path.relative_to(self.kit).as_posix(): path.read_bytes()
                for path in self.kit.rglob('*') if path.is_file()}

    def test_sync_imports_only_runmill_binaries_and_metadata_and_is_repeatable(self):
        before_mod = (self.kit / 'go.mod').read_bytes()
        dev.sync(self.source, context=self.context)
        self.assertFalse((self.local / 'removed.go').exists())
        self.assertEqual((self.local / 'README.md').read_text(), 'Kit documentation\n')
        self.assertIn(engine.LOCAL_RUNMILL, (self.local / 'example_test.go').read_text())
        self.assertTrue((self.local / 'signals_linux.go').exists())
        settings = json.loads((self.kit / 'runtime/dependencies.json').read_text())
        pin = settings['INFRAMILL']
        self.assertEqual(pin['revision'], self.git('rev-parse', 'HEAD'))
        self.assertEqual(settings['OPENTOFU_VERSION'], '1.11.5')
        self.assertEqual(pin['runmill_sha256'], engine.runmill_digest(self.local))
        self.assertEqual(set(pin['binaries']), set(engine.PLATFORMS))
        for platform in engine.PLATFORMS:
            binary = self.kit / 'bin' / engine.binary_name(platform)
            self.assertEqual(binary.read_bytes(), (self.source / 'bin' / binary.name).read_bytes())
        self.assertEqual((self.kit / 'bin/inframill').read_bytes(), (self.kit / 'bin/inframill-linux-amd64').read_bytes())
        self.assertEqual((self.kit / 'go.mod').read_bytes(), before_mod)
        self.assertFalse(any(path.name in ('private-engine.go', 'source.tar') for path in self.kit.rglob('*')))
        before = self.snapshot()
        dev.sync(self.source, context=self.context)
        self.assertEqual(self.snapshot(), before)

    def test_dirty_sibling_and_missing_outputs_leave_kit_unchanged(self):
        for condition in ('dirty', 'missing'):
            before = self.snapshot()
            if condition == 'dirty':
                (self.upstream / 'runmill.go').write_text('uncommitted\n')
            else:
                (self.source / 'bin/inframill-linux-arm64').unlink()
            with self.subTest(condition=condition), self.assertRaises(ValueError):
                dev.sync(self.source, context=self.context)
            self.assertEqual(self.snapshot(), before)
            (self.upstream / 'runmill.go').write_text('package runmill\n')

    def test_failed_install_restores_previous_sources_pins_and_binaries(self):
        dev.sync(self.source, context=self.context)
        before = self.snapshot()
        original = Path.rename
        def fail_metadata(path, target):
            if path.name == 'dependencies.json':
                raise OSError('cannot install pin')
            return original(path, target)
        with patch.object(Path, 'rename', fail_metadata), self.assertRaises(OSError):
            dev.sync(self.source, context=self.context)
        self.assertEqual(self.snapshot(), before)

    def test_pinned_copy_rejects_changed_sibling_binaries_without_replacing_outputs(self):
        dev.sync(self.source, context=self.context)
        context = Context(self.kit, env={**self.context.env, 'INFRAMILL_DIR': str(self.source)})
        before = self.snapshot()
        engine.copy(context=context)
        self.assertEqual(self.snapshot(), before)
        (self.source / 'bin/inframill-linux-arm64').write_text('changed sibling binary')
        with self.assertRaisesRegex(ValueError, 'differ from runtime pin'):
            engine.copy(context=context)
        self.assertEqual(self.snapshot(), before)
        self.assertEqual([path.name for path in (self.kit / '.build').iterdir()], ['engine'])
