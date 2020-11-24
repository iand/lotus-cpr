package main

import (
	"context"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	"github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipfs-blockstore"
)

var _ (BlockCache) = (*HttpBlockCache)(nil)

type HttpBlockCache struct {
	base     string
	hc       *http.Client
	upstream BlockCache
	tlogger  logr.Logger // request tracing
	stats    CacheStats
}

func NewHttpBlockCache(base string, logger logr.Logger) *HttpBlockCache {
	if logger == nil {
		logger = logr.Discard()
	}

	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	return &HttpBlockCache{
		base:    base,
		hc:      &http.Client{},
		tlogger: logger.V(LogLevelTrace),
	}
}

func (bc *HttpBlockCache) Has(ctx context.Context, c cid.Cid) (bool, error) {
	u := bc.base + c.String() + "/data.raw"
	if bc.tlogger.Enabled() {
		bc.tlogger.Info("Has", "block", c, "url", u)
	}
	resp, err := bc.hc.Head(u)
	if err != nil {
		bc.stats.Error()
		if bc.tlogger.Enabled() {
			bc.tlogger.Error(err, "Has failed", "block", c)
		}
		if bc.upstream == nil {
			return false, err
		}
		if bc.tlogger.Enabled() {
			bc.tlogger.Info("Fulfilling Has from upstream", "block", c)
		}
		return bc.upstream.Has(ctx, c)
	}
	if resp.StatusCode == 200 {
		bc.stats.Hit()
		return true, nil
	}
	bc.stats.Miss()

	if bc.tlogger.Enabled() {
		bc.tlogger.Info("Block not found", "block", c)
	}
	if bc.upstream == nil {
		return false, nil
	}
	if bc.tlogger.Enabled() {
		bc.tlogger.Info("Fulfilling has from upstream", "block", c)
	}
	return bc.upstream.Has(ctx, c)
}

func (bc *HttpBlockCache) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	u := bc.base + c.String() + "/data.raw"
	if bc.tlogger.Enabled() {
		bc.tlogger.Info("Get", "block", c, "url", u)
	}
	resp, err := bc.hc.Get(u)
	if err != nil {
		bc.stats.Error()
		if bc.tlogger.Enabled() {
			bc.tlogger.Error(err, "Get failed", "block", c)
		}
		if bc.upstream == nil {
			return nil, err
		}
		if bc.tlogger.Enabled() {
			bc.tlogger.Info("Fulfilling get from upstream", "block", c)
		}
		return bc.upstream.Get(ctx, c)
	}
	defer resp.Body.Close()
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 200 {
		bc.stats.Hit()
		return blocks.NewBlockWithCid(buf, c)
	}
	if bc.tlogger.Enabled() {
		bc.tlogger.Info("Get failed", "block", c, "http_status", resp.StatusCode)
	}
	bc.stats.Miss()

	if bc.upstream == nil {
		return nil, blockstore.ErrNotFound
	}

	if bc.tlogger.Enabled() {
		bc.tlogger.Info("Fulfilling get from upstream", "block", c)
	}
	return bc.upstream.Get(ctx, c)
}

func (bc *HttpBlockCache) SetUpstream(u BlockCache) {
	bc.upstream = u
}

func (bc *HttpBlockCache) LogStats(dlogger logr.Logger) {
	bc.stats.Log("http", dlogger)
}
