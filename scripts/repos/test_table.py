"""Tables retain multiline output and wrap long cells without truncation."""
import unittest

from scripts.repos.table import table


class TableTests(unittest.TestCase):
    def test_long_and_multiline_cells_are_complete_and_aligned(self):
        output = table(('Repo', 'Output'), [('one', 'first\n\n    indented\n' + 'X' * 250 + '\nlast')],
                       width=40)
        lines = output.splitlines()
        self.assertEqual(len({len(line) for line in lines}), 1)
        self.assertLessEqual(len(lines[0]), 40)
        self.assertEqual(output.count('X'), 250)
        for text in ('first', '    indented', 'last'):
            self.assertIn(text, output)

    def test_terminal_controls_are_removed_or_escaped_and_empty_rows_work(self):
        output = table(('Result',), [('\x1b[31mred\x1b[0m\x00',)])
        self.assertNotIn('\x1b', output)
        self.assertIn('red\\x00', output)
        self.assertIn('Repository', table(('Repository',), []))

    def test_many_columns_form_groups_with_repeated_repository_names(self):
        headers = ['Repository', *[f'field_{index}' for index in range(12)]]
        values = ['my-repo', *[f'value-{index}' for index in range(12)]]
        output = table(headers, [values], width=60)
        self.assertIn('\n\n', output)
        for group in output.split('\n\n'):
            self.assertIn('Repository', group)
            self.assertIn('my-repo', group)
            self.assertTrue(all(len(line) <= 60 for line in group.splitlines()))
        for value in headers[1:] + values[1:]:
            self.assertIn(value, output)
