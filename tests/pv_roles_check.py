"""Verify pv-cloud role selection with harmless local roles and colliding names."""
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

import yaml


def check(tool, source):
    plays = yaml.safe_load(pathlib.Path(source).read_text())[1:]
    roles = ['common', 'docker', 'web', 'data']
    names = roles + ['worker']
    with tempfile.TemporaryDirectory(prefix='kit-pv-roles-') as td:
        root = pathlib.Path(td)
        config = root / 'ansible.cfg'
        config.write_text('[defaults]\n')
        for role in roles:
            tasks = root / 'roles' / role / 'tasks'
            tasks.mkdir(parents=True)
            (tasks / 'main.yml').write_text(yaml.safe_dump([
                {'ansible.builtin.debug': {'msg': 'selected-' + role + '-{{ inventory_hostname }}'}},
            ]))
        playbook = root / 'site.yml'
        playbook.write_text(yaml.safe_dump(plays))
        env = {**os.environ, 'ANSIBLE_CONFIG': str(config),
               'ANSIBLE_LOCAL_TEMP': str(root / 'local'),
               'ANSIBLE_REMOTE_TEMP': str(root / 'remote'),
               'ANSIBLE_ROLES_PATH': str(root / 'roles')}
        cases = [(role + '-only', {role: names}) for role in roles] + [
            ('name-collision', {'common': ['data', 'worker'], 'docker': ['data', 'worker'],
                                'data': ['worker']}),
        ]
        for name, groups in cases:
            inventory = {'all': {'hosts': {name: {} for name in names}, 'children': {
                role: {'hosts': {host: {} for host in hosts}} for role, hosts in groups.items()
            }}}
            path = root / (name + '.json')
            path.write_text(json.dumps(inventory))
            result = subprocess.run(
                [str(tool), '-i', str(path), str(playbook), '-c', 'local', '-e', 'ansible_become=false'],
                env=env, capture_output=True, text=True)
            assert result.returncode == 0, result.stdout + result.stderr
            actual = set(re.findall(r'selected-(?:common|docker|web|data)-[a-z][a-z0-9_-]*', result.stdout))
            expected = {'selected-' + role + '-' + host
                        for role, hosts in groups.items() for host in hosts}
            assert actual == expected, (name, actual, expected, result.stdout)
            print(name + ': role membership verified', flush=True)


if __name__ == '__main__':
    check(pathlib.Path(sys.argv[1]).resolve(), sys.argv[2])
