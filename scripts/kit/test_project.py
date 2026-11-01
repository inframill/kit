"""Metadata and environment overrides stay inside the selected checkout."""
from pathlib import Path
import tempfile
import unittest

from scripts.core.context import Context
from scripts.kit import engine, project
from ._test_fixtures import project as fixture


class ProjectTests(unittest.TestCase):
    def test_independent_checkouts_keep_versions_paths_and_registry_settings(self):
        with tempfile.TemporaryDirectory() as temporary:
            first = fixture(Path(temporary) / 'first', version='1.2.3', env={
                'REGISTRY': 'localhost:5000', 'NAMESPACE': 'testing', 'OPENTOFU_PATH': 'tools/tofu',
                'INFRAMILL_DIR': '../engine'})
            second = fixture(Path(temporary) / 'second', version='9.8.7')
            self.assertEqual(project.project_version(context=first), '1.2.3')
            self.assertEqual(project.project_version(context=second), '9.8.7')
            self.assertEqual(project.image_reference(context=first), 'localhost:5000/testing/kit:1.2.3')
            self.assertEqual(project.image_reference(context=second), 'ghcr.io/inframill/kit:9.8.7')
            self.assertEqual(project.bundle_output(context=second), second.root / 'dist/kit/9.8.7')
            self.assertEqual(project.local_tools(context=first)[0], first.root / 'tools/tofu')
            self.assertEqual(project.local_tools(context=second)[0], second.root / '.cache/tools/opentofu/1.0.0/tofu')
            self.assertEqual(project.local_executable(context=first), first.root / 'bin/kit-exec')
            self.assertEqual(project.engine_version(context=first), '3.0.0')
            self.assertEqual(engine.engine_checkout(context=first), first.root.parent / 'engine')
            self.assertEqual(engine.engine_checkout(context=second), second.root.parent / 'inframill')

    def test_invalid_registry_and_namespace_overrides_are_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            for name, value in [('REGISTRY', 'host/path'), ('NAMESPACE', '../escape')]:
                context = Context(Path(temporary), env={name: value})
                with self.subTest(name=name), self.assertRaises(ValueError):
                    project.image_reference('1.2.3', context=context)
