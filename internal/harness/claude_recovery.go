package harness

// SDK 0.3.202 getSessionMessages on native 2.1.289 correlates input UUIDs but
// returns conversation history, not terminal results or idle observations.
// Even an assistant end_turn survives a lost terminal frame, and a crash after
// acceptance can leave no history at all (qualify-claude-history.py). Neither
// observation can repair an incomplete transport journal. Starting a new
// stream must not forget unfinished work or let an old approval act on a fresh
// connection. Require every wake to have an accepted UUID, a qualified native
// terminal result and a subsequent idle observation, all in the original
// journal. Transport EOF alone neither completes nor invalidates such a turn.
// Native 2.1.289 reports both measured cancellation seams as errors:
// aborted_streaming while awaiting a response, aborted_tools at an approval.
// Those errors remain visible; they terminate an input, not its assigned work.
// Manual /compact cancellation on 2.1.290 and 2.1.291 emits local-command success
// without a boundary or terminal reason, after the wake timed out awaiting
// acceptance. Neither the late acknowledgment nor that result repairs delivery
// uncertainty. Interrupted compaction fixtures retain these original journals
// and prove refusal before native launch, ledger refresh or journal mutation.
// This is conservative admission, not evidence of effects, validation or landing.
type claudeRecovery struct {
	turns      map[string]string
	uncertain  bool
	boundary   string
	compacted  bool
	boundaries map[string]bool
}

func (s *claudeRecovery) observe(r Receipt) {
	if s.turns == nil {
		s.turns = map[string]string{}
	}
	if a := r.ApprovalResponse; a != nil {
		s.turns[a.TurnID] = "active"
	}
	e := r.NativeEvent
	if e == nil {
		return
	}
	if e.NativeSessionID != r.Identity.SessionID {
		s.uncertain = true
		return
	}
	switch e.Kind {
	case "compaction_boundary":
		// A boundary alone is not a terminal input. Leave it outstanding
		// until its separate local result is journalled; never synthesize
		// a missing result from native conversation history.
		if !claudeValidUUID(e.ItemID) || e.TurnID != "" || s.boundary != "" || s.boundaries[e.ItemID] {
			s.uncertain = true
		}
		if s.boundaries == nil {
			s.boundaries = make(map[string]bool)
		}
		s.boundaries[e.ItemID] = true
		s.boundary = e.ItemID
		s.compacted = true
	case "ended":
		s.uncertain = true
	case "busy", "acknowledged", "approval", "approval_resolved":
		s.turns[e.TurnID] = "active"
	case "result":
		s.turns[e.TurnID] = "uncertain"
		terminal := e.ResultStatus == "completed" && e.Err == "" ||
			e.ResultStatus == "aborted_streaming" || e.ResultStatus == "aborted_tools"
		if e.ResultStatus == "local_compact_completed" {
			terminal = e.Err == "" && s.boundary != "" && e.ItemID == s.boundary
			if terminal && e.ResultRef != "" {
				s.boundary = ""
			}
			s.compacted = true
		}
		if terminal && e.ResultRef != "" {
			s.turns[e.TurnID] = "result"
		}
	case "idle":
		if s.turns[e.TurnID] == "result" {
			s.turns[e.TurnID] = "complete"
		} else {
			s.turns[e.TurnID] = "uncertain"
		}
	}
}

func (s *claudeRecovery) resumable(sent map[int64]Delivery, version string) bool {
	if s.uncertain || s.boundary != "" || s.compacted && !admissionVersionGrants("claude", version, TierCompaction) || len(sent) == 0 || len(s.turns) == 0 {
		return false
	}
	for turn, state := range s.turns {
		if turn == "" || state != "complete" {
			return false
		}
	}
	for _, d := range sent {
		if d.State != "delivered" || d.TurnID == "" || s.turns[d.TurnID] != "complete" {
			return false
		}
	}
	return true
}
