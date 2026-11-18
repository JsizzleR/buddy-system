#!/usr/bin/env python3
r"""issue-author-gate.py -- a Claude Code PreToolUse hook on Bash and WebFetch:
GitHub issue and pull-request text reaches a session only through the issue
reader (scripts/issue-read.py), which prints only what the authenticated gh
user wrote (D-215).

    python3 -I "$HOME/bin/issue-author-gate.py"           # the hook: payload on stdin
    python3 -I -B scripts/hooks/issue-author-gate.py --self-test

WHY. The repository is public: anyone with a GitHub account can open an issue
or comment on one, and a lane that runs `gh issue view 12` puts a stranger's
words into its context as if they were the operator's work order. Prose did
not hold the line it was asked to (the operator's rule "work only issues I
filed" lived in a memory file, and no shipped skill even taught how to read an
issue: zero `gh issue` hits in skills/, scripts/ and docs/USAGE.md when this
was written). A hook cannot filter a tool's OUTPUT, only refuse the call, and
asking GitHub who the caller is took 0.27 s against the 100 ms hook budget, so
the author check lives in the reader and this hook only routes every other
read to it. Measured when written (2026-11-17): all 144 issues, the one PR and
all 109 comments on the repository were the operator's (its sole
collaborator), so the hole was latent; this closes it before it is used.

WHAT IT REFUSES, exit 2 with one line on stderr that names the reader and
echoes nothing from the payload. Bash and Monitor commands (Monitor's runs in
Bash's shell) and WebFetch urls:
  - `gh issue` / `gh pr` unless the word right after is a known WRITE (issue:
    create, comment, close, edit, reopen; pr: the same, ready and merge). An
    ALLOW-list, so a read subcommand gh adds later (and view, list, status,
    diff, checkout, checks, develop) refuses by default. The subcommand must be
    the very next word: `gh issue -R comment view 5` runs `view` (-R takes
    `comment` as its value), so a flag there refuses rather than being skipped.
    `gh issue` alone and `gh issue <sub> --help` are help.
  - `gh api` when an endpoint (a positional, not a flag's value) has an API
    path segment naming issue, PR or comment text (DENY_SEGMENTS below, before
    any segment that starts a file path: `contents`, `git`, ...), or is one the
    gate cannot read (a `$`, a substitution, `{branch}`).
  - every other top-level gh command but the ones known to read no issue or PR
    text (ALLOWED_TOP below): `search`, `status`, `discussion`, `project`,
    `agent-task`, an alias (`co` is `pr checkout`), `extension exec` of
    anything but `stack`, a command spelled through a variable, `gh browse`
    under a BROWSER assignment. gh refuses a root flag but --help/--version,
    and those only alone (`gh --help --help=false issue view 1` runs the read).
  - any gh under xargs or parallel: they append words the gate cannot see.
  - a Bash word or WebFetch url that IS a GitHub URL to issue or PR text: an
    API path as above (api.github.com, patch-diff.githubusercontent.com); a
    github.com site page (`/search`, `/notifications`, `/issues`, ...) or a
    repository's issues, pull, discussions, pulse, labels, milestones or
    projects page; any `.atom` feed. Normalised as a URL parser would read it
    (NFKC, backslashes as slashes, tabs and newlines dropped, percent-decoded,
    curl's glob braces dropped, one to three slashes, a trailing-dot host).
    Repository files pass (`/blob/`, `/tree/`, raw.githubusercontent.com), and
    so does an org or repo named `graphql` or `events`. A sentence citing a URL
    is not refused; a word with no space that holds `://`, and WebFetch's url,
    are searched through, so a proxy wrapping the GitHub URL is read too. A
    gh write's own operands are its own (`gh issue comment <url> -b x`).

HOW IT READS A COMMAND. Its own small shell lexer, not shlex: posix shlex ends
a word at a `#` in its middle and drops the rest of the line as a comment
(`echo a#b; gh issue view 1` read as `echo a`), keeps `"$(gh issue view 1)"`
one opaque word, and cannot see here-documents, so every commit written as
`git commit -m "$(cat <<'EOF' ... don't ... EOF)"` failed to tokenise (the
apostrophe). The lexer splits words and simple commands the way sh does for
the parts that matter here: quotes, backslashes, $'...' escapes (a NUL ends
the word, as the shells do), `$$`, comments only after a blank or an operator,
`;` `&` `|` `(` `)` and newlines between commands, redirection operators
(`&>`, zsh's `>!`) and their targets dropped from argv (`gh issue >comment
view 1` runs `view`), here-strings and here-documents (a backslash-newline
joined in an unquoted one, its delimiter included). The text a shell certainly
runs is read too, to a depth of four: `$(...)`, backticks (unescaped first, as
sh does) and `<(...)`, inside double quotes and unquoted here-documents as
well (a `case` pattern's `)` does not close a substitution); a `-c` operand
after a shell (past its other flags), `eval`'s, `trap`'s and `env -S`'s text;
a here-document or here-string read by a shell or `. /dev/stdin` it is
attached to or pipes into, across the body when the line ends in `|`; the
words of commands piped into a shell reading stdin (`echo '...' | sh`); and a
here-document with no terminator (sh reads to the end, and a `<<` the lexer
mistook for one, `(( 1 << 2 ))`, must not hide the lines after it). A
here-document read by curl, wget and the like has its GitHub URLs judged. A gh
word is matched by basename, case-folded (the default macOS volume finds `GH`
as gh), and every gh word is judged, not only the first. In command position
(after assignments, or a prefix: env, sudo, timeout, noglob, if, ...) every
refusal applies; elsewhere (`grep -n gh README.md`, `which gh jq`) only a read
or a gh subcommand or flag spelled out after it. Quoted mentions stay one word
and pass (`grep -rn "gh issue view" skills/`, `git commit -m 'never gh issue
view'`); the same words unquoted are refused.

JURISDICTION AND FAILURE. Outside it, exit 0 and silent: stdin that is not a
JSON object, another tool, a command or url that is not a string, and a
command that never mentions `gh` (as a word, after joining backslash-newlines,
percent-decoding, NFKC and deleting quote, backslash and `$` characters),
`github` or `$'` (whose escapes can spell gh): invariant 3's shape, a call this
gate has no opinion on costs nothing. Inside it, any failure to read the
command (an unterminated quote or substitution, nesting past the depth cap,
any exception) REFUSES, exit 2: a parser bug costs only calls that name gh or
GitHub, where agent-model-gate.py (D-117) fails open for every call because it
guards a convention, not untrusted input. Wired but missing, the hook blocks
EVERY Bash, WebFetch and Monitor call (python3 exits 2 on a missing script,
measured 2026-10-29, and exit 2 blocks a PreToolUse call); install.sh's report
says so loudly.

DECLARED, NOT CLOSED (D-005: a seatbelt, not a sandbox; the Fable
cross-check's verified inputs are in D-215): a command reached through a
variable, function, shell or gh alias, wrapper script, or a substitution or
glob spelling the command (`$(which gh)`, `g$()h`, `g[h]`, zsh `=gh`);
text a shell reads from a substitution's OUTPUT (`source <(...)`, `bash
<(...)`, `eval "$(...)"`, `sh -c "$(...)"`); a here-document fed by a brace
group closed on a later line; `python3 -c`, node and other interpreters;
brace expansion spelling gh; `#` read as a comment inside `${...}` or a
regex, zsh-only `case`/paren parses inside a quoted substitution, `${ ...; }`
command substitution, and bash 3.2 ending a here-document at `EOF)`;
`hub`; zsh glob
qualifiers and `<1-10>` patterns; `su -c`, `ssh host '...'`, `fish
--command=`, git aliases running `!gh`; curl `[a-b]` globs, a request to
GitHub's IP with a Host header, mirror domains, GitHub Enterprise hosts;
commit pages (commit comments); `gh run list`/`view` and the API's run
`display_title` (a fork PR's title); `gh release view`; `GH_DEBUG=api`
dumping a write's response; a title echoed by `gh issue close`/`reopen` and
`gh pr close`/`reopen`/`merge`; Codex sessions (no hooks.json row yet); MCP
GitHub tools, Chrome and WebSearch; `git fetch origin pull/N/head`; the
operator's own `!` shell. Known over-refusals: an unquoted mention of the raw
form (`echo gh issue view`), a here-document read by a shell that names it, a
word that IS an issue URL (`git commit -m "https://github.com/o/r/issues/5"`),
`gh api` writes to an issue path (use `gh issue`), and other repositories'
issues (the reader reads one repository; a fenced, byte-capped --reference
mode is a follow-up).

INSTALLED BY HAND, like agent-model-gate.py (D-120): copy this file AND
scripts/issue-read.py into the bin dir, then add to `~/.claude/settings.json`,
`hooks.PreToolUse`:
  {"matcher": "Bash|WebFetch|Monitor", "hooks": [{"type": "command", "command": "python3 -I \"$HOME/bin/issue-author-gate.py\"", "timeout": 10}]}
install.sh reports the copy and the wiring and changes neither.

CUT. A `buddy` verb (cmd/buddy is the no-network binary). Recognition inside
the claims gate's ghRoute (internal/cli/ghstack.go): that changes what buddy's
gate refuses, full tier, and would put GitHub policy in a binary that must work
offline. Substring matching on the raw command (it refused the grep above).
Allowing reads of an issue the operator wrote straight from gh: a hook cannot
ask GitHub who wrote it inside its budget, and a comment by anyone rides along
with `gh issue view --comments`.
"""
import json
import os
import re
import sys
import unicodedata
import urllib.parse

