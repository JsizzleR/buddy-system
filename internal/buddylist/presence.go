package buddylist

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Per-session buddies: one connection per live session, so the room's nick
// list IS the fleet. A session is present while it works, carries its claim in
// an away message, goes idle, and then leaves on its own.
//
// Four rules keep this PRESENTATION ONLY, which is the whole reason the tier
// is allowed to exist at all:
//
//  1. It never reads the ledger. Claim slugs arrive from the caller that
//     already had them — the alert hook computes ChatIdentity for its own
//     mention tokens — so presence adds no new coupling between the chat stack
//     and the claims ledger. Chat must never be able to drag the safety half.
//  2. It never journals. Every session connection sees the same room traffic
//     the concierge does, so journaling from one would duplicate every message
//     N times and break the journal's "what the server saw" contract, which is
//     one connection's view by construction. Their events are drained and
//     dropped.
//  3. It never blocks a caller. note is map writes and a non-blocking wake;
//     dialing, joining, and away-setting all happen on the manager loop. The
//     alert hook that carries presence has a 750ms budget and a measured 1.2ms
//     warm cost — presence must not appear in it, even when the chat server is
//     down and every dial is timing out.
//  4. It never speaks. A session's messages keep going through the concierge
//     relay, whose @sent outbox is the discriminator that stops the alert path
//     from alerting a session about its own posts. Moving sends onto these
//     connections would move that discriminator, which is a separate change
//     with its own evidence to gather.
const (
	// presenceIdleAfter is when a session stops reading as active. It is not a
	// disconnect: an agent waiting on the operator is still there, and AIM had
	// exactly this state.
	presenceIdleAfter = 5 * time.Minute
	// presenceDropAfter is when the buddy leaves. It matches the ledger's own
	// STALE threshold so the room and `buddy ls` tell the same story.
	presenceDropAfter = 30 * time.Minute
	// presenceIdleBucket coarsens the idle minutes in the away text. Without
	// it every idle buddy rewrites its away line once a minute, which is
	// server traffic that says nothing new.
	presenceIdleBucket = 5 * time.Minute
	presenceTick       = 15 * time.Second
	// maxPresenceBuddies bounds the sockets a runaway fleet can open. ergo
	// exempts localhost from its own limits, so this is ours to enforce.
	maxPresenceBuddies = 16
	// presenceNickTries bounds the collision walk (nick, nick-2, nick-3…).
	// A dead predecessor whose socket has not timed out yet is the common
	// case, and it clears on its own.
	presenceNickTries = 4
	maxNickLen        = 30
	presenceMaxDelay  = 2 * time.Minute
)

// ErrNickInUse reports that registration was refused because the name is
// taken. It lives here, on the Conn seam, rather than being imported from a
// backend: the daemon deliberately does not know whether it is talking to IRC
// or OSCAR, and a DialAs implementation wraps its own refusal in this.
var ErrNickInUse = errors.New("chat: nick in use")

// buddy is one session's presence. Every field is owned by presence.mu; the
// dial goroutine takes a copy of what it needs and reports back under the lock.
type buddy struct {
	session  string
	label    string
	room     string
	slugs    []string
	lastSeen time.Time

	conn    Conn
	nick    string // the nick actually registered (may carry a collision suffix)
	applied string // away text last put on the wire; "" means none yet

	dialing  bool
	failures int
	nextTry  time.Time
}

type presence struct {
	d    *Daemon
	wake chan struct{}

	mu      sync.Mutex
	buddies map[string]*buddy
	capped  bool // reported the buddy cap once; not once per tool call

	// Test seams. Production values come from the constants above.
	idleAfter, dropAfter, tick time.Duration
}

func newPresence(d *Daemon) *presence {
	return &presence{
		d:         d,
		wake:      make(chan struct{}, 1),
		buddies:   map[string]*buddy{},
		idleAfter: presenceIdleAfter,
		dropAfter: presenceDropAfter,
		tick:      presenceTick,
	}
}

