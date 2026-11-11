# A fleet of fleets

Part of the `buddy-fleet` skill, moved out of its SKILL.md to be read before
you coordinate other coordinators (D-171). A section named in parentheses is
SKILL.md's unless this file has it.

For one session over several coordinators, each running its own repo's
fleet, with the operator reading one pane. Every name is per ledger
(labels, slugs, the roster, the inbox, `pause`), and buddy has no
cross-ledger verb, so this is a convention over the verbs above, run end to
end over a real ledger and a scratch fleet ledger (buddy-system's
`docs/BEYOND-CODE-PLAN.md`).

- **The fleet repo.** You live in a small git repository of your own
  (`git init`, then `buddy init`): a file listing the coordinated project
  paths, one per line, beside your briefs, handoff files and reports. You
  have your own row, inbox and `pause` there, and hold the coordination
  claim `orc-<fleet>` (`<fleet>` is its directory's name, which must differ
  from every coordinated project's: a chat alert matches claim slugs in every
  room, so `orc-<fleet>` equal to some `orc-<project>` alerts both).
- **Launch each coordinator in its own repo**, with the relay clause
  (Opening lanes) naming you by label and full session id. One level up it
  is the same clause and the same floor: a coordinator's install, push,
  setting, permission, trust or mode stays the operator's, in that
  coordinator's pane.
- **Send every message in the RECIPIENT's home ledger**, where its hooks
  drain it and `msg` prints its wake line.
  - Down: once per coordinated ledger, register yourself there by hand,
    `(cd <repo> && buddy hello --session $CLAUDE_CODE_SESSION_ID --label <your label> </dev/null)`,
    then send from there, `(cd <repo> && buddy msg orc-<project> "<text>")`.
    It signs with your label, and while the coordinator is idle the result
    carries its wake address.
  - Up: each coordinator's brief has it register by hand in the fleet ledger,
    `(cd <fleet> && buddy hello --session <its id> --label <its label> </dev/null)`,
    and report to your claim slug there, never your label:
    `(cd <fleet> && buddy msg orc-<fleet> "<text>")`. Your own hooks drain
    it, and once your Stop has reported you idle, its result carries your
    wake address (none before that).
  - A foreign registration only identifies a SENDER. Send nothing TO one: no
    hook of its session runs in that ledger, so mail queued there is never
    drained, and `msg` takes it without refusing ("registered … and not seen
    since").
- **Keep your own cwd in the fleet repo**: run each foreign command in a
  subshell, `(cd <repo> && buddy <verb> …)`, so your hooks keep beating your own
  ledger. A row your hooks have beaten is bound to your process, and a `bye`
  of a bound row while you are alive is refused (`stays live`).
- **Read each fleet on a timer wait of your own**
  (`buddy wait --until <dur> --note "<the fleets>"` in the fleet repo, then
  the `/loop`): `(cd <repo> && buddy who orc-<project>)`, and
  `buddy who <run>` there. A read moves only your row's `seen` in that
  ledger, so your foreign rows read STALE only when you have not looked for
  30 minutes.
- **Wear a label that is yours alone, numbered per successor** (`top-1`,
  `top-2`, as coordinators are `orc-<n>`), and have peers address the claim
  slug. A successor helloes while you are still live, and two LIVE wearers
  make `buddy msg <label>` ambiguous; an ended wearer yields to a live one
  (D-139). A claim slug
  resolves to its one live holder past an ended lane's label, and moves with
  `release --to`.
- **Hand off** as `handoff.md` says, and in each coordinated ledger, while
  you are live and in this order: `(cd <repo> && buddy msg orc-<project> "<successor label> (session <id>) is your coordinator from now; it relays the same decisions I did.")`,
  then `(cd <repo> && buddy bye $CLAUDE_CODE_SESSION_ID </dev/null)`, which
  ends your unregistered-process row there (no output, exit 0). After the
  bye a send from you there signs `unregistered s-<8hex>` (D-105). Your
  successor's first act in each is its own hand `hello`. In the fleet repo,
  one `buddy release orc-<fleet> <run> --to <successor>` hands every claim
  on.
- **A coordinator's handoff retires its fleet row too**, and its brief says
  so: `(cd <fleet> && buddy bye <its id> </dev/null)` at its own handoff,
  after it has told you who succeeds it. A coordinator whose hooks ever ran
  in the fleet ledger is bound there, and that `bye` is refused while it
  lives (`stays live`): then it is a plain `bye` once its process has
  exited, run by you or its successor.
- **What the one window cannot do.** A tool approval waits in the pane that
  asked for it; a reply typed in yours is not that approval. `pause` is per
  ledger: pausing yourself pauses no coordinator and no lane. Stopping
  everything is the operator's `buddy pause all --note "<why>"`, run in each
  listed ledger from a bare shell. Whether you may run `pause` in a
  coordinated ledger on the operator's relayed word is the operator's to
  decide; until they say, do not.
- **Your view is counts and exceptions**, not rosters concatenated: two
  fleets' rosters together already pass the 9,000-byte digest budget. The
  full roster is one `cd` away.
