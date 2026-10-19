package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// MaxCommandBytes bounds a command's message, as on every device transport.
const MaxCommandBytes = 64 << 10

// CommandAccepted is a command queued for the element's device.
type CommandAccepted struct {
	CommandID uuid.UUID `json:"command_id" doc:"The id of the element event the command became"`
	ElementID uuid.UUID `json:"element_id"`
	Status    string    `json:"status" enum:"accepted" doc:"accepted: on its way to the device; GET /api/v1/commands/{id} and the stream tell what became of it"`
	Time      time.Time `json:"time"`
}

// SendCommand sends a user's command (in engineering units) to an element's
// device, like a dashboard does: it becomes an element event, published
// through the outbox in the transaction that audits it, and the gateways
// deliver it (converted by the element's pipeline) to the device's
// connections and MQTT downlinks. The caller has checked the RC grant.
func (s *Service) SendCommand(ctx context.Context, a Actor, elementID uuid.UUID, message json.RawMessage) (CommandAccepted, error) {
	message = bytes.TrimSpace(message)
	switch {
	case len(message) == 0 || string(message) == "null":
		return CommandAccepted{}, invalid("message", "required")
	case len(message) > MaxCommandBytes:
		return CommandAccepted{}, invalid("message", "at most 64 KiB")
	}
	if err := history.ValidateMessage(message); err != nil {
		return CommandAccepted{}, invalid("message", err.Error())
	}
	el, err := store.GetElement(ctx, s.Pool, elementID)
	if err != nil {
		return CommandAccepted{}, err
	}
	dev, err := store.GetDevice(ctx, s.Pool, el.DeviceID)
	if err != nil {
		return CommandAccepted{}, err
	}
	// a command the pipeline can't convert would never reach the device:
	// refuse it here, as the dashboard does
	if cur, err := store.GetElementPipeline(ctx, s.Pool, elementID); err == nil {
		pipe, err := ValidatePipeline(cur.Steps)
		if err == nil && pipe.Invertible() {
			dec := json.NewDecoder(bytes.NewReader(message))
			dec.UseNumber()
			var v any
			if err := dec.Decode(&v); err != nil {
				return CommandAccepted{}, invalid("message", "invalid JSON")
			}
			msg, _ := elementpipe.WrapScalar(v)
			if _, err := pipe.InverseWithContext(msg, elementpipe.Context{Element: el.Name, Device: dev.Name, Time: time.Now().UTC()}); err != nil {
				return CommandAccepted{}, invalid("message", "the element's pipeline can't convert it for the device: "+err.Error())
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return CommandAccepted{}, err
	}

	actorID := ""
	if a.UserID != nil {
		actorID = idStr(*a.UserID)
	}
	ev, err := events.New(events.TypeElementMessage, events.SourceAPI, elementID.String(), events.ElementMessage{
		ElementID: elementID, DeviceID: el.DeviceID, Source: events.SourceUser,
		Actor: events.Actor{ID: actorID, Name: a.Name}, Message: message, Track: true,
		Origin: events.Origin{GatewayID: "api", ConnID: "rest"}, // no gateway delivered it locally
	})
	if err != nil {
		return CommandAccepted{}, err
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return CommandAccepted{}, err
	}
	id, err := uuid.Parse(ev.ID)
	if err != nil {
		return CommandAccepted{}, err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.InsertCommand(ctx, tx, store.Command{ID: id, ElementID: elementID, UserID: a.UserID, Actor: a.Name, Message: message}); err != nil {
			return err
		}
		if err := store.EnqueueOutbox(ctx, tx, events.TopicElementEvents, ev.PartitionKey, payload); err != nil {
			return err
		}
		return record(ctx, tx, a, "command", "element", elementID.String(), map[string]any{"command_id": id, "message": message})
	})
	if err != nil {
		return CommandAccepted{}, err
	}
	return CommandAccepted{CommandID: id, ElementID: elementID, Status: "accepted", Time: ev.Time.Time}, nil
}