// note records that a session is alive and what it is claiming. It is called
// on the socket's hot path, so it does no I/O: worst case it stores a map
// entry and pokes the manager.
func (p *presence) note(session, label string, slugs []string) {
	if p == nil || session == "" || label == "" {
		return
	}
	room := p.d.servedRoom(label)
	if room == "" {
		return // a label whose project has no room here is simply not presented
	}
	nick := nickFor(label)
	if nick == "" {
		return
	}
	now := p.d.now()

	p.mu.Lock()
	b := p.buddies[session]
	if b == nil {
		if len(p.buddies) >= maxPresenceBuddies {
			capped := p.capped
			p.capped = true
			p.mu.Unlock()
			if !capped {
				p.d.log.Warn("presence buddy cap reached; further sessions stay unpresented",
					"cap", maxPresenceBuddies)
			}
			return
		}
		b = &buddy{session: session, nick: nick}
		p.buddies[session] = b
	}
	b.label, b.room = label, room
	b.slugs = append(b.slugs[:0], slugs...)
	sort.Strings(b.slugs)
	b.lastSeen = now
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default: // a reconcile is already pending; it will see this write
	}
}

// forget drops a session's presence immediately (SessionEnd), rather than
// waiting out dropAfter.
func (p *presence) forget(session string) {
	if p == nil || session == "" {
		return
	}
	p.mu.Lock()
	var conn Conn
	if b := p.buddies[session]; b != nil {
		conn, b.conn = b.conn, nil
		delete(p.buddies, session)
		p.capped = false
	}
	p.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// run reconciles wanted presence against live connections until ctx ends.
func (p *presence) run(ctx context.Context) {
	t := time.NewTicker(p.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			p.closeAll()
			return
		case <-t.C:
		case <-p.wake:
		}
		p.reconcile(ctx)
	}
}

// reconcile is the only place connections are created, retired, or updated.
// It runs off the caller's path, so it may block on the wire.
func (p *presence) reconcile(ctx context.Context) {
	now := p.d.now()

	type awayJob struct {
		b    *buddy
		conn Conn
		text string
	}
	// A retired buddy's connection is taken under the lock, not read back
	// after: its drain goroutine is still live and owns the same field.
	type gone struct {
		conn       Conn
		nick, room string
	}
	var expired []gone
	var dials []*buddy
	var aways []awayJob

	p.mu.Lock()
	for id, b := range p.buddies {
		switch {
		case now.Sub(b.lastSeen) >= p.dropAfter:
			delete(p.buddies, id)
			p.capped = false
			expired = append(expired, gone{conn: b.conn, nick: b.nick, room: b.room})
			b.conn = nil
		case b.conn == nil:
			if !b.dialing && !now.Before(b.nextTry) {
				b.dialing = true
				dials = append(dials, b)
			}
		default:
			if text := p.awayText(b, now); text != b.applied {
				aways = append(aways, awayJob{b: b, conn: b.conn, text: text})
			}
		}
	}
	p.mu.Unlock()

	for _, g := range expired {
		if g.conn != nil {
			g.conn.Close()
		}
		p.d.log.Info("presence buddy left", "nick", g.nick, "room", g.room)
	}
	for _, job := range aways {
		err := job.conn.SetAway(job.text)
		p.mu.Lock()
		if job.b.conn == job.conn { // a reconnect may have replaced it meanwhile
			if err == nil {
				job.b.applied = job.text
			} else {
				job.b.applied = ""
			}
		}
		p.mu.Unlock()
		if err != nil {
			p.d.log.Warn("presence away failed", "nick", job.b.nick, "err", err)
		}
	}
	for _, b := range dials {
		go p.dial(ctx, b)
	}
}

// dial brings one buddy online: connect under a free nick, join its room, and
// set its away text. Every failure is this buddy's alone — it backs off and
// the room simply shows one fewer session.
func (p *presence) dial(ctx context.Context, b *buddy) {
	p.mu.Lock()
	base, room := b.nick, b.room
	p.mu.Unlock()

	conn, nick, err := p.connect(ctx, base)
	if err == nil {
		err = conn.ChatJoin(room)
		if err != nil {
			conn.Close()
		}
	}
	if err != nil {
		p.mu.Lock()
		b.dialing = false
		b.failures++
		b.nextTry = p.d.now().Add(backoffFor(b.failures))
		fails := b.failures
		p.mu.Unlock()
		// One line per failure would fill the log while the server is down;
		// the first is the informative one.
		if fails == 1 {
			p.d.log.Warn("presence dial failed", "nick", base, "room", room, "err", err)
		}
		p.wakeSoon()
		return
	}

	p.mu.Lock()
	b.conn, b.nick, b.dialing = conn, nick, false
	b.failures, b.applied = 0, ""
	p.mu.Unlock()
	p.d.log.Info("presence buddy joined", "nick", nick, "room", room)

	go p.drain(b, conn)
	p.wakeSoon() // the away text is applied by the next reconcile
}

