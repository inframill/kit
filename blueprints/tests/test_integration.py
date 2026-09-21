import unittest

import integration


class IntegrationConfigTests(unittest.TestCase):
    def test_fixture_blueprint_versions_select_real_driver_requirements(self):
        for case in integration.fixture_cases():
            requirements = integration.requirements(case['name'], case['version'])
            self.assertEqual({d['version'] for d in requirements}, {'0.2.0'})
            self.assertEqual(set(integration.driver_ids(case['name'],case['version'])), {'opentofu','ansible'})

