"""ascii-fold.py: fold a Codex review prompt to ASCII (stdin to stdout).

WHY. macOS pgrep/pkill abort with "illegal byte sequence" on a UTF-8 pattern, so a
prompt carrying an em dash in a live run's argv cannot be matched or killed by
pattern (codex-review.sh, its item 4; D-122). Every non-ASCII character therefore
leaves as ASCII: the ones the charter and prompts use get a readable spelling here,
and anything else becomes `?` through encode("ascii", "replace").

That fallback is lossy, and it lied once: `fence.Field` renders a space as `␣` and an
empty result as `∅`, the two GIVENs about that package said so, and the table had
neither, so a reviewer was told `fence.Field` renders a space as `?` (Codex report
A5). They are now `<SP>` and `<EMPTY>`, spelled like the existing `<CR>` for `⏎`.
check-charter.sh folds the charter through THIS file and fails if the folded text has
a `?` its source lacks, so the next unmapped character in a GIVEN is caught before a
reviewer reads it.

A separate file, not inline in codex-review.sh, so that check reads the very table the
review applies, not a copy of it.
"""

import sys

TABLE = (
    ("—", "--"),  # em dash
    ("–", "-"),  # en dash
    ("’", "'"),  # right single quote
    ("“", '"'),  # left double quote
    ("”", '"'),  # right double quote
    ("→", "->"),  # rightwards arrow
    ("≥", ">="),  # greater-than or equal
    ("§", "sec"),  # section sign
    ("…", "..."),  # ellipsis
    ("×", "x"),  # multiplication sign
    ("⏎", "<CR>"),  # return symbol: fence.Line's newline
    ("␣", "<SP>"),  # open box: fence.Field's space
    ("∅", "<EMPTY>"),  # empty set: fence.Field's empty result
)


def fold(text):
    for a, b in TABLE:
        text = text.replace(a, b)
    return text.encode("ascii", "replace").decode("ascii")


def main():
    s = sys.stdin.buffer.read().decode("utf-8", "replace")
    sys.stdout.write(fold(s))
    return 0


if __name__ == "__main__":
    sys.exit(main())
