package store

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func adapterID(si SessionInfo) AdapterIdentity { return AdapterIdentity{si.SessionID, si.Incarnation} }

func adapterPair(t *testing.T) (*Store, *pinnedClock, AdapterIdentity, AdapterIdentity) {
	t.Helper()
	st, clock := openTest(t)
	return st, clock, adapterID(beginLogical(t, st, "sender", "")), adapterID(beginLogical(t, st, "recipient", ""))
}

func TestAdapterDeliveryIsExplicitCapturedAndIdempotent(t *testing.T) {
	st, clock, sender, recipient := adapterPair(t)
	queued, err := st.QueueAdapter(sender, recipient, "assignment")
	if err != nil || queued.State != "queued" || queued.Sender != sender || queued.Recipient != recipient {
		t.Fatalf("queue: %+v %v", queued, err)
	}
	for range 2 {
		inbox, err := st.AdapterInbox(recipient)
		if err != nil || inbox.Count != 1 || len(inbox.Items) != 1 || inbox.Items[0].Body != "assignment" {
			t.Fatalf("fetch consumed or changed mail: %+v %v", inbox, err)
		}
	}
	// A sender ending does not erase a durable message or lend it a new sender.
	if err := st.EndLogical(sender.SessionID, sender.Incarnation); err != nil {
		t.Fatal(err)
	}
	newSender := adapterID(beginLogical(t, st, sender.SessionID, sender.Incarnation))
	if newSender == sender {
		t.Fatal("restart did not change identity")
	}
	delivered, err := st.DeliverAdapter(recipient, queued.MessageID)
	if err != nil || delivered.State != "delivered" || delivered.Sender != sender || delivered.Delivered == 0 ||
		delivered.Acknowledged.Status != "unknown" || delivered.Completed.Status != "unknown" {
		t.Fatalf("delivery overclaims or changed sender: %+v %v", delivered, err)
	}
	clock.advance(time.Minute)
	repeated, err := st.DeliverAdapter(recipient, queued.MessageID)
	if err != nil || !reflect.DeepEqual(delivered, repeated) {
		t.Fatalf("non-idempotent delivery: %+v %v", repeated, err)
	}
	inbox, err := st.AdapterInbox(recipient)
	if err != nil || inbox.Count != 0 {
		t.Fatalf("delivered mail still queued: %+v %v", inbox, err)
	}
}

func TestAdapterQueueAndDeliveryRefuseDelayedIncarnations(t *testing.T) {
	st, _, sender, recipient := adapterPair(t)
	queued, err := st.QueueAdapter(sender, recipient, "old assignment")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EndLogical(recipient.SessionID, recipient.Incarnation); err != nil {
		t.Fatal(err)
	}
	current := adapterID(beginLogical(t, st, recipient.SessionID, recipient.Incarnation))
	for _, target := range []AdapterIdentity{recipient, {recipient.SessionID, ""}, {recipient.SessionID, "wrong"}} {
		if _, err := st.QueueAdapter(sender, target, "late"); !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("queue accepted stale target %+v: %v", target, err)
		}
		if _, err := st.AdapterInbox(target); !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("inbox accepted stale target %+v: %v", target, err)
		}
		if _, err := st.InspectAdapter(target); !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("inspect accepted stale target %+v: %v", target, err)
		}
		if _, err := st.DeliverAdapter(target, queued.MessageID); !errors.Is(err, ErrLogicalIdentity) {
			t.Fatalf("delivered accepted stale target %+v: %v", target, err)
		}
	}
	if _, err := st.DeliverAdapter(current, queued.MessageID); err == nil {
		t.Fatal("successor consumed predecessor's assignment")
	}
	inbox, err := st.AdapterInbox(current)
	if err != nil || inbox.Count != 0 {
		t.Fatalf("successor sees old mail: %+v %v", inbox, err)
	}
	control, err := st.QueueAdapter(sender, current, "new assignment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeliverAdapter(current, control.MessageID); err != nil {
		t.Fatalf("current identity control refused: %v", err)
	}
	if _, err := st.DeliverAdapter(sender, control.MessageID); err == nil {
		t.Fatal("non-recipient delivered another session's message")
	}
	if err := st.EndLogical(sender.SessionID, sender.Incarnation); err != nil {
		t.Fatal(err)
	}
	beginLogical(t, st, sender.SessionID, sender.Incarnation)
	if _, err := st.QueueAdapter(sender, current, "late sender"); !errors.Is(err, ErrLogicalIdentity) {
		t.Fatalf("queue accepted stale sender: %v", err)
	}
}

