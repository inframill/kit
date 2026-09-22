# SPDX-License-Identifier: MIT
"""Expose only host counters; task arguments, results and credentials stay private."""
import json
from ansible.plugins.callback import CallbackBase

class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'stdout'
    CALLBACK_NAME = 'callback'

    def v2_playbook_on_stats(self, stats):
        counters = {host: stats.summarize(host) for host in sorted(stats.processed)}
        self._display.display(json.dumps({'schema': 1, 'hosts': counters}, sort_keys=True))
