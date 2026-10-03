import json
import unittest

import yaml
from jsonschema import Draft202012Validator

import integration


class LocalSourceTests(unittest.TestCase):
    def validate_source(self, source, examples):
        definition = yaml.safe_load((source / 'blueprint.yaml').read_text())
        self.assertEqual(definition['api_version'], 'inframill.dev/blueprint/v1')
        bindings = definition['provider_settings']
        self.assertEqual({p.name for p in (source / 'templates').iterdir()}, set(bindings))
        schema = definition['values_schema']
        Draft202012Validator.check_schema(schema)
        for example in examples:
            with self.subTest(example=example):
                Draft202012Validator(schema).validate(json.loads(example.read_text()))
        for identity, binding in bindings.items():
            self.assertEqual(binding['version'], '0.0' if identity == 'debug' else '0.3')
            if identity != 'debug':
                self.assertTrue((source / 'templates' / identity / 'kit-provider.json.gotmpl').is_file())

    def test_local_blueprints_and_examples(self):
        for name in ['pv-cloud', 'hello-world']:
            examples = [integration.BLUEPRINTS / f'examples/{name}.json']
            if name == 'pv-cloud':
                examples += sorted((integration.FIXTURES / 'examples').glob('pv-cloud-*.json'))
            self.validate_source(integration.BLUEPRINTS / name, examples)

    def test_local_fixture_sources(self):
        for case in integration.fixture_cases():
            reference = integration.fixture_reference(case['version'])
            source = integration.FIXTURES / reference.removeprefix('local:')
            self.validate_source(source, [integration.FIXTURES / f"examples/local-resource-{case['version']}.json"])
