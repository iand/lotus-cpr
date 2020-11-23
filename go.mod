module github.com/iand/lotus-cpr

go 1.16

require (
	github.com/filecoin-project/go-address v0.0.5-0.20201103152444-f2023ef3f5bb
	github.com/filecoin-project/go-bitfield v0.2.3-0.20201110211213-fe2c1862e816
	github.com/filecoin-project/go-jsonrpc v0.1.2-0.20201008195726-68c6a2704e49
	github.com/filecoin-project/go-state-types v0.0.0-20201102161440-c8033295a1fc
	github.com/filecoin-project/lotus v1.2.1
	github.com/go-logr/logr v0.3.0
	github.com/gorilla/mux v1.7.4
	github.com/iand/gonudb v0.2.0
	github.com/iand/logfmtr v0.1.5
	github.com/ipfs/go-block-format v0.0.2
	github.com/ipfs/go-cid v0.0.7
	github.com/ipfs/go-ipfs-blockstore v1.0.3
	github.com/multiformats/go-multiaddr v0.3.1
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/urfave/cli/v2 v2.3.0
	go.opencensus.io v0.22.5 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	golang.org/x/crypto v0.0.0-20201117144127-c1f2f97bffc9 // indirect
	golang.org/x/sys v0.0.0-20201119102817-f84b799fce68 // indirect
	golang.org/x/tools v0.0.0-20201121010211-780cb80bd7fb // indirect
)

replace github.com/filecoin-project/filecoin-ffi => github.com/filecoin-project/statediff/extern/filecoin-ffi v0.0.0-20201112214200-3592b9922dcc