DEPTH_CAP = 4
SHELLS = frozenset(("sh", "bash", "zsh", "dash", "ksh", "mksh", "yash", "fish", "csh", "tcsh"))
WRITES = {
    "issue": frozenset(("create", "comment", "close", "edit", "reopen")),
    "pr": frozenset(("create", "comment", "close", "edit", "reopen", "ready", "merge")),
}
# Top-level gh commands that read no issue or PR text. Anything else refuses.
ALLOWED_TOP = frozenset((
    "auth", "browse", "cache", "codespace", "completion", "config", "extension", "ext",
    "gist", "gpg-key", "help", "label", "licenses", "org", "release", "repo", "ruleset",
    "run", "secret", "ssh-key", "stack", "variable", "version", "workflow", "attestation",
))
READS_TOP = frozenset(("search", "status", "discussion", "project", "agent-task", "co"))
# An API path (api.github.com, gh api) naming one of these, before a segment
# that starts a file path (API_FILES), reads issue, PR or comment text.
DENY_SEGMENTS = frozenset((
    "issues", "pull", "pulls", "comments", "search", "graphql", "notifications", "events",
    "received_events", "timeline", "discussions", "logs", "projects", "deliveries", "milestones",
))
API_FILES = frozenset(("contents", "git", "readme", "tarball", "zipball", "blobs", "trees"))
# A github.com page: the first segment for a site-wide page, the third (after
# owner/repo) for a repository's; any `.atom` feed.
WEB_TOP = frozenset(("search", "notifications", "issues", "pulls", "discussions"))
WEB_KINDS = frozenset(("issues", "pull", "pulls", "discussions", "pulse", "labels", "milestone",
                       "milestones", "projects"))
API_VALUE_FLAGS = frozenset(("-X", "--method", "-H", "--header", "-f", "--raw-field", "-F", "--field",
                             "-q", "--jq", "-t", "--template", "--hostname", "--input", "-p",
                             "--preview", "--cache"))
FEEDERS = frozenset(("xargs", "parallel"))
FETCHERS = frozenset(("curl", "wget", "http", "https", "xh", "aria2c", "lynx", "w3m", "links", "elinks"))
# What may stand before a command's own name in a simple command.
PREFIXES = frozenset(("env", "sudo", "doas", "command", "builtin", "exec", "time", "nohup", "nice",
                      "ionice", "timeout", "stdbuf", "caffeinate", "unbuffer", "xargs", "parallel",
                      "noglob", "nocorrect", "arch", "if", "then", "else", "elif", "do", "while", "until",
                      "!", "{"))
ASSIGN = re.compile(r"[A-Za-z_][A-Za-z0-9_]*=")
HOST = re.compile(
    r"(?:[a-z][a-z0-9+.-]*:)?/{0,3}(?:[^/@\s]*@)?(?P<sub>(?:[a-z0-9-]+\.)*)"
    r"(?P<dom>github\.com|githubusercontent\.com)\.?(?::[0-9]*)?(?P<path>[/?#].*)?")
SCHEME = re.compile(r"[a-z][a-z0-9+.-]*://")
MENTION = re.compile(r"(?<![a-z0-9_.+-])gh(?![a-z0-9_+-])|github")
OPAQUE = "\x00"  # stands for text only the shell knows: a substitution's output


class ParseError(Exception):
    pass


def fold(s):
    return unicodedata.normalize("NFC", s).lower()


def reader_hint():
    here = os.path.dirname(os.path.abspath(__file__))
    for p in (os.path.join(here, "issue-read.py"), os.path.join(os.path.dirname(here), "issue-read.py")):
        if os.path.isfile(p):
            return 'python3 -I "%s"' % p
    return "python3 -I scripts/issue-read.py (from the buddy-system checkout)"