func TestAdapterMessageCannotLeakThroughTokenlessDrain(t *testing.T) {
	st, _, sender, recipient := adapterPair(t)
	queued, err := st.QueueAdapter(sender, recipient, "captured")
	if err != nil {
		t.Fatal(err)
	}
	target, err := st.ResolveTarget(recipient.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := st.Send(target, "operator", "legacy", SendOpts{SenderKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := st.Undelivered(recipient.SessionID, "")
	if err != nil || len(inbox) != 1 || inbox[0].ID != legacy {
		t.Fatalf("legacy drain saw captured mail: %+v %v", inbox, err)
	}
	if err := st.MarkDelivered(recipient.SessionID, []int64{legacy, queued.MessageID}); err == nil {
		t.Fatal("tokenless delivery accepted adapter id")
	}
	inbox, err = st.Undelivered(recipient.SessionID, "")
	if err != nil || len(inbox) != 1 {
		t.Fatalf("refused batch partially delivered: %+v %v", inbox, err)
	}
	if err := st.MarkDelivered(recipient.SessionID, []int64{legacy}); err != nil {
		t.Fatal(err)
	}
	if err := st.EndLogical(recipient.SessionID, recipient.Incarnation); err != nil {
		t.Fatal(err)
	}
	beginLogical(t, st, recipient.SessionID, recipient.Incarnation)
	inbox, err = st.Undelivered(recipient.SessionID, "")
	if err != nil || len(inbox) != 0 {
		t.Fatalf("successor legacy hook saw predecessor mail: %+v %v", inbox, err)
	}
}

func TestAdapterQueuePublicationRollsBackTogether(t *testing.T) {
	st, _, sender, recipient := adapterPair(t)
	if _, err := st.db.Exec(`CREATE TRIGGER fail_adapter BEFORE INSERT ON adapter_messages BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueAdapter(sender, recipient, "must roll back"); err == nil {
		t.Fatal("injected publication failure ignored")
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM inbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial inbox publication n=%d err=%v", n, err)
	}
	if _, err := st.db.Exec(`DROP TRIGGER fail_adapter`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.QueueAdapter(sender, recipient, "positive control"); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterInspectionSeparatesUnknownAbsentAndReportedOutcomes(t *testing.T) {
	st, _, sender, recipient := adapterPair(t)
	initial, err := st.InspectAdapter(recipient)
	if err != nil || initial.ObservedState.Status != "unknown" || initial.PendingApproval.Status != "unknown" || initial.Endpoint.Status != "unknown" || initial.ReadySHA.Status != "observed_absent" || initial.QueuedMessages.Count != 0 {
		t.Fatalf("unknown/absent collapsed: %+v %v", initial, err)
	}
	if err := st.Claim(sender.SessionID, sender.Incarnation, "run", "", []string{".buddy/slot/run"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.DeclareWaitReady(recipient.SessionID, recipient.Incarnation, []string{"run"}, time.Hour, "", "exact-ready-sha"); err != nil {
		t.Fatal(err)
	}
	if err := st.IdleLogical(recipient.SessionID, recipient.Incarnation, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReleaseOutcome(sender.SessionID, sender.Incarnation, "run", "fail", "reported failure"); err != nil {
		t.Fatal(err)
	}
	got, err := st.InspectAdapter(recipient)
	if err != nil || got.ObservedState.Value != "idle" || got.ReadySHA.Value != "exact-ready-sha" || len(got.RunOutcomes) != 1 || got.RunOutcomes[0].Outcome.Value != "fail" {
		t.Fatalf("inspection lost existing wait facts: %+v %v", got, err)
	}
	w, _, err := st.WaitOf(recipient.SessionID)
	if err != nil || !w.IsOpen() || w.Checks != 0 {
		t.Fatalf("inspection consumed wait: %+v %v", w, err)
	}
	if err := st.BusyLogical(recipient.SessionID, recipient.Incarnation); err != nil {
		t.Fatal(err)
	}
	got, err = st.InspectAdapter(recipient)
	if err != nil || got.ObservedState.Status != "unknown" {
		t.Fatalf("missing idle inferred busy: %+v %v", got, err)
	}
}

func TestAdapterInboxIsBoundedWithoutLosingQueue(t *testing.T) {
	st, _, sender, recipient := adapterPair(t)
	var last AdapterDelivery
	for i := 0; i < AdapterMessageLimit+1; i++ {
		var err error
		if last, err = st.QueueAdapter(sender, recipient, fmt.Sprintf("message %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.AdapterInbox(recipient)
	if err != nil || got.Count != AdapterMessageLimit+1 || len(got.Items) != AdapterMessageLimit || !got.More {
		t.Fatalf("unbounded or lost queue: %+v %v", got, err)
	}
	point, err := st.AdapterInboxMessage(recipient, last.MessageID)
	if err != nil || point.Count != 1 || len(point.Items) != 1 || point.More || point.Items[0].MessageID != last.MessageID {
		t.Fatalf("message beyond first page reported absent: %+v %v", point, err)
	}
	for _, id := range []int64{0, -1} {
		if _, err := st.AdapterInboxMessage(recipient, id); err == nil {
			t.Fatalf("invalid point query id accepted: %d", id)
		}
	}
	for _, msg := range got.Items {
		if _, err := st.DeliverAdapter(recipient, msg.MessageID); err != nil {
			t.Fatal(err)
		}
	}
	got, err = st.AdapterInbox(recipient)
	if err != nil || got.Count != 1 || len(got.Items) != 1 || got.More {
		t.Fatalf("remaining queue lost: %+v %v", got, err)
	}
	if _, err := st.DeliverAdapter(recipient, last.MessageID); err != nil {
		t.Fatal(err)
	}
	point, err = st.AdapterInboxMessage(recipient, last.MessageID)
	if err != nil || point.Count != 0 || len(point.Items) != 0 {
		t.Fatalf("delivered point query still queued: %+v %v", point, err)
	}
}

func TestAdapterEndpointIsCapturedProcessObservation(t *testing.T) {
	st, _, _, recipient := adapterPair(t)
	proc := ProcRef{PID: 333, Born: 444}
	absent, err := st.RegisterAdapterEndpoint(recipient, proc, "")
	if err != nil || absent.Status != "observed_absent" {
		t.Fatalf("absence: %+v %v", absent, err)
	}
	present, err := st.RegisterAdapterEndpoint(recipient, proc, "/private/tmp/custom-claude/receive.sock")
	if err != nil || present.Status != "observed" {
		t.Fatalf("custom endpoint: %+v %v", present, err)
	}
	view, err := st.InspectAdapter(recipient)
	if err != nil || view.Endpoint != present {
		t.Fatalf("endpoint not exposed: %+v %v", view.Endpoint, err)
	}
	for _, bad := range []string{"relative.sock", "/tmp/../tmp/socket", "/tmp/socket\nforged"} {
		if _, err := st.RegisterAdapterEndpoint(recipient, proc, bad); err == nil {
			t.Fatalf("accepted socket %q", bad)
		}
	}
	if _, err := st.RegisterAdapterEndpoint(recipient, ProcRef{PID: 333}, "/tmp/socket"); err == nil {
		t.Fatal("accepted unbound process birth")
	}
	// Process recycling cannot keep an older endpoint observation usable.
	if _, err := st.db.Exec(`UPDATE session_procs SET born=445 WHERE session_id=?`, recipient.SessionID); err != nil {
		t.Fatal(err)
	}
	view, err = st.InspectAdapter(recipient)
	if err != nil || view.Endpoint.Status != "unknown" {
		t.Fatalf("recycled process lent its endpoint: %+v %v", view.Endpoint, err)
	}
	if err := st.EndLogical(recipient.SessionID, recipient.Incarnation); err != nil {
		t.Fatal(err)
	}
	current := adapterID(beginLogical(t, st, recipient.SessionID, recipient.Incarnation))
	if _, err := st.RegisterAdapterEndpoint(recipient, proc, "/tmp/stale.sock"); !errors.Is(err, ErrLogicalIdentity) {
		t.Fatalf("old endpoint reached successor: %v", err)
	}
	view, err = st.InspectAdapter(current)
	if err != nil || view.Endpoint.Status != "unknown" {
		t.Fatalf("successor inherited endpoint: %+v %v", view.Endpoint, err)
	}
	if _, err := st.RegisterAdapterEndpoint(current, ProcRef{PID: 555, Born: 666}, "/tmp/current.sock"); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterSchemaMigrationKeepsExistingInbox(t *testing.T) {
	st, _, sender, recipient := adapterPair(t)
	target, err := st.ResolveTarget(recipient.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Send(target, "old sender", "existing message", SendOpts{SenderSession: sender.SessionID, SenderKnown: true}); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`DROP TABLE adapter_messages`, `DROP TABLE session_endpoints`, `PRAGMA user_version=14`} {
		if _, err := st.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(st.db); err != nil {
		t.Fatal(err)
	}
	inbox, err := st.Undelivered(recipient.SessionID, "")
	if err != nil || len(inbox) != 1 || inbox[0].Body != "existing message" {
		t.Fatalf("migration lost old inbox: %+v %v", inbox, err)
	}
	if _, err := st.QueueAdapter(sender, recipient, "new message"); err != nil {
		t.Fatal(err)
	}
}
