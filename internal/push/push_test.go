package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Seergs/viku-apn-relay/internal/apns"
	"github.com/Seergs/viku-apn-relay/internal/notify"
	"github.com/Seergs/viku-apn-relay/internal/webhook"
)

// taskCreatedFixture is a delivery recorded from Vikunja v2.7.0. Its doer is
// user 1 and its task is "Example task" in project "Inbox".
const taskCreatedFixture = "../webhook/testdata/task_created.json"

type fakeSender struct {
	err      error
	tokens   []string
	payloads [][]byte
}

func (f *fakeSender) Send(_ context.Context, token string, payload []byte) error {
	f.tokens = append(f.tokens, token)
	f.payloads = append(f.payloads, payload)
	return f.err
}

type fakeRemover struct {
	err error
	ids []string
}

func (f *fakeRemover) Remove(id string) error {
	f.ids = append(f.ids, id)
	return f.err
}

const testToken = "device-token-secret-1234"

func target(userID int64) webhook.Target {
	return webhook.Target{ID: "reg-1", VikunjaUserID: userID, APNsToken: testToken}
}

func loadFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(taskCreatedFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func newLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestDispatchSendsPushForAnotherUsersEvent(t *testing.T) {
	var logs bytes.Buffer
	s, r := &fakeSender{}, &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	if err := d.Dispatch(target(2), loadFixture(t)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(s.tokens) != 1 || s.tokens[0] != testToken {
		t.Fatalf("sent to %v, want the registered token once", s.tokens)
	}

	var got struct {
		APS struct {
			Alert struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"alert"`
		} `json:"aps"`
		Event string `json:"event"`
	}
	if err := json.Unmarshal(s.payloads[0], &got); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if got.APS.Alert.Title != "Inbox" || got.APS.Alert.Body != "Example task" || got.Event != notify.TaskCreated {
		t.Fatalf("payload = %+v", got)
	}
	if len(r.ids) != 0 {
		t.Fatal("registration removed after a successful send")
	}
}

func TestDispatchDropsSelfCausedEvent(t *testing.T) {
	var logs bytes.Buffer
	s, r := &fakeSender{}, &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	// The fixture's doer is user 1, so registering user 1 makes the event self-caused.
	if err := d.Dispatch(target(1), loadFixture(t)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(s.tokens) != 0 {
		t.Fatal("self-caused event was sent")
	}
}

func TestDispatchRemovesUnregisteredDevice(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeSender{err: apns.ErrUnregistered}
	r := &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	if err := d.Dispatch(target(2), loadFixture(t)); err != nil {
		t.Fatalf("Dispatch on 410 = %v, want nil so the delivery is not retried", err)
	}
	if len(r.ids) != 1 || r.ids[0] != "reg-1" {
		t.Fatalf("removed %v, want [reg-1]", r.ids)
	}
}

func TestDispatchReturnsErrorWhenRemovalFails(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeSender{err: apns.ErrUnregistered}
	r := &fakeRemover{err: errors.New("db down")}
	d := NewDispatcher(s, r, newLogger(&logs))

	if err := d.Dispatch(target(2), loadFixture(t)); err == nil {
		t.Fatal("Dispatch hid a failed removal")
	}
}

func TestDispatchReturnsErrorForRejectedPush(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeSender{err: &apns.StatusError{Code: 400, Reason: "BadTopic"}}
	r := &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	err := d.Dispatch(target(2), loadFixture(t))
	if err == nil {
		t.Fatal("Dispatch returned nil for a rejected push, so the handler would answer 200")
	}
	if len(r.ids) != 0 {
		t.Fatal("registration removed for a non-410 rejection")
	}
}

func TestDispatchLogsNeverContainTokenOrTaskContent(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeSender{err: &apns.StatusError{Code: 400, Reason: "BadDeviceToken"}}
	d := NewDispatcher(s, &fakeRemover{}, newLogger(&logs))

	_ = d.Dispatch(target(2), loadFixture(t))
	out := logs.String()
	if strings.Contains(out, testToken) {
		t.Fatal("log contains the device token")
	}
	if strings.Contains(out, "Example task") {
		t.Fatal("log contains task content")
	}
}

func TestDispatchRejectsMalformedBody(t *testing.T) {
	var logs bytes.Buffer
	d := NewDispatcher(&fakeSender{}, &fakeRemover{}, newLogger(&logs))

	if err := d.Dispatch(target(2), []byte(`{`)); err == nil {
		t.Fatal("Dispatch accepted a malformed body")
	}
}

func TestPayloadFallsBackToVikunjaTitle(t *testing.T) {
	p, err := Payload(notify.Notification{Event: notify.ProjectUpdated})
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if !strings.Contains(string(p), `"title":"Vikunja"`) {
		t.Fatalf("payload = %s, want the fallback title", p)
	}
	if strings.Contains(string(p), `"body"`) {
		t.Fatalf("payload = %s, body should be omitted when there is no task", p)
	}
}
