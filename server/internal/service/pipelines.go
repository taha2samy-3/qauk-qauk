package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// MaxPipelineBytes bounds one element's pipeline (its steps JSON). Every
// element of a device travels in one device-config.v1 record, so this keeps
// snapshots well under the bus record limit (see maxSnapshotBytes).
const MaxPipelineBytes = 64 << 10

type PipelineDetails struct {
	ElementName string                         `json:"element_name"`
	Current     *store.ElementPipeline         `json:"current"`
	Versions    []store.ElementPipelineVersion `json:"versions"`
}

func (s *Service) GetElementPipeline(ctx context.Context, elementID uuid.UUID) (PipelineDetails, error) {
	el, err := store.GetElement(ctx, s.Pool, elementID)
	if err != nil {
		return PipelineDetails{}, err
	}
	details := PipelineDetails{ElementName: el.Name}
	cur, err := store.GetElementPipeline(ctx, s.Pool, elementID)
	switch {
	case err == nil:
		details.Current = &cur
	case !errors.Is(err, store.ErrNotFound):
		return PipelineDetails{}, err
	}
	versions, err := store.ListElementPipelineVersions(ctx, s.Pool, elementID)
	if err != nil {
		return PipelineDetails{}, err
	}
	if versions == nil {
		versions = []store.ElementPipelineVersion{}
	}
	details.Versions = versions
	return details, nil
}

// ValidatePipeline checks steps the way a save would, without saving.
func ValidatePipeline(steps json.RawMessage) (*elementpipe.Pipeline, error) {
	if len(steps) > MaxPipelineBytes {
		return nil, invalid("steps", fmt.Sprintf("the pipeline is %d KiB; at most %d KiB", len(steps)>>10, MaxPipelineBytes>>10))
	}
	p, err := elementpipe.CompileJSON(1, steps)
	if err != nil {
		return nil, invalid("steps", err.Error())
	}
	return p, nil
}

func (s *Service) SaveElementPipeline(ctx context.Context, a Actor, elementID uuid.UUID, steps json.RawMessage) (store.ElementPipeline, error) {
	return s.savePipeline(ctx, a, elementID, steps, "update")
}

// RollbackElementPipeline saves an older version's steps as a new version.
func (s *Service) RollbackElementPipeline(ctx context.Context, a Actor, elementID uuid.UUID, targetVersion int) (store.ElementPipeline, error) {
	v, err := store.GetElementPipelineVersion(ctx, s.Pool, elementID, targetVersion)
	if err != nil {
		return store.ElementPipeline{}, err
	}
	return s.savePipeline(ctx, a, elementID, v.Steps, "rollback")
}

// DeleteElementPipeline clears the pipeline by saving an empty version, so
// the history (and rollback to it) stays.
func (s *Service) DeleteElementPipeline(ctx context.Context, a Actor, elementID uuid.UUID) error {
	_, err := s.savePipeline(ctx, a, elementID, json.RawMessage("[]"), "delete")
	return err
}

func (s *Service) savePipeline(ctx context.Context, a Actor, elementID uuid.UUID, steps json.RawMessage, action string) (store.ElementPipeline, error) {
	if len(steps) == 0 || string(steps) == "null" {
		steps = json.RawMessage("[]")
	}
	if _, err := ValidatePipeline(steps); err != nil {
		return store.ElementPipeline{}, err
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
		return record(ctx, tx, a, action, "element_pipeline", elementID.String(), p,
			events.ControlChanged{Kind: events.KindElement, Op: events.OpUpdate, ID: elementID.String(), ElementID: uid(elementID), DeviceID: uid(el.DeviceID)})
	})
	return p, err
}
