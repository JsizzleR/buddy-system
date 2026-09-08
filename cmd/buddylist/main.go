// buddylist: the concierge chat daemon and its socket clients.
//
//	buddylist serve --server 127.0.0.1:9898 --name SmarterChild --rooms lobby,ops
//	buddylist say    <room> <text...> [--from <label>]
//	buddylist read   <room> [--after <seq>] [--tail <n>] [--before <seq>] [--mentions a,b]
//	buddylist status [--session <id>] [--mentions a,b]
//	buddylist alert  [--session <id>] [--cwd <dir>]   (PostToolUse hook)
//	buddylist who | dm <to> <text...> | health
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JsizzleR/buddy-system/internal/buddylist"
	"github.com/JsizzleR/buddy-system/internal/cli"
	"github.com/JsizzleR/buddy-system/internal/ircwire"
	"github.com/JsizzleR/buddy-system/internal/tocwire"
)

func stateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".buddylist")
}

func defaultSocket() string { return filepath.Join(stateDir(), "buddylist.sock") }

// daemon is the socket client every verb but `serve` talks through. The path
// and the five-second bound used to be spelled out at each call site; see
// buddylist.Client for why one spelling of both matters more than the six
// characters it saves.
func daemon() buddylist.Client { return buddylist.Client{Socket: defaultSocket()} }

