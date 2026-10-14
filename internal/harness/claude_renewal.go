package harness

// Which Claude completions renew a claim (F-26, D-078).
//
// A renewal needs a COMPLETED write naming a path, on this worker's main
// thread, in this launch. An assistant tool_use or a can_use_tool request is
// only an attempt: it is recorded here as a correlation, never renewed. Only
// the matching non-replay main-thread tool_result renews, and only when it
// establishes success:
//
//   - `is_error: false`, explicitly: success, on every admitted version.
//   - `is_error: true`: nothing. A gate denial and an operator denial both look
//     like this natively (2.1.291 adds tool_result_meta non_execution_kind to
//     the gate's), and so does a failed edit.
//   - `is_error` OMITTED: this is what native 2.1.291 sends for a SUCCESSFUL
//     Write (receipts of the 2026-10-13 rerun, claude-interrupted-compaction:
//     the Bash result carries `is_error:false`, the Write result carries none).
//     The adopted design said "explicit false only" until a version's omission
//     semantics were qualified; it is qualified here for Write on 2.1.291
//     only, and only with a positive witness: the frame's structured
//     tool_use_result is an object whose type is create or update and whose
//     filePath is byte-for-byte the recorded input path, the frame carries
//     exactly one tool_result, and no tool_result_meta marks it unexecuted.
//     Anything else with is_error omitted renews nothing and reports renewal
//     coverage unknown, because a write may have succeeded unseen. A
//     successful Edit was measured on 2.1.291 and 2.1.299 (2026-10-13,
//     qualify-claude.py): is_error OMITTED, and a tool_use_result with no
//     `type` (filePath, oldString, newString, originalFile, structuredPatch,
//     replaceAll, userModified). One sample per version and no failed-Edit
//     control, so Edit omission stays unqualified: an Edit renews only on an
//     explicit false, which no measured version sends. NotebookEdit was not
//     measured.
//
// Bash renews nothing, `sed -i` included: no qualified path extraction. A
// subagent's result (non-null parent_tool_use_id) renews nothing. A result
// whose provenance field is ABSENT is an unexpected shape: nothing renews and
// coverage is reported unknown. An unknown tool_use_id renews nothing; it may
// be history replayed into a resumed stream, which is why correlations live
// in this backend and die with it: nothing pairs a tool_use with a tool_result
// across a resume.

import (
	"encoding/json"
	"strings"
)

type claudeFileTool struct {
	name, path, turn string
}

// claudeWriteOmissionQualified names the versions on which a successful Write
// was measured to omit is_error and carry the structured create/update
// witness. A new version joins only after its own native measurement.
func claudeWriteOmissionQualified(version string) bool {
	return version == "2.1.291"
}

func (c *claudeBackend) observeRenewals(m *renewalMailbox) {
	c.mu.Lock()
	c.renewals = m
	c.mu.Unlock()
}

// claudeFilePath extracts the one path a file tool names, or "" when the tool
// renews nothing or its input does not name a usable path.
func claudeFilePath(name string, input json.RawMessage) (string, bool) {
	key := ""
	switch name {
	case "Write", "Edit":
		key = "file_path"
	case "NotebookEdit":
		key = "notebook_path"
	default:
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return "", true
	}
	p, ok := jsonString(fields[key])
	if !ok || p == "" || len(p) > renewPathMax || strings.ContainsRune(p, 0) {
		return "", true
	}
	return p, true
}

// recordFileToolLocked notes an attempted main-thread file-tool use of the
// active turn. Callers hold c.mu. A second sighting of the same id (the
// assistant block and then its can_use_tool request) must name the same tool
// and path; a disagreement drops the correlation rather than choose one.
//
// A non-file sighting of a recorded id disagrees too (Codex review of the code
// pass, finding 1): an assistant Write and a Bash can_use_tool sharing an id
// used to leave the Write correlated, and the Bash result renewed a file that
// nothing wrote.
func (c *claudeBackend) recordFileToolLocked(id, name string, input json.RawMessage) (lost string) {
	if id == "" {
		return ""
	}
	p, fileTool := claudeFilePath(name, input)
	old, seen := c.fileTools[id]
	drop := func(reason string) string {
		if seen {
			delete(c.fileTools, id)
			c.fileBytes -= len(old.path)
		}
		return reason
	}
	if !fileTool {
		if seen {
			return drop("a tool_use_id named a file tool and another tool")
		}
		return ""
	}
	if p == "" {
		return drop("a file tool's input named no usable path")
	}
	if c.fileTools == nil {
		c.fileTools = map[string]claudeFileTool{}
	}
	entry := claudeFileTool{name: name, path: p, turn: c.active}
	if seen {
		if old != entry {
			return drop("a tool_use_id named two different file-tool inputs")
		}
		return ""
	}
	if len(c.fileTools) >= correlationMax || c.fileBytes+len(p) > correlationMaxBytes {
		return "the file-tool correlation bound was reached"
	}
	c.fileTools[id] = entry
	c.fileBytes += len(p)
	return ""
}

