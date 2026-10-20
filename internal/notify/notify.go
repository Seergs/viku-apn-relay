// Package notify turns a verified Vikunja delivery into the content of a
// push notification. It drops deliveries caused by the registered user.
package notify

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
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
// self-action rule never drops them. TasksOverdue is not listed here: push.
// Dispatch intercepts it before Map ever sees it (see MapOverdueDigest).
var userLevel = map[string]bool{
	TaskOverdue:       true,
	TaskReminderFired: true,
}

// IsUserDirected reports whether event is one of the few events whose scope
// overlap, a project-level and a user-level webhook both matching, can
// legitimately deliver the exact same occurrence twice to one device. Only
// these are eligible for push.Dispatcher's dedup: every other event's
// TaskID is not unique to one occurrence (several different comments on the
// same task share a TaskID, and several different project-level events
// share ProjectID 0 since they have none), so deduping them would risk
// dropping two genuinely different events that happen to land within the
// same window.
func IsUserDirected(event string) bool {
	return userLevel[event]
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
}

// Notification is the normalized content the push package renders into an
// APNs alert. Fields are empty, never absent, when the event has no such
// data (e.g. ActorName for a system-fired reminder, AssigneeName outside an
// assignee event) — push.Body is responsible for the per-field fallback.
type Notification struct {
	Event          string
	TaskID         int64
	TaskTitle      string
	ProjectID      int64
	ProjectName    string
	ActorName      string
	AssigneeName   string
	TeamName       string
	CommentExcerpt string
	// EventUserID is the id of the user a userLevel event is directed at
	// (from the payload's own data.user field), zero for every other event.
	// It is NOT the registered device's own Vikunja user id (see Map's
	// userID parameter). It is what lets push.Dispatcher's dedup tell apart
	// "my own reminder, delivered twice" (same EventUserID, a true
	// duplicate when a project-level and a user-level webhook both match)
	// from "a project-mate's reminder" (a different EventUserID, not a
	// duplicate) when both scopes are subscribed to the same project.
	EventUserID int64
}

// delivery holds only the fields the relay reads. Everything else in the body
// is ignored and never kept.
type delivery struct {
	EventName string `json:"event_name"`
	Data      struct {
		Doer *struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"doer"`
		Task *struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"task"`
		Project *struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"project"`
		Assignee *struct {
			Name string `json:"name"`
		} `json:"assignee"`
		Team *struct {
			Name string `json:"name"`
		} `json:"team"`
		Comment *struct {
			Comment string `json:"comment"`
		} `json:"comment"`
		User *struct {
			ID int64 `json:"id"`
		} `json:"user"`
		Tasks []struct {
			ID        int64  `json:"id"`
			Title     string `json:"title"`
			ProjectID int64  `json:"project_id"`
		} `json:"tasks"`
		Projects map[string]struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"projects"`
	} `json:"data"`
}

// OverdueTask is one task out of a tasks.overdue digest, with its project
// name already resolved. Unlike Notification, there is no ActorName: the
// digest is a system-fired, user-level event with no doer.
type OverdueTask struct {
	TaskID      int64
	TaskTitle   string
	ProjectID   int64
	ProjectName string
}

// commentExcerptLimit is the max rune length of CommentExcerpt. Vikunja
// comments are HTML and can be long; the push body only needs enough to
// recognize the comment, not the whole thing.
const commentExcerptLimit = 120

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// plainTextExcerpt strips HTML tags and entities from a Vikunja comment body
// and truncates it to commentExcerptLimit runes on a rune boundary (the
// source is user text, so byte-slicing could cut a multi-byte character in
// half).
func plainTextExcerpt(htmlBody string) string {
	text := html.UnescapeString(htmlTag.ReplaceAllString(htmlBody, " "))
	text = strings.Join(strings.Fields(text), " ")

	runes := []rune(text)
	if len(runes) <= commentExcerptLimit {
		return text
	}
	return string(runes[:commentExcerptLimit]) + "…"
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
		n.TaskID = d.Data.Task.ID
		n.TaskTitle = d.Data.Task.Title
	}
	if d.Data.Project != nil {
		n.ProjectID = d.Data.Project.ID
		n.ProjectName = d.Data.Project.Title
	}
	if d.Data.Doer != nil {
		n.ActorName = d.Data.Doer.Name
	}
	if d.Data.Assignee != nil {
		n.AssigneeName = d.Data.Assignee.Name
	}
	if d.Data.Team != nil {
		n.TeamName = d.Data.Team.Name
	}
	if d.Data.Comment != nil {
		n.CommentExcerpt = plainTextExcerpt(d.Data.Comment.Comment)
	}
	if userLevel[d.EventName] && d.Data.User != nil {
		n.EventUserID = d.Data.User.ID
	}
	return n, true, nil
}

// IsOverdueDigest reports whether body is a tasks.overdue delivery. push.
// Dispatch checks this before Map, because a digest fans out into one push
// per task instead of mapping to a single Notification.
func IsOverdueDigest(body []byte) (bool, error) {
	var d struct {
		EventName string `json:"event_name"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return false, fmt.Errorf("decode delivery: %w", err)
	}
	return d.EventName == TasksOverdue, nil
}

// DigestUserID returns the id of the user a tasks.overdue digest is
// directed at. Every task in one digest shares the same user (Vikunja
// dispatches one TasksOverdueEvent per user with all of that user's overdue
// tasks attached), so this is read once per delivery rather than per task.
// See push.Dispatcher's per-task dedup key in dispatchOverdueDigest.
func DigestUserID(body []byte) (int64, error) {
	var d delivery
	if err := json.Unmarshal(body, &d); err != nil {
		return 0, fmt.Errorf("decode delivery: %w", err)
	}
	if d.Data.User == nil {
		return 0, nil
	}
	return d.Data.User.ID, nil
}

// MapOverdueDigest parses a verified tasks.overdue delivery body into one
// OverdueTask per task, each with its project name resolved from the
// delivery's project map. A task whose project is missing from that map
// (should not happen, but the field is attacker-controlled input) gets an
// empty ProjectName; push.PayloadForTask falls back to "Vikunja" for it.
func MapOverdueDigest(body []byte) ([]OverdueTask, error) {
	var d delivery
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("decode delivery: %w", err)
	}

	tasks := make([]OverdueTask, 0, len(d.Data.Tasks))
	for _, t := range d.Data.Tasks {
		ot := OverdueTask{TaskID: t.ID, TaskTitle: t.Title, ProjectID: t.ProjectID}
		if p, ok := d.Data.Projects[strconv.FormatInt(t.ProjectID, 10)]; ok {
			ot.ProjectName = p.Title
		}
		tasks = append(tasks, ot)
	}
	return tasks, nil
}
