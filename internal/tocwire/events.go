package tocwire

import "strings"

// Event is a typed server-to-client TOC message. The text field of an event
// is always the last colon-separated field and may itself contain colons;
// parsing therefore always uses strings.SplitN with the exact field count.
// Server-sent text arrives raw (the server does not TOC-escape outbound
// lines), so no unescaping happens here.
type Event interface{ isEvent() }

// ChatJoin confirms a room join: CHAT_JOIN:<id>:<name>.
type ChatJoin struct {
	RoomID string
	Room   string
}

// ChatIn is a room message: CHAT_IN:<id>:<user>:<T/F>:<text>.
type ChatIn struct {
	RoomID  string
	From    string
	Whisper bool
	Text    string
}

// IMIn is an instant message: IM_IN:<user>:<T/F>:<text>.
type IMIn struct {
	From string
	Auto bool
	Text string
}

// UpdateBuddy is buddy presence:
// UPDATE_BUDDY:<name>:<T/F>:<evil>:<signon-epoch>:<idle>:<class>.
// Only name and online are parsed; Raw keeps the whole line for callers that
// want the remaining fields.
type UpdateBuddy struct {
	Raw    string
	Name   string
	Online bool
}

// ChatUpdateBuddy is room presence:
// CHAT_UPDATE_BUDDY:<id>:<T/F>:<name1>:<name2>...
type ChatUpdateBuddy struct {
	RoomID  string
	Present bool
	Names   []string
}

// ServerError is ERROR:<code>[:args]; Code is just the code field.
type ServerError struct {
	Code string
}

// Unknown carries any line this package does not parse — including malformed
// instances of known verbs. Nothing is dropped silently.
type Unknown struct {
	Raw string
}

func (ChatJoin) isEvent()        {}
func (ChatIn) isEvent()          {}
func (IMIn) isEvent()            {}
func (UpdateBuddy) isEvent()     {}
func (ChatUpdateBuddy) isEvent() {}
func (ServerError) isEvent()     {}
func (Unknown) isEvent()         {}

// parseEvent maps one server line to an Event. Lines whose verb is known but
// whose shape is wrong are surfaced as Unknown, never dropped.
func parseEvent(raw string) Event {
	verb, _, _ := strings.Cut(raw, ":")
	switch verb {
	case "CHAT_JOIN":
		if f := strings.SplitN(raw, ":", 3); len(f) == 3 {
			return ChatJoin{RoomID: f[1], Room: f[2]}
		}
	case "CHAT_IN":
		if f := strings.SplitN(raw, ":", 5); len(f) == 5 {
			return ChatIn{RoomID: f[1], From: f[2], Whisper: f[3] == "T", Text: f[4]}
		}
	case "IM_IN":
		if f := strings.SplitN(raw, ":", 4); len(f) == 4 {
			return IMIn{From: f[1], Auto: f[2] == "T", Text: f[3]}
		}
	case "UPDATE_BUDDY":
		// Fields past online are left in Raw; SplitN(4) proves the shape.
		if f := strings.SplitN(raw, ":", 4); len(f) >= 3 {
			return UpdateBuddy{Raw: raw, Name: f[1], Online: f[2] == "T"}
		}
	case "CHAT_UPDATE_BUDDY":
		// Screen names cannot contain colons, so a full split is exact.
		if f := strings.Split(raw, ":"); len(f) >= 3 {
			return ChatUpdateBuddy{RoomID: f[1], Present: f[2] == "T", Names: f[3:]}
		}
	case "ERROR":
		if f := strings.SplitN(raw, ":", 3); len(f) >= 2 {
			return ServerError{Code: f[1]}
		}
	}
	return Unknown{Raw: raw}
}
