package validator

import (
	"context"
	"github.com/bloxapp/ssv-spec/qbft"
	"github.com/bloxapp/ssv-spec/ssv"
	"github.com/bloxapp/ssv-spec/types"
	"github.com/bloxapp/ssv/protocol/v2/ssv/msgqueue"
	"github.com/bloxapp/ssv/protocol/v2/ssv/runner"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// Validator represents an SSV ETH consensus validator Share assigned, coordinates duty execution and more.
// Every validator has a validatorID which is validator's public key.
// Each validator has multiple DutyRunners, for each duty type.
type Validator struct {
	ctx context.Context
	logger *zap.Logger
	DutyRunners runner.DutyRunners
	Network     Network
	Beacon      ssv.BeaconNode
	Storage     ssv.Storage
	Share       *types.Share
	Signer      types.KeyManager
	Q 			msgqueue.MsgQueue

	// TODO: move somewhere else
	Identifier []byte
}

func NewValidator(
	network ssv.Network,
	beacon ssv.BeaconNode,
	storage ssv.Storage,
	share *types.Share,
	signer types.KeyManager,
	runners runner.DutyRunners,
) *Validator {
	// makes sure that we have a sufficient interface, otherwise wrap it
	n, ok := network.(Network)
	if !ok {
		n = newNilNetwork(network)
	}
	l := zap.L() // TODO: real logger
	// TODO: handle error
	q, _ := msgqueue.New(l)
	return &Validator{
		ctx: context.Background(), // TODO: real context
		logger: l,
		DutyRunners: runners,
		Network:     n,
		Beacon:      beacon,
		Storage:     storage,
		Share:       share,
		Signer:      signer,
		Q: 			 q,
	}
}

func (v *Validator) Start() error {
	// TODO
	return nil
}

// StartDuty starts a duty for the validator
func (v *Validator) StartDuty(duty *types.Duty) error {
	dutyRunner := v.DutyRunners[duty.Type]
	if dutyRunner == nil {
		return errors.Errorf("duty type %s not supported", duty.Type.String())
	}
	return dutyRunner.StartNewDuty(duty)
}

// ProcessMessage processes Network Message of all types
func (v *Validator) ProcessMessage(msg *types.SSVMessage) error {
	dutyRunner := v.DutyRunners.DutyRunnerForMsgID(msg.GetID())
	if dutyRunner == nil {
		return errors.Errorf("could not get duty runner for msg ID")
	}

	if err := v.validateMessage(dutyRunner, msg); err != nil {
		return errors.Wrap(err, "Message invalid")
	}

	switch msg.GetType() {
	case types.SSVConsensusMsgType:
		signedMsg := &qbft.SignedMessage{}
		if err := signedMsg.Decode(msg.GetData()); err != nil {
			return errors.Wrap(err, "could not get consensus Message from network Message")
		}
		return dutyRunner.ProcessConsensus(signedMsg)
	case types.SSVPartialSignatureMsgType:
		signedMsg := &ssv.SignedPartialSignatureMessage{}
		if err := signedMsg.Decode(msg.GetData()); err != nil {
			return errors.Wrap(err, "could not get post consensus Message from network Message")
		}

		if signedMsg.Message.Type == ssv.PostConsensusPartialSig {
			return dutyRunner.ProcessPostConsensus(signedMsg)
		}
		return dutyRunner.ProcessPreConsensus(signedMsg)
	default:
		return errors.New("unknown msg")
	}
}

func (v *Validator) validateMessage(runner runner.Runner, msg *types.SSVMessage) error {
	if !v.Share.ValidatorPubKey.MessageIDBelongs(msg.GetID()) {
		return errors.New("msg ID doesn't match validator ID")
	}

	if len(msg.GetData()) == 0 {
		return errors.New("msg data is invalid")
	}

	return nil
}
