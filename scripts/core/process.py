"""Run argument lists with an explicit working directory and environment."""
import shlex
import subprocess
import sys


def run(args, *, cwd, env, check=True, log=True, timeout=120, **kwargs):
    if isinstance(args, (str, bytes)) or kwargs.get('shell'):
        raise ValueError('Commands must be argument lists without a shell')
    command = [str(arg) for arg in args]
    if log:
        print('+', shlex.join(command), file=sys.stderr, flush=True)
    return subprocess.run(command, cwd=cwd, env=dict(env), check=check,
                          text=True, timeout=timeout, **kwargs)
