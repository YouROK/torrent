package torrent

import (
	"testing"

	"github.com/anacrolix/missinggo/bitmap"
	"github.com/anacrolix/torrent/metainfo"
)

// TestReaderTiers checks how pieces are laid out over the priority tiers from the reader position.
func TestReaderTiers(t *testing.T) {
	const pieceLength = 1 << 20
	tor := newTierTestTorrent(t, pieceLength, 64)

	r := newTierTestReader(tor, 64<<20)
	r.readahead = 16 << 20
	r.next = 4 << 20
	r.zone = 48 << 20

	ranges := r.rangesUncached()
	if ranges.pieces.begin != 0 || ranges.pieces.end != 16 {
		t.Fatalf("readahead range: got %v, want [0,16)", ranges.pieces)
	}
	if ranges.next.begin != 0 || ranges.next.end != 4 {
		t.Fatalf("next range: got %v, want [0,4)", ranges.next)
	}
	if ranges.zone.begin != 0 || ranges.zone.end != 48 {
		t.Fatalf("zone range: got %v, want [0,48)", ranges.zone)
	}

	r.ranges = ranges
	tor.updateReaderPieces()

	want := map[int]piecePriority{
		0:  PiecePriorityNow,       // the piece under the read position
		1:  PiecePriorityNext,      // the near window
		3:  PiecePriorityNext,      // the end of the near window
		4:  PiecePriorityReadahead, // the far window
		15: PiecePriorityReadahead, // the end of the far window
		16: PiecePriorityNormal,    // the rest of the download zone
		47: PiecePriorityNormal,    // the end of the download zone
		48: PiecePriorityNone,      // nothing beyond the zone is fetched
		63: PiecePriorityNone,
	}
	for index, expected := range want {
		if got := tor.pieces[index].uncachedPriority(); got != expected {
			t.Errorf("piece %d priority: got %v, want %v", index, got, expected)
		}
	}
}

// TestReaderTierShift checks that the tiers follow the reader when it moves.
func TestReaderTierShift(t *testing.T) {
	const pieceLength = 1 << 20
	tor := newTierTestTorrent(t, pieceLength, 64)

	r := newTierTestReader(tor, 64<<20)
	r.readahead = 4 << 20
	r.next = 2 << 20
	r.zone = 16 << 20
	r.ranges = r.rangesUncached()
	tor.updateReaderPieces()

	// A piece beyond the zone is not wanted.
	if got := tor.pieces[20].uncachedPriority(); got != PiecePriorityNone {
		t.Fatalf("piece 20 before shift: got %v, want None", got)
	}

	r.pos = 20 << 20
	r.ranges = r.rangesUncached()
	tor.updateReaderPieces()

	if got := tor.pieces[20].uncachedPriority(); got != PiecePriorityNow {
		t.Errorf("piece 20 after shift: got %v, want Now", got)
	}
	if got := tor.pieces[0].uncachedPriority(); got != PiecePriorityNone {
		t.Errorf("piece 0 after shift: got %v, want None", got)
	}
}

// TestReaderAdvanceUpdatesNowPriority checks that the piece under the reader becomes Now on a shift,
// instead of keeping the tier it had as part of the next window.
func TestReaderAdvanceUpdatesNowPriority(t *testing.T) {
	const pieceLength = 1 << 20
	tor := newTierTestTorrent(t, pieceLength, 64)

	r := newTierTestReader(tor, 64<<20)
	r.readahead = 16 << 20
	r.next = 4 << 20
	r.zone = 48 << 20

	from := r.rangesUncached()
	r.pos = pieceLength
	to := r.rangesUncached()

	// Piece 0 leaves the window and piece 1 becomes its first piece. Both must be re-evaluated,
	// otherwise the piece under the reader keeps the stale Next tier.
	changed := readerChangedPieces(from, to)
	for _, want := range []int{0, 1} {
		if !changed.Contains(want) {
			t.Errorf("piece %d missing from changed set %v", want, changed.ToSortedSlice())
		}
	}

	// Check the resulting priorities at the new position.
	r.ranges = to
	tor.updateReaderPieces()

	if got := tor.pieces[1].uncachedPriority(); got != PiecePriorityNow {
		t.Errorf("piece 1 after advance: got %v, want Now", got)
	}
	if got := tor.pieces[0].uncachedPriority(); got != PiecePriorityNone {
		t.Errorf("piece 0 after advance: got %v, want None", got)
	}
}

