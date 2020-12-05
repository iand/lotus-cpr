package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-bitfield"
	"github.com/filecoin-project/go-jsonrpc"
	"github.com/filecoin-project/go-jsonrpc/auth"
	"github.com/filecoin-project/go-state-types/abi"
	lotusapi "github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/api/client"
	"github.com/filecoin-project/lotus/chain/actors/builtin/miner"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/go-logr/logr"
	"github.com/iand/circuit"
	"github.com/ipfs/go-cid"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

func apiURI(addr string) string {
	return "ws://" + addr + "/rpc/v0"
}

func apiHeaders(token string) http.Header {
	headers := http.Header{}
	headers.Add("Authorization", "Bearer "+token)
	return headers
}

var (
	_ NodeBlockCacheAPI = (*apiClient)(nil)
	_ ProxyAPI          = (*apiClient)(nil)
)

type apiClient struct {
	maddr   string
	uri     string
	headers http.Header
	cb      *circuit.Breaker
	logger  logr.Logger

	mu     sync.Mutex // guards api and closer
	api    lotusapi.FullNode
	closer jsonrpc.ClientCloser
}

func newAPIClient(maddr string, token string, errorThreshold int, maxConcurrency int, resetTimeout time.Duration, logger logr.Logger) (*apiClient, error) {
	parsedAddr, err := ma.NewMultiaddr(maddr)
	if err != nil {
		return nil, fmt.Errorf("parse api multiaddress: %w", err)
	}

	_, addr, err := manet.DialArgs(parsedAddr)
	if err != nil {
		return nil, fmt.Errorf("convert api multiaddress: %w", err)
	}

	a := &apiClient{
		maddr:   maddr,
		uri:     apiURI(addr),
		headers: apiHeaders(token),
		cb: &circuit.Breaker{
			Threshold:    uint32(errorThreshold), // number of consecutive errors allowed before the circuit is opened
			Concurrency:  uint32(maxConcurrency), // number of concurrent requests allowed
			ResetTimeout: resetTimeout,           // time to wait once the circuit breaker trips open before allowing another attempt
		},
		logger: logger.V(LogLevelInfo),
	}
	a.cb.OnOpen = a.onCircuitOpen
	a.cb.OnReset = a.onCircuitReset
	a.cb.OnClose = a.onCircuitClose

	a.connect()

	return a, nil
}

func (a *apiClient) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Close the connection to the upstream api if it was open
	if a.closer != nil {
		a.closer()
		a.closer = nil
	}
	a.api = nil
}

func (a *apiClient) onCircuitOpen(r circuit.OpenReason) {
	a.logger.Info("Disconnecting from lotus", "maddr", a.maddr, "reason", reason(r))
	reportMeasurement(context.Background(), circuitStatus.M(1))

	a.mu.Lock()
	defer a.mu.Unlock()

	// Close the connection to the upstream api if it was open
	if a.closer != nil {
		a.closer()
		a.closer = nil
	}
	a.api = nil
}

func (a *apiClient) onCircuitReset() {
	a.connect()
}

func (a *apiClient) onCircuitClose() {
	reportMeasurement(context.Background(), circuitStatus.M(0))
}

func (a *apiClient) connect() {
	upstream, closer, err := client.NewFullNodeRPC(context.Background(), a.uri, a.headers)
	if err != nil {
		a.logger.Error(err, "Connecting to lotus", "maddr", a.maddr, "uri", a.uri)
		a.mu.Lock()
		a.api = nil
		a.closer = nil
		a.mu.Unlock()
		return
	}
	a.logger.Info("Connected to lotus", "maddr", a.maddr)

	a.mu.Lock()
	a.api = upstream
	a.closer = closer
	a.mu.Unlock()
}

func (a *apiClient) withApi(ctx context.Context, fn func(api lotusapi.FullNode) error) error {
	a.mu.Lock()
	api := a.api
	a.mu.Unlock()
	if api == nil {
		return ErrLotusUnavailable
	}
	// pass the function through the circuit breaker
	return a.cb.Do(ctx, func() error {
		reportEvent(ctx, circuitRequest)
		err := fn(api)
		if err != nil {
			a.logger.Error(err, "with api")
			reportEvent(ctx, circuitFailure)
		}

		return err
	})
}

