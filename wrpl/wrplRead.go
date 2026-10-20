/*
	wrpl: War Thunder replay parsing library (golang)
	Copyright (C) 2025 flexcoral

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU Affero General Public License as published
	by the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU Affero General Public License for more details.

	You should have received a copy of the GNU Affero General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package wrpl

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/klauspost/compress/zlib"
	"github.com/klauspost/compress/zstd"
)

const (
	replayHeaderSize       = 1234
	replayVersionZstd      = 0x00018C1C
	newSettingsSizeOffset  = 748
	newResultsOffsetOffset = 684
)

type zstdReadCloser struct {
	*zstd.Decoder
}

func (z zstdReadCloser) Close() error {
	z.Decoder.Close()
	return nil
}

// type Player struct {
// 	Name    string
// 	ClanTag string
// 	UserID  uint32
// 	Title   string
// }

// type EntityPosition struct {
// 	Eid     uint64
// 	Time    uint32
// 	X, Y, Z float64
// }

type ReplayReader struct {
	Header   WRPLHeader
	Settings []byte
	// **decompressed** packet stream, needs to be closed
	PacketStream io.ReadCloser
	Results      []byte
}

func (rpl *ReplayReader) Close() error {
	if rpl.PacketStream != nil {
		return rpl.PacketStream.Close()
	}
	return nil
}

// func ReadPartedWRPLFolder(folderPath string) (ret *WRPL, err error) {
// 	rplsDir, err := os.ReadDir(folderPath)
// 	if err != nil {
// 		return nil, err
// 	}
// 	parts := [][]byte{}
// 	for _, v := range rplsDir {
// 		if v.IsDir() {
// 			continue
// 		}
// 		if !strings.HasSuffix(v.Name(), ".wrpl") {
// 			continue
// 		}
// 		part, err := os.ReadFile(filepath.Join(folderPath, v.Name()))
// 		if err != nil {
// 			return nil, err
// 		}
// 		parts = append(parts, part)
// 	}
// 	return ReadPartedWRPL(parts)
// }

func OpenPartedReplay(replayBytes [][]byte) (ret *ReplayReader, err error) {
	if len(replayBytes) == 0 {
		return nil, nil
	}
	parts := map[int]*ReplayReader{}
	var sessionID uint64
	for i, b := range replayBytes {
		rpl, err := OpenReplay(bytes.NewReader(b), true, true, true)
		if err != nil {
			return nil, fmt.Errorf("parsing replay part file %d: %w", i, err)
		}
		if i == 0 {
			sessionID = rpl.Header.SessionID
		} else {
			if sessionID != rpl.Header.SessionID {
				return nil, fmt.Errorf("multiple sessions %016x and %016x at file %d", sessionID, rpl.Header.SessionID, i)
			}
		}
		if rpl.Header.IsServer() {
			parts[int(rpl.Header.ReplayPartNumber)] = rpl
		} else {
			rpl.Close()
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("no server-side replays found in the set")
	}
	keys := slices.Collect(maps.Keys(parts))
	slices.Sort(keys)
	if !slices.Contains(keys, 0) {
		return nil, errors.New("no replay part 0 found")
	}
	prevState := -1
	// 0  1  3  5  7  9...
	for _, v := range keys {
		if v%2 == 1 {
			if prevState+2 != v {
				return nil, fmt.Errorf("found orderd part %d but previous was %d", v, prevState)
			} else {
				prevState = v
			}
		}
	}
	streams := []io.ReadCloser{}
	for _, k := range keys {
		streams = append(streams, parts[k].PacketStream)
	}
	ret = &ReplayReader{
		Header:       parts[0].Header,
		Settings:     parts[0].Settings,
		Results:      parts[keys[len(keys)-1]].Results,
		PacketStream: MultiReadCloser(streams...),
	}
	return
}

// OpenReplay reads header and conditionally settings, packets and resuls blobs
//
// If openPackets is true then Replay will have zlib-backed io that needs to be closed via Close
func OpenReplay(r io.ReadSeeker, readSettings, openPackets, readResults bool) (ret *ReplayReader, err error) {
	ret = &ReplayReader{}
	headerBytes := make([]byte, replayHeaderSize)
	if _, err = io.ReadFull(r, headerBytes); err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}
	err = binary.Read(bytes.NewReader(headerBytes), binary.LittleEndian, &ret.Header)
	if err != nil {
		return nil, fmt.Errorf("parsing header: %w", err)
	}
	if !bytes.Equal(ret.Header.Magic[:], []byte{0xe5, 0xac, 0x00, 0x10}) {
		return nil, fmt.Errorf("wrong magic (got %v)", ret.Header.Magic)
	}

	// Version 0x00018C1C moved some header fields and switched the packet
	// stream to zstd. Keep the old layout for older replays.
	settingsSize := ret.Header.SettingsBLKSize
	resultsOffset := ret.Header.ResultsBlkOffset
	if ret.Header.Version == replayVersionZstd {
		settingsSize = binary.LittleEndian.Uint16(headerBytes[newSettingsSizeOffset : newSettingsSizeOffset+2])
		resultsOffset = int32(binary.LittleEndian.Uint32(headerBytes[newResultsOffsetOffset : newResultsOffsetOffset+4]))
		ret.Header.SettingsBLKSize = settingsSize
		ret.Header.ResultsBlkOffset = resultsOffset
	}
	settingsOffset := int64(replayHeaderSize)
	if ret.Header.Version != replayVersionZstd {
		// Legacy replays place the settings block directly after the encoded
		// header struct (binary.Size), while the full header reserves 8 more bytes.
		settingsOffset = int64(binary.Size(ret.Header))
	}

	if settingsSize > 0 && readSettings {
		if _, err := r.Seek(settingsOffset, io.SeekStart); err != nil {
			return ret, fmt.Errorf("seeking for settings blk: %w", err)
		}
		ret.Settings = make([]byte, settingsSize)
		_, err := io.ReadFull(r, ret.Settings)
		if err != nil {
			return ret, fmt.Errorf("reading settings blk: %w", err)
		}
	}

	if resultsOffset > 0 && readResults {
		_, err := r.Seek(int64(resultsOffset), io.SeekStart)
		if err != nil {
			return ret, fmt.Errorf("seeking for results blk")
		}
		ret.Results, err = io.ReadAll(r)
		if err != nil {
			return ret, fmt.Errorf("reading results blk: %w", err)
		}
	}

	if openPackets {
		streamOffset := int64(replayHeaderSize) + int64(settingsSize)
		streamLength := int64(1 << 62)
		if endOffset, seekErr := r.Seek(0, io.SeekEnd); seekErr == nil && endOffset > streamOffset {
			streamLength = endOffset - streamOffset
			if resultsOffset > 0 && int64(resultsOffset) > streamOffset && int64(resultsOffset) <= endOffset {
				streamLength = int64(resultsOffset) - streamOffset
			}
		}

		var src io.Reader
		magic := make([]byte, 4)
		if ra, ok := r.(io.ReaderAt); ok {
			if _, err := ra.ReadAt(magic, streamOffset); err != nil {
				return ret, fmt.Errorf("reading packet stream magic: %w", err)
			}
			src = io.NewSectionReader(ra, streamOffset, streamLength)
		} else {
			if _, err := r.Seek(streamOffset, io.SeekStart); err != nil {
				return ret, fmt.Errorf("seeking for packets: %w", err)
			}
			if _, err := io.ReadFull(r, magic); err != nil {
				return ret, fmt.Errorf("reading packet stream magic: %w", err)
			}
			if _, err := r.Seek(streamOffset, io.SeekStart); err != nil {
				return ret, fmt.Errorf("rewinding packet stream: %w", err)
			}
			src = r
		}
		if bytes.Equal(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
			dec, err := zstd.NewReader(src)
			if err != nil {
				return ret, fmt.Errorf("opening zstd packets stream: %w", err)
			}
			ret.PacketStream = zstdReadCloser{dec}
		} else {
			ret.PacketStream, err = zlib.NewReader(src)
			if err != nil {
				return ret, fmt.Errorf("opening zlib packets stream: %w", err)
			}
		}
	}

	return
}
