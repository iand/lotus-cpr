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
	ctx = cacheContext(ctx, "node")
	has, err := n.node.ChainHasObj(ctx, c)
	if err != nil {
		if errors.Is(err, blockstore.ErrNotFound) {
			return false, err
		}
		if n.tlogger.Enabled() {
			n.tlogger.Error(err, "Has failed", "block", c)
		}
		return false, err
	}

	return has, nil
}

func (n *NodeBlockCache) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	ctx = cacheContext(ctx, "node")
	reportEvent(ctx, getRequest)
	stop := startTimer(ctx, getDuration)
	defer stop()

	data, err := n.node.ChainReadObj(ctx, c)
	if err != nil {
		if errors.Is(err, blockstore.ErrNotFound) {
			reportEvent(ctx, getMiss)
			return nil, err
		}
		reportEvent(ctx, getFailure)
		if n.tlogger.Enabled() {
			n.tlogger.Error(err, "Get failed", "block", c)
		}
		return nil, err
	}

	reportEvent(ctx, getHit)
	reportSize(ctx, getSize, len(data))
	return blocks.NewBlockWithCid(data, c)
}

func (n *NodeBlockCache) SetUpstream(u BlockCache) {
	panic("Not supported")
}