def refusal(kind):
    hint = reader_hint()
    if kind == "read":
        return ("ISSUE-AUTHOR gate: GitHub issue and pull-request text is read only through the "
                "issue reader, which prints only what the authenticated gh user wrote: %s <number> "
                "| --list [--repo OWNER/REPO]. gh issue/pr writes (create, comment, close, edit, "
                "reopen; pr ready, merge) pass; their reads, gh search/status, gh api on issue, PR, comment "
                "or search paths, and GitHub issue or PR URLs do not.\n" % hint)
    if kind == "unknown":
        return ("ISSUE-AUTHOR gate: this gh command is not one the gate knows reads no issue or PR "
                "text (an alias, an extension, a root flag, an argument spelled through a variable "
                "or substitution, or a command the gate does not list); spell it out, or read issues "
                "through %s <number> | --list.\n" % hint)
    return ("ISSUE-AUTHOR gate: this command names gh or GitHub and the gate could not read it "
            "(an unterminated quote or substitution, or nesting past %d levels), so it refuses: a "
            "command it cannot read could be an issue read. Issues: %s <number> | --list.\n"
            % (DEPTH_CAP, hint))


# ---------------------------------------------------------------- the lexer


class Lexer:
    """Split shell text into simple commands (lists of words). Substitutions,
    shell -c operands, eval/trap text and shell-fed here-documents are lexed
    as commands of their own, appended to the same output."""

    def __init__(self, depth=0):
        if depth > DEPTH_CAP:
            raise ParseError("depth")
        self.depth = depth
        self.out = []
        self.piped = set()    # ids of commands whose output a `|` sends on

    def sub(self, text):
        lx = Lexer(self.depth + 1)
        lx.run(text, 0, None)
        self.out.extend(lx.out)

    # Lex text[i:] until `stop` (")" or "`") at this level, or the end;
    # return the index just past the stop.
    def run(self, text, i, stop):
        n = len(text)
        seg, line_segs = [], []
        word, quoted, inword = [], False, False
        pending = []          # here-documents waiting for their line's end
        bodies = []           # bodies read, waiting for their pipeline's end
        redir = None          # None, "target" or "delim"
        delim_strip = False
        pd = 0
        case = 0              # open `case` commands: their `)` closes a pattern

        def flush():
            nonlocal word, quoted, inword, redir
            if inword:
                w = "".join(word)
                if redir == "delim":
                    pending.append((w, quoted, delim_strip, line_segs, False, seg))
                elif redir == "string":
                    pending.append((w, True, False, line_segs, True, seg))
                elif redir != "target":
                    seg.append(w)
                redir = None
            word, quoted, inword = [], False, False

        def end_seg():
            nonlocal seg, case
            flush()
            if seg and seg[0] == "case":
                case += 1
            case -= sum(1 for w in seg if w == "esac")
            case = max(case, 0)
            if seg:
                self.out.append(seg)
                line_segs.append(seg)
            seg = []

        while i < n:
            c = text[i]
            if stop == "`" and c == "`":
                end_seg()
                return i + 1
            if c in " \t":
                flush()
                i += 1
            elif c == "\n":
                end_seg()
                i = self.heredocs(text, i + 1, pending, bodies)
                pending = []
                # A line ending in `|` goes on past its here-documents' bodies
                # (`cat <<EOF |` ... `EOF` then `sh`): who reads them is
                # known only where the pipeline ends.
                if not (line_segs and id(line_segs[-1]) in self.piped):
                    self.resolve(bodies, line_segs)
                    self.piped_to_shell(line_segs)
                    bodies, line_segs = [], []
            elif c == "\\":
                if i + 1 < n and text[i + 1] == "\n":
                    i += 2
                    continue
                if i + 1 < n:
                    word.append(text[i + 1])
                inword = quoted = True
                i += 2
            elif c == "'":
                j = text.find("'", i + 1)
                if j < 0:
                    raise ParseError("quote")
                word.append(text[i + 1:j])
                inword = quoted = True
                i = j + 1
            elif c == '"':
                s, i = self.dquote(text, i + 1, '"')
                word.append(s)
                inword = quoted = True
            elif c == "$" and i + 1 < n and text[i + 1] == "'":
                s, i = ansi_c(text, i + 2)
                word.append(s)
                inword = quoted = True
            elif c == "$" and i + 1 < n and text[i + 1] == '"':
                s, i = self.dquote(text, i + 2, '"')
                word.append(s)
                inword = quoted = True
            elif c == "$" and i + 1 < n and text[i + 1] == "$":
                word.append("$$")  # the pid: `$$'` opens no $'...'
                inword = True
                i += 2
            elif c == "$" and i + 1 < n and text[i + 1] == "(":
                lx = Lexer(self.depth + 1)
                i = lx.run(text, i + 2, ")")
                self.out.extend(lx.out)
                word.append(OPAQUE)
                inword = True
            elif c == "`":
                i = self.backtick(text, i + 1)
                word.append(OPAQUE)
                inword = True
            elif c == "#" and not inword and (i == 0 or text[i - 1] in " \t\n;&|"):
                # A comment only where a word may start after a blank or an
                # operator: `[[ $l =~ (#|$) ]]` holds a regex, not a comment.
                j = text.find("\n", i)
                i = n if j < 0 else j
            elif c == "&" and i + 1 < n and text[i + 1] == ">":
                # &> and &>>: both streams to a file, not a separator.
                flush()
                i += 2
                while i < n and text[i] in ">|!":
                    i += 1
                redir = "target"
            elif c in ";&|":
                end_seg()
                if text.startswith("||", i) or text.startswith("&&", i) or text.startswith(";;", i):
                    i += 2
                else:
                    # One `|` (or `|&`) pipes this command into the next.
                    if c == "|" and line_segs:
                        self.piped.add(id(line_segs[-1]))
                    i += 2 if text.startswith("|&", i) else 1
            elif c == "(":
                end_seg()
                pd += 1
                i += 1
            elif c == ")":
                end_seg()
                i += 1
                if pd == 0 and stop == ")" and case == 0:
                    return i
                pd = max(pd - 1, 0)
            elif c in "<>" and i + 1 < n and text[i + 1] == "(":
                # <(...) and >(...): a process substitution, run like $(...).
                lx = Lexer(self.depth + 1)
                i = lx.run(text, i + 2, ")")
                self.out.extend(lx.out)
                word.append(OPAQUE)
                inword = True
            elif c in "<>":
                # An all-digit word right before the operator is its fd.
                if inword and not quoted and "".join(word).isdigit():
                    word, inword = [], False
                flush()
                if text.startswith("<<<", i):
                    i += 3
                    redir = "string"
                elif text.startswith("<<", i):
                    i += 2
                    delim_strip = i < n and text[i] == "-"
                    if delim_strip:
                        i += 1
                    redir = "delim"
                else:
                    i += 1
                    # zsh's >! and >>! clobber; the ! is the operator's.
                    while i < n and text[i] in "<>&|!":
                        i += 1
                    redir = "target"
            else:
                word.append(c)
                inword = True
                i += 1
        if stop is not None:
            raise ParseError("unterminated substitution")
        end_seg()
        if pending:
            self.heredocs(text, n, pending, bodies)
        self.resolve(bodies, line_segs)
        self.piped_to_shell(line_segs)
        return n

    def backtick(self, text, i):
        """Lex a `...` substitution from text[i:] (just past the backquote) the
        way sh does: the span runs to the next unescaped backquote, quotes or
        not, and \\, \` and \$ inside it are unescaped before it is read."""
        n, buf = len(text), []
        while i < n:
            ch = text[i]
            if ch == "\\" and i + 1 < n and text[i + 1] in "\\`$":
                buf.append(text[i + 1])
                i += 2
            elif ch == "`":
                self.sub("".join(buf))
                return i + 1
            else:
                buf.append(ch)
                i += 1
        raise ParseError("unterminated substitution")

    def piped_to_shell(self, line_segs):
        """`echo 'gh issue view 1' | sh`: a shell reading its commands from a
        pipe runs what the commands before it print, so their words that name
        gh are read as commands (speculatively: one that does not lex is data)."""
        for k in range(1, len(line_segs)):
            if id(line_segs[k - 1]) in self.piped and reads_stdin(line_segs[k]):
                j = k - 1
                while j >= 0 and (j == k - 1 or id(line_segs[j]) in self.piped):
                    for w in line_segs[j]:
                        if mentions(w):
                            try:
                                self.sub(w)
                            except ParseError:
                                pass
                    j -= 1

    def dquote(self, text, i, term):
        """Read a double-quoted string (term '"') or an unquoted here-document
        body (term None) from text[i:]; substitutions inside are lexed."""
        n, buf = len(text), []
        while i < n:
            c = text[i]
            if term is not None and c == term:
                return "".join(buf), i + 1
            if c == "\\" and i + 1 < n:
                nxt = text[i + 1]
                if nxt == "\n":
                    i += 2
                    continue
                if nxt in '$`"\\':
                    buf.append(nxt)
                    i += 2
                    continue
                buf.append(c)
                i += 1
            elif c == "$" and i + 1 < n and text[i + 1] == "(":
                lx = Lexer(self.depth + 1)
                i = lx.run(text, i + 2, ")")
                self.out.extend(lx.out)
                buf.append(OPAQUE)
            elif c == "`":
                i = self.backtick(text, i + 1)
                buf.append(OPAQUE)
            else:
                buf.append(c)
                i += 1
        if term is not None:
            raise ParseError("quote")
        return "".join(buf), n

    def heredocs(self, text, i, pending, bodies):
        """Consume each pending here-document's body from text[i:]; return
        the index after the last one. A body with no terminator runs to the
        end, as sh reads it."""
        n = len(text)
        for delim, quoted, strip, line_segs, herestring, hseg in pending:
            found = True
            if herestring:
                # A here-string's word is its body; its substitutions were
                # lexed where it stood.
                btext = delim
            else:
                body, found = [], False
                while i < n:
                    j = text.find("\n", i)
                    line = text[i:] if j < 0 else text[i:j]
                    i = n if j < 0 else j + 1
                    # Unquoted, sh joins a backslash-newline first, in the
                    # body and in the closing delimiter (EO\<newline>F ends EOF).
                    while not quoted and i < n and odd_backslashes(line):
                        j = text.find("\n", i)
                        line = line[:-1] + (text[i:] if j < 0 else text[i:j])
                        i = n if j < 0 else j + 1
                    if (line.lstrip("\t") if strip else line) == delim and OPAQUE not in delim:
                        found = True
                        break
                    body.append(line)
                btext = "\n".join(body)
                if not quoted:
                    self.dquote(btext, 0, None)
            bodies.append((btext, hseg, found))
        return i

    def resolve(self, bodies, line_segs):
        for btext, hseg, found in bodies:
            # Who reads the body: the command it is attached to, and every
            # command that command pipes into, on its line.
            chain = []
            for s in line_segs:
                if chain or s is hseg:
                    chain.append(s)
                    if id(s) not in self.piped:
                        break
            if not chain:
                chain = [hseg]
            if not found:
                # No terminator: sh reads to the end, and so may a `<<` this
                # lexer took for one where the shell did not (`(( 1 << 2 ))`,
                # a delimiter spelled by a substitution). Either way the rest
                # is read as commands.
                self.sub(btext)
            elif any(reads_stdin(s) for s in chain):
                self.sub(btext)
            elif any(first_word(s) in FETCHERS for s in chain):
                # curl -K - <<EOF reads its URL from the body: every GitHub
                # URL in it is judged as a word.
                for tok in re.split(r"[\s'\"=<>]+", btext):
                    if "github" in fold(tok):
                        self.out.append([tok])


