# Review charter: adapter GIVENs (full text)

Appended after `docs/review-charter.md` by `scripts/codex-review.sh` only when
`CODEX_CHARTER=adapter` is set: the full text of the GIVENs only adapter code rests on
(`internal/harness`, `cmd/buddy-adapter`, `scripts/qualify-*.py`, the adapter's
`scripts/check-*.py`, `docs/admissions/`, `docs/USAGE.md`'s structured-adapter sections).
The main charter keeps a one-line stub of each, under the same number, naming its rule, its
D-numbers and its fences; this text is the GIVEN in full, and it binds exactly as the main
charter does (D-128). The chosen set and why the rest stay in full: D-128.

72. **Claude admission uses a pinned native built-in Mod profile (D-071).**
    `hooks.mods: "builtin-only-<version>"` selects the separately checked profile
    matching the exact declared and observed native version. The admitted pairs are
    the committed admission records (D-079), not this GIVEN; each new build joins
    only after its own qualification. Start and resume reject a cross-version/profile pair;
    no semver range or automatic conversation upgrade is admitted. Legacy
    `no_mods` evidence refuses. The adapter checks effective settings, reloads
    plugins in its owned child before input, requires exactly the two expected
    native built-ins with their source/path identities and no reload errors,
    then checks effective settings again. Missing/additional inventory refuses.
    The agents-md built-in contains a Mod, so empty optional configuration is
    not evidence of Mod absence. This is a startup snapshot, not continuous
    attestation, plugin startup isolation or a read-only diagnostic. Native
    initialization/reload precedes inventory admission. Actual native claim denial is a
    separate check; optional/managed Mods outside the profile and later native
    configuration changes remain unqualified.

76. **Recovery admission is exact-version and terminal-evidence-bound (D-075).** Codex
    resumes the captured thread; Codex `reconcile` is withdrawn until a bounded history
    read is qualified (D-077). Claude resume
    needs every journalled turn's terminal result for its exact input (`completed`,
    `aborted_streaming`, `aborted_tools`) and idle; anything unfinished or unknown refuses
    before ledger refresh and launch, and SDK history never substitutes. Manual compaction
    is admitted only on versions where its boundary and local result were measured. D-075
    supersedes D-070's "Claude resume is unsupported".

78. **The adapter owns its worker's process group and validates the operator descriptor
    before any child (D-077).** `--operator-fd` must be open, `O_RDONLY`, a FIFO/char
    device/regular file, and is marked close-on-exec before the version probe; anything else
    refuses with no child and no journal. Close ends the worker's process group (TERM, then
    KILL as soon as the leader exits or after 2 s at most — the grace is the leader's, so a
    tool can get TERM and KILL back to back) while its leader is unreaped, and closes the
    stdout reader; INT/TERM/HUP run
    Close. A refusal before any native byte is `not_submitted` and retryable; a write that
    may have happened stays uncertain. An unrecorded event makes state unknown, never
    `observed_absent`. Claude gate settings use exact case-sensitive keys, no duplicates, a
    matcher that is `""`, `*` or `.*`, or a literal list (plain or `^(…)$`-anchored) naming
    all three tools, `timeout` 10-600 s. Executables are absolute,
    resolved once, identity-checked before exec. Do not propose discarding oversized
    frames, reading full Codex history on the live transport, or treating a free journal
    lock as proof the worker exited. **Resume is fail-closed on an ownership record
    (D-077, audit A-02):** every spawn publishes `<receipt>.proc`
    (intent before the child, the leader's pid/start/pgid/sid before the first native
    byte) through a single-use launch authorization consumed before `cmd.Start`; resume
    refuses with no record, a record bound to another journal/receipt/identity, an
    intent-only record, or while the recorded adapter, leader or group lives (`kill(-pgid,
    0)`, ESRCH alone is absence); `--wait-prior` is a bounded passive re-check. `.start`
    fences only Start; the flock plus intent-before-spawn fence every spawn. **Narrow
    contract (D-077, the operator's choice):** descendants that left the group
    are sampled every second (one `kern.proc.all` read; parentage only, every parent
    re-checked by pid and start time after the read), recorded in `.proc`, captured again
    before stdin closes on every adapter-initiated shutdown, reported at close (`ended`,
    `survivors`, `unknown`; `unattributable` is always "unknown") and on one stderr line,
    and NEVER signalled; resume refuses waitably while a recorded one lives, and outright
    on overflow (256), an unresolved discovery error or a malformed section. Excluded, do
    not re-propose: record rollback detection (invariant 6), killing what the adapter
    cannot pin (a pid and start-time re-read is not a pin), `--end-prior`. Setsid waits on
    native qualification Q1/Q3.

80. **Native admission is a committed exact-artifact record, embedded in the adapter
    (D-079).** `internal/harness/admissions/<harness>/<version>-<sha8>/admission.json`
    (`buddy.admission.v1`), strict parse (unknown, duplicate or missing keys, a malformed
    hash, an unearned tier, a `reconcile` grant: each its own refusal; one bad record
    refuses every launch). Before anything runs, the executable is hashed and matched by
    bytes, and Codex's whole release tree is matched to its manifest (an undeclared entry
    refuses). Then contract id, platform, tier and profile, the exact version text, and
    the live checks, unchanged. Tiers are `start`, `resume`, `compaction_observation`;
    each needs the code's suites with an independent checker's pass. Capabilities are
    tiers intersected with code support, and reconcile is never granted. Do not propose a runtime registry, ranges or
    probing alone, a metadata hash cache, or invalidation by adapter hash (an embedded
    record cannot bind its own binary; the contract id is bumped instead). A version-keyed
    parse rule whose outcomes are identical on every measured frame of every recorded
    version, proven by a committed hermetic replay, does not bump the contract id; a change
    that alters any such outcome does, and requalifies (the operator's choice). A record is
    produced before a planned adapter run, for the build it launches, not per release; an
    unadmitted installed build is expected between runs (D-156).

81. **A qualification candidate (D-080)** is `buddy-adapter --qualify-candidate <artifact>
    --qualify-sha256 <hex>` in the shipped binary: the typed digest binds the artifact, the
    committed records are REPLACED by one synthesized record for those bytes, an observer
    `--executable` is hashed and must stay unchanged, and every start and resume needs a
    loopback-IP provider (Claude `ANTHROPIC_BASE_URL`; Codex `config.toml` `model_provider`
    `base_url`). Every receipt carries `qualification_candidate`; cross-mode resume refuses.
    The rename window between the post-hash re-check and `--version` is F-23's stated
    path-exec window (no worker starts on a replacement); `--retained` is the mitigation.

82. **A qualification run binds its own binary; checkers read the records (D-081).** Each
    runner hashes the binary it was given, reads `--version`, and types the candidate flags
    onto every adapter it starts (no `BUDDY_QUALIFY_*` env transport). Each checker takes its
    versions from the committed records and `--candidate harness:version:sha256`, and accepts a
    candidate run only for exactly those bound bytes with the route visible in the adapter's
    receipts. A run bound to other bytes is never accepted because the version has a record.
    `scripts/qualify-release.py` retains the build (staged, verified, renamed, never
    overwritten), runs the code's suites from the copy, and writes a record only for tiers
    whose suites and checkers all passed, only with `start`, and only on a clean tree at one
    HEAD. Do not propose per-checker version lists or hand-written records.
