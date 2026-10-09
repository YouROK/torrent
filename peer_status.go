package torrent

import (
	"math"
	"time"
)

// PeerStatus is a snapshot of one established peer connection. It exists to explain where download
// bandwidth goes: which peer delivers, which one stopped, and whether a peer that used to be fast
// is still connected.
type PeerStatus struct {
	Addr       string `json:"addr"`
	ClientName string `json:"client_name"`
	// Discovery is the source the peer was learned from, such as a tracker or the DHT.
	Discovery string `json:"discovery"`
	Encrypted bool   `json:"encrypted"`
	Utp       bool   `json:"utp"`

	// Local view of the connection.
	Interested bool `json:"interested"`
	// Choked is true when the peer refuses to serve us.
	Choked bool `json:"choked"`

	// Remote view of the connection.
	PeerInterested bool `json:"peer_interested"`
	PeerChoked     bool `json:"peer_choked"`
	PeerHasAll     bool `json:"peer_has_all"`

	BytesReadUseful int64 `json:"bytes_read_useful"`
	BytesReadData   int64 `json:"bytes_read_data"`
	BytesReadTotal  int64 `json:"bytes_read_total"`
	BytesWritten    int64 `json:"bytes_written"`

	ChunksReadUseful int64 `json:"chunks_read_useful"`
	ChunksRead       int64 `json:"chunks_read"`
	ChunksReadWasted int64 `json:"chunks_read_wasted"`
	ChunksWritten    int64 `json:"chunks_written"`

	// Requests is the number of chunks requested from this peer and not yet received.
	Requests int `json:"requests"`
	// NominalMaxRequests is the current cap on Requests, derived from the peer's observed speed.
	NominalMaxRequests int `json:"nominal_max_requests"`
	// PeerMaxRequests is the cap the peer itself advertised.
	PeerMaxRequests int `json:"peer_max_requests"`
	// PeerRequests is the number of chunks the peer asked us for.
	PeerRequests int `json:"peer_requests"`

	LastUsefulChunkReceived time.Time     `json:"last_useful_chunk_received"`
	LastMessageReceived     time.Time     `json:"last_message_received"`
	ConnectedFor            time.Duration `json:"connected_for"`

	// DownloadRate is the useful payload rate averaged over the time the peer was serving us.
	DownloadRate float64 `json:"download_rate"`
}

// PeerStatuses returns a snapshot of every established peer connection of the torrent.
func (t *Torrent) PeerStatuses() []PeerStatus {
	t.cl.rLock()
	defer t.cl.rUnlock()

	conns := t.connsAsSlice()
	ret := make([]PeerStatus, 0, len(conns))
	for _, cn := range conns {
		ret = append(ret, cn.peerStatus())
	}
	return ret
}

func (cn *connection) peerStatus() (ret PeerStatus) {
	ret.Addr = cn.remoteAddr.String()
	ret.ClientName = cn.PeerClientName
	ret.Discovery = string(cn.Discovery)
	ret.Encrypted = cn.headerEncrypted
	ret.Utp = cn.utp()

	ret.Interested = cn.Interested
	ret.Choked = cn.Choked
	ret.PeerInterested = cn.PeerInterested
	ret.PeerChoked = cn.PeerChoked
	if all, known := cn.peerHasAllPieces(); known {
		ret.PeerHasAll = all
	}

	ret.BytesReadUseful = cn.stats.BytesReadUsefulData.Int64()
	ret.BytesReadData = cn.stats.BytesReadData.Int64()
	ret.BytesReadTotal = cn.stats.BytesRead.Int64()
	ret.BytesWritten = cn.stats.BytesWritten.Int64()

	ret.ChunksReadUseful = cn.stats.ChunksReadUseful.Int64()
	ret.ChunksRead = cn.stats.ChunksRead.Int64()
	ret.ChunksReadWasted = cn.stats.ChunksReadWasted.Int64()
	ret.ChunksWritten = cn.stats.ChunksWritten.Int64()

	ret.Requests = len(cn.requests)
	ret.NominalMaxRequests = cn.nominalMaxRequests()
	ret.PeerMaxRequests = cn.PeerMaxRequests
	ret.PeerRequests = len(cn.PeerRequests)

	ret.LastUsefulChunkReceived = cn.lastUsefulChunkReceived
	ret.LastMessageReceived = cn.lastMessageReceived
	if !cn.completedHandshake.IsZero() {
		ret.ConnectedFor = time.Since(cn.completedHandshake)
	}
	ret.DownloadRate = cn.downloadRate()
	if math.IsNaN(ret.DownloadRate) || math.IsInf(ret.DownloadRate, 0) {
		// A peer that never served anything divides zero bytes by zero interest time.
		ret.DownloadRate = 0
	}
	return
}
