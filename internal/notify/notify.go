// Package notify turns a verified Vikunja delivery into the content of a
// push notification. It drops deliveries caused by the registered user.
package notify

import (
	"encoding/json"
	"fmt"
)

// Event names the relay pushes. They match the events Vikunja offers on a
// project webhook.
const (
	ProjectDeleted        = "project.deleted"
	ProjectSharedTeam     = "project.shared.team"
	ProjectSharedUser     = "project.shared.user"
	ProjectUpdated        = "project.updated"
	TaskAssigneeCreated   = "task.assignee.created"
	TaskAssigneeDeleted   = "task.assignee.deleted"
	TaskAttachmentCreated = "task.attachment.created"
	TaskAttachmentDeleted = "task.attachment.deleted"
	TaskCommentCreated    = "task.comment.created"
	TaskCommentDeleted    = "task.comment.deleted"
	TaskCommentEdited     = "task.comment.edited"
	TaskCreated           = "task.created"
	TaskDeleted           = "task.deleted"
	TaskOverdue           = "task.overdue"
	TaskRelationCreated   = "task.relation.created"
	TaskRelationDeleted   = "task.relation.deleted"
	TaskReminderFired     = "task.reminder.fired"
	TaskUpdated           = "task.updated"
	TasksOverdue          = "tasks.overdue"
)

// userLevel events are sent for the signed-in user, not for a project, so the
// self-action rule never drops them.
var userLevel = map[string]bool{
	TaskOverdue:       true,
	TaskReminderFired: true,
	TasksOverdue:      true,
}

var supported = map[string]bool{
	ProjectDeleted:        true,
	ProjectSharedTeam:     true,
	ProjectSharedUser:     true,
	ProjectUpdated:        true,
	TaskAssigneeCreated:   true,
	TaskAssigneeDeleted:   true,
	TaskAttachmentCreated: true,
	TaskAttachmentDeleted: true,
	TaskCommentCreated:    true,
	TaskCommentDeleted:    true,
	TaskCommentEdited:     true,
	TaskCreated:           true,
	TaskDeleted:           true,
	TaskOverdue:           true,
	TaskRelationCreated:   true,
	TaskRelationDeleted:   true,
	TaskReminderFired:     true,
	TaskUpdated:           true,
	TasksOverdue:          true,
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
