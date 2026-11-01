"""Kit's command tree. Import job implementations only after argument parsing."""
import json
from pathlib import Path


def register(namespaces):
    kit = namespaces.add_parser('kit', help='Kit development, runtime and release jobs')
    groups = kit.add_subparsers(dest='group', required=True)

    dev = groups.add_parser('dev', help='Synchronize development sources and pins')
    commands = dev.add_subparsers(dest='action', required=True)
    commands.add_parser('sync', help='Import Runmill and verified engine artifacts').set_defaults(handler=dev_command)

    engine = groups.add_parser('engine', help='Build or import sibling engine binaries')
    commands = engine.add_subparsers(dest='action', required=True)
    for action, help_text in [('copy', 'Import and verify pinned sibling binaries'),
                              ('build', 'Build binaries in the sibling checkout')]:
        commands.add_parser(action, help=help_text).set_defaults(handler=engine_command)

    runtime = groups.add_parser('runtime', help='Install local tools or generate configuration')
    commands = runtime.add_subparsers(dest='action', required=True)
    commands.add_parser('setup', help='Install pinned OpenTofu and Ansible').set_defaults(handler=runtime_command)
    config = commands.add_parser('config', help='Print or write native development configuration')
    config.add_argument('--output', type=Path, help='Write to this repository-relative or absolute path')
    config.set_defaults(handler=runtime_command)

    bundle = groups.add_parser('bundle', help='Package, verify, load or publish prepared artifacts')
    commands = bundle.add_subparsers(dest='action', required=True)
    for action, help_text in [('capture', 'Record build source inputs'),
                              ('image', 'Build and load the local image'),
                              ('release', 'Assemble the prepared image into a release bundle'),
                              ('verify', 'Verify an existing bundle'),
                              ('load', 'Verify and load an existing bundle'),
                              ('push', 'Verify and publish an existing bundle')]:
        command = commands.add_parser(action, help=help_text)
        if action in ('verify', 'load', 'push'):
            command.add_argument('--bundle', type=Path, help='Bundle path (default: dist/kit/<version>)')
        command.set_defaults(handler=bundle_command)


def dev_command(args, context):
    from . import dev, engine
    dev.sync(engine.engine_checkout(context=context), context=context)


def engine_command(args, context):
    from . import engine
    {'copy': engine.copy, 'build': engine.build_sibling}[args.action](context=context)


def runtime_command(args, context):
    from . import runtime
    if args.action == 'setup':
        runtime.setup(context=context)
    else:
        runtime.config(output=args.output, context=context)


def bundle_command(args, context):
    from . import bundle
    from .project import bundle_output
    if args.action in ('capture', 'image', 'release'):
        {'capture': bundle.capture, 'image': bundle.build_image,
         'release': bundle.release}[args.action](context=context)
    else:
        folder = context.path(args.bundle) if args.bundle is not None else bundle_output(context=context)
        if args.action == 'verify':
            print(json.dumps(bundle.verify(folder), indent=2))
        else:
            {'load': bundle.load_image, 'push': bundle.push}[args.action](folder, context=context)
