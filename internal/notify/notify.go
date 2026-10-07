// Package notify turns a verified Vikunja delivery into the content of a
// push notification. It decides whether the delivery should be pushed at all.
package notify

import (
	"encoding/json"
	"fmt"
)

// Event names the relay pushes. Project-level names come from earlier
// research and still need checking against the Swagger spec of the target
// Vikunja version (see docs/PUSH_NOTIFICATIONS.md).
const (
	TaskOverdue         = "task.overdue"
	TaskReminderFired   = "task.reminder.fired"
	TaskCreated         = "task.created"
	TaskUpdated         = "task.updated"
	TaskDeleted         = "task.deleted"
	TaskAssigneeCreated = "task.assignee.created"
	TaskAssigneeDeleted = "task.assignee.deleted"
	TaskCommentCreated  = "task.comment.created"
)

var userLevel = map[string]bool{
	TaskOverdue:       true,
	TaskReminderFired: true,
}

var supported = map[string]bool{
	TaskOverdue:         true,
	TaskReminderFired:   true,
	TaskCreated:         true,
	TaskUpdated:         true,
	TaskDeleted:         true,
	TaskAssigneeCreated: true,
	TaskAssigneeDeleted: true,
	TaskCommentCreated:  true,
}

// Notification is the minimal content sent to APNs.
type Notification struct {
	Event       string
	TaskTitle   string
	ProjectName string
}

// delivery holds only the fields the relay reads. Everything else in the body
// is ignored and never kept.
type delivery struct {
	EventName string `json:"event_name"`
	Data      struct {
		Doer *struct {
			ID int64 `json:"id"`
		} `json:"doer"`
		Task *struct {
			Title string `json:"title"`
		} `json:"task"`
		Project *struct {
			Title string `json:"title"`
		} `json:"project"`
	} `json:"data"`
}

// Map parses a verified delivery body and returns the notification to push to
// the device registered for userID. ok is false when nothing should be pushed:
// the event is not supported, or it is a project-level event caused by the
// registered user.
func Map(body []byte, userID int64) (n Notification, ok bool, err error) {
	var d delivery
	if err := json.Unmarshal(body, &d); err != nil {
		return Notification{}, false, fmt.Errorf("decode delivery: %w", err)
	}
	if !supported[d.EventName] {
		return Notification{}, false, nil
	}
	if !userLevel[d.EventName] && d.Data.Doer != nil && d.Data.Doer.ID == userID {
		return Notification{}, false, nil
	}

	n.Event = d.EventName
	if d.Data.Task != nil {
		n.TaskTitle = d.Data.Task.Title
	}
	if d.Data.Project != nil {
		n.ProjectName = d.Data.Project.Title
	}
	return n, true, nil
}
