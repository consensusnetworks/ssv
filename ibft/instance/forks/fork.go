package forks

import (
	"github.com/bloxapp/ssv/ibft/pipeline"
	"github.com/bloxapp/ssv/protocol/v1/qbft/instance"
)

// Fork will apply fork modifications on an ibft instance
type Fork interface {
	pipeline.Pipelines
	Apply(instance instance.Instance)
}
