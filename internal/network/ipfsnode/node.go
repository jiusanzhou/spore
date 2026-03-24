/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package ipfsnode provides an embedded IPFS node that reuses an existing
// libp2p host. Content is stored in a local blockstore and exchanged
// with peers via the Bitswap protocol.
//
// Each Spore agent IS an IPFS-capable node — no external daemon needed.
package ipfsnode

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/ipfs/boxo/bitswap"
	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/blockservice"
	blockstore "github.com/ipfs/boxo/blockstore"
	chunker "github.com/ipfs/boxo/chunker"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	ufshelpers "github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	mh "github.com/multiformats/go-multihash"
)

// Node is a lightweight embedded IPFS node.
type Node struct {
	host   host.Host
	bstore blockstore.Blockstore
	bswap  *bitswap.Bitswap
	bserv  blockservice.BlockService
	dag    ipld.DAGService
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
}

// Config holds configuration for the embedded IPFS node.
type Config struct {
	Host    host.Host
	Routing routing.ContentRouting // DHT for content routing; can be nil
}

// New creates an embedded IPFS node reusing an existing libp2p host.
func New(cfg Config) (*Node, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// In-memory datastore for blocks.
	dstore := dssync.MutexWrap(ds.NewMapDatastore())
	bstore := blockstore.NewBlockstore(dstore)

	// Bitswap network from libp2p host.
	net := bsnet.NewFromIpfsHost(cfg.Host)

	// Content discovery for Bitswap.
	var discovery routing.ContentDiscovery
	if cfg.Routing != nil {
		discovery = cfg.Routing
	} else {
		discovery = &nilDiscovery{}
	}

	// Bitswap exchange.
	bswap := bitswap.New(ctx, net, discovery, bstore)

	// Block service = local blockstore + Bitswap.
	bserv := blockservice.New(bstore, bswap)

	// DAG service for UnixFS.
	dag := merkledag.NewDAGService(bserv)

	return &Node{
		host:   cfg.Host,
		bstore: bstore,
		bswap:  bswap,
		bserv:  bserv,
		dag:    dag,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Add stores data as an IPFS UnixFS DAG and returns the root CID.
// Use for content > 256KB that needs chunking.
func (n *Node) Add(data []byte) (cid.Cid, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	r := bytes.NewReader(data)
	splitter := chunker.DefaultSplitter(r)

	dbp := ufshelpers.DagBuilderParams{
		Maxlinks: ufshelpers.DefaultLinksPerBlock,
		Dagserv:  n.dag,
	}
	db, err := dbp.New(splitter)
	if err != nil {
		return cid.Undef, fmt.Errorf("dag builder: %w", err)
	}

	nd, err := balanced.Layout(db)
	if err != nil {
		return cid.Undef, fmt.Errorf("layout: %w", err)
	}

	return nd.Cid(), nil
}

// AddRaw stores raw data as a single CIDv1 block (SHA2-256).
// Suitable for content ≤ 256KB.
func (n *Node) AddRaw(data []byte) (cid.Cid, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	hash, err := mh.Sum(data, mh.SHA2_256, -1)
	if err != nil {
		return cid.Undef, fmt.Errorf("multihash: %w", err)
	}
	c := cid.NewCidV1(cid.Raw, hash)

	blk, err := blocks.NewBlockWithCid(data, c)
	if err != nil {
		return cid.Undef, fmt.Errorf("new block: %w", err)
	}

	if err := n.bstore.Put(n.ctx, blk); err != nil {
		return cid.Undef, fmt.Errorf("put block: %w", err)
	}

	return c, nil
}

// Get retrieves raw data by CID. Checks local blockstore first,
// then uses Bitswap to fetch from peers.
func (n *Node) Get(ctx context.Context, c cid.Cid) ([]byte, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	blk, err := n.bserv.GetBlock(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("get block %s: %w", c, err)
	}

	return blk.RawData(), nil
}

// Has checks if a CID is available in the local blockstore.
func (n *Node) Has(c cid.Cid) bool {
	ok, _ := n.bstore.Has(n.ctx, c)
	return ok
}

// Close shuts down the IPFS node.
func (n *Node) Close() error {
	n.cancel()
	return n.bswap.Close()
}

// DAGService returns the underlying DAG service.
func (n *Node) DAGService() ipld.DAGService {
	return n.dag
}

// Host returns the libp2p host.
func (n *Node) Host() host.Host {
	return n.host
}

// nilDiscovery is a no-op content discovery for when DHT is not available.
type nilDiscovery struct{}

func (r *nilDiscovery) FindProvidersAsync(ctx context.Context, c cid.Cid, n int) <-chan peer.AddrInfo {
	ch := make(chan peer.AddrInfo)
	close(ch)
	return ch
}
