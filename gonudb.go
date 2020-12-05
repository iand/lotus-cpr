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
	logger   logr.Logger
}

func NewDBBlockCache(s *gonudb.Store, logger logr.Logger) *DBBlockCache {
	if logger == nil {
		logger = logr.Discard()
	}
	return &DBBlockCache{
		store:  s,
		logger: logger.V(LogLevelInfo),
	}
}

func (d *DBBlockCache) Has(ctx context.Context, c cid.Cid) (bool, error) {
	ctx = cacheContext(ctx, "gonudb")
	_, err := d.store.FetchReader(string(c.Hash()))
	if err != nil {
		data, err := d.fillFromUpstream(ctx, c)
		if err != nil {
			return false, err
		}
		return data != nil, nil
	}

	return true, nil
}

func (d *DBBlockCache) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	ctx = cacheContext(ctx, "gonudb")
	reportEvent(ctx, getRequest)
	stop := startTimer(ctx, getDuration)
	defer stop()

	r, err := d.store.FetchReader(string(c.Hash()))
	if err != nil {
		data, err := d.fillFromUpstream(ctx, c)
		if err != nil {
			reportEvent(ctx, getFailure)
			return nil, err
		}
		reportEvent(ctx, getMiss)
		reportSize(ctx, getSize, len(data))
		return blocks.NewBlockWithCid(data, c)
	}

	buf, err := ioutil.ReadAll(r)
	if err != nil {
		reportEvent(ctx, getFailure)
		return nil, err
	}
	reportEvent(ctx, getHit)
	reportSize(ctx, getSize, len(buf))
	return blocks.NewBlockWithCid(buf, c)
}

func (d *DBBlockCache) SetUpstream(u BlockCache) {
	d.upstream = u
}

func (d *DBBlockCache) fillFromUpstream(ctx context.Context, c cid.Cid) ([]byte, error) {
	reportEvent(ctx, fillRequest)
	stop := startTimer(ctx, fillDuration)
	defer stop()

	if d.upstream == nil {
		reportEvent(ctx, fillFailure)
		return nil, blockstore.ErrNotFound
	}

	blk, err := d.upstream.Get(ctx, c)
	if err != nil {
		reportEvent(ctx, fillFailure)
		d.logger.Error(err, "upstream get", "cid", c.String())
		return nil, err
	}

	data := blk.RawData()

	// gonudb doesn't support zero sized blocks so don't add them
	if len(data) == 0 {
		reportEvent(ctx, fillZero)
		return data, nil
	}

	// Only insert if the block data and cid match, since we can't delete from the store
	chkc, err := c.Prefix().Sum(data)
	if err != nil {
		reportEvent(ctx, fillFailure)
		d.logger.Error(err, "compute block hash", "cid", c.String())
		return nil, err
	}

	if !chkc.Equals(c) {
		reportEvent(ctx, fillFailure)
		d.logger.Error(err, "wrong block hash", "cid", c.String(), "hash", chkc.String())
		return nil, blocks.ErrWrongHash
	}

	if err := d.store.Insert(string(c.Hash()), data); err != nil {
		// Data may have been inserted while we were fetching
		if !errors.Is(err, gonudb.ErrKeyExists) {
			reportEvent(ctx, fillFailure)
			d.logger.Error(err, "insert", "cid", c.String())
		}
		return data, nil
	}
	reportEvent(ctx, fillSuccess)
	reportSize(ctx, fillSize, len(data))
	return data, nil
}

func (d *DBBlockCache) ReportMetrics(ctx context.Context) {
	reportMeasurement(ctx, gonudbRecordCount.M(int64(d.store.RecordCount())))
	reportMeasurement(ctx, gonudbRate.M(d.store.Rate()))
}
