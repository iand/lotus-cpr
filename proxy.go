package main

import (
	"context"
	// "fmt"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-jsonrpc/auth"
	blocks "github.com/ipfs/go-block-format"
	// "github.com/filecoin-project/go-bitfield"
	"github.com/filecoin-project/go-bitfield"
	"github.com/filecoin-project/go-state-types/abi"
	// "github.com/filecoin-project/go-state-types/dline"
	// "github.com/filecoin-project/go-state-types/network"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/actors/builtin/miner"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/go-logr/logr"
	"github.com/ipfs/go-cid"
)

type BlockCache interface {
	Has(context.Context, cid.Cid) (bool, error)
	Get(context.Context, cid.Cid) (blocks.Block, error)
	SetUpstream(BlockCache)
	LogStats(logr.Logger)
}

type Proxy struct {
	node    api.FullNode
	cache   BlockCache
	tlogger logr.Logger // request tracing
}

func NewAPIProxy(node api.FullNode, cache BlockCache, logger logr.Logger) *Proxy {
	if logger == nil {
		logger = logr.Discard()
	}
	return &Proxy{
		node:    node,
		cache:   cache,
		tlogger: logger.WithName("proxy").V(LogLevelTrace),
	}
}

// Common subset

func (p *Proxy) AuthVerify(ctx context.Context, token string) ([]auth.Permission, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("AuthVerify")
	}
	return p.node.AuthVerify(ctx, token)
}

func (p *Proxy) AuthNew(ctx context.Context, perms []auth.Permission) ([]byte, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("AuthNew")
	}
	return p.node.AuthNew(ctx, perms)
}

func (p *Proxy) Version(ctx context.Context) (api.Version, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("Version")
	}
	return p.node.Version(ctx)
}

// Chain subset

func (p *Proxy) ChainNotify(ctx context.Context) (<-chan []*api.HeadChange, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainNotify")
	}
	return p.node.ChainNotify(ctx)
}

func (p *Proxy) ChainHead(ctx context.Context) (*types.TipSet, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainHead")
	}
	return p.node.ChainHead(ctx)
}

func (p *Proxy) ChainGetBlock(ctx context.Context, obj cid.Cid) (*types.BlockHeader, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetBlock", "block", obj)
	}
	sb, err := p.cache.Get(ctx, obj)
	if err != nil {
		if p.tlogger.Enabled() {
			p.tlogger.Error(err, "Failed to get block from cache", "obj", obj)
		}
		return nil, err
	}

	bh, err := types.DecodeBlock(sb.RawData())
	if err != nil {
		if p.tlogger.Enabled() {
			p.tlogger.Error(err, "decode block", "obj", obj, "data", string(sb.RawData()))
		}
	}
	return bh, err
}

func (p *Proxy) ChainGetTipSet(ctx context.Context, tsk types.TipSetKey) (*types.TipSet, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetTipSet", "tsk", tsk)
	}
	cids := tsk.Cids()
	blks := make([]*types.BlockHeader, len(cids))
	for i, c := range cids {
		b, err := p.ChainGetBlock(ctx, c)
		if err != nil {
			return nil, err
		}
		blks[i] = b
	}

	ts, err := types.NewTipSet(blks)
	if err != nil {
		return nil, err
	}

	return ts, nil
}

func (p *Proxy) ChainGetBlockMessages(ctx context.Context, blockCid cid.Cid) (*api.BlockMessages, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetBlockMessages", "block", blockCid)
	}
	return p.node.ChainGetBlockMessages(ctx, blockCid)
}

func (p *Proxy) ChainGetParentReceipts(ctx context.Context, blockCid cid.Cid) ([]*types.MessageReceipt, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetParentReceipts", "block", blockCid)
	}
	return p.node.ChainGetParentReceipts(ctx, blockCid)
}

func (p *Proxy) ChainGetParentMessages(ctx context.Context, blockCid cid.Cid) ([]api.Message, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetParentMessages", "block", blockCid)
	}
	return p.node.ChainGetParentMessages(ctx, blockCid)
}

func (p *Proxy) ChainGetTipSetByHeight(ctx context.Context, h abi.ChainEpoch, tsk types.TipSetKey) (*types.TipSet, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetTipSetByHeight", "height", h, "tsk", tsk)
	}
	return p.node.ChainGetTipSetByHeight(ctx, h, tsk)
}

func (p *Proxy) ChainReadObj(ctx context.Context, obj cid.Cid) ([]byte, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainReadObj", "obj", obj)
	}
	blk, err := p.cache.Get(ctx, obj)
	if err != nil {
		logger.Error(err, "cache get", "cid", obj)
		return p.node.ChainReadObj(ctx, obj)
	}

	return blk.RawData(), nil
}

