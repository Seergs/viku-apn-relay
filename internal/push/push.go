// Package push connects a verified Vikunja delivery to an APNs alert. It is
// the webhook.Dispatcher used by the relay.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Seergs/viku-apn-relay/internal/apns"
	"github.com/Seergs/viku-apn-relay/internal/notify"
	"github.com/Seergs/viku-apn-relay/internal/webhook"
)

const sendTimeout = 10 * time.Second

// Sender delivers one payload to one APNs device token. collapseID, when
// non-empty, is sent as the apns-collapse-id header so APNs can replace a
// still-queued delivery for the same task/project instead of stacking it.
type Sender interface {
	Send(ctx context.Context, deviceToken string, payload []byte, collapseID string) error
}

// Remover deletes a registration by its opaque id.
type Remover interface {
	Remove(id string) error
}

// Dispatcher maps a delivery to a push and sends it. Logs carry status codes
// and outcomes only, never the payload or the device token.
type Dispatcher struct {
	sender  Sender
	remover Remover
	log     *slog.Logger
}

// NewDispatcher returns a Dispatcher that sends through sender and removes
// registrations through remover.
func NewDispatcher(sender Sender, remover Remover, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{sender: sender, remover: remover, log: logger}
}

// Dispatch pushes the notification for body to the target device. Deliveries
// that map to no push return nil. A device APNs reports as unregistered is
// removed and returns nil, because retrying cannot succeed.
//
// A tasks.overdue delivery is a digest of several tasks, not one event, so it
// is fanned out into one push per task instead of going through notify.Map.
func (d *Dispatcher) Dispatch(t webhook.Target, body []byte) error {
	isDigest, err := notify.IsOverdueDigest(body)
	if err != nil {
		return err
	}
	if isDigest {
		return d.dispatchOverdueDigest(t, body)
	}

	n, ok, err := notify.Map(body, t.VikunjaUserID)
	if err != nil {
		return err
	}
	if !ok {
		d.log.Info("delivery not pushed", "outcome", "dropped")
		return nil
	}

	payload, collapseID, err := Payload(n, t.ID, t.AccountKey)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	_, err = d.send(ctx, t, payload, collapseID)
	return err
}

// dispatchOverdueDigest sends one push per task in a tasks.overdue delivery,
// sharing each task's project thread-id so iOS stacks them together instead
// of showing one row per task. It stops at the first task whose send fails:
// on 410 Unregistered that means the registration is already gone, and on
// any other error Vikunja will retry the whole digest.
func (d *Dispatcher) dispatchOverdueDigest(t webhook.Target, body []byte) error {
	tasks, err := notify.MapOverdueDigest(body)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		d.log.Info("delivery not pushed", "outcome", "dropped")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	for _, task := range tasks {
		payload, collapseID, err := PayloadForTask(task, t.ID, t.AccountKey)
		if err != nil {
			return err
		}
		if stop, err := d.send(ctx, t, payload, collapseID); stop {
			return err
		}
	}
	return nil
}

// send delivers one payload and handles the outcome the same way for every
// caller: log and return nil on success, remove the registration on 410, or
// log and return the error for anything else. stop reports whether the
// caller's loop (if any) should send no further pushes to t.
func (d *Dispatcher) send(ctx context.Context, t webhook.Target, payload []byte, collapseID string) (stop bool, err error) {
	sendErr := d.sender.Send(ctx, t.APNsToken, payload, collapseID)
	switch {
	case sendErr == nil:
		d.log.Info("push sent", "status", 200)
		return false, nil
	case errors.Is(sendErr, apns.ErrUnregistered):
		if rerr := d.remover.Remove(t.ID); rerr != nil {
			d.log.Error("remove unregistered registration", "error", rerr)
			return true, rerr
		}
		d.log.Info("registration removed", "status", 410, "reason", "Unregistered")
		return true, nil
	}

	var se *apns.StatusError
	if errors.As(sendErr, &se) {
		d.log.Warn("push failed", "status", se.Code, "reason", se.Reason)
	} else {
		d.log.Error("push failed", "error", sendErr)
	}
	return true, sendErr
}

