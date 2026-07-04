package container

import (
	"os"
	"path/filepath"
	"testing"
)

// --- synthetic MKV byte builders (front-of-file only) ---

func vintID(id uint32) []byte {
	switch {
	case id <= 0xFF:
		return []byte{byte(id)}
	case id <= 0xFFFF:
		return []byte{byte(id >> 8), byte(id)}
	case id <= 0xFFFFFF:
		return []byte{byte(id >> 16), byte(id >> 8), byte(id)}
	default:
		return []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	}
}

// vintSize encodes n in the 1-byte EBML size form (valid for n <= 126).
func vintSize(n int) []byte { return []byte{0x80 | byte(n)} }

func elem(id uint32, payload []byte) []byte {
	b := vintID(id)
	b = append(b, vintSize(len(payload))...)
	return append(b, payload...)
}

// uintBytes is a minimal big-endian encoding (>=1 byte).
func uintBytes(v uint64) []byte {
	if v == 0 {
		return []byte{0}
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte(v)}, b...)
		v >>= 8
	}
	return b
}

func seekEntry(targetID uint32, pos uint64) []byte {
	body := elem(0x53AB, vintID(targetID))               // SeekID -> target element's ID bytes
	body = append(body, elem(0x53AC, uintBytes(pos))...) // SeekPosition
	return elem(0x4DBB, body)                            // Seek
}

// writeMKV builds a front-of-file MKV with a SeekHead pointing Cues at
// cuesPos (relative to segment data start) and a Cluster right after the
// SeekHead. It returns the path plus the expected FrontEnd and CueStart.
func writeMKV(t *testing.T, cuesPos uint64) (path string, wantFront, wantCue int64) {
	t.Helper()
	ebml := elem(0x1A45DFA3, []byte{0x01})                       // EBML header; body content irrelevant (skipped by size)
	seekHead := elem(0x114D9B74, seekEntry(0x1C53BB6B, cuesPos)) // Cues
	cluster := elem(0x1F43B675, []byte{0x00, 0x00})              // Cluster; body not parsed
	segBody := append(append([]byte{}, seekHead...), cluster...)
	segHeader := append(vintID(0x18538067), vintSize(len(segBody))...)
	file := append(append([]byte{}, ebml...), segHeader...)
	segDataStart := int64(len(file)) // first byte after the segment size
	file = append(file, segBody...)
	firstCluster := segDataStart + int64(len(seekHead))
	dir := t.TempDir()
	p := filepath.Join(dir, "sample.mkv")
	if err := os.WriteFile(p, file, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, firstCluster, segDataStart + int64(cuesPos)
}

func TestInspectLocatesCuesAndFront(t *testing.T) {
	const size = int64(20 << 30) // pretend a 20 GB file; cuesPos is well within it
	p, wantFront, wantCue := writeMKV(t, 19_000_000_000)
	got, ok := Inspect(p, size)
	if !ok {
		t.Fatal("Inspect ok=false, want true")
	}
	if got.FrontEnd != wantFront {
		t.Errorf("FrontEnd = %d, want %d", got.FrontEnd, wantFront)
	}
	if got.CueStart != wantCue {
		t.Errorf("CueStart = %d, want %d", got.CueStart, wantCue)
	}
}

func TestInspectRejectsNonEBML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.mkv")
	if err := os.WriteFile(p, []byte("not an mkv file at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Inspect(p, 1<<30); ok {
		t.Error("Inspect ok=true on non-EBML input, want false")
	}
}

func TestInspectRejectsCueBeyondEOF(t *testing.T) {
	// cuesPos resolves to an absolute offset past the (small) declared size:
	// a bogus/linked-segment pointer must yield ok=false, not a huge tail.
	p, _, wantCue := writeMKV(t, 5_000_000_000)
	if _, ok := Inspect(p, 100_000); ok {
		t.Errorf("Inspect ok=true with CueStart %d >= size 100000, want false", wantCue)
	}
}

func TestInspectMissingFile(t *testing.T) {
	if _, ok := Inspect("/no/such/file.mkv", 1<<30); ok {
		t.Error("Inspect ok=true on missing file, want false")
	}
}