// assistantToolUses records the main-thread file-tool uses of an assistant
// frame. Subagent frames and frames outside an active input record nothing.
func (c *claudeBackend) assistantToolUses(raw json.RawMessage) {
	var f struct {
		ParentToolUseID json.RawMessage `json:"parent_tool_use_id"`
		Message         struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &f) != nil || string(f.ParentToolUseID) != "null" {
		return
	}
	var blocks []struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(f.Message.Content, &blocks) != nil {
		return
	}
	var lost string
	c.mu.Lock()
	if c.active != "" && !c.closed {
		for _, b := range blocks {
			if b.Type == "tool_use" {
				if l := c.recordFileToolLocked(b.ID, b.Name, b.Input); l != "" && lost == "" {
					lost = l
				}
			}
		}
	}
	m := c.renewals
	c.mu.Unlock()
	if lost != "" {
		m.unknown(lost)
	}
}

// toolResults classifies the tool_result blocks of one non-replay user frame
// and offers the paths of the writes it establishes as completed.
func (c *claudeBackend) toolResults(raw json.RawMessage) {
	var f struct {
		SessionID       string          `json:"session_id"`
		ParentToolUseID json.RawMessage `json:"parent_tool_use_id"`
		IsSynthetic     bool            `json:"isSynthetic"`
		Message         struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		ToolUseResult  json.RawMessage `json:"tool_use_result"`
		ToolResultMeta json.RawMessage `json:"tool_result_meta"`
	}
	// A frame this function cannot read is lost coverage while any use
	// awaits its result, never a silent return (Codex review, finding 6): the
	// turn's end would discard the use and nothing would say a write may
	// have completed unseen. A text input (content a string) is not a result.
	unexpected := func(reason string) {
		c.mu.Lock()
		outstanding, m := len(c.fileTools) > 0, c.renewals
		c.mu.Unlock()
		if outstanding {
			m.unknown(reason)
		}
	}
	if json.Unmarshal(raw, &f) != nil {
		unexpected("a user frame could not be read")
		return
	}
	if f.IsSynthetic {
		return
	}
	content := strings.TrimLeft(string(f.Message.Content), " \t\r\n")
	if strings.HasPrefix(content, `"`) {
		return
	}
	var blocks []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   json.RawMessage `json:"is_error"`
	}
	if !strings.HasPrefix(content, "[") || json.Unmarshal(f.Message.Content, &blocks) != nil {
		unexpected("a user frame's content was neither text nor a list of blocks")
		return
	}
	var meta []struct {
		ID string `json:"id"`
	}
	if len(f.ToolResultMeta) > 0 && string(f.ToolResultMeta) != "null" && json.Unmarshal(f.ToolResultMeta, &meta) != nil {
		unexpected("a user frame's tool_result_meta was not a list")
		return
	}
	results := 0
	for _, b := range blocks {
		if b.Type == "tool_result" {
			results++
		}
	}
	unexecuted := map[string]bool{}
	for _, m := range meta {
		unexecuted[m.ID] = true
	}
	var paths []string
	var lost string
	note := func(reason string) {
		if lost == "" {
			lost = reason
		}
	}
	c.mu.Lock()
	version, generation, m := c.version, c.generation, c.renewals
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		tool, ok := c.fileTools[b.ToolUseID]
		if !ok {
			continue // not a recorded main-thread file tool: renews nothing
		}
		// Provenance BEFORE the correlation is consumed (Codex review,
		// finding 2): a rejected result naming the id used to delete it, and
		// the genuine main-thread result that followed renewed nothing.
		switch {
		case c.closed:
			continue
		case f.ParentToolUseID == nil:
			note("a file tool's result arrived without parent_tool_use_id provenance")
			continue
		case string(f.ParentToolUseID) != "null":
			continue // a subagent's result
		case f.SessionID != c.nativeID:
			note("a file tool's result arrived without its session identity")
			continue
		}
		delete(c.fileTools, b.ToolUseID)
		c.fileBytes -= len(tool.path)
		if unexecuted[b.ToolUseID] {
			continue // native marks it not executed (a gate denial)
		}
		if b.IsError != nil {
			isError, ok := jsonBool(b.IsError)
			if !ok {
				note("a file tool's result carried a non-boolean is_error")
			} else if !isError {
				paths = append(paths, tool.path)
			}
			continue
		}
		if tool.name == "Write" && claudeWriteOmissionQualified(version) && results == 1 && claudeWriteWitness(f.ToolUseResult, tool.path) {
			paths = append(paths, tool.path)
			continue
		}
		note("a file tool's result omitted is_error without a qualified success witness")
	}
	c.mu.Unlock()
	m.offer(generation, paths)
	if lost != "" {
		m.unknown(lost)
	}
}

// claudeWriteWitness is the measured 2.1.291 success shape of a Write's
// tool_use_result: {"type":"create"|"update","filePath":<the input path>,...}.
// A denial's tool_use_result is a string ("Error: ...") and never matches.
func claudeWriteWitness(raw json.RawMessage, path string) bool {
	var w map[string]json.RawMessage
	if json.Unmarshal(raw, &w) != nil || w == nil {
		return false
	}
	kind, ok := jsonString(w["type"])
	if !ok || kind != "create" && kind != "update" {
		return false
	}
	got, ok := jsonString(w["filePath"])
	return ok && got == path
}
