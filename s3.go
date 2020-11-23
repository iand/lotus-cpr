package main

import (
	"context"
	"io/ioutil"
	"net/http"
	"path"

	"github.com/go-logr/logr"
	"github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipfs-blockstore"
)

var _ (BlockCache) = (*S3BlockCache)(nil)

type S3BlockCache struct {
	bucket   string
	hc       *http.Client
	upstream BlockCache
	tlogger  logr.Logger // request tracing
	stats    CacheStats
}

func NewS3BlockCache(bucket string, logger logr.Logger) *S3BlockCache {
	if logger == nil {
		logger = logr.Discard()
	}
	return &S3BlockCache{
		bucket:  bucket,
		hc:      &http.Client{},
		tlogger: logger.V(LogLevelTrace),
	}
}

func (s *S3BlockCache) Has(ctx context.Context, c cid.Cid) (bool, error) {
	u := "https://" + path.Join(s.bucket, "mainnet", "blocks", c.String(), "data.raw")
	if s.tlogger.Enabled() {
		s.tlogger.Info("Has", "block", c, "url", u)
	}
	resp, err := s.hc.Head(u)
	if err != nil {
		s.stats.Error()
		if s.tlogger.Enabled() {
			s.tlogger.Error(err, "Has failed", "block", c)
		}
		if s.upstream == nil {
			return false, err
		}
		if s.tlogger.Enabled() {
			s.tlogger.Info("Fulfilling Has from upstream", "block", c)
		}
		return s.upstream.Has(ctx, c)
	}
	if resp.StatusCode == 200 {
		s.stats.Hit()
		return true, nil
	}
	s.stats.Miss()

	if s.tlogger.Enabled() {
		s.tlogger.Info("Block not found", "block", c)
	}
	if s.upstream == nil {
		return false, nil
	}
	if s.tlogger.Enabled() {
		s.tlogger.Info("Fulfilling has from upstream", "block", c)
	}
	return s.upstream.Has(ctx, c)
}

func (s *S3BlockCache) Get(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	u := "https://" + path.Join(s.bucket, "mainnet", "blocks", c.String(), "data.raw")
	if s.tlogger.Enabled() {
		s.tlogger.Info("Get", "block", c, "url", u)
	}
	resp, err := s.hc.Get(u)
	if err != nil {
		s.stats.Error()
		if s.tlogger.Enabled() {
			s.tlogger.Error(err, "Get failed", "block", c)
		}
		if s.upstream == nil {
			return nil, err
		}
		if s.tlogger.Enabled() {
			s.tlogger.Info("Fulfilling get from upstream", "block", c)
		}
		return s.upstream.Get(ctx, c)
	}
	defer resp.Body.Close()
	buf, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 200 {
		s.stats.Hit()
		return blocks.NewBlockWithCid(buf, c)
	}
	if s.tlogger.Enabled() {
		s.tlogger.Info("Get failed", "block", c, "http_status", resp.StatusCode)
	}
	s.stats.Miss()

	if s.upstream == nil {
		return nil, blockstore.ErrNotFound
	}

	if s.tlogger.Enabled() {
		s.tlogger.Info("Fulfilling get from upstream", "block", c)
	}
	return s.upstream.Get(ctx, c)
}

func (s *S3BlockCache) SetUpstream(u BlockCache) {
	s.upstream = u
}

func (s *S3BlockCache) LogStats(dlogger logr.Logger) {
	s.stats.Log("s3", dlogger)
}
