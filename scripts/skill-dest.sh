# skill-dest.sh — the ONE judgement install-skill.sh and uninstall.sh make of a
# skill's destination directory before they write or remove anything in it
# (D-181, #94). Sourced, never run; POSIX sh, no Python (uninstall runs where
# nothing else does, D-107).
#
# WHY. Both scripts tested only the last component of a file's path for a
# symlink ([ -L "$d/$b" ]). With ~/.claude/skills/buddy a symlink to the
# checkout's own skills/buddy (a live-edit setup), $d/$b IS the checkout's
# tracked file: uninstall's cmp compared it with itself, planned every file,
# and deleted the checkout's skills/buddy/* through the link (#94); install
# wrote its lock, temp files and manifest into the checkout, and through a
# link to ANOTHER clone overwrote that clone's tracked files. A file reached
# that way is not an installed copy, so D-173's "remove only what is provably
# ours" leaves it.
#
# WHAT IS JUDGED: the destination directory, BY WHERE IT LANDS. Links ABOVE it
# are not refused as such: /var on macOS is a link to /private/var (every
# mktemp -d), and a dotfiles layout links ~/.claude. They are judged by the
# directory they reach: the checkout's own skills/<n> (-ef: device and inode,
# so a link anywhere above, a relative or `..` knob, and a case-folded
# spelling on the default macOS volume all land on it), or any
# version-control work tree (router.py's VCS_MARKERS at the physical path or
# above, D-179's in_tree shape, in shell). The LAST hop is judged by `[ -L ]`
# on the path with its trailing slashes dropped (Codex design pass: `link/`
# made `[ -L ]` look past the link), and a `.` or `..` component ANYWHERE is
# refused: last (`link/.`, `link/sub/..`), `[ -L ]` sees no link there; above
# (`<d>/missing/../buddy`, Codex code pass), an absent component hides an
# existing directory from every check until `mkdir -p` makes it. Both scripts
# refuse such a knob up front; the `hop` verdict is the judge's own guard.
# REFUTED: comparing the physical path with the parent's plus the name. With
# the two rules above it differs only for a case-folded spelling of a real
# directory (the external pwd -P returns the name as stored), which is not a
# link.
#
# THE PHYSICAL PATH IS THE EXTERNAL pwd's (Fable cross-check, measured): the
# builtin `pwd -P` of macOS /bin/sh (bash 3.2) keeps a case-folded spelling as
# typed, and a /System/Volumes/Data prefix, so `checkout` and `clone`, string
# tests on that path, read <ROOT in another case>/docs as `vcs` and a link to
# <A>/Skills as `ok`. `env pwd -P` (BSD, GNU and busybox alike) returns the
# name as stored and drops the firmlink prefix. No fallback to the builtin
# (Codex re-verify: it reopened the miss wherever no external pwd ran): a
# path it cannot read is one the judge cannot place: the destination, its
# deepest existing ancestor or the checkout itself unplaceable makes it
# `unsearchable` (an absent one's forecast too), which uninstall leaves and
# install refuses.
#
# REFUTED (Codex design pass): `cd -P -- "$d"` on a RELATIVE knob: `-` is
# $OLDPWD even after `--`, so the judge looked at the caller's directory while
# the removal reached $ROOT/-. Every path is made absolute first. And a
# physical path read by a plain substitution loses a trailing newline, so a
# directory named "foreign<LF>" was judged as "foreign"; it is kept, then
# refused.

SKD_NL='
'

# skd_abs <path> <root>: SKD_P, the path absolute (a relative one under
# <root>, where both scripts run), each run of slashes one slash (Fable
# cross-check: `skills//buddy-fleet` hid a clone's absent skills/ from
# skd_under's name test) and its trailing slashes dropped. No lexical `..`
# collapse: across a link that names another directory.
skd_abs() {
	case $1 in /*) SKD_P=$1 ;; *) SKD_P=$2/$1 ;; esac
	while :; do
		case $SKD_P in *//*) SKD_P=${SKD_P%%//*}/${SKD_P#*//} ;; *) break ;; esac
	done
	while :; do
		case $SKD_P in / | *[!/]) break ;; esac
		SKD_P=${SKD_P%/}
	done
}

