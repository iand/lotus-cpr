# lotus-cpr

A smart caching proxy for Lotus filecoin nodes.

## Overview

Lotus-cpr provides a read-only subset of the [Lotus](https://github.com/filecoin-project/lotus) RPC
API suitable for querying the state of the Filecoin network. Immutable blocks are cached locally
for fast retrieval using [gonudb](https://github.com/iand/gonudb) and many IO intensive RPC methods
can be served without calling Lotus. Fronting Lotus with Lotus-cpr can reduce load on the node,
reducing the chance that it falls behind when syncing with the Filecoin chain.


## Usage

Install using:

	go get -u github.com/iand/lotus-cpr

Lotus-cpr uses a multi-tier caching system. By default no caching is performed and requests are
forwarded directly to the Lotus node. 

Caching is enabled by specifying a database directory using the `--store` parameter. When set
Lotus-cpr will look for blocks in this database before fetching from upstream. Any blocks retrieved
from an upstream source will be stored in the database to satisfy future requests.

To further reduce load on the Lotus node blocks may also be retrived from an S3 cache. Use the
`--s3-bucket` parameter to specify the name of the bucket containing the blocks. When enabled
Lotus-cpr will consult the local database first, then S3 and finally revert to direct node access.


                              +-----------+               +-----------+                  +-----------+
                              |           |               |           |                  |           |
    client --- json/rpc ----> | lotus-cpr | -- block? --> |   gondb   | -+--- http ----> |     S3    | 
                              |           |               |           |  |               |           |
                              +-----------+               +-----------+  |               +-----------+
                                    |                                    |
                                    |                                    |               +-----------+
                                    |                                    |               |           |
                                    +---------- proxy call --------------+- json/rpc --> |   Lotus   | 
                                                                                         |           |
                                                                                         +-----------+
                                             
The gonudb and S3 caches only store immutable block data and Lotus-cpr will only attempt to use this data
when it is sure that the request requires no other state.


Command line options:

 - `--api` (required) Token and multiaddress of Lotus node (format: <oauth_token>:/ip4/127.0.0.1/tcp/1234/http)
 - `--store` (optional) Path to directory containing gonudb store used to cache blocks.
 - `--s3-bucket` (optional) Bucket containing blocks from the filecoin chain.


## Author

Written by:

* [Ian Davis](http://github.com/iand) - <http://iandavis.com/>

## License

This is free and unencumbered software released into the public domain. Anyone is free to 
copy, modify, publish, use, compile, sell, or distribute this software, either in source 
code form or as a compiled binary, for any purpose, commercial or non-commercial, and by 
any means. For more information, see <http://unlicense.org/> or the 
accompanying [`UNLICENSE`](UNLICENSE) file.
