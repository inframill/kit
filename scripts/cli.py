"""Explicit command group registration and command failure handling."""
import argparse
from pathlib import Path
import subprocess
import sys

from .core.context import Context


ROOT = Path(__file__).resolve().parents[1]


def parser():
    result = argparse.ArgumentParser(prog='python3 -m scripts',
                                     description='Kit repository automation')
    groups = result.add_subparsers(dest='group', required=True)
    # CLI modules declare arguments; job imports and effects wait for dispatch.
    from .repos.cli import register as register_repos
    register_repos(groups)
    from .kit.cli import register as register_kit
    register_kit(groups)
    return result


def main(argv=None, *, context=None):
    args = parser().parse_args(argv)
    try:
        result = args.handler(args, context if context is not None else Context(ROOT))
    except subprocess.CalledProcessError as error:
        print(str(error), file=sys.stderr)
        return error.returncode if error.returncode > 0 else 128 - error.returncode
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print('Error: ' + str(error), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print('Interrupted', file=sys.stderr)
        return 130
    return result if result is not None else 0
