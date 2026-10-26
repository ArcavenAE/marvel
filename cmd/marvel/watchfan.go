package main

import (
	"time"

	"github.com/arcavenae/marvel/internal/api"
)

// The watch view across clusters (docs/design/multicluster-read-view-plan.md,
// section 3 and ship order item 13, #832).

// fanWatchCluster is one entry of the set. An entry whose address was already
// dialled, with the same identity, is not polled: repeatOf names the first.
type fanWatchCluster struct {
	name     string
	repeatOf string
}

// fanFetch is what one poll of one cluster brings back.
type fanFetch struct {
	rec         clusterRecord
	rows        []api.Session
	fingerprint string
	stamp       daemonStamp
	stampOK     bool
}

// fanEvent is what the loop did with one event, for a test to order itself by.
type fanEvent struct {
	Kind    string // result, timeout, stale, key, tick
	Cluster string
	Seq     uint64
	Applied bool
}

type fanWatchConfig struct {
	clusters []fanWatchCluster
	interval time.Duration
	stale    time.Duration // no answer this long reads late
	timeout  time.Duration // no answer this long frees the cluster's slot
	// fetch polls one cluster; wantStamp asks it to read the daemon's pid and
	// start time too, which the merge needs when another entry shares its key.
	fetch func(cluster string, seq uint64, wantStamp bool) fanFetch
	// keyOf is daemonKey for a cluster and the fingerprint its dial verified.
	keyOf func(cluster, fingerprint string) string
	now   func() time.Time
	out   func(frame string)
	trace func(fanEvent)
}

func fanWatchLoop(ws *watchSort, cfg fanWatchConfig, keys <-chan byte, ticks <-chan time.Time) {
}