def first_word(seg):
    """The command's own name: past assignments and prefixes."""
    for w in seg:
        if not (ASSIGN.match(w) or base(w) in PREFIXES or w.startswith("-")):
            return base(w)
    return ""


def reads_stdin(seg):
    """A shell (or `.`/`source` of /dev/stdin) that takes its commands from
    stdin: no script operand, or -s."""
    k = 0
    while k < len(seg) and (ASSIGN.match(seg[k]) or base(seg[k]) in PREFIXES):
        k += 1
    if k >= len(seg):
        return False
    b, rest = base(seg[k]), seg[k + 1:]
    if b in (".", "source"):
        return any(w in ("/dev/stdin", "/dev/fd/0", "-") for w in rest)
    if b not in SHELLS:
        return False
    return "-s" in rest or not any(not w.startswith("-") for w in rest)


def odd_backslashes(line):
    return (len(line) - len(line.rstrip("\\"))) % 2 == 1


def ansi_c(text, i):
    """Decode a $'...' string from text[i:] (just past the quote)."""
    esc = {"n": "\n", "t": "\t", "r": "\r", "a": "\a", "b": "\b", "f": "\f", "v": "\v",
           "e": "\x1b", "E": "\x1b", "\\": "\\", "'": "'", '"': '"', "?": "?"}
    n, buf = len(text), []
    while i < n:
        c = text[i]
        if c == "'":
            # The shells end the word's text at a NUL: $'gh\0zz' is gh.
            return "".join(buf).split("\x00", 1)[0], i + 1
        if c == "\\" and i + 1 < n:
            d = text[i + 1]
            if d in esc:
                buf.append(esc[d])
                i += 2
            elif d == "x" and re.match(r"\{[0-9A-Fa-f]+\}", text[i + 2:]):
                m = re.match(r"\{([0-9A-Fa-f]+)\}", text[i + 2:])
                buf.append(chr(min(int(m.group(1), 16), 0x10FFFF)))
                i += 2 + len(m.group(0))
            elif d == "x":
                m = re.match(r"[0-9A-Fa-f]{1,2}", text[i + 2:])
                buf.append(chr(int(m.group(0), 16)) if m else "\\x")
                i += 2 + (len(m.group(0)) if m else 0)
            elif d in "uU":
                m = re.match(r"[0-9A-Fa-f]{1,%d}" % (4 if d == "u" else 8), text[i + 2:])
                buf.append(chr(min(int(m.group(0), 16), 0x10FFFF)) if m else "\\" + d)
                i += 2 + (len(m.group(0)) if m else 0)
            elif d in "01234567":
                m = re.match(r"[0-7]{1,3}", text[i + 1:])
                buf.append(chr(int(m.group(0), 8) & 0xFF))
                i += 1 + len(m.group(0))
            else:
                buf.append("\\" + d)
                i += 2
        else:
            buf.append(c)
            i += 1
    raise ParseError("quote")