func (a *apiClient) AuthVerify(ctx context.Context, token string) ([]auth.Permission, error) {
	var (
		r []auth.Permission
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.AuthVerify(ctx, token)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) AuthNew(ctx context.Context, perms []auth.Permission) ([]byte, error) {
	var (
		r []byte
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.AuthNew(ctx, perms)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) Version(ctx context.Context) (lotusapi.Version, error) {
	var (
		r lotusapi.Version
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.Version(ctx)
		return e
	}); err != nil {
		return lotusapi.Version{}, err
	}

	return r, e
}

func (a *apiClient) ChainNotify(ctx context.Context) (<-chan []*lotusapi.HeadChange, error) {
	var (
		r <-chan []*lotusapi.HeadChange
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainNotify(ctx)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainHead(ctx context.Context) (*types.TipSet, error) {
	var (
		r *types.TipSet
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainHead(ctx)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetBlock(ctx context.Context, obj cid.Cid) (*types.BlockHeader, error) {
	var (
		r *types.BlockHeader
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetBlock(ctx, obj)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetTipSet(ctx context.Context, tsk types.TipSetKey) (*types.TipSet, error) {
	var (
		r *types.TipSet
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetTipSet(ctx, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetBlockMessages(ctx context.Context, blockCid cid.Cid) (*lotusapi.BlockMessages, error) {
	var (
		r *lotusapi.BlockMessages
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetBlockMessages(ctx, blockCid)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetParentReceipts(ctx context.Context, blockCid cid.Cid) ([]*types.MessageReceipt, error) {
	var (
		r []*types.MessageReceipt
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetParentReceipts(ctx, blockCid)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetParentMessages(ctx context.Context, blockCid cid.Cid) ([]lotusapi.Message, error) {
	var (
		r []lotusapi.Message
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetParentMessages(ctx, blockCid)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetTipSetByHeight(ctx context.Context, h abi.ChainEpoch, tsk types.TipSetKey) (*types.TipSet, error) {
	var (
		r *types.TipSet
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetTipSetByHeight(ctx, h, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainHasObj(ctx context.Context, obj cid.Cid) (bool, error) {
	var (
		r bool
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainHasObj(ctx, obj)
		return e
	}); err != nil {
		return false, err
	}

	return r, e
}

func (a *apiClient) ChainReadObj(ctx context.Context, obj cid.Cid) ([]byte, error) {
	var (
		r []byte
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainReadObj(ctx, obj)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainStatObj(ctx context.Context, obj cid.Cid, base cid.Cid) (lotusapi.ObjStat, error) {
	var (
		r lotusapi.ObjStat
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainStatObj(ctx, obj, base)
		return e
	}); err != nil {
		return lotusapi.ObjStat{}, err
	}

	return r, e
}

func (a *apiClient) ChainGetGenesis(ctx context.Context) (*types.TipSet, error) {
	var (
		r *types.TipSet
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetGenesis(ctx)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainTipSetWeight(ctx context.Context, tsk types.TipSetKey) (types.BigInt, error) {
	var (
		r types.BigInt
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainTipSetWeight(ctx, tsk)
		return e
	}); err != nil {
		return types.BigInt{}, err
	}

	return r, e
}

func (a *apiClient) ChainGetNode(ctx context.Context, path string) (*lotusapi.IpldObject, error) {
	var (
		r *lotusapi.IpldObject
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetNode(ctx, path)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetMessage(ctx context.Context, mc cid.Cid) (*types.Message, error) {
	var (
		r *types.Message
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetMessage(ctx, mc)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) ChainGetPath(ctx context.Context, from types.TipSetKey, to types.TipSetKey) ([]*lotusapi.HeadChange, error) {
	var (
		r []*lotusapi.HeadChange
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.ChainGetPath(ctx, from, to)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateChangedActors(ctx context.Context, old cid.Cid, new cid.Cid) (map[string]types.Actor, error) {
	var (
		r map[string]types.Actor
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateChangedActors(ctx, old, new)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateGetReceipt(ctx context.Context, msg cid.Cid, tsk types.TipSetKey) (*types.MessageReceipt, error) {
	var (
		r *types.MessageReceipt
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateGetReceipt(ctx, msg, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateListMiners(ctx context.Context, tsk types.TipSetKey) ([]address.Address, error) {
	var (
		r []address.Address
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateListMiners(ctx, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateListActors(ctx context.Context, tsk types.TipSetKey) ([]address.Address, error) {
	var (
		r []address.Address
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateListActors(ctx, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateGetActor(ctx context.Context, actor address.Address, tsk types.TipSetKey) (*types.Actor, error) {
	var (
		r *types.Actor
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateGetActor(ctx, actor, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateReadState(ctx context.Context, actor address.Address, tsk types.TipSetKey) (*lotusapi.ActorState, error) {
	var (
		r *lotusapi.ActorState
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateReadState(ctx, actor, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateMinerSectors(ctx context.Context, addr address.Address, sectorNos *bitfield.BitField, tsk types.TipSetKey) ([]*miner.SectorOnChainInfo, error) {
	var (
		r []*miner.SectorOnChainInfo
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateMinerSectors(ctx, addr, sectorNos, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateMinerPower(ctx context.Context, addr address.Address, tsk types.TipSetKey) (*lotusapi.MinerPower, error) {
	var (
		r *lotusapi.MinerPower
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateMinerPower(ctx, addr, tsk)
		return e
	}); err != nil {
		return nil, err
	}

	return r, e
}

func (a *apiClient) StateVMCirculatingSupplyInternal(ctx context.Context, tsk types.TipSetKey) (lotusapi.CirculatingSupply, error) {
	var (
		r lotusapi.CirculatingSupply
		e error
	)

	if err := a.withApi(ctx, func(api lotusapi.FullNode) error {
		r, e = api.StateVMCirculatingSupplyInternal(ctx, tsk)
		return e
	}); err != nil {
		return lotusapi.CirculatingSupply{}, err
	}

	return r, e
}

func reason(r circuit.OpenReason) string {
	switch r {
	case circuit.OpenReasonThreshold:
		return "error threshold breached"
	case circuit.OpenReasonConcurrency:
		return "concurrency limit breached"
	case circuit.OpenReasonTrial:
		return "trial request failed"
	default:
		return fmt.Sprintf("unknown (%d)", r)
	}
}
