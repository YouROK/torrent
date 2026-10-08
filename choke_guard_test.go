package torrent

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

func chokeFixture(pieces int) *connection {
	length := int64(pieces * (2 << 20))
	cl := &Client{config: &ClientConfig{NoUpload: true}}
	t := &Torrent{cl: cl, networkingEnabled: true, requestStrategy: 3, chunkSize: 16 << 10,
		duplicateRequestTimeout: time.Hour, info: &metainfo.Info{PieceLength: 2 << 20, Length: length, Pieces: make([]byte, 20*pieces)},
		length: &length, pieces: make([]Piece, pieces), pendingRequests: make(map[request]int), lastRequested: make(map[request]*time.Timer)}
	cn := &connection{t: t, PeerChoked: true, Choked: true, peerSentHaveAll: true, PeerMaxRequests: 128}
	t.conns = map[*connection]struct{}{cn: {}}
	for i := range t.pieces {
		t.pieces[i] = Piece{t: t, index: pieceIndex(i)}
		t.pendingPieces.Set(i, i)
		cn.pieceRequestOrder.Set(i, i)
	}
	return cn
}

func stopChokeFixture(cn *connection) {
	cn.t.lastRequestedMu.Lock()
	defer cn.t.lastRequestedMu.Unlock()
	for _, timer := range cn.t.lastRequested {
		if timer != nil {
			timer.Stop()
		}
	}
}

func TestChokeGuardInterestAndNoRequests(t *testing.T) {
	for _, irrelevantFast := range []bool{false, true} {
		cn := chokeFixture(2)
		defer stopChokeFixture(cn)
		if irrelevantFast {
			cn.peerAllowedFast.Add(2)
		}
		var messages []pp.Message
		cn.fillWriteBuffer(func(msg pp.Message) bool { messages = append(messages, msg); return true })
		if !cn.Interested || len(cn.requests) != 0 {
			t.Fatal("interest or choke request invariant violated")
		}
		if len(messages) != 1 || messages[0].Type != pp.Interested {
			t.Fatalf("unexpected messages: %v", messages)
		}
		messages = nil
		cn.fillWriteBuffer(func(msg pp.Message) bool { messages = append(messages, msg); return true })
		if len(messages) != 0 {
			t.Fatal("repeated unnecessary interest message")
		}
		cn.pieceRequestOrder.Clear()
		cn.fillWriteBuffer(func(msg pp.Message) bool { messages = append(messages, msg); return true })
		if cn.Interested || len(messages) != 1 || messages[0].Type != pp.NotInterested {
			t.Fatal("interest did not clear")
		}
	}
}

func TestChokeGuardAllowedFast(t *testing.T) {
	cn := chokeFixture(2)
	defer stopChokeFixture(cn)
	cn.peerAllowedFast.Add(1)
	cn.fillWriteBuffer(func(msg pp.Message) bool {
		if msg.Type == pp.Request && msg.Index != 1 {
			t.Fatal("non-Allowed-Fast request while choked")
		}
		return true
	})
	if len(cn.requests) == 0 {
		t.Fatal("lost Allowed Fast exception")
	}
	for r := range cn.requests {
		if r.Index != 1 {
			t.Fatal("wrong Allowed Fast piece")
		}
	}
}

func TestChokeGuardUnchoke(t *testing.T) {
	cn := chokeFixture(2)
	defer stopChokeFixture(cn)
	cn.fillWriteBuffer(func(pp.Message) bool { return true })
	cn.PeerChoked = false
	cn.fillWriteBuffer(func(pp.Message) bool { return true })
	if len(cn.requests) == 0 {
		t.Fatal("download did not resume after unchoke")
	}
}

func TestChokeGuardFullMessageBuffer(t *testing.T) {
	cn := chokeFixture(2)
	defer stopChokeFixture(cn)
	calls := 0
	cn.fillWriteBuffer(func(pp.Message) bool { calls++; return false })
	if calls != 1 || len(cn.requests) != 0 {
		t.Fatal("continued after exhausted message buffer")
	}
}

func TestChokeGuardPreservesUploadHandling(t *testing.T) {
	for _, noUpload := range []bool{false, true} {
		cn := chokeFixture(2)
		defer stopChokeFixture(cn)
		cn.t.cl.config.NoUpload = noUpload
		cn.Choked = !noUpload
		var messages []pp.Message
		cn.fillWriteBuffer(func(m pp.Message) bool { messages = append(messages, m); return true })
		want := pp.Unchoke
		if noUpload {
			want = pp.Choke
		}
		if len(messages) != 2 || messages[0].Type != pp.Interested || messages[1].Type != want {
			t.Fatalf("NoUpload=%v: upload handling skipped: %v", noUpload, messages)
		}
		if cn.Choked != noUpload || len(cn.requests) != 0 {
			t.Fatal("upload choke state or download requests changed incorrectly")
		}
	}
}

func TestChokeGuardNetworkingDisabled(t *testing.T) {
	cn := chokeFixture(2)
	defer stopChokeFixture(cn)
	cn.Interested = true
	cn.t.networkingEnabled = false
	var messages []pp.Message
	cn.fillWriteBuffer(func(m pp.Message) bool { messages = append(messages, m); return true })
	if cn.Interested || len(cn.requests) != 0 || len(messages) != 1 || messages[0].Type != pp.NotInterested {
		t.Fatalf("disabled networking changed: %v", messages)
	}
}

func TestChokeGuardUnchokedMatchesOriginal(t *testing.T) {
	original, optimized := chokeFixture(2), chokeFixture(2)
	defer stopChokeFixture(original)
	defer stopChokeFixture(optimized)
	original.PeerChoked = false
	optimized.PeerChoked = false
	var a, b []pp.Message
	original.fillWriteBufferBeforeChokeOptimization(func(m pp.Message) bool { a = append(a, m); return true })
	optimized.fillWriteBuffer(func(m pp.Message) bool { b = append(b, m); return true })
	if len(a) != len(b) {
		t.Fatal("message count differs")
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].Index != b[i].Index || a[i].Begin != b[i].Begin || a[i].Length != b[i].Length {
			t.Fatal("unchoked request sequence changed")
		}
	}
}

func BenchmarkChokeGuard(b *testing.B) {
	for _, original := range []bool{true, false} {
		name := "Optimized"
		if original {
			name = "Original"
		}
		b.Run(name, func(b *testing.B) {
			cn := chokeFixture(512)
			defer stopChokeFixture(cn)
			for p := pieceIndex(0); p < 512; p++ {
				for i := pp.Integer(0); i < 128; i++ {
					cn.t.lastRequested[request{pp.Integer(p), cn.t.chunkIndexSpec(i, p)}] = nil
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if original {
					cn.fillWriteBufferBeforeChokeOptimization(func(pp.Message) bool { return true })
				} else {
					cn.fillWriteBuffer(func(pp.Message) bool { return true })
				}
			}
		})
	}
}
