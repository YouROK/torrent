package torrent

import (
	"github.com/anacrolix/missinggo/bitmap"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

// Keep the original scheduler as a test-only oracle for the unchoked sequence
// and the synthetic busy-chunk benchmark (upstream 8aba96333166).
func (cn *connection) fillWriteBufferBeforeChokeOptimization(msg func(pp.Message) bool) {
	if !cn.t.networkingEnabled {
		if !cn.SetInterested(false, msg) {
			return
		}
		if len(cn.requests) != 0 {
			for r := range cn.requests {
				cn.deleteRequest(r)
				// log.Printf("%p: cancelling request: %v", cn, r)
				if !msg(makeCancelMessage(r)) {
					return
				}
			}
		}
	}
	if len(cn.requests) <= cn.requestsLowWater {
		filledBuffer := false
		cn.iterPendingPieces(func(pieceIndex pieceIndex) bool {
			cn.iterPendingRequests(pieceIndex, func(r request) bool {
				if !cn.SetInterested(true, msg) {
					filledBuffer = true
					return false
				}
				if len(cn.requests) >= cn.nominalMaxRequests() {
					return false
				}
				// Choking is looked at here because our interest is dependent
				// on whether we'd make requests in its absence.
				if cn.PeerChoked {
					if !cn.peerAllowedFast.Get(bitmap.BitIndex(r.Index)) {
						return false
					}
				}
				if _, ok := cn.requests[r]; ok {
					return true
				}
				filledBuffer = !cn.request(r, msg)
				return !filledBuffer
			})
			return !filledBuffer
		})
		if filledBuffer {
			// If we didn't completely top up the requests, we shouldn't mark
			// the low water, since we'll want to top up the requests as soon
			// as we have more write buffer space.
			return
		}
		cn.requestsLowWater = len(cn.requests) / 2
	}

	cn.upload(msg)
}
