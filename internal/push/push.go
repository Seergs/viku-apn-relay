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

// Sender delivers one payload to one APNs device token.
type Sender interface {
	Send(ctx context.Context, deviceToken string, payload []byte) error
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
func (d *Dispatcher) Dispatch(t webhook.Target, body []byte) error {
	n, ok, err := notify.Map(body, t.VikunjaUserID)
	if err != nil {
		return err
	}
	if !ok {
		d.log.Info("delivery not pushed", "outcome", "dropped")
		return nil
	}

	payload, err := Payload(n)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	err = d.sender.Send(ctx, t.APNsToken, payload)
	switch {
	case err == nil:
		d.log.Info("push sent", "status", 200)
		return nil
	case errors.Is(err, apns.ErrUnregistered):
		if rerr := d.remover.Remove(t.ID); rerr != nil {
			d.log.Error("remove unregistered registration", "error", rerr)
			return rerr
		}
		d.log.Info("registration removed", "status", 410, "reason", "Unregistered")
		return nil
	}

	var se *apns.StatusError
	if errors.As(err, &se) {
		d.log.Warn("push failed", "status", se.Code, "reason", se.Reason)
	} else {
		d.log.Error("push failed", "error", err)
	}
	return err
}

// alert is the APNs aps.alert object. The title stays the project name (so a
// later thread-id grouping by project reads naturally); the body is a
// per-event sentence built by Body.
type alert struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// Payload builds the APNs JSON body for n.
func Payload(n notify.Notification) ([]byte, error) {
	title := n.ProjectName
	if title == "" {
		title = "Vikunja"
	}
	return json.Marshal(struct {
		APS struct {
			Alert alert `json:"alert"`
		} `json:"aps"`
		Event string `json:"event"`
	}{
		APS: struct {
			Alert alert `json:"alert"`
		}{Alert: alert{Title: title, Body: Body(n)}},
		Event: n.Event,
	})
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
	notify.TasksOverdue: func(notify.Notification) string {
		return "You have overdue tasks"
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
