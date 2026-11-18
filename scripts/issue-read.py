#!/usr/bin/env python3
r"""issue-read.py -- read a GitHub issue's text as the operator wrote it, and
nobody else's (D-215).

    python3 -I scripts/issue-read.py <number> [--repo OWNER/REPO]
    python3 -I scripts/issue-read.py --list [--state open|closed|all] [--repo OWNER/REPO]
    python3 -I -B scripts/issue-read.py --self-test

WHY. On a public repository anyone can open an issue or comment on one, and an
agent that reads `gh issue view` takes a stranger's words into its context next
to the operator's. The issue-author hook (scripts/hooks/issue-author-gate.py)
refuses every other way a session reads issue text and names this one; this is
where the author is checked, because a hook cannot filter output and cannot
ask GitHub who the caller is inside its 100 ms budget.

WHO COUNTS. The authenticated gh user (`gh api user`), compared by NUMERIC
account id, never by login: a login can be renamed and then registered by
someone else, an id cannot. That is invariant 6's machine user and needs no
configuration. Not the repository owner (wrong for an organisation or a
fork) and not "has push access" (a remote role taken as authority, where
invariant 4's shape is a local record). A per-repository list kept outside
every checkout is a follow-up.

WHAT IT PRINTS. For <number>: the title on one line, the state and dates, the
body, then each comment the operator wrote, numbered in order with its
timestamp, then "last allowed comment: K". Comments by anyone else are a COUNT
only: no login, no text, no position (invariant 8's shape: the room, the
count, never the words). An issue someone else opened is refused with its
number alone, no title. A pull request is refused (v1 reads issues; a PR's
review threads and diff are a follow-up). For --list: the operator's issues,
number and title, and a count of the rest.

FAILS CLOSED. Any gh failure (not installed, not logged in, a network or HTTP
error, a page that is not JSON, an id that is not an integer) prints no issue
text, one line naming the step and gh's exit code, and exits 2; a refused
issue exits 3. There is no fallback identity: a reader that could not learn
who the operator is shows nothing.

RESIDUAL. Anyone with write access can edit the operator's issue body and
comments, and REST does not say who edited them (GraphQL's `editor` would, unverified); with
the operator the repository's only collaborator, that is nobody today.
"""
import json
import re
import subprocess
import sys

REPO_RE = re.compile(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}")


class Refused(Exception):
    def __init__(self, msg, code):
        Exception.__init__(self, msg)
        self.code = code


def text_of(v):
    """A field's text, or nothing: a body that is not a string is not text."""
    return v if isinstance(v, str) else ""


def one_line(s, cap=None):
    s = " ".join(str(s).split())
    return s if cap is None or len(s) <= cap else s[:cap] + "..."


