# SPDX-License-Identifier: MIT
from ansible.plugins.callback import CallbackBase
import json

class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'stdout'
    CALLBACK_NAME = 'kit_callback'

    def v2_playbook_on_stats(self, stats):
        # Only bounded counters and host identities; never task args/results/secrets.
        hosts = {host: stats.summarize(host) for host in sorted(stats.processed)}
        self._display.display(json.dumps({'schema': 1, 'hosts': hosts}, sort_keys=True))