def base(w):
    return fold(w.rsplit("/", 1)[-1])


def lex(text):
    lx = Lexer(0)
    lx.run(text, 0, None)
    # Text a shell certainly runs, carried as a word: a -c operand after a
    # shell, eval's words, trap's first operand. Walk the list as it grows, so
    # what these yield is read the same way, bounded by the depth cap.
    segs, k = lx.out, 0
    depth_of = {}
    while k < len(segs):
        seg, d = segs[k], depth_of.get(k, 0)
        texts = []
        for idx, w in enumerate(seg):
            b = base(w)
            nxt = next((x for x in seg[idx + 1:] if not x.startswith("-")), None)
            if b == "eval":
                texts.append(" ".join(seg[idx + 1:]))
            elif b == "trap" and nxt is not None:
                texts.append(nxt)
            elif (w.startswith("-") and not w.startswith("--") and "c" in w[1:]
                  and nxt is not None and any(base(x) in SHELLS for x in seg[:idx])):
                # sh -c -e 'cmd', bash -c -- 'cmd': the first operand.
                texts.append(nxt)
            elif w in ("-S", "--split-string") and base(seg[0]) == "env" and idx + 1 < len(seg):
                texts.append(seg[idx + 1])
            elif w.startswith("--split-string=") and base(seg[0]) == "env":
                texts.append(w.split("=", 1)[1])
        for t in texts:
            if d + 1 > DEPTH_CAP:
                raise ParseError("depth")
            sub = Lexer(d + 1)
            sub.run(t, 0, None)
            for s in sub.out:
                depth_of[len(segs)] = d + 1
                segs.append(s)
        k += 1
    return segs


# ---------------------------------------------------------------- the verdict


def norm_url(s):
    """What a URL parser makes of the text: NFKC (fullwidth letters), case,
    backslashes as slashes, tabs and newlines dropped, percent-decoding, and
    curl's {...} glob braces dropped."""
    s = unicodedata.normalize("NFKC", s).lower()
    s = re.sub(r"[\t\r\n{}]", "", s.replace("\\", "/"))
    return urllib.parse.unquote(s).strip()


def segments_of(path):
    return [x for x in re.split(r"[/?#&]+", norm_url(path)) if x]


def named(seg, names):
    return seg in names or seg.split(".", 1)[0] in names


def denied_segment(path):
    """An API path: a denied segment before any segment that starts a file
    path (`repos/o/r/contents/docs/search.md` names a file)."""
    for x in segments_of(path):
        if x in API_FILES:
            return False
        if named(x, DENY_SEGMENTS):
            return True
    return False


def denied_url(sub, dom, path):
    segs = segments_of(path or "")
    if any(x.endswith(".atom") for x in segs):
        return True
    if dom == "githubusercontent.com":
        # Repository files, avatars, release assets; a PR's patch is not.
        return sub == "patch-diff." and denied_segment(path or "")
    if sub.startswith("api."):
        return denied_segment(path or "")
    if not segs:
        return False
    return segs[0] in WEB_TOP or (len(segs) >= 3 and named(segs[2], WEB_KINDS))


def url_refused(word, anywhere=False):
    """True when the word IS a GitHub URL naming issue or PR text. A word
    that is itself a URL (or WebFetch's url, anywhere=True) is searched
    through, so a proxy or redirector wrapping the GitHub URL is read too."""
    w = norm_url(word)
    cands = [w]
    if "=" in w:
        cands.append(w.split("=", 1)[1])
    # A word with no space that holds :// is a URL, perhaps wrapping one
    # (`r.jina.ai/https://github.com/...`); a sentence citing a URL is not.
    if anywhere or SCHEME.match(w) or ("://" in w and not re.search(r"\s", word)):
        cands += [w[m.start():] for m in re.finditer(r"(?<![a-z0-9-])(?:[a-z0-9-]+\.)*github(?:usercontent)?\.com", w)]
    for c in cands:
        m = HOST.match(c)
        if m and denied_url(m.group("sub"), m.group("dom"), m.group("path")):
            return True
    return False


def gh_verdict(seg, i):
    """Judge the gh word at seg[i]; None to allow, else a refusal kind."""
    args = seg[i + 1:]
    # xargs and parallel append words the gate cannot see: `echo view 1 |
    # xargs gh issue` runs a read, whatever is spelled here.
    if any(base(x) in FEEDERS for x in seg[:i]):
        return "unknown"
    if not args:
        return None
    a0 = args[0]
    if a0.startswith("-"):
        # Alone only: `gh --help --help=false issue view 1` runs the read.
        return None if a0 in ("--help", "-h", "--version") and len(args) == 1 else "unknown"
    if a0 in WRITES:
        rest = args[1:]
        if not rest or (len(rest) == 1 and rest[0] in ("--help", "-h")):
            return None
        if len(rest) == 2 and rest[1] in ("--help", "-h") and not rest[0].startswith("-"):
            return None  # gh issue view --help: help, not the read
        return None if rest[0] in WRITES[a0] else "read"
    if a0 == "api":
        # The endpoint is a positional; a flag's value (`--jq '"$" + .x'`)
        # is not one.
        pos, k = [], 1
        while k < len(args):
            if args[k] in API_VALUE_FLAGS:
                k += 2
                continue
            if not args[k].startswith("-"):
                pos.append(args[k])
            k += 1
        for a in pos:
            # {branch} is the current branch's name, which the gate cannot see.
            if "$" in a or "`" in a or OPAQUE in a or "{branch}" in a:
                return "unknown"
        return "read" if any(denied_segment(a) for a in pos) else None
    if a0 in ("extension", "ext") and len(args) > 1 and args[1] == "exec":
        # exec runs an extension by name: only stack is known.
        return None if len(args) > 2 and args[2] == "stack" else "unknown"
    if a0 == "browse" and any(re.match(r"(?:GH_)?BROWSER=", w) for w in seg[:i]):
        # GH_BROWSER='curl -sL' gh browse 5 prints the issue page.
        return "unknown"
    if a0 in ALLOWED_TOP:
        return None
    return "read" if a0 in READS_TOP else "unknown"


def command_position(seg, i):
    """True when seg[i] may be the command: only assignments before it, or
    a prefix (env, sudo, timeout, xargs, if, ...) first, whose options and
    their values (`sudo -u x`) are not read as a command name."""
    k = 0
    while k < i and ASSIGN.match(seg[k]):
        k += 1
    if k == i:
        return True
    if base(seg[k]) not in PREFIXES:
        return False
    # Past the first prefix, only more prefixes, assignments, flags, counts
    # and a short flag's value (`sudo -u x`, `timeout 5s`, `xargs -I {}`);
    # `command curl -s -o gh ...` is curl's.
    for j in range(k + 1, i):
        w = seg[j]
        if not (base(w) in PREFIXES or ASSIGN.match(w) or w.startswith("-")
                or re.fullmatch(r"[0-9.]+[smhd]?", w)
                or (len(seg[j - 1]) == 2 and seg[j - 1].startswith("-"))):
            return False
    return True