// alert is the APNs aps.alert object. The title stays the project name; the
// body is a per-event sentence built by Body.
type alert struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// Payload builds the APNs JSON body for n, plus the apns-collapse-id header
// value Dispatch should send alongside it (empty when there's nothing to
// collapse against, e.g. the tasks.overdue digest).
//
// Both thread-id (grouping, inside the JSON) and collapse-id (de-duplication,
// an HTTP header set by the caller) are scoped by APNs to this device's APNs
// token and the app's topic only — never to a Vikunja account. One device can
// hold several registrations, one per connected Vikunja instance, all sharing
// that same token, and two different self-hosted instances can easily assign
// the same project or task id. Building these values from n's ids alone would
// group, or even collapse, notifications from unrelated accounts, so both are
// namespaced with registrationID — webhook.Target.ID, already unique per
// account per device (it's the same opaque id the webhook URL is built from).
//
// task_id/project_id/account_key ride alongside the alert as custom data so
// the app can resolve and open the right task on tap: task_id/project_id are
// only unique within accountKey's Vikunja instance, and the app may have
// several accounts registered on this device, so accountKey (the app's own
// account_key, passed through unchanged) is what tells it which saved
// connection to resolve them against.
func Payload(n notify.Notification, registrationID, accountKey string) (payload []byte, collapseID string, err error) {
	title := n.ProjectName
	if title == "" {
		title = "Vikunja"
	}

	var threadID string
	if n.ProjectID != 0 {
		threadID = fmt.Sprintf("%s-project-%d", registrationID, n.ProjectID)
	}
	switch {
	case n.TaskID != 0:
		collapseID = fmt.Sprintf("%s-task-%d", registrationID, n.TaskID)
	case n.ProjectID != 0:
		collapseID = fmt.Sprintf("%s-project-%d", registrationID, n.ProjectID)
	}

	payload, err = json.Marshal(struct {
		APS struct {
			Alert    alert  `json:"alert"`
			ThreadID string `json:"thread-id,omitempty"`
		} `json:"aps"`
		Event      string `json:"event"`
		TaskID     int64  `json:"task_id,omitempty"`
		ProjectID  int64  `json:"project_id,omitempty"`
		AccountKey string `json:"account_key,omitempty"`
	}{
		APS: struct {
			Alert    alert  `json:"alert"`
			ThreadID string `json:"thread-id,omitempty"`
		}{Alert: alert{Title: title, Body: Body(n)}, ThreadID: threadID},
		Event:      n.Event,
		TaskID:     n.TaskID,
		ProjectID:  n.ProjectID,
		AccountKey: accountKey,
	})
	return payload, collapseID, err
}

// PayloadForTask builds the APNs JSON body for one task out of a
// tasks.overdue digest, plus its apns-collapse-id. It mirrors Payload's
// grouping scheme (thread-id by project, collapse-id by task, both
// namespaced by registrationID) so a digest push stacks in the same thread
// as any other notification about that project. See Payload for why
// task_id/project_id/account_key ride alongside the alert.
func PayloadForTask(t notify.OverdueTask, registrationID, accountKey string) (payload []byte, collapseID string, err error) {
	title := t.ProjectName
	if title == "" {
		title = "Vikunja"
	}

	var threadID string
	if t.ProjectID != 0 {
		threadID = fmt.Sprintf("%s-project-%d", registrationID, t.ProjectID)
	}
	if t.TaskID != 0 {
		collapseID = fmt.Sprintf("%s-task-%d", registrationID, t.TaskID)
	}

	payload, err = json.Marshal(struct {
		APS struct {
			Alert    alert  `json:"alert"`
			ThreadID string `json:"thread-id,omitempty"`
		} `json:"aps"`
		Event      string `json:"event"`
		TaskID     int64  `json:"task_id,omitempty"`
		ProjectID  int64  `json:"project_id,omitempty"`
		AccountKey string `json:"account_key,omitempty"`
	}{
		APS: struct {
			Alert    alert  `json:"alert"`
			ThreadID string `json:"thread-id,omitempty"`
		}{Alert: alert{Title: title, Body: fmt.Sprintf("%q is overdue", t.TaskTitle)}, ThreadID: threadID},
		Event:      notify.TasksOverdue,
		TaskID:     t.TaskID,
		ProjectID:  t.ProjectID,
		AccountKey: accountKey,
	})
	return payload, collapseID, err
}

