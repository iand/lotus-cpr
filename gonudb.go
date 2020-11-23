package main

import (
	"context"
	"errors"
	"io/ioutil"

	"github.com/go-logr/logr"
	"github.com/iand/gonudb"
	"github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipfs-blockstore"
)

var _ (BlockCache) = (*DBBlockCache)(nil)

type DBBlockCache struct {
	store    *gonudb.Store
	upstream BlockCache
	tlogger  logr.Logger // request tracing
	stats    CacheStats
}

func NewDBBlockCache(s *gonudb.Store, logger logr.Logger) *DBBlockCache {
	if logger == nil {
		logger = logr.Discard()
	}
	return &DBBlockCache{
		store:   s,
		tlogger: logger.V(LogLevelTrace),
	}
}

func (d *DBBlockCache) Has(ctx context.Context, c cid.Cid) (bool, error) {
	if d.tlogger.Enabled() {
		d.tlogger.Info("Has", "block", c)
	}
	cstr := c.String()
	_, err := d.store.FetchReader(cstr)
	if err != nil {
		if d.tlogger.Enabled() {
			if errors.Is(err, gonudb.ErrKeyNotFound) {
				d.stats.Miss()
				d.tlogger.Info("Not found in store", "block", c)
			} else {
				d.stats.Error()
				d.tlogger.Error(err, "FetchReader failed", "block", c)
			}
		}
		data, err := d.fillFromUpstream(ctx, c)
		if err != nil {
			if d.tlogger.Enabled() {
				d.tlogger.Error(err, "Upstream fill failed", "block", c)
			}
			return false, err
		}
		return data != nil, nil
	}

	d.stats.Hit()
	return true, nil
}

func (d *DBBlockCache) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	if d.tlogger.Enabled() {
		d.tlogger.Info("Has", "block", c)
	}
	cstr := c.String()
	r, err := d.store.FetchReader(cstr)
	if err != nil {
		if errors.Is(err, gonudb.ErrKeyNotFound) {
			d.stats.Miss()
			if d.tlogger.Enabled() {
				d.tlogger.Info("Not found in store", "block", c)
			}
		} else {
			d.stats.Error()
			if d.tlogger.Enabled() {
				d.tlogger.Error(err, "FetchReader failed", "block", c)
			}
		}
		data, err := d.fillFromUpstream(ctx, c)
		if err != nil {
			if d.tlogger.Enabled() {
				d.tlogger.Error(err, "Upstream fill failed", "block", c)
			}
			return nil, err
		}
		return blocks.NewBlockWithCid(data, c)
	}

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		d.stats.Error()
		return nil, err
	}
	d.stats.Hit()
	return blocks.NewBlockWithCid(buf, c)
}

func (d *DBBlockCache) SetUpstream(u BlockCache) {
	d.upstream = u
}

func (d *DBBlockCache) fillFromUpstream(ctx context.Context, c cid.Cid) ([]byte, error) {
	if d.upstream == nil {
		if d.tlogger.Enabled() {
			d.tlogger.Info("No upstream to fill from", "block", c)
		}
		return nil, blockstore.ErrNotFound
	}
	if d.tlogger.Enabled() {
		d.tlogger.Info("Filling from upstream", "block", c)
	}

	blk, err := d.upstream.Get(ctx, c)
	if err != nil {
		if d.tlogger.Enabled() {
			d.tlogger.Error(err, "Upstream fill failed", "block", c)
		}
		return nil, err
	}

	data := blk.RawData()
	// Only insert if the block data and cid match, since we can't delete from the store
	chkc, err := c.Prefix().Sum(data)
	if err != nil {
		return nil, err
	}

	if !chkc.Equals(c) {
		return nil, blocks.ErrWrongHash
	}

	if err := d.store.Insert(c.String(), data); err != nil {
		if d.tlogger.Enabled() {
			d.tlogger.Error(err, "Insert failed", "block", c)
		}
	}
	return data, nil
}

func (d *DBBlockCache) LogStats(dlogger logr.Logger) {
	d.stats.Log("db", dlogger)
}