func (p *Proxy) ChainHasObj(ctx context.Context, obj cid.Cid) (bool, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainHasObj", "obj", obj)
	}
	has, err := p.cache.Has(ctx, obj)
	if err != nil {
		logger.Error(err, "cache has", "cid", obj)
		return p.node.ChainHasObj(ctx, obj)
	}
	return has, nil
}

func (p *Proxy) ChainStatObj(ctx context.Context, obj cid.Cid, base cid.Cid) (api.ObjStat, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainStatObj", "obj", obj, "base", base)
	}
	return p.node.ChainStatObj(ctx, obj, base)
}

func (p *Proxy) ChainGetGenesis(ctx context.Context) (*types.TipSet, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetGenesis")
	}
	return p.node.ChainGetGenesis(ctx)
}

func (p *Proxy) ChainTipSetWeight(ctx context.Context, tsk types.TipSetKey) (types.BigInt, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainTipSetWeight", "tsk", tsk)
	}
	return p.node.ChainTipSetWeight(ctx, tsk)
}

func (p *Proxy) ChainGetNode(ctx context.Context, path string) (*api.IpldObject, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetNode", "path", path)
	}
	return p.node.ChainGetNode(ctx, path)
}

func (p *Proxy) ChainGetMessage(ctx context.Context, mc cid.Cid) (*types.Message, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetMessage", "msg", mc)
	}
	return p.node.ChainGetMessage(ctx, mc)
}

func (p *Proxy) ChainGetPath(ctx context.Context, from types.TipSetKey, to types.TipSetKey) ([]*api.HeadChange, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("ChainGetPath", "from", from, "to", to)
	}
	return p.node.ChainGetPath(ctx, from, to)
}

// State subset

func (p *Proxy) StateChangedActors(ctx context.Context, old cid.Cid, new cid.Cid) (map[string]types.Actor, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateChangedActors", "old", old, "new", new)
	}
	return p.node.StateChangedActors(ctx, old, new)
}

func (p *Proxy) StateGetReceipt(ctx context.Context, msg cid.Cid, tsk types.TipSetKey) (*types.MessageReceipt, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateGetReceipt", "msg", msg, "tsk", tsk)
	}
	return p.node.StateGetReceipt(ctx, msg, tsk)
}

func (p *Proxy) StateListMiners(ctx context.Context, tsk types.TipSetKey) ([]address.Address, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateListMiners", "tsk", tsk)
	}
	return p.node.StateListMiners(ctx, tsk)
}

func (p *Proxy) StateListActors(ctx context.Context, tsk types.TipSetKey) ([]address.Address, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateListActors", "tsk", tsk)
	}
	return p.node.StateListActors(ctx, tsk)
}

func (p *Proxy) StateGetActor(ctx context.Context, actor address.Address, tsk types.TipSetKey) (*types.Actor, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateGetActor", "actor", actor, "tsk", tsk)
	}
	return p.node.StateGetActor(ctx, actor, tsk)
}

func (p *Proxy) StateReadState(ctx context.Context, actor address.Address, tsk types.TipSetKey) (*api.ActorState, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateReadState", "actor", actor, "tsk", tsk)
	}
	return p.node.StateReadState(ctx, actor, tsk)
}

func (p *Proxy) StateMinerSectors(ctx context.Context, addr address.Address, sectorNos *bitfield.BitField, tsk types.TipSetKey) ([]*miner.SectorOnChainInfo, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateMinerSectors", "addr", addr, "tsk", tsk)
	}
	return p.node.StateMinerSectors(ctx, addr, sectorNos, tsk)
}

func (p *Proxy) StateMinerPower(ctx context.Context, addr address.Address, tsk types.TipSetKey) (*api.MinerPower, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateMinerPower", "addr", addr, "tsk", tsk)
	}
	return p.node.StateMinerPower(ctx, addr, tsk)
}

func (p *Proxy) StateVMCirculatingSupplyInternal(ctx context.Context, tsk types.TipSetKey) (api.CirculatingSupply, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("StateVMCirculatingSupplyInternal", "tsk", tsk)
	}
	return p.node.StateVMCirculatingSupplyInternal(ctx, tsk)
}

func (p *Proxy) GetTipSetFromKey(ctx context.Context, tsk types.TipSetKey) (*types.TipSet, error) {
	if p.tlogger.Enabled() {
		p.tlogger.Info("GetTipSetFromKey", "tsk", tsk)
	}
	if tsk.IsEmpty() {
		return p.node.ChainHead(ctx) // equivalent to Chain.GetHeaviestTipSet
	}
	return p.ChainGetTipSet(ctx, tsk)
}
