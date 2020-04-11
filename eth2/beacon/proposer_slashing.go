package beacon

import (
	"errors"

	"github.com/protolambda/zrnt/eth2/util/bls"
	"github.com/protolambda/zssz"
	. "github.com/protolambda/ztyp/view"
)

var ProposerSlashingSSZ = zssz.GetSSZ((*ProposerSlashing)(nil))

type ProposerSlashing struct {
	ProposerIndex       ValidatorIndex
	SignedHeader1       SignedBeaconBlockHeader
	SignedHeader2       SignedBeaconBlockHeader
}

// Beacon operations
var ProposerSlashingType =  ContainerType("ProposerSlashing", []FieldDef{
	{"proposer_index", ValidatorIndexType},
	{"header_1", SignedBeaconBlockHeaderType},
	{"header_2", SignedBeaconBlockHeaderType},
})

var ProposerSlashingsType = ListType(ProposerSlashingType, MAX_PROPOSER_SLASHINGS)

type ProposerSlashings []ProposerSlashing

func (*ProposerSlashings) Limit() uint64 {
	return MAX_PROPOSER_SLASHINGS
}

func (state *BeaconStateView) ProcessProposerSlashings(epc *EpochsContext, ops []ProposerSlashing) error {
	for i := range ops {
		if err := state.ProcessProposerSlashing(epc, &ops[i]); err != nil {
			return err
		}
	}
	return nil
}

func (state *BeaconStateView) ProcessProposerSlashing(epc *EpochsContext, ps *ProposerSlashing) error {
	// Verify header slots match
	if ps.SignedHeader1.Message.Slot != ps.SignedHeader2.Message.Slot {
		return errors.New("proposer slashing requires slashing headers to have the same slot")
	}
	// Verify the headers are different
	if ps.SignedHeader1.Message == ps.SignedHeader2.Message {
		return errors.New("proposer slashing requires two different headers")
	}
	currentEpoch := epc.CurrentEpoch.Epoch
	// Verify the proposer is slashable
	validators, err := state.Validators()
	if err != nil {
		return err
	}
	validator, err := validators.Validator(ps.ProposerIndex)
	if err != nil {
		return err
	}
	if slashable, err := validator.IsSlashable(currentEpoch); err != nil {
		return err
	} else if !slashable {
		return errors.New("proposer slashing requires proposer to be slashable")
	}
	domain, err := state.GetDomain(DOMAIN_BEACON_PROPOSER, ps.SignedHeader1.Message.Slot.ToEpoch())
	if err != nil {
		return err
	}
	pubkey, ok := epc.Pubkey(ps.ProposerIndex)
	if !ok {
		return errors.New("could not find pubkey of proposer")
	}
	// Verify signatures
	if !bls.Verify(
		pubkey,
		ComputeSigningRoot(ps.SignedHeader1.Message.HashTreeRoot(),	domain),
		ps.SignedHeader1.Signature) {
		return errors.New("proposer slashing header 1 has invalid BLS signature")
	}
	if !bls.Verify(
		pubkey,
		ComputeSigningRoot(ps.SignedHeader2.Message.HashTreeRoot(),	domain),
		ps.SignedHeader2.Signature) {
		return errors.New("proposer slashing header 2 has invalid BLS signature")
	}
	return state.SlashValidator(epc, ps.ProposerIndex, nil)
}