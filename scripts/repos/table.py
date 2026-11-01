"""Small, dependency-free tables with wrapped, complete multiline cells."""
import re
import shutil
import textwrap


_ANSI = re.compile(r'\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)')


def _plain(value):
    value = _ANSI.sub('', str(value)).expandtabs(4)
    return ''.join(character if character in '\n\r' or character.isprintable()
                   else f'\\x{ord(character):02x}' for character in value)


def table(headers, rows, *, width=None):
    """Wrap long lines instead of truncating output; strip terminal formatting."""
    headers, rows = list(headers), list(rows)
    width = width if width is not None else shutil.get_terminal_size((120, 24)).columns
    # When the column headers alone cannot fit, repeat the row identifier in
    # successive groups. Every column and value remains visible without scrolling.
    minimums = [max(len(_plain(header)), 8) for header in headers]
    groups, group = [], [0]
    for index in range(1, len(headers)):
        candidate = [*group, index]
        if len(group) > 1 and sum(minimums[column] + 3 for column in candidate) + 1 > width:
            groups.append(group)
            group = [0]
        group.append(index)
    groups.append(group)
    if len(groups) > 1:
        return '\n\n'.join(table([headers[column] for column in group],
                                 [[row[column] for column in group] for row in rows], width=width)
                           for group in groups)
    cells = [[_plain(value) for value in row] for row in [headers, *rows]]
    widths = [max(len(header), min(width, max(len(line) for row in cells
                                              for line in (row[index].splitlines() or ['']))))
              for index, header in enumerate(cells[0])]
    minimums = [max(len(header), 8) for header in cells[0]]
    while sum(widths) + 3 * len(widths) + 1 > width:
        candidates = [index for index, size in enumerate(widths) if size > minimums[index]]
        if not candidates:
            break
        index = max(candidates, key=lambda candidate: widths[candidate])
        widths[index] -= 1

    border = '+' + '+'.join('-' * (size + 2) for size in widths) + '+'
    lines = [border]
    for row in cells:
        wrapped = [[part for line in (value.splitlines() or [''])
                    for part in (textwrap.wrap(line, size, replace_whitespace=False,
                                               drop_whitespace=False, break_on_hyphens=False)
                                 or [''])]
                   for value, size in zip(row, widths)]
        for index in range(max(map(len, wrapped))):
            lines.append('| ' + ' | '.join((cell[index] if index < len(cell) else '').ljust(size)
                                           for cell, size in zip(wrapped, widths)) + ' |')
        lines.append(border)
    return '\n'.join(lines)
