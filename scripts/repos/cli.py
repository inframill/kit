"""Repository recipe arguments and table/JSON adapters; import jobs on dispatch."""
import json
import sys


def register(groups):
    repos = groups.add_parser('repos', help='Run named recipes in local repositories')
    commands = repos.add_subparsers(dest='command', required=True)
    listing = commands.add_parser('list', help='List configured repository paths')
    listing.set_defaults(handler=list_command)
    recipes = commands.add_parser('recipes', help='List available recipes')
    recipes.set_defaults(handler=recipes_command)
    status = commands.add_parser('status', help='Run the status recipe')
    status.set_defaults(handler=run_command, recipe='status')
    run = commands.add_parser('run', help='Run one named recipe')
    run.add_argument('recipe', help='Recipe name (see repos recipes)')
    run.add_argument('--command', dest='command_text', metavar='TEXT',
                     help='Shell command string for the command recipe')
    run.add_argument('--output-limit', type=int, metavar='CHARS',
                     help='Command output characters per repo (default: 1000)')
    run.add_argument('--timeout', type=float, metavar='SECONDS',
                     help='Command timeout per repo (default: 120 seconds)')
    run.set_defaults(handler=run_command)
    for command in (listing, recipes, status, run):
        command.add_argument('--json', action='store_true', help='Write machine-readable JSON')
    for command in (status, run):
        command.add_argument('--repo', action='append', dest='repositories', metavar='NAME',
                             help='Select a repository; repeat to select several (default: all)')


def _json(value):
    print(json.dumps(value, indent=2, allow_nan=False))


def list_command(args, context):
    from .jobs import repository_paths
    # Discovery resolves paths without requiring the directories or Git to exist.
    repositories = repository_paths(context)
    if args.json:
        _json({'repositories': [{'name': name, 'path': str(path)}
                                for name, path in repositories.items()]})
    else:
        from .table import table
        print(table(('Repository', 'Path'), list(repositories.items())))


def recipes_command(args, context):
    from .jobs import RECIPES
    recipes = [{'name': name, 'description': function.__doc__ or ''}
               for name, function in RECIPES.items()]
    if args.json:
        _json({'recipes': recipes})
    else:
        from .table import table
        print(table(('Recipe', 'Description'),
                    [(recipe['name'], recipe['description']) for recipe in recipes]))


def _cell(value):
    return value if isinstance(value, str) else json.dumps(value, allow_nan=False)


def run_command(args, context):
    from .jobs import RECIPES, bind_command, run_recipe
    try:
        options = {name: getattr(args, name, None)
                   for name in ('command_text', 'output_limit', 'timeout')}
        recipes = RECIPES
        if args.recipe == 'command':
            recipe = bind_command(options['command_text'],
                                  output_limit=1000 if options['output_limit'] is None else options['output_limit'],
                                  timeout=120 if options['timeout'] is None else options['timeout'],
                                  env=context.env)
            recipes = {**RECIPES, 'command': recipe}
        elif any(value is not None for value in options.values()):
            raise ValueError('--command, --output-limit and --timeout require the command recipe')
        report = run_recipe(context, args.recipe, selected=args.repositories, recipes=recipes)
    except ValueError as error:
        report = {'recipe': args.recipe, 'results': [], 'error': str(error)}
    if args.json:
        _json(report)
    elif report.get('error'):
        print('Error: ' + report['error'], file=sys.stderr)
    else:
        from .table import table
        keys = list(dict.fromkeys(key for result in report['results'] for key in result['values']))
        show_errors = any(result['error'] is not None for result in report['results'])
        headers = ['Repository', *keys, *(['Error'] if show_errors else [])]
        rows = [[result['repo'],
                 *[_cell(result['values'][key]) if key in result['values'] else '' for key in keys],
                 *([result['error'] or ''] if show_errors else [])]
                for result in report['results']]
        print(table(headers, rows))
    if report.get('interrupted'):
        return 130
    return int(bool(report.get('error')) or
               any(result['error'] is not None or
                   (args.recipe == 'command' and result['values'].get('exit_code', 0) != 0)
                   for result in report['results']))
