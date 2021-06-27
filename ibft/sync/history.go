package sync

import (
	"encoding/hex"
	"fmt"
	"github.com/bloxapp/ssv/ibft/proto"
	"github.com/bloxapp/ssv/network"
	"github.com/bloxapp/ssv/storage/collections"
	"github.com/bloxapp/ssv/storage/kv"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"sync"
)

// HistorySync is responsible for syncing and iBFT instance when needed by
// fetching decided messages from the network
type HistorySync struct {
	logger              *zap.Logger
	network             network.Network
	ibftStorage         collections.Iibft
	validateDecidedMsgF func(msg *proto.SignedMessage) error
	identifier          []byte
}

// NewHistorySync returns a new instance of HistorySync
func NewHistorySync(logger *zap.Logger, identifier []byte, network network.Network, ibftStorage collections.Iibft, validateDecidedMsgF func(msg *proto.SignedMessage) error, ) *HistorySync {
	return &HistorySync{
		logger:              logger,
		identifier:          identifier,
		network:             network,
		validateDecidedMsgF: validateDecidedMsgF,
		ibftStorage:         ibftStorage,
	}
}

// Start the sync
func (s *HistorySync) Start() error {
	// fetch remote highest
	remoteHighest, fromPeer, err := s.findHighestInstance()
	if err != nil {
		return errors.Wrap(err, "could not fetch highest instance during sync")
	}

	// fetch local highest
	localHighest, err := s.ibftStorage.GetHighestDecidedInstance(s.identifier)
	if err != nil && err.Error() != kv.EntryNotFoundError { // if not found, don't continue with sync
		return errors.Wrap(err, "could not fetch local highest instance during sync")
	}

	syncStartSeqNumber := uint64(0)
	if localHighest != nil {
		syncStartSeqNumber = localHighest.Message.SeqNumber + 1
	}

	// check we are behind and need to sync
	if syncStartSeqNumber >= remoteHighest.Message.SeqNumber {
		s.logger.Info("node is synced", zap.Uint64("highest seq", syncStartSeqNumber))
		return nil
	}

	// fetch, validate and save missing data
	highestSaved, err := s.fetchValidateAndSaveInstances(fromPeer, syncStartSeqNumber, remoteHighest.Message.SeqNumber)
	if err != nil {
		return errors.Wrap(err, "could not fetch decided by range during sync")
	}

	// save highest
	if highestSaved != nil {
		if err := s.ibftStorage.SaveHighestDecidedInstance(highestSaved); err != nil {
			return errors.Wrap(err, "could not save highest decided msg during sync")
		}
	}

	s.logger.Info("node is synced", zap.Uint64("highest seq", highestSaved.Message.SeqNumber))

	return nil
}

// findHighestInstance returns the highest found decided signed message and the peer it was received from
func (s *HistorySync) findHighestInstance() (*proto.SignedMessage, string, error) {
	// pick up to 4 peers
	// TODO - why 4? should be set as param?
	// TODO select peers by quality/ score?
	// TODO - should be changed to support multi duty
	usedPeers, err := s.network.AllPeers(s.identifier)
	if err != nil {
		return nil, "", err
	}
	if len(usedPeers) > 4 {
		usedPeers = usedPeers[:4]
	}

	// fetch response
	wg := &sync.WaitGroup{}
	results := make([]*network.SyncMessage, 4)
	for i, p := range usedPeers {
		wg.Add(1)
		go func(index int, peer string, wg *sync.WaitGroup) {
			res, err := s.network.GetHighestDecidedInstance(peer, &network.SyncMessage{
				Type:   network.Sync_GetHighestType,
				Lambda: s.identifier,
			})
			if err != nil {
				s.logger.Error("received error when fetching highest decided", zap.Error(err),
					zap.String("identifier", hex.EncodeToString(s.identifier)))
			} else {
				results[index] = res
			}
			wg.Done()
		}(i, p, wg)
	}

	wg.Wait()

	// validate response and find highest decided
	var ret *proto.SignedMessage
	var fromPeer string
	for _, res := range results {
		if res == nil {
			continue
		}

		if len(res.SignedMessages) != 1 || res.SignedMessages[0] == nil {
			s.logger.Debug("received invalid highest decided", zap.Error(err),
				zap.String("identifier", hex.EncodeToString(s.identifier)))
			continue
		}

		signedMsg := res.SignedMessages[0]

		// validate
		if err := s.validateDecidedMsgF(signedMsg); err != nil {
			s.logger.Debug("received invalid highest decided", zap.Error(err),
				zap.String("identifier", hex.EncodeToString(s.identifier)))
			continue
		}

		if ret == nil {
			ret = signedMsg
			fromPeer = res.FromPeerID
		}
		if ret.Message.SeqNumber < signedMsg.Message.SeqNumber {
			ret = signedMsg
			fromPeer = res.FromPeerID
		}
	}

	if ret == nil {
		s.logger.Debug("could not fetch highest decided from peers",
			zap.String("identifier", hex.EncodeToString(s.identifier)))
		return nil, "", errors.New("could not fetch highest decided from peers")
	}

	return ret, fromPeer, nil
}

// FetchValidateAndSaveInstances fetches, validates and saves decided messages from the P2P network.
// Range is start to end seq including
func (s *HistorySync) fetchValidateAndSaveInstances(fromPeer string, startSeq uint64, endSeq uint64) (highestSaved *proto.SignedMessage, err error) {
	failCount := 0
	start := startSeq
	done := false
	var latestError error
	for {
		if failCount == 5 {
			return highestSaved, latestError
		}
		if done {
			return highestSaved, nil
		}

		res, err := s.network.GetDecidedByRange(fromPeer, &network.SyncMessage{
			Lambda: s.identifier,
			Params: []uint64{start, endSeq},
			Type:   network.Sync_GetInstanceRange,
		})
		if err != nil {
			failCount++
			latestError = err
			continue
		}

		// validate and save
		for _, msg := range res.SignedMessages {
			// if msg is invalid, break and try again with an updated start seq
			if s.validateDecidedMsgF(msg) != nil {
				start = msg.Message.SeqNumber
				continue
			}

			// save
			if err := s.ibftStorage.SaveDecided(msg); err != nil {
				return highestSaved, err
			}

			// set highest
			if highestSaved == nil {
				highestSaved = msg
			}
			if highestSaved.Message.SeqNumber < msg.Message.SeqNumber {
				highestSaved = msg
			}

			start = msg.Message.SeqNumber + 1

			if msg.Message.SeqNumber == endSeq {
				done = true
			}
		}
		s.logger.Info(fmt.Sprintf("fetched and saved instances up to sequence number %d", endSeq))
	}
}
