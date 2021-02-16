package chain

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/protolambda/zrnt/eth2/beacon"
	"github.com/protolambda/zrnt/eth2/db/states"
)

type Root = beacon.Root
type Epoch = beacon.Epoch
type Slot = beacon.Slot
type ValidatorIndex = beacon.ValidatorIndex
type Gwei = beacon.Gwei
type Checkpoint = beacon.Checkpoint

type ChainEntry interface {
	// Slot of this entry
	Slot() Slot
	// BlockRoot returns the last block root, replicating the previous block root if the current slot has none.
	// There is only 1 block root, double block proposals by the same validator are accepted,
	// only the first is incorporated into the chain.
	BlockRoot() (root Root)
	// The parent block root. If this is an empty slot, it will just be previous block root. Can also be zeroed if unknown.
	ParentRoot() (root Root)
	// If this is an empty slot entry, i.e. no block
	IsEmpty() bool
	// State root of the post-state of this entry, with or without block, depending on IsEmpty.
	// Should match state-root in the block at the same slot (if any)
	StateRoot() Root
	// The context of this chain entry (shuffling, proposers, etc.)
	EpochsContext(ctx context.Context) (*beacon.EpochsContext, error)
	// StateExclBlock retrieves the state of this slot.
	// - If IsEmpty: it is the state after processing slots to Slot() (incl.),
	//   with ProcessSlots(slot), but without any block processing.
	// - if not IsEmpty: post-block processing (if any block), excl. latest-header update of next slot.
	State(ctx context.Context) (*beacon.BeaconStateView, error)
}

type Chain interface {
	// Get the chain entry for the given state root (post slot processing or post block processing)
	ByStateRoot(root Root) (entry ChainEntry, ok bool)
	// Get the chain entry for the given block root and slot, may be an empty slot,
	// or may be in-between slot processing and block processing if the parent block root is requested for the slot.
	ByBlockSlot(root Root, slot Slot) (entry ChainEntry, ok bool)
	// Find closest ref in subtree, up to given slot (may return entry of fromBlockRoot itself),
	// without any blocks after fromBlockRoot.
	// Err if no entry, even not fromBlockRoot, could be found.
	Closest(fromBlockRoot Root, toSlot Slot) (ChainEntry, error)
	// First gets the closets ref from the given block root to the requested slot,
	// then transitions empty slots to get up to the requested slot.
	// A strict context should be provided to avoid costly long transitions.
	// An error is also returned if the fromBlockRoot is past the requested toSlot.
	Towards(ctx context.Context, fromBlockRoot Root, toSlot Slot) (ChainEntry, error)
	// Returns true if the given root is something that builds (maybe indirectly)
	// on the ofRoot on the same chain.
	// If root == ofRoot, then it is NOT considered an ancestor here.
	IsAncestor(root Root, ofRoot Root) (unknown bool, isAncestor bool)
	BySlot(slot Slot) (ChainEntry, error)
	Iter() (ChainIter, error)
}

type ChainIter interface {
	// Start is the minimum slot to reach to, inclusive.
	Start() Slot
	// End is the maximum slot to reach to, exclusive.
	End() Slot
	// Entry fetches the chain entry at the given slot.
	// If it does not exist, entry=nil, err=nil.
	// If the request is out of bounds or fails, an error may be returned.
	Entry(slot Slot) (entry ChainEntry, err error)
}

type BlockSlotKey struct {
	Slot Slot
	Root Root
}

func (key BlockSlotKey) Bytes() (out [40]byte) {
	copy(out[0:32], key.Root[:])
	binary.LittleEndian.PutUint64(out[32:40], uint64(key.Slot))
	return
}

type FullChain interface {
	Chain
	HotChain
	ColdChain
}

type HotColdChain struct {
	HotChain
	ColdChain
	Spec *beacon.Spec
}

func NewHotColdChain(anchor *HotEntry, spec *beacon.Spec, stateDB states.DB) (*HotColdChain, error) {
	coldCh := NewFinalizedChain(anchor.slot, spec, stateDB)
	hotCh, err := NewUnfinalizedChain(anchor,
		BlockSinkFn(func(entry *HotEntry, canonical bool) error {
			if canonical {
				return coldCh.OnFinalizedEntry(entry)
			}
			return nil
			// TODO keep track of pruned non-finalized blocks?
		}), spec)
	if err != nil {
		return nil, err
	}

	c := &HotColdChain{
		HotChain:  hotCh,
		ColdChain: coldCh,
		Spec:      spec,
	}
	return c, nil
}