# skd_phys <absolute dir>: SKD_PHYS, its physical path with every byte kept
# (the trailing `.` keeps a trailing newline through the substitution).
# Status 1 when it cannot be entered.
skd_phys() {
	SKD_PHYS=$(CDPATH= cd -P -- "$1" 2>/dev/null && env pwd -P 2>/dev/null && echo .) || return 1
	SKD_PHYS=${SKD_PHYS%.}
	SKD_PHYS=${SKD_PHYS%"$SKD_NL"}
}

# skd_mark <marker>: status 0, SKD_MARK set, when $skd_p holds it (any kind:
# a linked worktree's .git is a file).
skd_mark() {
	case $skd_p in /) SKD_MARK=/$1 ;; *) SKD_MARK=$skd_p/$1 ;; esac
	[ -e "$SKD_MARK" ] || [ -L "$SKD_MARK" ]
}

# skd_under <absent dest dir> <root>: SKD_UNDER, where `mkdir -p` would make
# it, judged on the PHYSICAL path it would have (the deepest existing
# ancestor's, plus the names below it, which `mkdir -p` makes as plain
# directories): `dangling` if a link on the way points at nothing (SKD_MARK),
# `newline` if that path holds one, `checkout` inside <root>,
# `clone` as <T>/skills/<n> of another buddy checkout <T> (SKD_MARK), else
# empty. install-skill.sh reads it before it makes the directory (the panel:
# a skill the other clone had not got yet was made there, and only then
# judged). The Codex code pass: the clone test asked only a parent that
# already existed, so a clone with no skills/ yet had skills/<n> made in it
# and then stopped the run; and a link above to a directory named
# "foreign<LF>" passed, and a directory was made there before the refusal.
# Sound only because no knob holds a `.` or `..` component (above).
skd_under() {
	skd_a=${1%/*}
	[ -n "$skd_a" ] || skd_a=/
	skd_rest=${1##*/}
	# The deepest ancestor that exists: `mkdir -p` makes everything below it.
	# A dangling link on the way is no ancestor (Fable cross-check: the walk
	# climbed past it, and mkdir -p then failed under set -e, stopping
	# install.sh and setup-clone.sh): `dangling`, SKD_MARK the link.
	while [ ! -e "$skd_a" ] && [ "$skd_a" != / ]; do
		if [ -L "$skd_a" ]; then SKD_UNDER=dangling SKD_MARK=$skd_a; return 0; fi
		skd_rest=${skd_a##*/}/$skd_rest
		skd_a=${skd_a%/*}
		[ -n "$skd_a" ] || skd_a=/
	done
	skd_phys "$skd_a" || { SKD_UNDER=unsearchable; return 0; }
	case $SKD_PHYS in /) skd_w=/$skd_rest ;; *) skd_w=$SKD_PHYS/$skd_rest ;; esac
	case $skd_w in *"$SKD_NL"*) SKD_UNDER=newline; return 0 ;; esac
	skd_phys "$2" || { SKD_UNDER=unsearchable; return 0; }
	case $skd_w/ in "$SKD_PHYS"/*) SKD_UNDER=checkout; return 0 ;; esac
	skd_pu=${skd_w%/*}
	case ${skd_pu##*/} in
	skills)
		if [ -f "${skd_pu%/*}/scripts/install-skill.sh" ]; then SKD_UNDER=clone SKD_MARK=${skd_pu%/*}; fi
		;;
	esac
	return 0
}

# skd_judge <dest dir> <the checkout's skills/<n>> <root>: SKD_KIND, one of
#   absent        nothing there (or not visible: nothing to write through, nothing to remove);
#                 SKD_UNDER says where it would be made (skd_under: unsearchable, newline, checkout, clone, dangling or empty)
#   ok            a directory of its own, outside any work tree
#   vcs           a directory of its own in a version-control work tree (SKD_MARK names the marker)
#   own           the checkout's own skills/<n>, however reached
#   link          a symlink (to anything else)
#   hop           it is `/`, or has a `.` or `..` component
#   notdir        not a directory
#   unsearchable  cannot be entered, so what it holds cannot be judged
#   newline       its physical path holds a newline
#   clone         ANOTHER buddy checkout's skills/<n> (SKD_MARK names that checkout)
#   checkout      inside the checkout (<root>), not its own skills/<n>: another
#                 skill's directory there, or anywhere else in the tree
# <dest dir> is skd_abs's: absolute, no trailing slash. <root> is the checkout.
# The checkout clause (the panel): BUDDY_SKILL_DIR=<checkout>/skills/buddy-fleet
# was `vcs`, so install wrote the core's SKILL.md over the checkout's tracked
# fleet SKILL.md; in an export with no marker uninstall would have removed it.
skd_judge() {
	SKD_KIND="" SKD_MARK="" SKD_UNDER=""
	# First: an absent component before a `..` hides what the path reaches.
	# And `/` itself (Codex re-verify: `///` matched none of these, so `/` was
	# `ok` and /SKILL.md was the core's).
	case /$1/ in */./* | */../* | ///) SKD_KIND=hop; return 0 ;; esac
	if [ -L "$1" ]; then
		if [ "$1" -ef "$2" ]; then SKD_KIND=own; else SKD_KIND=link; fi
		return 0
	fi
	if [ ! -e "$1" ]; then
		SKD_KIND=absent
		skd_under "$1" "$3"
		return 0
	fi
	if [ ! -d "$1" ]; then SKD_KIND=notdir; return 0; fi
	if [ "$1" -ef "$2" ]; then SKD_KIND=own; return 0; fi
	if ! skd_phys "$1"; then SKD_KIND=unsearchable; return 0; fi
	case $SKD_PHYS in *"$SKD_NL"*) SKD_KIND=newline; return 0 ;; esac
	skd_me=$SKD_PHYS
	# A checkout the judge cannot place rules nothing out (Codex re-verify 2:
	# the containment test was skipped, and a checkout file read `ok`).
	if ! skd_phys "$3"; then SKD_KIND=unsearchable; return 0; fi
	case $skd_me/ in "$SKD_PHYS"/*) SKD_KIND=checkout; return 0 ;; esac
	SKD_PHYS=$skd_me
	# Another buddy checkout's skills/<n>, reached through a link above the
	# destination (the panel: ~/.claude/skills a link to clone A's skills/,
	# and an install from clone B overwrote A's tracked files). Its mark is
	# the installer itself, which every checkout since D-049 carries.
	skd_up=${skd_me%/*}
	case ${skd_up##*/} in
	skills)
		if [ -f "${skd_up%/*}/scripts/install-skill.sh" ]; then
			SKD_KIND=clone SKD_MARK=${skd_up%/*}
			return 0
		fi
		;;
	esac
	# Every ancestor of a directory just entered is searchable, so `-e` here
	# cannot read EACCES as absent.
	skd_p=$SKD_PHYS
	while :; do
		# One call per marker, not a `for` over a word list: uninstall.sh
		# splits on newlines only (IFS), where the list would be one word.
		if skd_mark .git || skd_mark .hg || skd_mark .svn || skd_mark .jj ||
			skd_mark .bzr || skd_mark _darcs || skd_mark .fslckout || skd_mark _FOSSIL_; then
			SKD_KIND=vcs
			return 0
		fi
		[ "$skd_p" != / ] || break
		skd_p=${skd_p%/*}
		[ -n "$skd_p" ] || skd_p=/
	done
	SKD_KIND=ok SKD_MARK=""
}