// bodyTemplates renders the alert body for each event notify.Map supports.
// One entry per event, each a single sentence over n's normalized fields —
// adding or rewording an event's copy is a one-line change here, not a new
// code path. actor/assignee/team fall back to a generic noun so a sentence
// never reads as broken when Vikunja's delivery omits that field.
var bodyTemplates = map[string]func(notify.Notification) string{
	notify.TaskCreated: func(n notify.Notification) string {
		return fmt.Sprintf("%s created %q", actorOf(n), n.TaskTitle)
	},
	notify.TaskUpdated: func(n notify.Notification) string {
		return fmt.Sprintf("%s updated %q", actorOf(n), n.TaskTitle)
	},
	notify.TaskDeleted: func(n notify.Notification) string {
		return fmt.Sprintf("%s deleted %q", actorOf(n), n.TaskTitle)
	},
	notify.TaskAssigneeCreated: func(n notify.Notification) string {
		return fmt.Sprintf("%s assigned %q to %s", actorOf(n), n.TaskTitle, assigneeOf(n))
	},
	notify.TaskAssigneeDeleted: func(n notify.Notification) string {
		return fmt.Sprintf("%s removed %s from %q", actorOf(n), assigneeOf(n), n.TaskTitle)
	},
	notify.TaskAttachmentCreated: func(n notify.Notification) string {
		return fmt.Sprintf("%s attached a file to %q", actorOf(n), n.TaskTitle)
	},
	notify.TaskAttachmentDeleted: func(n notify.Notification) string {
		return fmt.Sprintf("%s removed an attachment from %q", actorOf(n), n.TaskTitle)
	},
	notify.TaskCommentCreated: func(n notify.Notification) string {
		if n.CommentExcerpt == "" {
			return fmt.Sprintf("%s commented on %q", actorOf(n), n.TaskTitle)
		}
		return fmt.Sprintf("%s commented on %q: %s", actorOf(n), n.TaskTitle, n.CommentExcerpt)
	},
	notify.TaskCommentEdited: func(n notify.Notification) string {
		return fmt.Sprintf("%s edited a comment on %q", actorOf(n), n.TaskTitle)
	},
	notify.TaskCommentDeleted: func(n notify.Notification) string {
		return fmt.Sprintf("%s deleted a comment on %q", actorOf(n), n.TaskTitle)
	},
	notify.TaskRelationCreated: func(n notify.Notification) string {
		return fmt.Sprintf("%s linked %q to another task", actorOf(n), n.TaskTitle)
	},
	notify.TaskRelationDeleted: func(n notify.Notification) string {
		return fmt.Sprintf("%s removed a link from %q", actorOf(n), n.TaskTitle)
	},
	notify.ProjectUpdated: func(n notify.Notification) string {
		return fmt.Sprintf("%s updated the project", actorOf(n))
	},
	notify.ProjectDeleted: func(n notify.Notification) string {
		return fmt.Sprintf("%s deleted the project", actorOf(n))
	},
	notify.ProjectSharedUser: func(n notify.Notification) string {
		return fmt.Sprintf("%s shared the project", actorOf(n))
	},
	notify.ProjectSharedTeam: func(n notify.Notification) string {
		team := n.TeamName
		if team == "" {
			team = "a team"
		}
		return fmt.Sprintf("%s shared the project with %s", actorOf(n), team)
	},
	notify.TaskOverdue: func(n notify.Notification) string {
		return fmt.Sprintf("%q is overdue", n.TaskTitle)
	},
	notify.TaskReminderFired: func(n notify.Notification) string {
		return fmt.Sprintf("Reminder: %q", n.TaskTitle)
	},
}

// Body renders the alert body for n.Event. notify.Map only ever returns
// events bodyTemplates has an entry for (both are driven by the same event
// list), so the fallback below is defense in depth, not an expected path.
func Body(n notify.Notification) string {
	if render, ok := bodyTemplates[n.Event]; ok {
		return render(n)
	}
	return n.TaskTitle
}

func actorOf(n notify.Notification) string {
	if n.ActorName == "" {
		return "Someone"
	}
	return n.ActorName
}

func assigneeOf(n notify.Notification) string {
	if n.AssigneeName == "" {
		return "someone"
	}
	return n.AssigneeName
}