// TestReaderAdvanceChain checks a series of consecutive reader shifts: the piece under the reader
// must become Now every time, instead of keeping the tier from the previous window.
func TestReaderAdvanceChain(t *testing.T) {
	const pieceLength = 1 << 20
	tor := newTierTestTorrent(t, pieceLength, 64)

	r := newTierTestReader(tor, 64<<20)
	r.readahead = 16 << 20
	r.next = 4 << 20
	r.zone = 48 << 20

	// A full recalculation, as when a reader is added.
	r.ranges = r.rangesUncached()
	tor.updateReaderPieces()

	for step := 1; step <= 8; step++ {
		from := r.ranges
		r.pos = int64(step) * pieceLength
		to := r.rangesUncached()
		r.ranges = to

		// Recalculate only what changed, as readerPosChanged does.
		tor.updateReaderPieces()

		nowPiece := int(to.pieces.begin)
		if got := tor.pieces[nowPiece].uncachedPriority(); got != PiecePriorityNow {
			t.Errorf("step %d: piece %d under reader: got %v, want Now", step, nowPiece, got)
		}

		// The piece under the reader must be re-evaluated on every step.
		chg := readerChangedPieces(from, to)
		if !chg.Contains(nowPiece) {
			t.Errorf("step %d: piece %d under reader missing from changed set", step, nowPiece)
		}

		// A piece the reader left must not stay in the zone.
		left := int(from.pieces.begin)
		if left != nowPiece && tor.pieces[left].uncachedPriority() == PiecePriorityNow {
			t.Errorf("step %d: left piece %d still Now", step, left)
		}
	}
}

// TestPieceWanted checks that a piece outside every reader zone and outside the pending queue counts
// as unwanted, so that late chunks of it never reach the storage.
func TestPieceWanted(t *testing.T) {
	const pieceLength = 1 << 20
	tor := newTierTestTorrent(t, pieceLength, 64)

	r := newTierTestReader(tor, 64<<20)
	r.readahead = 4 << 20
	r.next = 2 << 20
	r.zone = 16 << 20
	r.ranges = r.rangesUncached()
	tor.updateReaderPieces()

	if !tor.pieceWanted(0) {
		t.Errorf("piece 0 inside zone: got unwanted, want wanted")
	}
	if !tor.pieceWanted(15) {
		t.Errorf("piece 15 inside zone: got unwanted, want wanted")
	}
	if tor.pieceWanted(20) {
		t.Errorf("piece 20 outside zone: got wanted, want unwanted")
	}

	// The reader moved on, so the old piece is no longer wanted.
	r.pos = 40 << 20
	r.ranges = r.rangesUncached()
	tor.updateReaderPieces()

	if tor.pieceWanted(0) {
		t.Errorf("piece 0 after shift: got wanted, want unwanted")
	}
	if !tor.pieceWanted(40) {
		t.Errorf("piece 40 after shift: got unwanted, want wanted")
	}
}

// TestAddRangeDiff checks the calculation of the changed pieces between two ranges.
func TestAddRangeDiff(t *testing.T) {
	var dst bitmap.Bitmap
	addRangeDiff(&dst, pieceRange{0, 4}, pieceRange{2, 6})
	got := dst.ToSortedSlice()
	want := []int{0, 1, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("diff: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("diff: got %v, want %v", got, want)
		}
	}
}

// newTierTestTorrent builds a torrent without networking: only info, pieces and length.
func newTierTestTorrent(t *testing.T, pieceLength, pieces int) *Torrent {
	t.Helper()
	length := int64(pieces) * int64(pieceLength)
	tor := &Torrent{
		info: &metainfo.Info{
			PieceLength: int64(pieceLength),
			Pieces:      make([]byte, pieces*20),
			Files:       []metainfo.FileInfo{{Length: length, Path: []string{"test.bin"}}},
		},
		readers: make(map[*reader]struct{}),
	}
	tor.length = &length
	tor.pieces = make([]Piece, pieces)
	for i := range tor.pieces {
		tor.pieces[i].t = tor
		tor.pieces[i].index = pieceIndex(i)
	}
	return tor
}

// newTierTestReader creates a reader bound to the torrent and registered in it.
func newTierTestReader(tor *Torrent, length int64) *reader {
	r := &reader{t: tor, length: length}
	tor.readers[r] = struct{}{}
	return r
}