def gh_json(args, step):
    try:
        p = subprocess.run(["gh"] + args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    except FileNotFoundError:
        raise Refused("issue-read: refused: gh is not on PATH (%s)" % step, 2)
    except subprocess.TimeoutExpired:
        raise Refused("issue-read: refused: gh timed out (%s)" % step, 2)
    if p.returncode != 0:
        err = p.stderr.decode("utf-8", "replace").strip().splitlines()
        cause = one_line(err[0], 160) if err else "no message"
        raise Refused("issue-read: refused: gh failed (%s, exit %d: %s)" % (step, p.returncode, cause), 2)
    try:
        return json.loads(p.stdout.decode("utf-8"))
    except (ValueError, UnicodeDecodeError):
        raise Refused("issue-read: refused: gh's answer is not JSON (%s)" % step, 2)


def account_id(user):
    """The numeric id of a user object, or None. A bool is not an id."""
    if not isinstance(user, dict):
        return None
    i = user.get("id")
    return i if type(i) is int else None


def me():
    i = account_id(gh_json(["api", "user"], "who is the authenticated gh user"))
    if i is None:
        raise Refused("issue-read: refused: gh api user named no numeric id", 2)
    return i


def default_repo():
    d = gh_json(["repo", "view", "--json", "nameWithOwner"], "which repository this checkout is")
    r = d.get("nameWithOwner") if isinstance(d, dict) else None
    if not isinstance(r, str) or not REPO_RE.fullmatch(r):
        raise Refused("issue-read: refused: gh repo view named no OWNER/REPO", 2)
    return r


def pages(repo, path, step):
    d = gh_json(["api", "--paginate", "--slurp", "repos/%s/%s" % (repo, path)], step)
    if not isinstance(d, list) or not all(isinstance(p, list) for p in d):
        raise Refused("issue-read: refused: %s did not return pages of a list" % step, 2)
    return [x for p in d for x in p]


def read_issue(repo, number, out):
    uid = me()
    issue = gh_json(["api", "repos/%s/issues/%d" % (repo, number)], "issue #%d" % number)
    if not isinstance(issue, dict) or type(issue.get("number")) is not int:
        raise Refused("issue-read: refused: issue #%d did not come back as an issue" % number, 2)
    if "pull_request" in issue:
        raise Refused("issue-read: #%d refused: it is a pull request (this reader reads issues)" % number, 3)
    if account_id(issue.get("user")) != uid:
        raise Refused("issue-read: #%d refused: not opened by the authenticated gh user" % number, 3)
    comments = pages(repo, "issues/%d/comments?per_page=100" % number, "issue #%d's comments" % number)
    w = out.write
    w("issue #%d: %s\n" % (number, one_line(text_of(issue.get("title")))))
    w("state: %s; opened %s; updated %s; by the authenticated gh user\n"
      % (one_line(issue.get("state") or "?"), one_line(issue.get("created_at") or "?"),
         one_line(issue.get("updated_at") or "?")))
    w("--- body\n%s\n" % text_of(issue.get("body")).rstrip("\n"))
    k = others = 0
    for c in comments:
        if isinstance(c, dict) and account_id(c.get("user")) == uid:
            k += 1
            w("--- comment %d (%s)\n%s\n" % (k, one_line(c.get("created_at") or "?"), text_of(c.get("body")).rstrip("\n")))
        else:
            others += 1
    w("--- end\n")
    w("comments: %d by the authenticated gh user shown; %d by other authors withheld (count only)\n" % (k, others))
    w("last allowed comment: %d\n" % k)


def list_issues(repo, state, out):
    uid = me()
    items = pages(repo, "issues?state=%s&per_page=100" % state, "the %s issues" % state)
    mine = others = 0
    for it in items:
        if not isinstance(it, dict) or type(it.get("number")) is not int:
            others += 1
            continue
        if "pull_request" in it or account_id(it.get("user")) != uid:
            others += 1
            continue
        mine += 1
        out.write("#%d %s: %s\n" % (it["number"], one_line(it.get("state") or "?"), one_line(text_of(it.get("title")))))
    out.write("issues: %d by the authenticated gh user listed; %d issues or pull requests by others, or "
              "pull requests, withheld (count only)\n" % (mine, others))


USAGE = ("usage: issue-read.py <number> [--repo OWNER/REPO]\n"
         "       issue-read.py --list [--state open|closed|all] [--repo OWNER/REPO]\n"
         "       issue-read.py --self-test\n")


def main(argv):
    if argv in (["-h"], ["--help"]):
        sys.stdout.write(USAGE)
        return 0
    repo = number = None
    listing, state = False, "open"
    i = 0
    while i < len(argv):
        a = argv[i]
        if a in ("--repo", "--state") and i + 1 < len(argv) and not argv[i + 1].startswith("-"):
            if a == "--repo":
                repo = argv[i + 1]
            else:
                state = argv[i + 1]
            i += 2
        elif a == "--list":
            listing = True
            i += 1
        elif re.fullmatch(r"[1-9][0-9]{0,9}", a) and number is None:
            number = int(a)
            i += 1
        else:
            sys.stderr.write(USAGE + "refused: %s\n" % ("a flag needs a value" if a in ("--repo", "--state")
                                                       else "unknown argument %s" % one_line(a, 40)))
            return 2
    if listing == (number is not None):
        sys.stderr.write(USAGE + "refused: name one issue number, or --list\n")
        return 2
    if state not in ("open", "closed", "all") or (repo is not None and not REPO_RE.fullmatch(repo)):
        sys.stderr.write(USAGE + "refused: --state is open, closed or all; --repo is OWNER/REPO\n")
        return 2
    # Everything is built before anything is printed, so a failure on the
    # comments' last page prints no part of the issue.
    import io
    buf = io.StringIO()
    try:
        repo = repo or default_repo()
        if listing:
            list_issues(repo, state, buf)
        else:
            read_issue(repo, number, buf)
    except Refused as e:
        sys.stderr.write(str(e) + "\n")
        return e.code
    sys.stdout.write(buf.getvalue())
    return 0


# ---------------------------------------------------------------- self-test

FAKE_GH = r'''#!/usr/bin/env python3
import json, os, sys
a = sys.argv[1:]
mode = os.environ["FAKE_GH_MODE"]
ME = {"id": 1001, "login": "operator"}
STRANGER = {"id": 2002, "login": "stranger-login"}
# The login trap: a stranger that took the operator's old login.
IMPOSTOR = {"id": 3003, "login": "operator"}
def op(x):
    return dict(x, user=ME)
if mode == "fail" and a[:2] == ["api", "user"]:
    sys.stderr.write("HTTP 401: Bad credentials\n"); sys.exit(1)
if a[:2] == ["api", "user"]:
    print(json.dumps({"id": "1001"} if mode == "stringid" else ME)); sys.exit(0)
if a[:2] == ["repo", "view"]:
    print(json.dumps({"nameWithOwner": "o/r"})); sys.exit(0)
ep = a[-1]
if ep == "repos/o/r/issues/7":
    print(json.dumps(op({"number": 7, "title": "operator title\nsecond line", "state": "open", "body": "operator body", "created_at": "t0", "updated_at": "t1"})))
elif ep == "repos/o/r/issues/11":
    print(json.dumps(op({"number": 11, "title": "t", "body": {"not": "text"}})))
elif ep.startswith("repos/o/r/issues/11/comments"):
    print(json.dumps([[op({"body": ["not", "text"], "created_at": "c"})]]))
elif ep == "repos/o/r/issues/8":
    print(json.dumps({"number": 8, "title": "STRANGER-TITLE", "body": "STRANGER-BODY", "user": STRANGER}))
elif ep == "repos/o/r/issues/9":
    print(json.dumps({"number": 9, "title": "IMPOSTOR-TITLE", "body": "IMPOSTOR-BODY", "user": IMPOSTOR}))
elif ep == "repos/o/r/issues/10":
    print(json.dumps(op({"number": 10, "title": "PR-TITLE", "pull_request": {}})))
elif ep.startswith("repos/o/r/issues/7/comments"):
    if mode == "pagefail":
        sys.stderr.write("HTTP 502\n"); sys.exit(1)
    c1 = op({"body": "operator comment one", "created_at": "c1"})
    c3 = op({"body": "operator comment two", "created_at": "c3"})
    s = {"body": "STRANGER-COMMENT", "created_at": "c2", "user": STRANGER}
    i = {"body": "IMPOSTOR-COMMENT", "created_at": "c4", "user": IMPOSTOR}
    if mode == "all-operator":
        print(json.dumps([[c1], [c3]]))
    else:
        print(json.dumps([[c1, s], [c3, i]]))
elif ep.startswith("repos/o/r/issues?"):
    print(json.dumps([[op({"number": 7, "title": "operator title", "state": "open"}),
                       {"number": 8, "title": "STRANGER-TITLE", "state": "open", "user": STRANGER}],
                      [op({"number": 10, "title": "PR-TITLE", "state": "open", "pull_request": {}})]]))
else:
    sys.stderr.write("fake gh: unexpected %r\n" % a); sys.exit(1)
'''


def self_test():
    """Run THIS file against a fake gh first on PATH. Each refusal sits beside
    its positive control: a stranger's comment withheld beside an
    all-operator thread, a stranger's issue refused beside the operator's, a
    gh failure refused beside the same read succeeding."""
    import os
    import shutil
    import tempfile
    d = tempfile.mkdtemp(prefix="issue-read-selftest.")
    try:
        gh = os.path.join(d, "gh")
        with open(gh, "w") as f:
            f.write(FAKE_GH.replace("#!/usr/bin/env python3", "#!" + sys.executable, 1))
        os.chmod(gh, 0o755)

        def run(mode, *args):
            env = dict(os.environ, PATH=d + os.pathsep + os.environ.get("PATH", ""), FAKE_GH_MODE=mode)
            p = subprocess.run([sys.executable, "-I", "-B", __file__] + list(args), env=env,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=60)
            return p.returncode, p.stdout.decode(), p.stderr.decode()

        bad = []

        def expect(name, got, rc, has=(), lacks=()):
            r, out, err = got
            both = out + err
            if r != rc:
                bad.append("%s: exit %d, want %d (stderr %r)" % (name, r, rc, err[:120]))
            for h in has:
                if h not in both:
                    bad.append("%s: output lacks %r" % (name, h))
            for x in lacks:
                if x in both:
                    bad.append("%s: output carries %r" % (name, x))

        secrets = ("STRANGER", "stranger-login", "IMPOSTOR", "2002", "3003")
        expect("control: all-operator thread", run("all-operator", "7"), 0,
               has=("issue #7: operator title second line\n", "operator body", "--- comment 1 (c1)\noperator comment one",
                    "--- comment 2 (c3)\noperator comment two", "2 by the authenticated gh user shown; 0 by other",
                    "last allowed comment: 2"), lacks=secrets)
        expect("stranger and impostor comments are a count", run("mixed", "7", "--repo", "o/r"), 0,
               has=("operator comment one", "operator comment two", "2 by the authenticated gh user shown; 2 by other authors withheld",
                    "last allowed comment: 2"), lacks=secrets)
        expect("a stranger's issue: number only", run("mixed", "8"), 3,
               has=("#8 refused: not opened by the authenticated gh user",), lacks=secrets)
        expect("the impostor (operator's login, another id)", run("mixed", "9"), 3,
               has=("#9 refused",), lacks=secrets)
        expect("a pull request", run("mixed", "10"), 3, has=("#10 refused: it is a pull request",), lacks=("PR-TITLE",))
        expect("gh failure: no identity", run("fail", "7"), 2,
               has=("refused: gh failed (who is the authenticated gh user, exit 1: HTTP 401",), lacks=("operator body",))
        expect("an id that is a string", run("stringid", "7"), 2, has=("named no numeric id",), lacks=("operator body",))
        expect("a failed comments page prints no part of the issue", run("pagefail", "7"), 2,
               has=("issue #7's comments",), lacks=("operator body", "operator title"))
        expect("control: list", run("mixed", "--list"), 0,
               has=("#7 open: operator title\n", "1 by the authenticated gh user listed; 2 issues"),
               lacks=secrets + ("PR-TITLE",))
        expect("usage: no number", run("mixed"), 2, has=("refused: name one issue number, or --list",))
        expect("usage: a bad repo", run("mixed", "7", "--repo", "o/r/../x"), 2, has=("--repo is OWNER/REPO",))
        expect("usage: a stray argument", run("mixed", "7", "8"), 2, has=("unknown argument 8",))
        expect("a body that is not a string is no text", run("mixed", "11"), 0,
               has=("issue #11: t\n", "--- body\n\n", "--- comment 1 (c)\n\n"))
        n = 13
    finally:
        shutil.rmtree(d, ignore_errors=True)
    for b in bad:
        print("issue-read self-test: FAIL %s" % b, file=sys.stderr)
    if bad:
        sys.exit(1)
    print("issue-read self-test: %d cases against a fake gh (3 positive controls)" % n)


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        self_test()
    else:
        sys.exit(main(sys.argv[1:]))
