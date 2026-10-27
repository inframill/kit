"""Pin migration checksums from a clean Millway checkout without changing it."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, default=Path('../millway'))
    parser.add_argument('--output', type=Path, action='append', required=True)
    args = parser.parse_args()
    source = args.source.resolve()
    changed = subprocess.check_output(['git', '--no-optional-locks', 'status', '--porcelain', '--',
                                       'VERSION', 'INFRAMILL_VERSION', 'internal/dal/db'], cwd=source, text=True)
    if changed.strip():
        raise SystemExit('Commit the Millway version and migrations before pinning their contract')
    migrations = {}
    for path in sorted((source / 'internal/dal/db/migrations').iterdir()):
        if path.is_symlink() or not path.is_file() or not re.fullmatch(r'\d{6}_[a-z0-9_]+\.sql', path.name):
            raise SystemExit('Millway migration directory contains an unsupported entry')
        migrations[path.name] = hashlib.sha256(path.read_bytes()).hexdigest()
    if not migrations:
        raise SystemExit('Millway migration set must not be empty')
    manifest = {'schema': 1, 'version': (source / 'VERSION').read_text().strip(),
                'inframill_version': (source / 'INFRAMILL_VERSION').read_text().strip(),
                'source_revision': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=source, text=True).strip(),
                'ledger': 'millway_schema_migrations', 'migrations': migrations}
    contents = json.dumps(manifest, indent=2) + '\n'
    for path in args.output:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
    print(f"Pinned Millway {manifest['version']}: {len(migrations)} migrations")


if __name__ == '__main__':
    main()