// callAt is the dep shape the MCP server and the alert hook want: those two
// choose their own bound per request (mcpCallTime, the alert's own budget), so
// they get a client carrying the timeout they named rather than the default.
func callAt(req buddylist.Request, timeout time.Duration) (buddylist.Response, error) {
	return buddylist.Client{Socket: defaultSocket(), Timeout: timeout}.Call(req)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: buddylist serve|say|read|status|alert|who|dm|health|mcp ...")
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = runChatd(args)
	case "say":
		err = runSay(args)
	case "read":
		err = runRead(args)
	case "status":
		err = runStatus(args)
	case "who":
		err = runWho()
	case "dm":
		err = runDM(args)
	case "health":
		err = runHealth()
	case "mcp":
		err = runMCP(args)
	case "alert":
		err = runAlert(args)
	case "presence":
		err = runPresence(args)
	default:
		fmt.Fprintf(os.Stderr, "buddylist: unknown command %q\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "buddylist %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func runChatd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	backend := fs.String("backend", "irc", "chat backend: irc (ergo) or toc (open-oscar-server)")
	server := fs.String("server", "", "chat server address (default: 127.0.0.1:6667 for irc, 127.0.0.1:9898 for toc)")
	name := fs.String("name", "SmarterChild", "concierge screen name")
	pass := fs.String("pass", "SmarterChild", "password (DISABLE_AUTH servers accept anything)")
	rooms := fs.String("rooms", "lobby", "comma-separated rooms to join and journal")
	api := fs.String("api", "127.0.0.1:8080", "management API (creates exchange-5 rooms; empty disables)")
	exchange := fs.Int("exchange", 4, "chat exchange: 4 = AIM Buddy Chat dialog territory (default), 5 = public/API-created")
	journalPath := fs.String("journal", filepath.Join(stateDir(), "journal.db"), "journal db path")
	socket := fs.String("socket", defaultSocket(), "control socket path")
	// "0 disables" is stated because it CHANGED: retention used to be an
	// unconditional j.Trim(*keep) here, and Trim(0) puts the cutoff at now, so
	// --keep 0 emptied the journal every hour. The daemon now reads 0 as "off"
	// (Config.Keep), which is the safe reading for the many Daemons built
	// without a window — but an operator who set --keep 0 to bound disk gets
	// the opposite of what they asked for, and silence here is how they find
	// out from the disk.
	keep := fs.Duration("keep", 14*24*time.Hour, "journal retention window; 0 disables trimming (was: trim everything)")
	presence := fs.Bool("presence", true, "per-session buddies: each live session joins its project room under its own name (irc backend only)")
	fs.Parse(args)

	if *server == "" {
		if *backend == "irc" {
			*server = "127.0.0.1:6667"
		} else {
			*server = "127.0.0.1:9898"
		}
	}

	if err := os.MkdirAll(filepath.Dir(*journalPath), 0o700); err != nil {
		return err
	}
	j, err := buddylist.OpenJournal(*journalPath, nil)
	if err != nil {
		return err
	}
	defer j.Close()

	var dial buddylist.Dialer
	var dialAs func(context.Context, string) (buddylist.Conn, error)
	apiAddr := ""
	switch *backend {
	case "irc":
		dial = func(ctx context.Context) (buddylist.Conn, error) {
			return ircwire.Dial(ctx, *server, *name, *pass)
		}
		if *presence {
			dialAs = func(ctx context.Context, nick string) (buddylist.Conn, error) {
				c, err := ircwire.Dial(ctx, *server, nick, *pass)
				if err != nil {
					// Translate the backend's refusal into the seam's
					// vocabulary; the daemon must not know what IRC is.
					if errors.Is(err, ircwire.ErrNickInUse) {
						return nil, fmt.Errorf("%s: %w", err, buddylist.ErrNickInUse)
					}
					return nil, err
				}
				return c, nil
			}
		}
	case "toc":
		apiAddr = *api // exchange-5 rooms need API pre-creation on the oscar server
		dial = func(ctx context.Context) (buddylist.Conn, error) {
			return tocwire.Dial(ctx, *server, *name, *pass, tocwire.WithChatExchange(*exchange))
		}
		// No per-session presence on the nostalgia path, deliberately. A TOC
		// screen name is an ACCOUNT, and a second signon under one boots the
		// first — so a nick collision there does not cost a suffix, it costs
		// somebody else's connection. IRC nicks are per-connection and the
		// collision is recoverable, which is what makes presence safe on it.
	default:
		return fmt.Errorf("unknown --backend %q (irc|toc)", *backend)
	}

	d, err := buddylist.New(buddylist.Config{
		Rooms:      strings.Split(*rooms, ","),
		APIAddr:    apiAddr,
		Exchange:   *exchange,
		SocketPath: *socket,
		Journal:    j,
		Keep:       *keep,
		Log:        slog.Default(),
		Dial:       dial,
		DialAs:     dialAs,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("buddylistd up", "backend", *backend, "server", *server, "rooms", *rooms, "socket", *socket)
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

// splitFrom pulls `--from <label>` out of an argument list from ANY position,
// leaving the rest as the message.
//
// It is hand-parsed, and a flag.FlagSet cannot replace it, which is worth
// writing down because the shape looks like an oversight next to every other
// verb here. `say` and `dm` take variadic text, and the documented form puts
// the flag last: `buddylist say lobby ship it --from alpha`. Go's flag package
// stops parsing at the first argument that does not begin with '-', so a
// FlagSet would see "lobby" and hand back "--from" and "alpha" as two more
// words of the message — relayed into the room, silently, as text. `read`
// escapes this only because its positional is single and can be sliced off
// before Parse; there is no equivalent for a trailing variadic.
func splitFrom(args []string) (rest []string, from string) {
	from = "operator"
	for i := 0; i < len(args); i++ {
		if args[i] == "--from" && i+1 < len(args) {
			from = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return rest, from
}

func runSay(args []string) error {
	args, from := splitFrom(args)
	if len(args) < 2 {
		return fmt.Errorf("usage: buddylist say <room> <text...> [--from <label>]")
	}
	_, err := daemon().Call(buddylist.Request{
		Op: "say", Room: args[0], From: from, Text: strings.Join(args[1:], " "),
	})
	return err
}

func runDM(args []string) error {
	args, from := splitFrom(args)
	if len(args) < 2 {
		return fmt.Errorf("usage: buddylist dm <to> <text...> [--from <label>]")
	}
	_, err := daemon().Call(buddylist.Request{
		Op: "dm", To: args[0], From: from, Text: strings.Join(args[1:], " "),
	})
	return err
}

func runRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	after := fs.Int64("after", 0, "return messages with seq greater than this")
	before := fs.Int64("before", 0, "return the messages just before this seq (walks backwards)")
	tail := fs.Int("tail", 0, "return the newest N messages")
	limit := fs.Int("limit", 50, "max messages")
	mentions := fs.String("mentions", "", "comma-separated names; show only messages naming one of them")
	room := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		room, args = args[0], args[1:]
	}
	fs.Parse(args)
	if room == "" {
		return fmt.Errorf("usage: buddylist read <room> [--after <seq>] [--before <seq>] [--tail <n>] [--limit <n>] [--mentions a,b]")
	}
	resp, err := daemon().Call(buddylist.Request{Op: "read", Room: room,
		After: *after, Before: *before, Tail: *tail, Limit: *limit,
		Mentions: splitList(*mentions)})
	if err != nil {
		return err
	}
	if resp.Gap {
		fmt.Println("(gap: messages before this point aged out of the journal)")
	}
	for _, m := range resp.Msgs {
		// Sender and body are wire-derived hostile text, and the fence lives
		// inside RenderRow — which is the SAME renderer the MCP reader uses.
		// It was a second copy of the format here, and a second copy is how a
		// peer's <BR>s end up fabricating perfectly formatted journal rows
		// (and ANSI escapes) on the operator's terminal from the one copy that
		// was not hardened.
		fmt.Println(buddylist.RenderRow(m))
	}
	return nil
}

// splitList turns a comma-separated flag into tokens, dropping the blanks a
// trailing or doubled comma leaves behind — an empty token would be refused
// by the journal and take the whole read down with it.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// runStatus prints the per-room backlog digest: counts only, never content.
func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	session := fs.String("session", "", "session id whose read cursor to report (default: none — unread is then the whole retained room)")
	mentions := fs.String("mentions", "", "comma-separated names to count as addressed")
	fs.Parse(args)
	resp, err := daemon().Call(buddylist.Request{Op: "stat",
		Session: *session, Mentions: splitList(*mentions)})
	if err != nil {
		return err
	}
	if *session == "" {
		fmt.Println("(no --session: unread is the whole retained room, not anyone's backlog)")
	}
	fmt.Println(buddylist.StatHeader())
	for _, st := range resp.Stats {
		gap := ""
		if st.Gap {
			// Terse for a terminal; the MCP tool result spells the same fact
			// out for a model. That wording is the only thing the two callers
			// of RenderStat are allowed to differ about.
			gap = "  [GAP]"
		}
		fmt.Println(buddylist.RenderStat(st, gap))
	}
	return nil
}

func runWho() error {
	resp, err := daemon().Call(buddylist.Request{Op: "who"})
	if err != nil {
		return err
	}
	if !resp.Connected {
		fmt.Println("(disconnected from the chat server — membership unknown)")
	}
	for room, names := range resp.Rooms {
		for i, n := range names {
			names[i] = buddylist.Fence(n, 64)
		}
		fmt.Printf("%s: %s\n", buddylist.Fence(room, 64), strings.Join(names, ", "))
	}
	return nil
}

// runMCP serves the Model Context Protocol on stdio: the agents' chat tools.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	profile := fs.String("profile", string(buddylist.ProfileCore), "tool profile: core or full")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: buddylist mcp [--profile core|full]")
	}
	cwd, _ := os.Getwd()
	return buddylist.ServeMCP(os.Stdin, os.Stdout, buddylist.MCPDeps{
		Profile: buddylist.MCPProfile(*profile),
		Call:    callAt,
		// Resolved per call, not once at startup: a session's ledger row is
		// written by its SessionStart hook, which can land after the MCP
		// server is spawned. Caching "unknown" here would key this session's
		// read cursor to nothing for its whole life.
		Label:     func() string { _, label := cli.SessionIdentityFor(cwd); return label },
		SessionID: func() string { id, _ := cli.SessionIdentityFor(cwd); return id },
		Slugs: func() []string {
			_, _, slugs := cli.ChatIdentity(cwd, "")
			return slugs
		},
	})
}

// runAlert is the PostToolUse hook half of proactive alerting: it tells this
// session that a room message names it, without making it read the room.
//
// It lives on the CHAT binary and gets its own hook entry on purpose. That is
// the answer to the coupling question this feature was held on: pushing chat
// through `buddy` would have put the chat stack in front of the claims
// ledger's hot path, and the whole design rests on chat never being able to
// drag the safety-critical half. Here the ledger is only READ (for the claim
// slugs peers actually address), the daemon call is bounded, and every
// failure — no daemon, no socket, no ledger, no session — costs the notice
// and nothing else. The hook line ends in `exit 0`.
func runAlert(args []string) error {
	fs := flag.NewFlagSet("alert", flag.ExitOnError)
	session := fs.String("session", "", "session id (default: from hook JSON on stdin)")
	dir := fs.String("cwd", "", "working directory whose ledger names this session (default: hook JSON, then $PWD)")
	fs.Parse(args)

	id, label, slugs, hookDriven := resolveIdentity(*session, *dir)
	deps := buddylist.AlertDeps{
		Call:      callAt,
		SessionID: id, Label: label, Slugs: slugs,
	}
	return buddylist.RunAlert(deps, func(text string) error {
		// Hand runs print the notice; only a hook gets the envelope. The
		// alert is silent by design, which makes "is it working?" an
		// unanswerable question without a surface to ask it from — this is
		// that surface, and it is the same code path, not a mirror of it.
		if !hookDriven {
			_, err := fmt.Print(text)
			return err
		}
		// One hook event emits one JSON document. The envelope is
		// buddylist.HookContext's, not main's: a wire format is not wiring.
		line, err := buddylist.HookContext(text)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(line)
		return err
	})
}

// maxNoteBytes caps a daemon note printed to the terminal. The note is the
// daemon's last system line, and that line quotes the connection's own error
// (an IRC ERROR reply, a server notice) verbatim and unbounded — so it is
// server-influenced text and gets the body cap, rendered on one line.
const maxNoteBytes = 4096

// presenceCallTime bounds the round-trip. SessionEnd is not the alert's hot
// path, but a wedged daemon must not hold up a session's exit either.
const presenceCallTime = 2 * time.Second

// runPresence announces or retires this session's buddy in its project room.
//
// The PostToolUse alert already carries presence for a working session, so the
// job left for a hook line is the retirement: SessionEnd runs this with --gone
// and the session leaves the room at once instead of aging out. Hand-run
// without --gone it announces, which is also how you check the feature is on.
//
// Like the alert, it is decoration on the chat binary: no daemon, no socket,
// no ledger, or no session each cost the presence update and nothing else.
func runPresence(args []string) error {
	fs := flag.NewFlagSet("presence", flag.ExitOnError)
	session := fs.String("session", "", "session id (default: from hook JSON on stdin)")
	dir := fs.String("cwd", "", "working directory whose ledger names this session (default: hook JSON, then $PWD)")
	gone := fs.Bool("gone", false, "retire this session's buddy now (SessionEnd)")
	fs.Parse(args)

	id, label, slugs, hookDriven := resolveIdentity(*session, *dir)
	if id == "" {
		return errors.New("no session identity (pass --session, or run inside a session)")
	}
	resp, err := buddylist.Client{Socket: defaultSocket(), Timeout: presenceCallTime}.Call(
		buddylist.Request{Op: "presence", Session: id, Label: label, Slugs: slugs, Gone: *gone})
	if err != nil {
		return err
	}
	if !hookDriven {
		// The note is the daemon's last system line, which embeds the wire's
		// own error text; a server can put a newline in that.
		fmt.Println(buddylist.Fence(resp.Note, maxNoteBytes))
	}
	return nil
}

// resolveIdentity answers "which session is this, and what does it claim" for
// the two hook-carried verbs, alert and presence. Flags win when given; the
// hook JSON on stdin fills what they leave blank; the cwd falls back to $PWD.
// hookDriven reports whether stdin was a hook payload, which decides the
// output envelope (a hook gets JSON, a hand run gets text).
//
// One helper because it was two verbatim copies, and the copies had already
// begun to drift: the alert's copy carried the comment explaining why hook
// JSON outranks the environment and the presence copy did not. The rule is
// the same for both and belongs in one place:
//
// Hook JSON is the authority when it is there. A session id taken from the
// environment can name a DIFFERENT session in the same checkout, and the
// alert cursor it would advance — or the buddy it would retire — is not ours.
func resolveIdentity(session, dir string) (id, label string, slugs []string, hookDriven bool) {
	sid, cwd := session, dir
	if sid == "" || cwd == "" {
		if h, err := readHookStdin(); err == nil {
			hookDriven = true
			if sid == "" {
				sid = h.SessionID
			}
			if cwd == "" {
				cwd = h.Cwd
			}
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	id, label, slugs = cli.ChatIdentity(cwd, sid)
	if id == "" {
		id = sid
	}
	return id, label, slugs, hookDriven
}

// hookStdin is the sliver of the hook payload the alert needs. It is parsed
// here rather than reused from internal/cli because that parser fails CLOSED
// for the gate; this one has no authority to fail closed over.
type hookStdin struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

const maxHookStdin = 1 << 20

func readHookStdin() (hookStdin, error) {
	var h hookStdin
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return h, errors.New("no hook input (stdin is a terminal)")
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxHookStdin))
	if err != nil || len(data) == 0 {
		return h, errors.New("no hook input on stdin")
	}
	return h, json.Unmarshal(data, &h)
}

func runHealth() error {
	resp, err := daemon().Call(buddylist.Request{Op: "health"})
	if err != nil {
		return err
	}
	state := "connected"
	if !resp.Connected {
		state = "DISCONNECTED"
	}
	fmt.Printf("%s  last: %s\n", state, buddylist.Fence(resp.Note, maxNoteBytes))
	return nil
}
