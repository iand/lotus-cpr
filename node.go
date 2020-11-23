package main

import (
	"context"
	"errors"

	"github.com/filecoin-project/lotus/api"
	"github.com/go-logr/logr"
	"github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipfs-blockstore"
)

var _ (BlockCache) = (*NodeBlockCache)(nil)

type NodeBlockCache struct {
	node    api.FullNode
	tlogger logr.Logger // request tracing
	stats   CacheStats
}

func NewNodeBlockCache(node api.FullNode, logger logr.Logger) *NodeBlockCache {
	if logger == nil {
		logger = logr.Discard()
	}
	return &NodeBlockCache{
		node:    node,
		tlogger: logger.V(LogLevelTrace),
	}
}

func (n *NodeBlockCache) Has(ctx context.Context, c cid.Cid) (bool, error) {
	if n.tlogger.Enabled() {
		n.tlogger.Info("Has", "block", c)
	}
	has, err := n.node.ChainHasObj(ctx, c)
	if err != nil {
		if errors.Is(err, blockstore.ErrNotFound) {
			n.stats.Miss()
			return false, err
		}
		n.stats.Error()
		if n.tlogger.Enabled() {
			n.tlogger.Error(err, "Has failed", "block", c)
		}
		return false, err
	}

	if has {
		n.stats.Hit()
	} else {
		n.stats.Miss()
	}

	return has, nil
}

func (n *NodeBlockCache) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	if n.tlogger.Enabled() {
		n.tlogger.Info("Get", "block", c)
	}
	data, err := n.node.ChainReadObj(ctx, c)
	if err != nil {
		if errors.Is(err, blockstore.ErrNotFound) {
			n.stats.Miss()
			return nil, err
		}
		n.stats.Error()
		if n.tlogger.Enabled() {
			n.tlogger.Error(err, "Get failed", "block", c)
		}
		return nil, err
	}

	n.stats.Hit()
	return blocks.NewBlockWithCid(data, c)
}

func (n *NodeBlockCache) SetUpstream(u BlockCache) {
	panic("Not supported")
}

func (n *NodeBlockCache) LogStats(dlogger logr.Logger) {
	n.stats.Log("node", dlogger)
}
