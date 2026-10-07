// Package push connects a verified Vikunja delivery to an APNs alert. It is
// the webhook.Dispatcher used by the relay.
package push

import (
	"context"
	"encoding/json"
	"errors"
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

// alert is the APNs aps.alert object. Only the project name, the task title
// and the event type are sent.
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
		}{Alert: alert{Title: title, Body: n.TaskTitle}},
		Event: n.Event,
	})
}
