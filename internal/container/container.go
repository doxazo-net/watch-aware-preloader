// Package container locates the byte regions a media player reads when opening
// and seeking a file, so the preloader can warm exactly those regions instead
// of guessing a fixed tail size. It parses only the front of the file (the
// Matroska SeekHead), so a spun-down disk is touched only at the front.
package container

import (
	"io"
	"os"
)

// frontReadCap bounds how much of the file front is read to locate the
// SeekHead, Tracks/Info, and the first Cluster.
const frontReadCap = 1 << 20

// Matroska/EBML element IDs (with their leading length-marker bits).
const (
	idEBML         = 0x1A45DFA3
	idSegment      = 0x18538067
	idSeekHead     = 0x114D9B74
	idSeek         = 0x4DBB
	idSeekID       = 0x53AB
	idSeekPosition = 0x53AC
	idCues         = 0x1C53BB6B
	idCluster      = 0x1F43B675
)

// Layout describes a file's read-critical regions, in absolute host bytes.
type Layout struct {
	FrontEnd int64 // end of front metadata; [0, FrontEnd) is read on open
	CueStart int64 // start of the seek index; [CueStart, size) is read to seek
}

// Inspect parses the front of the file to locate its read-critical regions.
// ok is false when the format is unrecognized, Cues is not listed in the front
// SeekHead, or the resolved CueStart falls outside [0, size) (a bogus or linked
// segment pointer); callers then fall back to flat tail sizing.
func Inspect(path string, size int64) (Layout, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Layout{}, false
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, frontReadCap)
	n, err := io.ReadFull(f, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		buf = buf[:n] // file smaller than frontReadCap
	} else if err != nil {
		return Layout{}, false
	}

	pos := 0
	// EBML header: verify, then skip its body.
	if id, adv, ok := readVint(buf, pos, true); !ok || id != idEBML {
		return Layout{}, false
	} else {
		pos += adv
	}
	sz, adv, ok := readVint(buf, pos, false)
	if !ok {
		return Layout{}, false
	}
	pos += adv + int(sz)
	// Segment header: record where its data starts (SeekPositions are relative).
	if pos >= len(buf) {
		return Layout{}, false
	}
	if id, adv2, ok := readVint(buf, pos, true); !ok || id != idSegment {
		return Layout{}, false
	} else {
		pos += adv2
	}
	if _, adv2, ok := readVint(buf, pos, false); !ok {
		return Layout{}, false
	} else {
		pos += adv2
	}
	segDataStart := int64(pos)

	// Walk top-level Segment children: capture the first SeekHead's entries and
	// the first Cluster offset, then stop.
	seek := map[int64]int64{}
	firstCluster := int64(-1)
	scan := pos
	for i := 0; i < 64 && scan < len(buf); i++ {
		cid, cn, ok := readVint(buf, scan, true)
		if !ok {
			break
		}
		csz, cn2, ok := readVint(buf, scan+cn, false)
		if !ok {
			break
		}
		body := scan + cn + cn2
		if cid == idCluster {
			firstCluster = int64(scan)
			break
		}
		if cid == idSeekHead && len(seek) == 0 {
			parseSeekHead(buf, body, body+int(csz), seek)
		}
		scan = body + int(csz)
	}

	cuesPos, ok := seek[idCues]
	if !ok {
		return Layout{}, false
	}
	cueStart := segDataStart + cuesPos
	if cueStart < 0 || cueStart >= size {
		return Layout{}, false
	}
	frontEnd := firstCluster
	if frontEnd < 0 {
		frontEnd = frontReadCap // not found in the front window; bounded fallback
	}
	if frontEnd > size {
		frontEnd = size
	}
	return Layout{FrontEnd: frontEnd, CueStart: cueStart}, true
}

// parseSeekHead reads Seek entries in buf[start:end], filling seek with
// elementID -> SeekPosition (first occurrence wins).
func parseSeekHead(buf []byte, start, end int, seek map[int64]int64) {
	if end > len(buf) {
		end = len(buf)
	}
	sp := start
	curID := int64(-1)
	for sp < end {
		id, n, ok := readVint(buf, sp, true)
		if !ok {
			return
		}
		sz, n2, ok := readVint(buf, sp+n, false)
		if !ok {
			return
		}
		body := sp + n + n2
		if id == idSeek { // master element: descend into it
			sp = body
			continue
		}
		if id == idSeekID {
			curID = beUint(buf, body, int(sz))
		} else if id == idSeekPosition {
			if curID >= 0 {
				if _, dup := seek[curID]; !dup {
					seek[curID] = beUint(buf, body, int(sz))
				}
				curID = -1
			}
		}
		sp = body + int(sz)
	}
}

// beUint reads n big-endian bytes at buf[off:], clamped to the buffer.
func beUint(buf []byte, off, n int) int64 {
	var v int64
	for i := 0; i < n && off+i < len(buf); i++ {
		v = v<<8 | int64(buf[off+i])
	}
	return v
}

// readVint reads an EBML variable-length integer at buf[pos]. With keepMarker
// true it returns the element ID (marker bits retained); false clears the
// length marker for a size/value. ok is false on a malformed or truncated vint.
func readVint(buf []byte, pos int, keepMarker bool) (val int64, n int, ok bool) {
	if pos < 0 || pos >= len(buf) {
		return 0, 0, false
	}
	first := buf[pos]
	if first == 0 {
		return 0, 0, false
	}
	length := 1
	mask := byte(0x80)
	for mask != 0 && first&mask == 0 {
		mask >>= 1
		length++
	}
	if length > 8 || pos+length > len(buf) {
		return 0, 0, false
	}
	if keepMarker {
		val = int64(first)
	} else {
		val = int64(first & (mask - 1))
	}
	for i := 1; i < length; i++ {
		val = val<<8 | int64(buf[pos+i])
	}
	return val, length, true
}
