import contextlib
import importlib.util
import io
import json
import pathlib
import types
import unittest
from unittest.mock import patch


class CallbackTests(unittest.TestCase):
    def setUp(self):
        # Callback contract tests need no Ansible installation.
        base = types.ModuleType('ansible.plugins.callback')
        base.CallbackBase = type('CallbackBase', (), {})
        path = pathlib.Path(__file__).parents[1] / 'roots/ansible/0.4.0/callback.py'
        spec = importlib.util.spec_from_file_location('kit_callback', path)
        module = importlib.util.module_from_spec(spec)
        with patch.dict('sys.modules', {'ansible.plugins.callback': base}):
            spec.loader.exec_module(module)
        self.callback = module.CallbackModule()

    def test_progress_and_safe_failure_reasons(self):
        output = io.StringIO()
        result = types.SimpleNamespace(
            _host=types.SimpleNamespace(get_name=lambda: 'node-1'),
            _result={'msg': 'Permission denied: synthetic-secret',
                     'stdout': 'synthetic-secret', 'invocation': 'synthetic-secret'})
        with contextlib.redirect_stderr(output):
            self.callback.v2_playbook_on_task_start(
                types.SimpleNamespace(action='ansible.builtin.wait_for_connection', name='synthetic-secret'), False)
            self.callback.v2_runner_on_failed(result)
            self.callback.v2_runner_on_unreachable(result)
            result._result['_ansible_no_log'] = True
            self.callback.v2_runner_on_failed(result)
        events = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(events[0]['action'], 'ansible.builtin.wait_for_connection')
        self.assertIn('SSH authentication failed', events[1]['reason'])
        self.assertEqual(events[2]['ansible'], 'unreachable')
        self.assertNotIn('authentication', events[3]['reason'])
        self.assertNotIn('synthetic-secret', output.getvalue())

    def test_changed_host_key_has_specific_safe_reason(self):
        for message in (
            'WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!\nHost key verification failed.',
            'Offending ECDSA key in synthetic-secret:3\nHost key verification failed.',
            'REMOTE HOST IDENTIFICATION HAS CHANGED!\nPermission denied (publickey).',
        ):
            with self.subTest(message=message):
                output = io.StringIO()
                result = types.SimpleNamespace(
                    _host=types.SimpleNamespace(get_name=lambda: 'node-1'),
                    _result={'msg': message + ' synthetic-secret'})
                with contextlib.redirect_stderr(output):
                    self.callback.v2_runner_on_unreachable(result)
                    result._result['_ansible_no_log'] = True
                    self.callback.v2_runner_on_unreachable(result)
                events = [json.loads(line) for line in output.getvalue().splitlines()]
                self.assertIn('SSH host key changed', events[0]['reason'])
                self.assertIn('accept-new rejects changed keys', events[0]['reason'])
                self.assertNotIn('host key changed', events[1]['reason'])
                self.assertNotIn('synthetic-secret', output.getvalue())

    def test_generic_host_key_failure_does_not_claim_key_changed(self):
        output = io.StringIO()
        result = types.SimpleNamespace(
            _host=types.SimpleNamespace(get_name=lambda: 'node-1'),
            _result={'msg': 'Host key verification failed.'})
        with contextlib.redirect_stderr(output):
            self.callback.v2_runner_on_unreachable(result)
        reason = json.loads(output.getvalue())['reason']
        self.assertIn('SSH host key verification failed', reason)
        self.assertNotIn('changed', reason)

    def test_recap_remains_machine_readable_and_is_also_visible(self):
        stdout, stderr = io.StringIO(), io.StringIO()
        self.callback._display = types.SimpleNamespace(display=lambda value: print(value))
        stats = types.SimpleNamespace(processed={'node-1': {}}, summarize=lambda host: {'ok': 1})
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            self.callback.v2_playbook_on_stats(stats)
        self.assertEqual(json.loads(stdout.getvalue()), {'schema': 1, 'hosts': {'node-1': {'ok': 1}}})
        self.assertEqual(json.loads(stderr.getvalue())['ansible'], 'recap')
