import contextlib
import importlib.util
import io
import json
import pathlib
import types
import unittest
from unittest.mock import patch


class DispatchCallbackTests(unittest.TestCase):
    def setUp(self):
        base = types.ModuleType('ansible.plugins.callback')
        base.CallbackBase = type('CallbackBase', (), {})
        path = pathlib.Path(__file__).parents[1] / 'roots/dispatch/0.1.0/callback.py'
        spec = importlib.util.spec_from_file_location('dispatch_callback', path)
        module = importlib.util.module_from_spec(spec)
        with patch.dict('sys.modules', {'ansible.plugins.callback': base}):
            spec.loader.exec_module(module)
        self.callback = module.CallbackModule()
        self.callback._display = types.SimpleNamespace(display=lambda value: print(value))

    def result(self, **data):
        return types.SimpleNamespace(
            _host=types.SimpleNamespace(get_name=lambda: 'web-1'),
            _task=types.SimpleNamespace(vars={'dispatch_target': 'build'}),
            _result=data)

    def test_make_diagnostics_and_ordered_exit_codes(self):
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            self.callback.v2_runner_on_ok(self.result(rc=0, stdout='build output'))
            failed = self.result(rc=2, stderr='compiler error', msg='Make failed')
            failed._task.vars['dispatch_target'] = 'deploy'
            self.callback.v2_runner_on_failed(failed)
            self.callback.v2_playbook_on_stats(types.SimpleNamespace(
                processed={'web-1': {}}, summarize=lambda host: {'ok': 1, 'failures': 1}))
        recap = json.loads(stdout.getvalue())
        self.assertEqual(recap['targets']['web-1'], [
            {'target': 'build', 'exit_code': 0}, {'target': 'deploy', 'exit_code': 2}])
        self.assertIn('compiler error', stderr.getvalue())
        self.assertNotIn('compiler error', stdout.getvalue())

    def test_no_log_suppresses_make_output_and_results(self):
        output = io.StringIO()
        with contextlib.redirect_stderr(output):
            self.callback.v2_runner_on_failed(self.result(
                rc=1, stdout='private', stderr='private', msg='private', _ansible_no_log=True))
        self.assertNotIn('private', output.getvalue())
        self.assertFalse(hasattr(self.callback, '_targets'))
