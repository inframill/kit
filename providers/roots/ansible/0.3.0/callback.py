# SPDX-License-Identifier: MIT
"""Stream safe progress to stderr and keep the verified recap alone on stdout."""
import json
import sys
from ansible.plugins.callback import CallbackBase

class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'stdout'
    CALLBACK_NAME = 'callback'

    def _event(self, status, **fields):
        sys.stderr.write(json.dumps({'ansible': status, **fields}, sort_keys=True) + '\n')
        sys.stderr.flush()

    def v2_playbook_on_task_start(self, task, is_conditional):
        # Task names can interpolate secrets; emit only the module/action name.
        self._event('task', action=task.action)

    def v2_runner_on_ok(self, result):
        self._event('ok', host=result._host.get_name())

    def _failure(self, result, status):
        reason = 'task failed; result details withheld'
        if not result._result.get('_ansible_no_log'):
            message = str(result._result.get('msg', '')).lower()
            # Classify common connection problems without printing arbitrary
            # module results, arguments, stdout, or credential-bearing errors.
            if 'permission denied' in message or 'authentication failed' in message:
                reason = 'SSH authentication failed; check the agent key and SSH user'
            elif 'host key verification failed' in message or 'host identification has changed' in message:
                reason = 'SSH host key verification failed; check known_hosts and host-key policy'
            elif 'connection refused' in message:
                reason = 'SSH connection refused; check the SSH service and port'
            elif 'timed out' in message or 'timeout' in message:
                reason = 'connection or task timed out; check reachability and the last task'
            elif 'no route to host' in message or 'network is unreachable' in message:
                reason = 'host is unreachable; check routing and firewall rules'
        self._event(status, host=result._host.get_name(), reason=reason)

    def v2_runner_on_failed(self, result, ignore_errors=False):
        self._failure(result, 'failed')

    def v2_runner_on_unreachable(self, result):
        self._failure(result, 'unreachable')

    def v2_runner_on_skipped(self, result):
        self._event('skipped', host=result._host.get_name())

    def v2_playbook_on_stats(self, stats):
        counters = {host: stats.summarize(host) for host in sorted(stats.processed)}
        self._event('recap', hosts=counters)
        self._display.display(json.dumps({'schema': 1, 'hosts': counters}, sort_keys=True))
