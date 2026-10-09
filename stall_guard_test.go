package torrent

import (
	"testing"
	"time"

	"github.com/anacrolix/missinggo/bitmap"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

// stallFixture builds a connection whose torrent already has chunk 0 of piece 0 requested from
// another peer, with the duplicate request timer still running.
func stallFixture(t *testing.T) *connection {
	t.Helper()
	cn := chokeFixture(2)
	cn.PeerChoked = false
	cn.t.duplicateRequestTimeout = time.Hour
	// One chunk of piece 0 sits in lastRequested, as if another peer had already asked for it.
	cn.t.lastRequested[request{0, cn.t.chunkIndexSpec(0, 0)}] = time.AfterFunc(time.Hour, func() {})
	return cn
}

// TestStallPronePieceFetchedRedundantly checks that the piece under the reader is requested even
// while the duplicate request timer runs, because its last chunks gate playback.
func TestStallPronePieceFetchedRedundantly(t *testing.T) {
	cn := stallFixture(t)
	defer stopChokeFixture(cn)

	// Piece 0 is the one under the reader.
	cn.t.readerNowPieces.Add(bitmap.BitIndex(0))

	var asked []request
	cn.fillWriteBuffer(func(m pp.Message) bool {
		if m.Type == pp.Request {
			asked = append(asked, newRequestFromMessage(&m))
		}
		return true
	})

	for _, r := range asked {
		if r.Index == 0 && r.Begin == 0 {
			return
		}
	}
	t.Fatalf("piece under reader was not re-requested; asked: %v", asked)
}

// TestNonUrgentPieceStillSuppressed checks that the rest of the zone still saves traffic: a
// requested chunk is not asked for again while the duplicate request timer runs.
func TestNonUrgentPieceStillSuppressed(t *testing.T) {
	cn := stallFixture(t)
	defer stopChokeFixture(cn)

	// Another piece is under the reader, so the requested chunk of piece 0 is not urgent.
	cn.t.readerNowPieces.Add(bitmap.BitIndex(1))

	var asked []request
	cn.fillWriteBuffer(func(m pp.Message) bool {
		if m.Type == pp.Request {
			asked = append(asked, newRequestFromMessage(&m))
		}
		return true
	})

	for _, r := range asked {
		if r.Index == 0 && r.Begin == 0 {
			t.Fatalf("suppressed chunk was re-requested: %v", asked)
		}
	}
}

// TestStallPronePieceOnlyForReaderPiece checks that the exception does not spread to the neighbouring
// pieces: they stay behind the duplicate request timer.
func TestStallPronePieceOnlyForReaderPiece(t *testing.T) {
	cn := stallFixture(t)
	defer stopChokeFixture(cn)

	cn.t.readerNowPieces.Add(bitmap.BitIndex(0))
	cn.t.readerNextPieces.Add(bitmap.BitIndex(1))
	// Chunk 0 of piece 1 has also been requested already.
	cn.t.lastRequested[request{1, cn.t.chunkIndexSpec(0, 1)}] = time.AfterFunc(time.Hour, func() {})

	var asked []request
	cn.fillWriteBuffer(func(m pp.Message) bool {
		if m.Type == pp.Request {
			asked = append(asked, newRequestFromMessage(&m))
		}
		return true
	})

	for _, r := range asked {
		if r.Index == 1 && r.Begin == 0 {
			t.Fatalf("piece ahead of reader was re-requested: %v", asked)
		}
	}
}