def judge_command(cmd):
    for seg in lex(cmd):
        operands = len(seg)
        for i, w in enumerate(seg):
            if i < operands and url_refused(w):
                return "read"
            if base(w) == "gh":
                v = gh_verdict(seg, i)
                # A gh word elsewhere (`grep -n gh README.md`) is an operand:
                # only a read spelled out after it refuses.
                if v and (v == "read" or command_position(seg, i)
                          or any(base(x) in FEEDERS for x in seg[:i])
                          or (seg[i + 1:] and (seg[i + 1].startswith("-") or seg[i + 1] in WRITES
                                                or seg[i + 1] == "api"))):
                    return v
                # An allowed write's operands are gh's: `gh issue comment
                # https://github.com/o/r/issues/1 -b x` names the issue it writes.
                if not v and command_position(seg, i) and len(seg) > i + 2 and seg[i + 1] in WRITES:
                    operands = i
    return None


def mentions(text):
    # $'...' can spell gh in escapes the deletion below cannot see.
    if "$'" in text:
        return True
    text = text.replace("\\\n", "")  # g\<newline>h is gh to a shell
    text = unicodedata.normalize("NFKC", urllib.parse.unquote(text)).lower()
    return MENTION.search(re.sub(r"['\"\\$]", "", text)) is not None


def verdict(raw):
    """Return None to allow, or a refusal kind. Raises inside jurisdiction."""
    try:
        d = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        return None
    if not isinstance(d, dict) or not isinstance(d.get("tool_input"), dict):
        return None
    tool, ti = d.get("tool_name"), d["tool_input"]
    if tool in ("Bash", "Monitor"):
        # Monitor's command runs in the same shell as Bash's.
        cmd = ti.get("command")
        if not isinstance(cmd, str) or not mentions(cmd):
            return None
        try:
            return judge_command(cmd)
        except Exception:
            return "parse"
    if tool == "WebFetch":
        url = ti.get("url")
        if not isinstance(url, str) or "github" not in norm_url(url):
            return None
        try:
            return "read" if url_refused(url, anywhere=True) else None
        except Exception:
            return "parse"
    return None


def hook():
    try:
        raw = sys.stdin.buffer.read()
    except Exception:
        sys.exit(0)
    try:
        kind = verdict(raw)
    except Exception:
        kind = "parse"
    if kind:
        sys.stderr.write(refusal(kind))
        sys.exit(2)
    sys.exit(0)


# ---------------------------------------------------------------- self-test


