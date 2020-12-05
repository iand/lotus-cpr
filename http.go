package main

import (
	"context"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipfs-blockstore"
)

var _ (BlockCache) = (*HttpBlockCache)(nil)

type HttpBlockCache struct {
	base     string
	hc       *http.Client
	upstream BlockCache
	name     string
}

func NewHttpBlockCache(base string, name string) *HttpBlockCache {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	return &HttpBlockCache{
		base: base,
		name: name,
		hc:   &http.Client{},
	}
}

func (bc *HttpBlockCache) Has(ctx context.Context, c cid.Cid) (bool, error) {
	ctx = cacheContext(ctx, bc.name)
	u := bc.base + c.String() + "/data.raw"
	resp, err := bc.hc.Head(u)
	if err != nil {
		if bc.upstream == nil {
			return false, err
		}
		return bc.upstream.Has(ctx, c)
	}
	if resp.StatusCode == 200 {
		return true, nil
	}

	if bc.upstream == nil {
		return false, nil
	}
	return bc.upstream.Has(ctx, c)
}

func (bc *HttpBlockCache) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	ctx = cacheContext(ctx, bc.name)
	reportEvent(ctx, getRequest)
	stop := startTimer(ctx, getDuration)
	defer stop()

	u := bc.base + c.String() + "/data.raw"
	resp, err := bc.hc.Get(u)
	if err != nil {
		reportEvent(ctx, getFailure)
		if bc.upstream == nil {
			return nil, err
		}
		return bc.upstream.Get(ctx, c)
	}
	defer resp.Body.Close()
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		reportEvent(ctx, getFailure)
		if bc.upstream == nil {
			return nil, err
		}
		return bc.upstream.Get(ctx, c)
	}
	if resp.StatusCode == 200 {
		reportEvent(ctx, getHit)
		reportSize(ctx, getSize, len(buf))
		return blocks.NewBlockWithCid(buf, c)
	}
	reportEvent(ctx, getMiss)

	if bc.upstream == nil {
		return nil, blockstore.ErrNotFound
	}

	return bc.upstream.Get(ctx, c)
}

func (bc *HttpBlockCache) SetUpstream(u BlockCache) {
	bc.upstream = u
}
