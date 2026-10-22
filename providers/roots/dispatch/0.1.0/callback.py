# SPDX-License-Identifier: MIT
"""Report bounded-by-runner Make diagnostics and a private, verified recap."""
import json
import sys
from ansible.plugins.callback import CallbackBase


class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'stdout'
    CALLBACK_NAME = 'callback'

    def _event(self, status, **fields):
        sys.stderr.write(json.dumps({'dispatch': status, **fields}, sort_keys=True) + '\n')
        sys.stderr.flush()

    def v2_playbook_on_task_start(self, task, is_conditional):
        self._event('task', action=task.action)

    def _result(self, result, status):
        host = result._host.get_name()
        data = result._result
        self._event(status, host=host)
        if data.get('_ansible_no_log'):
            return
        target = getattr(result._task, 'vars', {}).get('dispatch_target')
        if target is not None and isinstance(data.get('rc'), int):
            if not hasattr(self, '_targets'):
                self._targets = {}
            self._targets.setdefault(host, []).append({'target': target, 'exit_code': data['rc']})
            self._event('make', host=host, target=target, exit_code=data['rc'],
                        stdout=str(data.get('stdout', '')), stderr=str(data.get('stderr', '')))
        if status in ('failed', 'unreachable'):
            self._event('error', host=host, message=str(data.get('msg', 'task failed')))

    def v2_runner_on_ok(self, result):
        self._result(result, 'ok')

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._result(result, 'failed')

    def v2_runner_on_unreachable(self, result):
        self._result(result, 'unreachable')

    def v2_runner_on_skipped(self, result):
        self._event('skipped', host=result._host.get_name())

    def v2_playbook_on_stats(self, stats):
        hosts = {host: stats.summarize(host) for host in sorted(stats.processed)}
        self._event('recap', hosts=hosts)
        self._display.display(json.dumps({'schema': 1, 'hosts': hosts,
                                         'targets': getattr(self, '_targets', {})}, sort_keys=True))