def self_test():
    """Run THIS file as the harness does, one child per payload, and check its
    exit code and stderr. Each refusal sits beside its positive control, a
    write or a quoted mention of the same words, which must pass: a gate that
    also refused the control would be refusing gh, not the read."""
    import subprocess

    def bash(c):
        return json.dumps({"tool_name": "Bash", "tool_input": {"command": c}})

    def fetch(u):
        return json.dumps({"tool_name": "WebFetch", "tool_input": {"url": u, "prompt": "p"}})

    def run(payload):
        p = subprocess.run([sys.executable, "-I", "-B", __file__], input=payload.encode("utf-8"),
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        return p.returncode, p.stdout.decode("utf-8", "replace"), p.stderr.decode("utf-8", "replace")

    # (name, payload, refusal kind or None, its control's name or "")
    R, U, P = "read", "unknown", "parse"
    cases = [
        ("issue view", bash("gh issue view 12"), R),
        ("control: issue comment", bash("gh issue comment 12 --body 'done'"), None),
        ("issue view --comments", bash("gh issue view 12 --comments"), R),
        ("issue list", bash("gh issue list --state open"), R),
        ("control: issue create", bash("gh issue create --title t --body b"), None),
        ("issue status", bash("gh issue status"), R),
        ("issue develop (a read gh may add more of)", bash("gh issue develop 3 --list"), R),
        ("control: issue close/edit/reopen", bash("gh issue close 3 && gh issue edit 3 --add-label x && gh issue reopen 3"), None),
        ("pr view", bash("gh pr view 1"), R),
        ("pr diff", bash("gh pr diff 1"), R),
        ("pr checkout", bash("gh pr checkout 1"), R),
        ("pr checks", bash("gh pr checks 1"), R),
        ("control: pr create, ready", bash("gh pr create --fill && gh pr ready 4"), None),
        ("flag before the subcommand", bash("gh issue -R comment view 5"), R),
        ("control: issue help", bash("gh issue --help"), None),
        ("redirect target hides the subcommand", bash("gh issue >comment view 1"), R),
        ("control: redirect after a write", bash("gh issue comment 1 -b x >/dev/null 2>&1"), None),
        ("absolute path, env prefix", bash("X=1 /opt/homebrew/bin/gh issue view 1"), R),
        ("case-folded GH", bash("GH issue view 1"), R),
        ("quoted gh", bash("'g'h issue view 1"), R),
        ("escaped gh", bash("g\\h issue view 1"), R),
        ("ANSI-C gh", bash("$'gh' issue view 1"), R),
        ("after cd &&", bash("cd x && gh issue view 1"), R),
        ("second gh word judged", bash("gh issue comment 1 -b x; gh issue view 1"), R),
        ("newline-separated", bash("echo hi\ngh issue view 1"), R),
        ("subshell", bash("(gh pr view 1)"), R),
        ("process substitution", bash("cat <(gh issue view 1)"), R),
        ("control: a redirect, then a write", bash("cat </dev/null; gh issue comment 1 -b x"), None),
        ("a case pattern's paren inside a substitution", bash('echo "$(case x in a) gh issue view 1;; esac)"'), R),
        ("ANSI-C escapes spelling gh", bash("$'g\\x68' issue view 1"), R),
        ("brace group", bash("{ gh pr view 1; }"), R),
        ("unquoted substitution", bash("echo $(gh issue view 1)"), R),
        ("quoted substitution", bash('echo "$(gh issue view 1)"'), R),
        ("backticks", bash("echo `gh issue view 1`"), R),
        ("mid-word hash is not a comment", bash("echo a#b; gh issue view 1"), R),
        ("control: a comment", bash("ls # gh issue view 1"), None),
        ("sh -c", bash("sh -c 'gh issue view 1'"), R),
        ("bash -lc", bash("bash -lc \"gh pr view 1\""), R),
        ("eval", bash("eval 'gh issue view 1'"), R),
        ("trap", bash("trap 'gh issue view 1' EXIT"), R),
        ("xargs sh -c", bash("echo 1 | xargs -I{} sh -c 'gh issue view {}'"), R),
        ("xargs feeding a bare gh", bash("echo issue view 1 | xargs gh"), U),
        ("control: which gh", bash("which gh && command -v gh >/dev/null"), None),
        ("heredoc fed to a shell", bash("bash <<'EOF'\ngh issue view 1\nEOF"), R),
        ("heredoc piped to a shell", bash("cat <<'EOF' | sh\ngh issue view 1\nEOF"), R),
        ("unquoted heredoc substitution", bash("cat <<EOF\n$(gh issue view 1)\nEOF"), R),
        ("control: quoted heredoc naming it", bash("cat > x <<'EOF'\nnever gh issue view; don't\nEOF"), None),
        ("control: commit heredoc with an apostrophe",
         bash("git commit -q -m \"$(cat <<'EOF'\nthe gate's reader, not gh issue view\nEOF\n)\""), None),
        ("control: grep for the raw form", bash('grep -rn "gh issue view" skills/'), None),
        ("control: commit message naming it", bash("git commit -m 'never gh issue view'"), None),
        ("search", bash("gh search issues foo"), R),
        ("status", bash("gh status"), R),
        ("discussion", bash("gh discussion list"), R),
        ("project", bash("gh project item-list 1"), R),
        ("alias co", bash("gh co 5"), R),
        ("an alias or unknown command", bash("gh iv 5"), U),
        ("command through a variable", bash("gh $CMD 5"), U),
        ("root flag", bash("gh -R o/r issue view 1"), U),
        ("help cancelled after the gate allowed it", bash("gh --help --help=false issue view 1"), U),
        ("line continuation inside gh", bash("g\\\nh issue view 1"), R),
        ("a here-string is stdin, not argv", bash('gh issue <<<"comment" view 1'), R),
        ("a here-string fed to a shell", bash("bash <<< 'gh issue view 1'"), R),
        ("a continued here-document delimiter", bash("bash -c 'cat <<EOF\nEO\\\nF\ngh issue view 1\n'"), R),
        ("control: a quoted delimiter is not continued", bash("cat <<'EOF'\nEO\\\nF\ngh issue view 1\nEOF"), None),
        ("curl config from a here-document", bash("curl --config - <<'EOF'\nurl = \"https://api.github.com/repos/o/r/issues/1\"\nEOF"), R),
        ("curl config from a here-string", bash("curl -K - <<< 'url=https://github.com/o/r/issues/1'"), R),
        ("control: a commit here-document citing an issue URL", bash("git commit -F - <<'EOF'\nfixes https://github.com/o/r/issues/5\nEOF"), None),
        ("control: grep for a quoted gh", bash("grep -n 'gh' README.md && grep -rn gh scripts/"), None),
        ("an operand gh spelling out a read", bash("find . -exec gh issue view 1 \\;"), R),
        ("command position after prefixes", bash("sudo -u x timeout 5 gh api \"$EP\""), U),
        ("control: a write naming its issue by URL", bash("gh issue comment https://github.com/o/r/issues/1 --body done"), None),
        ("a read naming its issue by URL", bash("gh issue view https://github.com/o/r/issues/1"), R),
        ("Monitor's command", json.dumps({"tool_name": "Monitor", "tool_input": {"command": "gh issue view 1 --comments"}}), R),
        ("&> splits nothing", bash("gh issue &>out view 1; cat out"), R),
        ("&>> splits nothing", bash("gh &>>out issue view 1"), R),
        ("zsh >!", bash("gh issue >! comment view 1"), R),
        ("arithmetic << is not a here-document", bash("(( x = 1 << 2 ))\ngh issue view 1"), R),
        ("a delimiter spelled by a substitution", bash("cat <<$(x)\nbody\n$(x)\ngh issue view 1"), R),
        ("escaped backquotes nest", bash("echo `echo \\`gh issue view 1\\``"), R),
        ("escaped $ in backquotes", bash('echo `echo "\\$(gh issue view 1)"`'), R),
        ("a NUL ends $'...'", bash("$'gh\\0zz' issue view 1"), R),
        ("$'\\x{67}'", bash("$'\\x{67}h' issue view 1"), R),
        ("$$' opens no ANSI-C string", bash("echo $$'\\' ; gh issue view 1 ; echo 'x'\\'"), R),
        ("xargs finishing a subcommand", bash("echo view 1 | xargs gh issue"), U),
        ("xargs finishing an api path", bash("echo repos/o/r/issues/1 | xargs -n1 gh api"), U),
        ("noglob xargs gh", bash("echo issue view 1 | noglob xargs gh"), U),
        ("piped into a shell", bash("echo 'gh issue view 1' | sh"), R),
        ("piped into bash -s through tee", bash("echo 'gh pr view 1' | tee /dev/null | bash -s"), R),
        ("here-document piped on to a shell", bash("cat <<'EOF' |\ngh issue view 1\nEOF\nsh"), R),
        ("source /dev/stdin", bash(". /dev/stdin <<'EOF'\ngh issue view 1\nEOF"), R),
        ("bash -c -- operand", bash("bash -c -- 'gh issue view 1'"), R),
        ("sh -c -e operand", bash("sh -c -e 'gh issue view 1'"), R),
        ("trap -- operand", bash("trap -- 'gh issue view 1' EXIT"), R),
        ("env -S", bash("env -S 'gh issue view 1'"), R),
        ("csh -c", bash("csh -c 'gh issue view 1'"), R),
        ("g$\"h\"", bash('g$"h" issue view 1'), R),
        ("percent-encoded host", bash("curl -s https://api.git%68ub.com/repos/o/r/issues/1"), R),
        ("curl glob braces", bash("curl -s 'https://api.github.com/repos/o/r/{issues}/5'"), R),
        ("single-slash scheme", bash("curl -s https:/api.github.com/repos/o/r/issues/5"), R),
        ("trailing-dot host", bash("curl -s https://api.github.com./repos/o/r/issues/5"), R),
        ("proxy URL without a scheme", bash("curl -sL r.jina.ai/https://github.com/o/r/issues/5"), R),
        ("pulse, labels, milestone pages", fetch("https://github.com/o/r/pulse"), R),
        ("a label's issue list", fetch("https://github.com/o/r/labels/bug"), R),
        ("a user's atom feed", fetch("https://github.com/someuser.atom"), R),
        ("received_events", bash("gh api users/x/received_events"), R),
        ("hook deliveries", bash("gh api repos/o/r/hooks/1/deliveries/2"), R),
        ("WebFetch fullwidth host", fetch("https://\uff47\uff49\uff54\uff48\uff55\uff42.com/o/r/issues/5"), R),
        ("WebFetch backslashes", fetch("https://github.com\\o\\r\\issues\\5"), R),
        ("WebFetch a tab in the path", fetch("https://github.com/o/r/iss\tues/5"), R),
        ("WebFetch percent-encoded host", fetch("https://git%68ub.com/o/r/issues/5"), R),
        ("extension exec", bash("gh extension exec dash issue view 1"), U),
        ("control: extension exec stack", bash("gh ext exec stack view"), None),
        ("operand URL after a gh that is not the command", bash("command curl -s -o gh issue comment https://api.github.com/repos/o/r/issues/1"), R),
        ("help cancelled, not in command position", bash("noglob gh --help --help=false issue view 1"), U),
        ("control: a commit here-document beside a shell run", bash("sh scripts/install-skill.sh && git commit -q -F - <<'EOF'\nThe gate's reader, never gh issue view\nEOF"), None),
        ("control: repository paths named like denied ones", bash("git clone https://github.com/graphql/graphql-js && go get github.com/aws/aws-lambda-go/events && curl -sL https://raw.githubusercontent.com/o/r/main/src/search.py"), None),
        ("control: blob and tree pages", fetch("https://github.com/o/r/blob/main/internal/buddylist/events.go"), None),
        ("control: a tree named projects", fetch("https://github.com/o/r/tree/main/projects"), None),
        ("control: api contents path", bash("gh api repos/o/r/contents/docs/search.md"), None),
        ("control: a quoted $ in --jq", bash("gh api user --jq '\"$\" + .login'"), None),
        ("control: pr merge, view --help", bash("gh pr merge 5 --squash --delete-branch && gh issue view --help"), None),
        ("control: which, brew, ls of gh's config", bash("which gh jq; brew install gh jq; ls -la ~/.config/gh ~/.config/git; command -v gh >/dev/null || echo install gh first"), None),
        ("control: a write whose body cites an issue URL", bash("gh issue comment 12 --body https://github.com/o/r/issues/7"), None),
        ("a browser that prints the page", bash("GH_BROWSER='curl -sL' gh browse 5"), U),
        ("control: gh browse", bash("gh browse 5"), None),
        ("a regex's (# is not a comment", bash("[[ $l =~ ^[[:space:]]*(#|$) ]] || gh issue view 1"), R),
        ("api {branch} placeholder", bash("gh api 'repos/{owner}/{repo}/{branch}/5'"), U),
        ("control: api {owner}/{repo}", bash("gh api 'repos/{owner}/{repo}/releases'"), None),
        ("control: gh version, help, repo, run, stack", bash("gh --version; gh help; gh repo view; gh run list; gh stack view"), None),
        ("api issues", bash("gh api repos/o/r/issues/1"), R),
        ("api comments with flags first", bash("gh api -X GET --paginate repos/o/r/issues/comments"), R),
        ("api pulls", bash("gh api /repos/{owner}/{repo}/pulls"), R),
        ("api search", bash("gh api search/issues -f q=x"), R),
        ("api graphql", bash("gh api graphql -f query='{viewer{login}}'"), R),
        ("api percent-encoded", bash("gh api repos/o/r/iss%75es/1"), R),
        ("api upper-case", bash("gh api repos/o/r/ISSUES/1"), R),
        ("api run logs", bash("gh api repos/o/r/actions/runs/9/logs"), R),
        ("api endpoint in a variable", bash('gh api "$EP"'), U),
        ("control: api user, contents", bash("gh api user --jq .id && gh api repos/o/r/contents/README.md"), None),
        ("curl an issue URL", bash("curl -s https://api.github.com/repos/o/r/issues/1"), R),
        ("curl without a scheme", bash("curl github.com/o/r/pull/5.diff"), R),
        ("an atom feed", bash("curl https://github.com/o/r/issues.atom"), R),
        ("--url=", bash("curl --url=https://github.com/o/r/issues/5"), R),
        ("a URL wrapped in a proxy URL", bash("curl https://r.jina.ai/https://github.com/o/r/issues/5"), R),
        ("WebFetch through a proxy", fetch("https://r.jina.ai/https://github.com/o/r/issues/5"), R),
        ("control: clone, raw file", bash("git clone https://github.com/o/r && curl -s https://raw.githubusercontent.com/o/r/main/README.md"), None),
        ("control: message citing a URL", bash("git commit -m 'fixes https://github.com/o/r/issues/5'"), None),
        ("WebFetch an issue", fetch("https://github.com/JsizzleR/buddy-system/issues/5"), R),
        ("WebFetch a PR", fetch("https://github.com/o/r/pull/1/files"), R),
        ("WebFetch search", fetch("https://github.com/search?q=x&type=issues"), R),
        ("control: WebFetch a repo file", fetch("https://github.com/o/r/blob/main/scripts/issue-read.py"), None),
        ("control: WebFetch elsewhere", fetch("https://example.com/issues/1"), None),
        ("unterminated quote naming gh", bash("gh issue comment 1 -b 'x"), P),
        ("unterminated substitution naming gh", bash("echo $(gh repo view"), P),
        ("nesting past the cap", bash("sh -c \"sh -c 'sh -c \\\"sh -c gh\\\"'\" ; " + "$(" * 6 + "gh" + ")" * 6), P),
        ("control: unterminated quote, no gh", bash("echo 'x"), None),
        ("control: no mention", bash("ls -la && git status"), None),
        ("control: high, right, length", bash("echo high right length"), None),
        ("control: another tool", json.dumps({"tool_name": "Read", "tool_input": {"file_path": "gh issue view"}}), None),
        ("control: garbage stdin", "not json {", None),
        ("control: empty stdin", "", None),
        ("control: a JSON list", "[]", None),
        ("control: command not a string", json.dumps({"tool_name": "Bash", "tool_input": {"command": ["gh", "issue", "view"]}}), None),
    ]
    bad = []
    for name, payload, kind in cases:
        rc, out, err = run(payload)
        want = refusal(kind) if kind else ""
        if rc != (2 if kind else 0) or out or err != want:
            bad.append("case '%s': exit %d, stdout %d bytes, stderr %r" % (name, rc, len(out), err[:90]))
    refused = [c for c in cases if c[2]]
    # A refusal echoes nothing from the payload: no case's distinctive words.
    for kind in ("read", "unknown", "parse"):
        if "12" in refusal(kind) or "JsizzleR" in refusal(kind):
            bad.append("the %s refusal echoes payload text" % kind)
    # The settings line in the header is what the operator copies: it must parse.
    lines = [l.strip() for l in (__doc__ or "").splitlines() if l.strip().startswith('{"matcher"')]
    try:
        entry = json.loads(lines[0]) if len(lines) == 1 else None
        ok = (entry is not None and entry["matcher"] == "Bash|WebFetch|Monitor"
              and entry["hooks"][0]["command"] == 'python3 -I "$HOME/bin/issue-author-gate.py"')
    except Exception:
        ok = False
    if not ok:
        bad.append("the header's settings line is not one JSON entry running the installed script on Bash|WebFetch|Monitor")
    for b in bad:
        print("issue-author-gate self-test: FAIL %s" % b, file=sys.stderr)
    if bad:
        sys.exit(1)
    print("issue-author-gate self-test: %d cases, %d refused, %d allowed (controls)"
          % (len(cases), len(refused), len(cases) - len(refused)))


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        self_test()
    else:
        hook()
