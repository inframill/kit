"""Explicit namespace registration and common command failure handling."""
import argparse
from pathlib import Path
import subprocess
import sys

from .core.context import Context


ROOT = Path(__file__).resolve().parents[1]


def parser():
    result = argparse.ArgumentParser(prog='python3 -m scripts',
                                     description='Repository development automation')
    namespaces = result.add_subparsers(dest='namespace', required=True)
    # Register new namespaces here. Their CLI modules only declare commands;
    # implementations are imported when a command actually runs.
    from .kit.cli import register
    register(namespaces)
    return result


def main(argv=None, *, context=None):
    args = parser().parse_args(argv)
    try:
        args.handler(args, context if context is not None else Context(ROOT))
    except subprocess.CalledProcessError as error:
        print(str(error), file=sys.stderr)
        return error.returncode if error.returncode > 0 else 128 - error.returncode
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print('Error: ' + str(error), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print('Interrupted', file=sys.stderr)
        return 130
    return 0
