"""Disposable daemon and current Connect API helpers for acceptance checks."""
import json
import pathlib
import socket
import subprocess
import time
import urllib.error
import urllib.request


class Engine:
    def __init__(self, root, config, binary, env=None):
        self.root = pathlib.Path(root)
        self.root.mkdir(parents=True, exist_ok=True)
        self.config = json.loads(json.dumps(config))
        self.binary = str(binary)
        self.env = env
        self.process = None
        self.log = None

    def __enter__(self):
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1', 0))
            port = reservation.getsockname()[1]
        self.config.setdefault('main', {}).update(port=port, shutdownTimeout='5s')
        self.endpoint = f'http://127.0.0.1:{port}'
        path = self.root / 'config.json'
        path.write_text(json.dumps(self.config))
        self.log = (self.root / 'daemon.log').open('w+')
        try:
            self.process = subprocess.Popen(
                [self.binary, 'server', 'start', '--config', str(path)],
                env=self.env, stdout=self.log, stderr=self.log)

            def ready():
                if self.process.poll() is not None:
                    self.log.flush()
                    raise RuntimeError('Daemon exited: ' + (self.root / 'daemon.log').read_text())
                try:
                    return self.rpc('ListProviders')
                except (urllib.error.URLError, ConnectionError, TimeoutError):
                    return None
            self.poll(ready, 30)
            return self
        except BaseException:
            self.__exit__(None, None, None)
            raise

    def __exit__(self, *_):
        if self.process is not None:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
            self.process = None
        if self.log is not None:
            self.log.close()
            self.log = None

    @staticmethod
    def poll(fn, timeout=120):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            result = fn()
            if result:
                return result
            time.sleep(0.05)
        raise RuntimeError('Daemon operation timed out')

    def rpc(self, method, body=None, *, namespace=None):
        headers = {'Content-Type': 'application/json', 'Connect-Protocol-Version': '1'}
        if namespace is not None:
            headers['X-Namespace-ID'] = namespace
        request = urllib.request.Request(
            self.endpoint + '/inframill.v1.EngineService/' + method,
            json.dumps(body or {}).encode(),
            headers)
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)

    def create_namespace(self, name):
        return self.rpc('CreateNamespace', {'name': name})['namespace']

    def fetch(self, reference, namespace):
        return self.rpc('FetchBlueprint', {'reference': reference}, namespace=namespace)['blueprint']

    def initialize(self, namespace, name, reference, values):
        # Fetch is explicit: this also exercises initialization from an offline cache.
        return self.rpc('InitInstance', {
            'name': name, 'blueprint_reference': reference, 'values': {'json': json.dumps(values)},
        }, namespace=namespace)['instance']['id']

    def execute(self, namespace, instance, command, args=None):
        execution_id = self.rpc('SubmitCommand', {
            'instance_id': instance, 'command_name': command, 'arguments': {'json': json.dumps(args or {})},
        }, namespace=namespace)['executionId']

        def completed():
            result = self.rpc('GetCommandExecution', {
                'instance_id': instance, 'execution_id': execution_id,
            }, namespace=namespace)['execution']
            return result if result['status'] in (
                'COMMAND_EXECUTION_STATUS_SUCCEEDED', 'COMMAND_EXECUTION_STATUS_FAILED',
                'COMMAND_EXECUTION_STATUS_INTERRUPTED') else None
        result = self.poll(completed)
        if result['status'] != 'COMMAND_EXECUTION_STATUS_SUCCEEDED':
            raise RuntimeError(result)
        return json.loads(result['result']['json']) if 'result' in result else {}
