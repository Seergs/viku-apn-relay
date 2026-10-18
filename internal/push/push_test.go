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

// tasksOverdueFixture is a tasks.overdue digest recorded from a live
// instance: four tasks across two projects, "Inbox" (id 1) and "Example
// Project" (id 7).
const tasksOverdueFixture = "../webhook/testdata/tasks_overdue.json"

// taskReminderFiredFixture is a task.reminder.fired delivery: task 254,
// "Example task", in project "Inbox" (id 1).
const taskReminderFiredFixture = "../webhook/testdata/task_reminder_fired.json"

func loadOverdueDigestFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(tasksOverdueFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func loadReminderFiredFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(taskReminderFiredFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

type fakeSender struct {
	err         error
	tokens      []string
	payloads    [][]byte
	collapseIDs []string
}

func (f *fakeSender) Send(_ context.Context, token string, payload []byte, collapseID string) error {
	f.tokens = append(f.tokens, token)
	f.payloads = append(f.payloads, payload)
	f.collapseIDs = append(f.collapseIDs, collapseID)
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
	return webhook.Target{ID: "reg-1", VikunjaUserID: userID, APNsToken: testToken, AccountKey: "account-1"}
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
			ThreadID string `json:"thread-id"`
		} `json:"aps"`
		Event      string `json:"event"`
		TaskID     int64  `json:"task_id"`
		ProjectID  int64  `json:"project_id"`
		AccountKey string `json:"account_key"`
	}
	if err := json.Unmarshal(s.payloads[0], &got); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if got.APS.Alert.Title != "Inbox" || got.APS.Alert.Body != `Jane Doe created "Example task"` || got.Event != notify.TaskCreated {
		t.Fatalf("payload = %+v", got)
	}
	// target(2) registers under id "reg-1"; the fixture's project is id 1 and
	// its task is id 255.
	if got.APS.ThreadID != "reg-1-project-1" {
		t.Fatalf("thread-id = %q, want it namespaced by the registration id", got.APS.ThreadID)
	}
	if got.TaskID != 255 || got.ProjectID != 1 || got.AccountKey != "account-1" {
		t.Fatalf("payload = %+v, want task_id 255, project_id 1, account_key %q so the app can resolve and open the task", got, "account-1")
	}
	if len(s.collapseIDs) != 1 || s.collapseIDs[0] != "reg-1-task-255" {
		t.Fatalf("collapse-id = %v, want it namespaced by the registration id", s.collapseIDs)
	}
	if len(r.ids) != 0 {
		t.Fatal("registration removed after a successful send")
	}
}

