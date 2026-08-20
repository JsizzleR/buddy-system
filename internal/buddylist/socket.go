package buddylist

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// Request is one JSON line on the control socket.
type Request struct {
	Op    string `json:"op"` // say | read | who | dm | status | health
	Room  string `json:"room,omitempty"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Text  string `json:"text,omitempty"`
	After int64  `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// Response is the one JSON line answered per request.
type Response struct {
	OK        bool                `json:"ok"`
	Error     string              `json:"error,omitempty"`
	Msgs      []Msg               `json:"msgs,omitempty"`
	Gap       bool                `json:"gap,omitempty"`
	Connected bool                `json:"connected,omitempty"`
	Note      string              `json:"note,omitempty"`
	Rooms     map[string][]string `json:"rooms,omitempty"`
}

const maxRequestLine = 64 * 1024

func (d *Daemon) serveSocket(ctx context.Context) error {
	// Single-instance guard: an exclusive flock on a sidecar lockfile, held
	// for the daemon's lifetime and released by the kernel if it dies. Only
	// the lock holder may unlink and rebind the socket — stealing it from a
	// live daemon would leave that daemon connected but unreachable while
	// this one loops on a nick collision: a silent fleet-wide outage. (A
	// dial probe alone left a probe→unlink window where two daemons starting
	// together could both bind — Codex finding.)
	lock, err := os.OpenFile(d.cfg.SocketPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("buddylistd socket lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return fmt.Errorf("buddylistd socket: another daemon is already serving %s (its lock is held)", d.cfg.SocketPath)
	}
	defer lock.Close()              // drops the flock when the daemon's socket server exits
	_ = os.Remove(d.cfg.SocketPath) // ours by lock: any existing socket is stale
	ln, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("buddylistd socket: %w", err)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			d.log.Error("accept failed", "err", err)
			time.Sleep(100 * time.Millisecond) // no tight loop on persistent errors
			continue
		}
		go d.serveConn(conn)
	}
}

func (d *Daemon) serveConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), maxRequestLine)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			enc.Encode(Response{Error: "bad request: " + err.Error()})
			return
		}
		// A reply that cannot be written ends the connection: continuing after
		// a lost response would leave the caller guessing (Codex finding).
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if err := enc.Encode(d.dispatch(req)); err != nil {
			return
		}
	}
}

func (d *Daemon) dispatch(req Request) Response {
	switch req.Op {
	case "say":
		if req.Room == "" || req.Text == "" {
			return Response{Error: "say needs room and text"}
		}
		if err := d.Say(req.Room, req.From, req.Text); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true}
	case "dm":
		if req.To == "" || req.Text == "" {
			return Response{Error: "dm needs to and text"}
		}
		if err := d.DM(req.To, req.From, req.Text); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true}
	case "read":
		if req.Room == "" {
			return Response{Error: "read needs room"}
		}
		msgs, gap, err := d.cfg.Journal.ReadAfter(req.Room, req.After, req.Limit)
		if err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true, Msgs: msgs, Gap: gap}
	case "status":
		if err := d.Status(req.Text); err != nil {
			return Response{Error: err.Error()}
		}
		return Response{OK: true}
	case "who":
		connected, rooms := d.Who()
		return Response{OK: true, Connected: connected, Rooms: rooms}
	case "health":
		connected, note := d.Health()
		return Response{OK: true, Connected: connected, Note: note}
	default:
		return Response{Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

// Call is the client half: one request, one response, over the unix socket.
func Call(socketPath string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return Response{}, fmt.Errorf("buddylistd not reachable at %s: %w", socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	enc, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(enc, '\n')); err != nil {
		return Response{}, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 10*1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Response{}, err
		}
		return Response{}, errors.New("buddylistd closed the connection without answering")
	}
	var resp Response
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "chatd refused without a reason"
		}
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
