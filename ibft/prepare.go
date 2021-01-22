package ibft

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/bloxapp/ssv/ibft/types"
)

func (i *iBFTInstance) validatePrepare(msg *types.Message) error {
	// Only 1 prepare per peer per round is valid
	msgs := i.prepareMessages.ReadOnlyMessagesByRound(msg.Round)
	if val, found := msgs[msg.IbftId]; found {
		if !val.Compare(*msg) {
			return errors.New(fmt.Sprintf("another (different) prepare message for peer %d was received", msg.IbftId))
		}
	}

	if err := i.implementation.ValidatePrepareMsg(i.state, msg); err != nil {
		return err
	}

	return nil
}

func (i *iBFTInstance) prepareQuorum(round uint64, inputValue []byte) (quorum bool, t uint64, n uint64) {
	cnt := uint64(0)
	msgs := i.prepareMessages.ReadOnlyMessagesByRound(round)
	for _, v := range msgs {
		if bytes.Compare(inputValue, v.InputValue) == 0 {
			cnt += 1
		}
	}

	quorum = cnt*3 >= i.params.IbftCommitteeSize*2
	return quorum, cnt, i.params.IbftCommitteeSize
}

/**
### Algorithm 2 IBFT pseudocode for process pi: normal case operation
upon receiving a quorum of valid ⟨PREPARE, λi, ri, value⟩ messages do:
	pri ← ri
	pvi ← value
	broadcast ⟨COMMIT, λi, ri, value⟩
*/
func (i *iBFTInstance) uponPrepareMessage(msg *types.Message) {
	if err := i.validatePrepare(msg); err != nil {
		i.log.WithError(err).Errorf("prepare message is invalid")
	}

	// validate round
	if msg.Round != i.state.Round {
		i.log.Errorf("prepare round %d, expected %d", msg.Round, i.state.Round)
	}

	// TODO - can we process a prepare msg which has different inputValue than the pre-prepare msg?

	// add to prepare messages
	i.prepareMessages.AddMessage(*msg)
	i.log.Infof("received valid prepare message for round %d", msg.Round)

	// check if quorum achieved, act upon it.
	if quorum, t, n := i.prepareQuorum(msg.Round, msg.InputValue); quorum {
		i.log.Infof("Prepared (%d out of %d)", t, n)
		// set prepared state
		i.state.PreparedRound = msg.Round
		i.state.PreparedValue = msg.InputValue
		i.state.Stage = types.RoundState_Prepare

		// send commit msg
		broadcastMsg := &types.Message{
			Type:       types.RoundState_Commit,
			Round:      i.state.Round,
			Lambda:     i.state.Lambda,
			InputValue: i.state.InputValue,
			IbftId:     i.state.IBFTId,
		}
		if err := i.network.Broadcast(broadcastMsg); err != nil {
			i.log.WithError(err).Errorf("could not broadcast commit message")
		}
	}
}