func TestDispatchScopesGroupingPerRegistrationNotPerVikunjaID(t *testing.T) {
	// Two different accounts on the same physical device (same APNs token)
	// can each have a project/task with the same Vikunja id. The registration
	// id must keep their thread-id/collapse-id from colliding.
	var logsA, logsB bytes.Buffer
	sA, sB := &fakeSender{}, &fakeSender{}

	targetA := webhook.Target{ID: "account-a", VikunjaUserID: 2, APNsToken: testToken}
	targetB := webhook.Target{ID: "account-b", VikunjaUserID: 2, APNsToken: testToken}

	if err := NewDispatcher(sA, &fakeRemover{}, newLogger(&logsA)).Dispatch(targetA, loadFixture(t)); err != nil {
		t.Fatalf("Dispatch A: %v", err)
	}
	if err := NewDispatcher(sB, &fakeRemover{}, newLogger(&logsB)).Dispatch(targetB, loadFixture(t)); err != nil {
		t.Fatalf("Dispatch B: %v", err)
	}

	if sA.collapseIDs[0] == sB.collapseIDs[0] {
		t.Fatalf("collapse-id collided across accounts: %q", sA.collapseIDs[0])
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

func TestDispatchSetsReminderFiredCategory(t *testing.T) {
	var logs bytes.Buffer
	s, r := &fakeSender{}, &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	if err := d.Dispatch(target(1), loadReminderFiredFixture(t)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(s.payloads) != 1 {
		t.Fatalf("sent %d pushes, want 1", len(s.payloads))
	}

	var got struct {
		APS struct {
			Category string `json:"category"`
		} `json:"aps"`
	}
	if err := json.Unmarshal(s.payloads[0], &got); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if got.APS.Category != CategoryTaskReminderFired {
		t.Fatalf("category = %q, want %q", got.APS.Category, CategoryTaskReminderFired)
	}
}

func TestDispatchFansOutOverdueDigest(t *testing.T) {
	var logs bytes.Buffer
	s, r := &fakeSender{}, &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	if err := d.Dispatch(target(1), loadOverdueDigestFixture(t)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(s.tokens) != 4 {
		t.Fatalf("sent %d pushes, want one per task (4)", len(s.tokens))
	}

	var first struct {
		APS struct {
			Alert struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"alert"`
			ThreadID string `json:"thread-id"`
			Category string `json:"category"`
		} `json:"aps"`
		TaskID     int64  `json:"task_id"`
		ProjectID  int64  `json:"project_id"`
		AccountKey string `json:"account_key"`
	}
	if err := json.Unmarshal(s.payloads[0], &first); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	// The fixture's first task is id 243 in project 7, "Example Project".
	if first.APS.Alert.Title != "Example Project" || first.APS.Alert.Body != `"Example task 1" is overdue` {
		t.Fatalf("payload = %+v", first)
	}
	if first.APS.Category != CategoryTaskOverdue {
		t.Fatalf("category = %q, want %q", first.APS.Category, CategoryTaskOverdue)
	}
	if first.APS.ThreadID != "reg-1-project-7" {
		t.Fatalf("thread-id = %q, want it namespaced by registration and project", first.APS.ThreadID)
	}
	if first.TaskID != 243 || first.ProjectID != 7 || first.AccountKey != "account-1" {
		t.Fatalf("payload = %+v, want task_id 243, project_id 7, account_key %q", first, "account-1")
	}
	if s.collapseIDs[0] != "reg-1-task-243" {
		t.Fatalf("collapse-id = %q, want it namespaced by registration and task", s.collapseIDs[0])
	}
	// The fixture's second task (id 246) is in a different project (1,
	// "Inbox"), so it must not share the first task's thread.
	var second struct {
		APS struct {
			ThreadID string `json:"thread-id"`
		} `json:"aps"`
	}
	if err := json.Unmarshal(s.payloads[1], &second); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if second.APS.ThreadID == first.APS.ThreadID {
		t.Fatalf("tasks from different projects shared a thread-id: %q", second.APS.ThreadID)
	}
	if len(r.ids) != 0 {
		t.Fatal("registration removed after a successful digest")
	}
}

func TestDispatchOverdueDigestStopsOnUnregistered(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeSender{err: apns.ErrUnregistered}
	r := &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	if err := d.Dispatch(target(1), loadOverdueDigestFixture(t)); err != nil {
		t.Fatalf("Dispatch on 410 = %v, want nil so the delivery is not retried", err)
	}
	if len(s.tokens) != 1 {
		t.Fatalf("sent %d pushes, want the loop to stop after the first 410", len(s.tokens))
	}
	if len(r.ids) != 1 || r.ids[0] != "reg-1" {
		t.Fatalf("removed %v, want [reg-1]", r.ids)
	}
}

func TestDispatchOverdueDigestStopsOnRejectedPush(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeSender{err: &apns.StatusError{Code: 400, Reason: "BadTopic"}}
	r := &fakeRemover{}
	d := NewDispatcher(s, r, newLogger(&logs))

	if err := d.Dispatch(target(1), loadOverdueDigestFixture(t)); err == nil {
		t.Fatal("Dispatch returned nil for a rejected push, so the handler would answer 200")
	}
	if len(s.tokens) != 1 {
		t.Fatalf("sent %d pushes, want the loop to stop after the first rejection", len(s.tokens))
	}
}

func TestDispatchOverdueDigestDropsEmptyDigest(t *testing.T) {
	var logs bytes.Buffer
	s := &fakeSender{}
	d := NewDispatcher(s, &fakeRemover{}, newLogger(&logs))

	body := []byte(`{"event_name":"tasks.overdue","data":{"tasks":[],"projects":{}}}`)
	if err := d.Dispatch(target(1), body); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(s.tokens) != 0 {
		t.Fatal("empty digest sent a push")
	}
}

func TestPayloadForTask(t *testing.T) {
	p, collapseID, err := PayloadForTask(notify.OverdueTask{
		TaskID: 243, TaskTitle: "Example task 1", ProjectID: 7, ProjectName: "Example Project",
	}, "reg-1", "account-1")
	if err != nil {
		t.Fatalf("PayloadForTask: %v", err)
	}

	var got struct {
		APS struct {
			Alert struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"alert"`
			ThreadID string `json:"thread-id"`
		} `json:"aps"`
		Event      string `json:"event"`
		TaskID     int64  `json:"task_id"`
		ProjectID  int64  `json:"project_id"`
		AccountKey string `json:"account_key"`
	}
	if err := json.Unmarshal(p, &got); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if got.APS.Alert.Title != "Example Project" || got.APS.Alert.Body != `"Example task 1" is overdue` || got.Event != notify.TasksOverdue {
		t.Fatalf("payload = %+v", got)
	}
	if got.APS.ThreadID != "reg-1-project-7" {
		t.Fatalf("thread-id = %q, want it namespaced by the registration id", got.APS.ThreadID)
	}
	if collapseID != "reg-1-task-243" {
		t.Fatalf("collapse-id = %q, want it namespaced by the registration id", collapseID)
	}
	if got.TaskID != 243 || got.ProjectID != 7 || got.AccountKey != "account-1" {
		t.Fatalf("payload = %+v, want task_id 243, project_id 7, account_key %q", got, "account-1")
	}
}

func TestPayloadForTaskSetsOverdueCategory(t *testing.T) {
	p, _, err := PayloadForTask(notify.OverdueTask{TaskID: 243, TaskTitle: "Example task 1"}, "reg-1", "account-1")
	if err != nil {
		t.Fatalf("PayloadForTask: %v", err)
	}
	if !strings.Contains(string(p), `"category":"TASK_OVERDUE"`) {
		t.Fatalf("payload = %s, want aps.category set so iOS shows the overdue task's action buttons", p)
	}
}

func TestPayloadForTaskFallsBackToVikunjaTitle(t *testing.T) {
	p, _, err := PayloadForTask(notify.OverdueTask{TaskID: 1, TaskTitle: "Example task"}, "reg-1", "account-1")
	if err != nil {
		t.Fatalf("PayloadForTask: %v", err)
	}
	if !strings.Contains(string(p), `"title":"Vikunja"`) {
		t.Fatalf("payload = %s, want the fallback title", p)
	}
}

func TestPayloadFallsBackToVikunjaTitle(t *testing.T) {
	p, _, err := Payload(notify.Notification{Event: notify.ProjectUpdated}, "reg-1", "account-1")
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if !strings.Contains(string(p), `"title":"Vikunja"`) {
		t.Fatalf("payload = %s, want the fallback title", p)
	}
	if !strings.Contains(string(p), `"body":"Someone updated the project"`) {
		t.Fatalf("payload = %s, want a body with the actor fallback", p)
	}
}

func TestPayloadSetsReminderFiredCategory(t *testing.T) {
	p, _, err := Payload(notify.Notification{Event: notify.TaskReminderFired, TaskID: 254}, "reg-1", "account-1")
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if !strings.Contains(string(p), `"category":"TASK_REMINDER_FIRED"`) {
		t.Fatalf("payload = %s, want aps.category set so iOS shows the reminder's action buttons", p)
	}
}

func TestPayloadOmitsCategoryForNonActionableEvent(t *testing.T) {
	p, _, err := Payload(notify.Notification{Event: notify.TaskCreated, TaskID: 255}, "reg-1", "account-1")
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if strings.Contains(string(p), "category") {
		t.Fatalf("payload = %s, want no aps.category for an event with no actions", p)
	}
}

func TestPayloadOmitsTaskAndProjectIDWhenZero(t *testing.T) {
	p, _, err := Payload(notify.Notification{Event: notify.ProjectDeleted}, "reg-1", "")
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if strings.Contains(string(p), "task_id") || strings.Contains(string(p), "project_id") || strings.Contains(string(p), "account_key") {
		t.Fatalf("payload = %s, want task_id/project_id/account_key omitted when zero/empty", p)
	}
}

func TestPayloadGroupingIDs(t *testing.T) {
	cases := []struct {
		name           string
		n              notify.Notification
		wantThreadID   string
		wantCollapseID string
	}{
		{
			name:           "task event collapses on the task",
			n:              notify.Notification{Event: notify.TaskUpdated, ProjectID: 1, TaskID: 255},
			wantThreadID:   "reg-1-project-1",
			wantCollapseID: "reg-1-task-255",
		},
		{
			name:           "task-less project event falls back to the project",
			n:              notify.Notification{Event: notify.ProjectUpdated, ProjectID: 1},
			wantThreadID:   "reg-1-project-1",
			wantCollapseID: "reg-1-project-1",
		},
		{
			name:           "no project or task means no grouping at all",
			n:              notify.Notification{Event: notify.ProjectDeleted},
			wantThreadID:   "",
			wantCollapseID: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, collapseID, err := Payload(c.n, "reg-1", "account-1")
			if err != nil {
				t.Fatalf("Payload: %v", err)
			}
			var got struct {
				APS struct {
					ThreadID string `json:"thread-id"`
				} `json:"aps"`
			}
			if err := json.Unmarshal(p, &got); err != nil {
				t.Fatalf("payload is not JSON: %v", err)
			}
			if got.APS.ThreadID != c.wantThreadID {
				t.Fatalf("thread-id = %q, want %q", got.APS.ThreadID, c.wantThreadID)
			}
			if collapseID != c.wantCollapseID {
				t.Fatalf("collapse-id = %q, want %q", collapseID, c.wantCollapseID)
			}
		})
	}
}

func TestBodyFallsBackWhenActorOrAssigneeIsMissing(t *testing.T) {
	cases := []struct {
		name string
		n    notify.Notification
		want string
	}{
		{"actor missing", notify.Notification{Event: notify.TaskCreated, TaskTitle: "Example task"}, `Someone created "Example task"`},
		{"assignee missing", notify.Notification{Event: notify.TaskAssigneeCreated, TaskTitle: "Example task", ActorName: "Jane Doe"}, `Jane Doe assigned "Example task" to someone`},
		{"team missing", notify.Notification{Event: notify.ProjectSharedTeam, ActorName: "Jane Doe"}, `Jane Doe shared the project with a team`},
		{"comment missing", notify.Notification{Event: notify.TaskCommentCreated, TaskTitle: "Example task", ActorName: "Jane Doe"}, `Jane Doe commented on "Example task"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Body(c.n); got != c.want {
				t.Fatalf("Body = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBodyUnknownEventFallsBackToTaskTitle(t *testing.T) {
	got := Body(notify.Notification{Event: "something.new", TaskTitle: "Example task"})
	if got != "Example task" {
		t.Fatalf("Body = %q, want the raw task title as a last resort", got)
	}
}
