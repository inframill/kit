"""Invocation state passed explicitly to jobs; never change cwd or os.environ."""
from dataclasses import dataclass, field
import os
from pathlib import Path
import subprocess
from types import MappingProxyType
from typing import Mapping

from . import process


@dataclass(frozen=True)
class Context:
    root: Path
    env: Mapping[str, str] = field(default_factory=lambda: dict(os.environ))

    def __post_init__(self):
        object.__setattr__(self, 'root', Path(self.root).resolve())
        object.__setattr__(self, 'env', MappingProxyType(dict(self.env)))

    def path(self, path):
        """Resolve job paths relative to this checkout, including env overrides."""
        return (self.root / path).resolve()

    def run(self, args, *, cwd=None, env=None, **kwargs):
        environment = dict(self.env)
        for name, value in (env or {}).items():
            if value is None:
                environment.pop(name, None)
            else:
                environment[name] = value
        return process.run(args, cwd=self.path(cwd) if cwd is not None else self.root,
                           env=environment, **kwargs)

    def output(self, args, **kwargs):
        return self.run(args, stdout=subprocess.PIPE, log=False, **kwargs).stdout
