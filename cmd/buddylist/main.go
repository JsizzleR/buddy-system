// buddylist: the concierge chat daemon and its socket clients.
//
//	buddylist serve --server 127.0.0.1:9898 --name SmarterChild --rooms lobby,ops
//	buddylist say    <room> <text...> [--from <label>]
//	buddylist read   <room> [--after <seq>]
//	buddylist who | dm <to> <text...> | health
package main

import (
	"context"
	"flag"
	"fmt"
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: buddylist serve|say|read|who|dm|health|mcp ...")
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
	case "who":
		err = runWho()
	case "dm":
		err = runDM(args)
	case "health":
		err = runHealth()
	case "mcp":
		err = runMCP()
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
	keep := fs.Duration("keep", 14*24*time.Hour, "journal retention")
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
	trim := func() {
		if n, err := j.Trim(*keep); err != nil {
			slog.Error("journal trim failed", "err", err)
		} else if n > 0 {
			slog.Info("journal trimmed", "rows", n)
		}
	}
	trim()

	var dial buddylist.Dialer
	apiAddr := ""
	switch *backend {
	case "irc":
		dial = func(ctx context.Context) (buddylist.Conn, error) {
			return ircwire.Dial(ctx, *server, *name, *pass)
		}
	case "toc":
		apiAddr = *api // exchange-5 rooms need API pre-creation on the oscar server
		dial = func(ctx context.Context) (buddylist.Conn, error) {
			return tocwire.Dial(ctx, *server, *name, *pass, tocwire.WithChatExchange(*exchange))
		}
	default:
		return fmt.Errorf("unknown --backend %q (irc|toc)", *backend)
	}

	d, err := buddylist.New(buddylist.Config{
		Rooms:      strings.Split(*rooms, ","),
		APIAddr:    apiAddr,
		Exchange:   *exchange,
		SocketPath: *socket,
		Journal:    j,
		Log:        slog.Default(),
		Dial:       dial,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Retention is continuous, not launch-only: a long-lived daemon would
	// otherwise grow journal.db without bound.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				trim()
			}
		}
	}()
	slog.Info("buddylistd up", "backend", *backend, "server", *server, "rooms", *rooms, "socket", *socket)
	if err := d.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

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
	_, err := buddylist.Call(defaultSocket(), buddylist.Request{
		Op: "say", Room: args[0], From: from, Text: strings.Join(args[1:], " "),
	}, 5*time.Second)
	return err
}

func runDM(args []string) error {
	args, from := splitFrom(args)
	if len(args) < 2 {
		return fmt.Errorf("usage: buddylist dm <to> <text...> [--from <label>]")
	}
	_, err := buddylist.Call(defaultSocket(), buddylist.Request{
		Op: "dm", To: args[0], From: from, Text: strings.Join(args[1:], " "),
	}, 5*time.Second)
	return err
}

func runRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	after := fs.Int64("after", 0, "return messages with seq greater than this")
	limit := fs.Int("limit", 50, "max messages")
	room := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		room, args = args[0], args[1:]
	}
	fs.Parse(args)
	if room == "" {
		return fmt.Errorf("usage: buddylist read <room> [--after <seq>] [--limit <n>]")
	}
	resp, err := buddylist.Call(defaultSocket(), buddylist.Request{Op: "read", Room: room, After: *after, Limit: *limit}, 5*time.Second)
	if err != nil {
		return err
	}
	if resp.Gap {
		fmt.Println("(gap: messages before this point aged out of the journal)")
	}
	for _, m := range resp.Msgs {
		ts := time.Unix(m.At, 0).Format("15:04")
		who := m.Sender
		if who == "" {
			who = m.Kind
		}
		// Sender and body are wire-derived hostile text: fence them exactly
		// like the MCP reader does, or a peer's <BR>s fabricate perfectly
		// formatted journal rows (and ANSI escapes) on the operator's terminal.
		fmt.Printf("%d %s <%s> %s\n", m.Seq, ts, buddylist.Fence(who, 64), buddylist.Fence(m.Body, 2048))
	}
	return nil
}

func runWho() error {
	resp, err := buddylist.Call(defaultSocket(), buddylist.Request{Op: "who"}, 5*time.Second)
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
func runMCP() error {
	cwd, _ := os.Getwd()
	return buddylist.ServeMCP(os.Stdin, os.Stdout, buddylist.MCPDeps{
		Call: func(req buddylist.Request, timeout time.Duration) (buddylist.Response, error) {
			return buddylist.Call(defaultSocket(), req, timeout)
		},
		Label: func() string { return cli.SessionLabelFor(cwd) },
	})
}

func runHealth() error {
	resp, err := buddylist.Call(defaultSocket(), buddylist.Request{Op: "health"}, 5*time.Second)
	if err != nil {
		return err
	}
	state := "connected"
	if !resp.Connected {
		state = "DISCONNECTED"
	}
	fmt.Printf("%s  last: %s\n", state, resp.Note)
	return nil
}
