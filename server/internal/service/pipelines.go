package service

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type PipelineDetails struct {
	Current  *store.ElementPipeline         `json:"current"`
	Versions []store.ElementPipelineVersion `json:"versions"`
}

func (s *Service) GetElementPipeline(ctx context.Context, elementID uuid.UUID) (PipelineDetails, error) {
	if _, err := store.GetElement(ctx, s.Pool, elementID); err != nil {
		return PipelineDetails{}, err
	}
	var details PipelineDetails
	cur, err := store.GetElementPipeline(ctx, s.Pool, elementID)
	if err == nil {
		details.Current = &cur
	}
	versions, err := store.ListElementPipelineVersions(ctx, s.Pool, elementID)
	if err == nil {
		details.Versions = versions
	} else {
		details.Versions = []store.ElementPipelineVersion{}
	}
	return details, nil
}

func (s *Service) SaveElementPipeline(ctx context.Context, a Actor, elementID uuid.UUID, steps json.RawMessage) (store.ElementPipeline, error) {
	// Validate and compile the steps
	if len(steps) > 0 && string(steps) != "null" && string(steps) != "[]" {
		if _, err := elementpipe.CompileJSON(1, steps); err != nil {
			return store.ElementPipeline{}, invalid("steps", err.Error())
		}
	} else {
		steps = json.RawMessage("[]")
	}

	var p store.ElementPipeline
	err := s.tx(ctx, func(tx pgx.Tx) error {
		el, err := lockElementDevice(ctx, tx, elementID)
		if err != nil {
			return err
		}
		p, err = store.SaveElementPipeline(ctx, tx, elementID, steps, a.Name)
		if err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, el.DeviceID); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "element_pipeline", elementID.String(), p,
			events.ControlChanged{Kind: events.KindElement, Op: events.OpUpdate, ID: elementID.String(), ElementID: uid(elementID), DeviceID: uid(el.DeviceID)})
	})
	return p, err
}

func (s *Service) RollbackElementPipeline(ctx context.Context, a Actor, elementID uuid.UUID, targetVersion int) (store.ElementPipeline, error) {
	v, err := store.GetElementPipelineVersion(ctx, s.Pool, elementID, targetVersion)
	if err != nil {
		return store.ElementPipeline{}, err
	}
	return s.SaveElementPipeline(ctx, a, elementID, v.Steps)
}

func (s *Service) DeleteElementPipeline(ctx context.Context, a Actor, elementID uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		el, err := lockElementDevice(ctx, tx, elementID)
		if err != nil {
			return err
		}
		if err := store.DeleteElementPipeline(ctx, tx, elementID); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, el.DeviceID); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "element_pipeline", elementID.String(), nil,
			events.ControlChanged{Kind: events.KindElement, Op: events.OpUpdate, ID: elementID.String(), ElementID: uid(elementID), DeviceID: uid(el.DeviceID)})
	})
}