// connect walks the collision suffixes. Only a nick-in-use refusal advances
// the walk: any other failure is about the server or the network, and burning
// suffixes on it would leave a session renamed for no reason.
func (p *presence) connect(ctx context.Context, base string) (Conn, string, error) {
	var err error
	for try := 1; try <= presenceNickTries; try++ {
		nick := base
		if try > 1 {
			nick = suffixNick(base, try)
		}
		var conn Conn
		conn, err = p.d.cfg.DialAs(ctx, nick)
		if err == nil {
			return conn, nick, nil
		}
		if !errors.Is(err, ErrNickInUse) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("presence: no free nick near %q: %w", base, err)
}

// drain consumes a session connection's events and drops them. It exists for
// the protocol, not for the content: an unread event channel stops the client
// answering PING and the server disconnects us. Journaling here would write
// every room message once per live session.
func (p *presence) drain(b *buddy, conn Conn) {
	for range conn.Events() {
	}
	p.mu.Lock()
	if b.conn == conn {
		b.conn, b.applied = nil, ""
		b.failures++
		b.nextTry = p.d.now().Add(backoffFor(b.failures))
	}
	p.mu.Unlock()
	p.wakeSoon()
}

func (p *presence) wakeSoon() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *presence) closeAll() {
	p.mu.Lock()
	conns := make([]Conn, 0, len(p.buddies))
	for _, b := range p.buddies {
		if b.conn != nil {
			conns = append(conns, b.conn)
			b.conn = nil
		}
	}
	p.buddies = map[string]*buddy{}
	p.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

// live reports how many session buddies are connected, for `health`.
func (p *presence) live() (connected, wanted int) {
	if p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.buddies {
		if b.conn != nil {
			connected++
		}
	}
	return connected, len(p.buddies)
}

// awayText composes what the room sees: the claim a session holds, and
// whether it is still working. Callers hold p.mu.
func (p *presence) awayText(b *buddy, now time.Time) string {
	claim := "no claim"
	if len(b.slugs) > 0 {
		claim = "claim: " + strings.Join(b.slugs, ", ")
	}
	idle := now.Sub(b.lastSeen)
	if idle < p.idleAfter {
		return claim + " · active"
	}
	// Bucketed so an idle buddy is not rewriting its status every minute.
	mins := int(idle.Truncate(presenceIdleBucket).Minutes())
	return fmt.Sprintf("%s · idle %dm", claim, mins)
}

// backoffFor doubles per consecutive failure, capped. (The package's own min
// takes Durations, so the shifts are bounded the long way round.)
func backoffFor(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures > 7 {
		failures = 7
	}
	return min(time.Duration(1<<uint(failures))*time.Second, presenceMaxDelay)
}

// servedRoom maps a session label ("<project>/s-<id>") to the room this daemon
// actually serves for it, or "" when there is none. The derivation matches the
// SessionStart digest's: the project half of the label IS the room name.
func (d *Daemon) servedRoom(label string) string {
	project := label
	if i := strings.IndexByte(project, '/'); i > 0 {
		project = project[:i]
	}
	project = fold(project)
	if project == "" {
		return ""
	}
	for _, room := range d.cfg.Rooms {
		if fold(room) == project {
			return room
		}
	}
	return ""
}

// nickFor turns a session label into a legal IRC nick. Anything outside the
// permitted set becomes '-', because a label is repo-derived text and a raw
// one would be rejected at registration (or, worse, split the line).
func nickFor(label string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range label {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("[]\\^_{|}-", r)
		if !ok {
			r = '-'
		}
		if r == '-' {
			if prevDash || b.Len() == 0 {
				continue // no leading or doubled separators
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(r)
	}
	nick := strings.TrimRight(b.String(), "-")
	// The unique half of a label is its "s-<id>" tail, so an over-long nick
	// loses project characters rather than identity ones.
	if len(nick) > maxNickLen {
		nick = strings.TrimLeft(nick[len(nick)-maxNickLen:], "-")
	}
	// RFC nicks start with a letter or a special; ergo is lenient, but a
	// digit-leading nick is not worth the compatibility bet.
	if nick != "" && nick[0] >= '0' && nick[0] <= '9' {
		nick = "s" + nick
		if len(nick) > maxNickLen {
			nick = nick[:maxNickLen]
		}
	}
	return nick
}

// suffixNick appends a collision suffix without growing past the nick limit.
func suffixNick(base string, n int) string {
	suffix := fmt.Sprintf("-%d", n)
	if len(base)+len(suffix) > maxNickLen {
		base = base[:maxNickLen-len(suffix)]
	}
	return base + suffix
}
