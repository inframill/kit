import subprocess
from contextlib import redirect_stdout
import io
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

from scripts.core.context import Context
from scripts.kit import runtime
from scripts.kit.runtime import find_python
from ._test_fixtures import project


class PythonRuntimeTests(unittest.TestCase):
    def test_system_python_39_falls_back_to_supported_interpreter(self):
        with patch('scripts.kit.runtime.sys.executable', '/usr/bin/python3'), \
             patch('scripts.kit.runtime.shutil.which', side_effect=lambda name, **kwargs: {'/usr/bin/python3': name, 'python3.14': '/opt/homebrew/bin/python3.14'}.get(name)), \
             patch.object(Context, 'output', side_effect=['3.9.6\n', '3.14.7\n']):
            self.assertEqual(find_python(), ('/opt/homebrew/bin/python3.14', '3.14.7'))

    def test_supported_override_is_used(self):
        with patch('scripts.kit.runtime.shutil.which', return_value='/custom/python'), \
             patch.object(Context, 'output', return_value='3.13.1\n'):
            self.assertEqual(find_python('/custom/python'), ('/custom/python', '3.13.1'))

    def test_invalid_override_does_not_silently_fall_back(self):
        for version in ['3.9.6\n', '3.15.0\n', 'invalid\n']:
            with self.subTest(version=version), \
                 patch('scripts.kit.runtime.shutil.which', return_value='/custom/python') as lookup, \
                 patch.object(Context, 'output', return_value=version):
                with self.assertRaisesRegex(ValueError, 'PYTHON=/custom/python'):
                    find_python('/custom/python')
                self.assertEqual(lookup.call_args.args, ('/custom/python',))

    def test_missing_or_broken_interpreters_give_actionable_error(self):
        for executable in [None, '/broken/python']:
            with self.subTest(executable=executable), \
                 patch('scripts.kit.runtime.shutil.which', return_value=executable), \
                 patch.object(Context, 'output', side_effect=subprocess.CalledProcessError(1, 'python')):
                with self.assertRaisesRegex(ValueError, 'Install one alongside your system Python'):
                    find_python()

    def test_relative_interpreter_and_search_paths_use_the_checkout_and_preserve_venvs(self):
        with tempfile.TemporaryDirectory() as temporary:
            context = Context(Path(temporary), env={'PATH': 'venv/bin'})
            executable = context.root / 'venv/bin/python'
            executable.parent.mkdir(parents=True)
            executable.symlink_to(sys.executable)
            for candidate in ('venv/bin/python', 'python'):
                with self.subTest(candidate=candidate), patch.object(Context, 'output', return_value='3.13.1\n') as output:
                    self.assertEqual(find_python(candidate, context=context), (str(executable), '3.13.1'))
                    self.assertEqual(output.call_args.args[0][0], str(executable))

    def test_setup_passes_scoped_environment_to_the_ansible_virtualenv(self):
        with tempfile.TemporaryDirectory() as temporary:
            context = project(temporary, env={'PYTHON': '/custom/python', 'PATH': '/tools'})

            def prepare_tofu(platform, settings, output):
                output.mkdir()
                (output / 'tofu').write_text('prepared tool')

            with patch.object(runtime, 'find_python', return_value=('/custom/python', '3.13.1')) as find, \
                 patch.object(runtime, 'native_platform', return_value='linux/arm64'), \
                 patch.object(runtime, 'install_opentofu', side_effect=prepare_tofu) as tofu, \
                 patch.object(runtime, 'install_ansible') as ansible, redirect_stdout(io.StringIO()):
                runtime.setup(context=context)
            find.assert_called_once_with('/custom/python', context=context)
            self.assertEqual(tofu.call_args.args[0], 'linux/arm64')
            self.assertEqual((context.root / '.cache/tools/opentofu/1.0.0/tofu').read_text(), 'prepared tool')
            self.assertFalse(tofu.call_args.args[2].exists())
            venv = context.root / '.cache/ansible-2.0.0'
            self.assertEqual(ansible.call_args.args[2:], (venv, venv / 'python-packages.json'))
            self.assertEqual(ansible.call_args.kwargs, {'env': dict(context.env), 'cwd': context.root})

    def test_unsupported_python_fails_before_any_installation(self):
        with patch.object(runtime, 'find_python', side_effect=ValueError('unsupported Python')), \
             patch.object(runtime, 'install_opentofu') as tofu, \
             patch.object(runtime, 'install_ansible') as ansible, self.assertRaises(ValueError):
            runtime.setup()
        tofu.assert_not_called()
        ansible.assert_not_called()

    def test_setup_can_reuse_the_interpreter_inside_the_previous_ansible_environment(self):
        with tempfile.TemporaryDirectory() as temporary:
            context = project(temporary)
            venv = context.root / '.cache/ansible-2.0.0'
            executable = venv / 'bin/python'
            executable.parent.mkdir(parents=True)
            executable.symlink_to(sys.executable)
            (venv / 'obsolete').write_text('old package')

            def prepare_tofu(platform, settings, output):
                output.mkdir()
                (output / 'tofu').write_text('new tofu')

            def prepare_ansible(python, settings, output, report, **kwargs):
                self.assertEqual(Path(python).resolve(), Path(sys.executable).resolve())
                self.assertEqual(output, venv)
                (output / 'bin').mkdir(parents=True)
                (output / 'bin/ansible-playbook').write_text('#!' + str(output / 'bin/python'))

            with patch.object(runtime, 'find_python', return_value=(str(executable), '3.13.1')), \
                 patch.object(runtime, 'native_platform', return_value='linux/arm64'), \
                 patch.object(runtime, 'install_opentofu', side_effect=prepare_tofu), \
                 patch.object(runtime, 'install_ansible', side_effect=prepare_ansible), \
                 redirect_stdout(io.StringIO()):
                runtime.setup(context=context)
            self.assertEqual((venv / 'bin/ansible-playbook').read_text(), '#!' + str(executable))
            self.assertFalse((venv / 'obsolete').exists())
            self.assertEqual(list((context.root / '.cache').glob('setup-*')), [])

    def test_failed_or_interrupted_setup_restores_both_tools_and_cleans_staging(self):
        for failure in (RuntimeError, KeyboardInterrupt):
            for existing in (False, True):
                for phase in ('tofu', 'ansible'):
                    with self.subTest(failure=failure, existing=existing, phase=phase), \
                         tempfile.TemporaryDirectory() as temporary:
                        context = project(temporary, env={'PATH': '/tools'})
                        cache = context.root / '.cache'
                        tofu = cache / 'tools/opentofu/1.0.0/tofu'
                        ansible = cache / 'ansible-2.0.0/bin/ansible-playbook'
                        if existing:
                            for tool in (tofu, ansible):
                                tool.parent.mkdir(parents=True)
                                tool.write_text('previous installation')

                        def prepare_tofu(platform, settings, output):
                            output.mkdir()
                            (output / 'tofu').write_text('new tofu')
                            if phase == 'tofu':
                                raise failure('failed download')

                        def prepare_ansible(python, settings, output, report, **kwargs):
                            executable = output / 'bin/ansible-playbook'
                            executable.parent.mkdir(parents=True)
                            executable.write_text('partial installation')
                            raise failure('failed installation')

                        with patch.object(runtime, 'find_python', return_value=('/python', '3.13.1')), \
                             patch.object(runtime, 'native_platform', return_value='linux/arm64'), \
                             patch.object(runtime, 'install_opentofu', side_effect=prepare_tofu), \
                             patch.object(runtime, 'install_ansible', side_effect=prepare_ansible), \
                             redirect_stdout(io.StringIO()), self.assertRaises(failure):
                            runtime.setup(context=context)
                        for tool in (tofu, ansible):
                            self.assertEqual(tool.exists(), existing)
                            if existing:
                                self.assertEqual(tool.read_text(), 'previous installation')
                        self.assertEqual(list(cache.glob('setup-*')), [])