func (hc *HotColdChain) ByStateRoot(root Root) (ChainEntry, error) {
	hotEntry, hotErr := hc.HotChain.ByStateRoot(root)
	if hotErr != nil {
		coldEntry, coldErr := hc.ColdChain.ByStateRoot(root)
		if coldErr != nil {
			return nil, fmt.Errorf("could not find chain entry in hot or cold chain. "+
				"Hot: %v, Cold: %v", hotErr, coldErr)
		}
		return coldEntry, nil
	}
	return hotEntry, nil
}

func (hc *HotColdChain) ByBlockRoot(root Root) (ChainEntry, error) {
	hotEntry, hotErr := hc.HotChain.ByBlockRoot(root)
	if hotErr != nil {
		coldEntry, coldErr := hc.ColdChain.ByBlockRoot(root)
		if coldErr != nil {
			return nil, fmt.Errorf("could not find chain entry in hot or cold chain. "+
				"Hot: %v, Cold: %v", hotErr, coldErr)
		}
		return coldEntry, nil
	}
	return hotEntry, nil
}

func (hc *HotColdChain) Closest(fromBlockRoot Root, toSlot Slot) (ChainEntry, error) {
	hotEntry, hotErr := hc.HotChain.Closest(fromBlockRoot, toSlot)
	if hotErr != nil {
		coldEntry, coldErr := hc.ColdChain.Closest(fromBlockRoot, toSlot)
		if coldErr != nil {
			return nil, fmt.Errorf("could not find chain entry in hot or cold chain. "+
				"Hot: %v, Cold: %v", hotErr, coldErr)
		}
		return coldEntry, nil
	}
	return hotEntry, nil
}

func (hc *HotColdChain) Towards(ctx context.Context, fromBlockRoot Root, toSlot Slot) (ChainEntry, error) {

}

func (hc *HotColdChain) IsAncestor(root Root, ofRoot Root) (unknown bool, isAncestor bool) {
	// can't be ancestors if they are equal.
	if root == ofRoot {
		return false, false
	}
	// if the last two of the roots is known in the cold chain, just have the cold chain deal with it.
	_, err := hc.ColdChain.ByBlockRoot(root)
	if err == nil {
		return hc.ColdChain.IsAncestor(root, ofRoot)
	}
	// if the first of the roots is known in the cold chain, while the later one is not,
	//  then it's an ancestor if the later one builds on the dividing node between the two chains
	_, err = hc.ColdChain.ByBlockRoot(ofRoot)
	if err == nil {
		fin := hc.HotChain.Finalized()
		return hc.HotChain.IsAncestor(root, fin.Root)
	}
	// Both are not in the cold chain, have the hot chain deal with it.
	return hc.HotChain.IsAncestor(root, ofRoot)
}

func (hc *HotColdChain) BySlot(slot Slot) (ChainEntry, error) {
	hotEntry, hotErr := hc.HotChain.BySlot(slot)
	if hotErr != nil {
		coldEntry, coldErr := hc.ColdChain.BySlot(slot)
		if coldErr != nil {
			return nil, fmt.Errorf("could not find chain entry in hot or cold chain. "+
				"Hot: %v, Cold: %v", hotErr, coldErr)
		}
		return coldEntry, nil
	}
	return hotEntry, nil
}

func (hc *HotColdChain) Iter() (ChainIter, error) {
	hotIt, err := hc.HotChain.Iter()
	if err != nil {
		return nil, fmt.Errorf("cannot iter hot part: %v", err)
	}
	coldIt, err := hc.ColdChain.Iter()
	if err != nil {
		return nil, fmt.Errorf("cannot iter cold part: %v", err)
	}
	return &FullChainIter{
		HotIter:  hotIt,
		ColdIter: coldIt,
	}, nil
}

type FullChainIter struct {
	HotIter  ChainIter
	ColdIter ChainIter
}

func (fi *FullChainIter) Start() Slot {
	return fi.ColdIter.Start()
}

func (fi *FullChainIter) End() Slot {
	return fi.HotIter.End()
}

func (fi *FullChainIter) Entry(slot Slot) (entry ChainEntry, err error) {
	if slot < fi.ColdIter.End() {
		return fi.ColdIter.Entry(slot)
	} else {
		return fi.HotIter.Entry(slot)
	}
}

// TODO: chain copy