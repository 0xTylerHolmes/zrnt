package phase0

import (
	. "github.com/protolambda/zrnt/eth2/core"
	"github.com/protolambda/zrnt/eth2/meta"
)

type SlotProcessFeature struct {
	Meta interface {
		StateRoot() Root
		meta.Versioning
		meta.LatestHeaderUpdate
		meta.LatestHeader
		meta.HistoryUpdate
	}
}

func (f *SlotProcessFeature) ProcessSlot() error {
	// Cache latest known state root (for previous slot)
	latestStateRoot := f.Meta.StateRoot()

	if err := f.Meta.UpdateLatestBlockStateRoot(latestStateRoot); err != nil {
		return err
	}

	previousBlockRoot, err := f.Meta.GetLatestBlockRoot()
	if err != nil {
		return err
	}

	currentSlot, err := f.Meta.CurrentSlot()
	if err != nil {
		return err
	}

	// Cache latest known block and state root
	if err := f.Meta.SetRecentRoots(currentSlot, previousBlockRoot, latestStateRoot); err != nil {
		return err
	}

	return nil
}
