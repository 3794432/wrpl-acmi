/*
	wrpl: War Thunder replay parsing library (golang)
	Copyright (C) 2025 flexcoral

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU Affero General Public License as published
	by the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.
*/

package wrpl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
)

const (
	maxReplayDirectoryParts = 256
	maxReplayResultsSize    = 16 << 20
	replayPacketEndMarker   = 0
	replayPacketNextSegment = 5
)

// ReplayDirectoryInfo describes the server replay parts combined by
// OpenReplayDirectory. PartNumbers and Paths are in packet-stream order.
type ReplayDirectoryInfo struct {
	SessionID        uint64
	PartNumbers      []byte
	Paths            []string
	Incomplete       bool
	CompletenessNote string
}

// ReplayDirectoryOptions controls optional server replay directory behavior.
type ReplayDirectoryOptions struct {
	// AllowIncomplete permits a replay whose final part lacks EndMarker. All
	// structural, compression, and packet-framing validation remains strict.
	AllowIncomplete bool
}

type replayDirectoryPart struct {
	path       string
	header     WRPLHeader
	size       int64
	settings   []byte
	lastPacket byte
	sawEnd     bool
}

// OpenReplayDirectory opens a server replay split across top-level .wrpl
// files. It validates the complete set before returning so a returned replay
// always has a coherent, complete packet stream.
func OpenReplayDirectory(dir string) (*ReplayReader, *ReplayDirectoryInfo, error) {
	return OpenReplayDirectoryWithOptions(dir, ReplayDirectoryOptions{})
}

// OpenReplayDirectoryWithOptions opens a server replay directory with explicit
// handling for an incomplete tail.
func OpenReplayDirectoryWithOptions(dir string, opts ReplayDirectoryOptions) (*ReplayReader, *ReplayDirectoryInfo, error) {
	files, err := replayDirectoryFiles(dir)
	if err != nil {
		return nil, nil, err
	}

	parts := make([]replayDirectoryPart, 0, len(files))
	for _, path := range files {
		part, err := inspectReplayDirectoryPart(path)
		if err != nil {
			return nil, nil, fmt.Errorf("inspecting replay part %q: %w", path, err)
		}
		if part.header.SessionID == 0 {
			return nil, nil, fmt.Errorf("replay part %q has session ID 0", path)
		}
		if !part.header.IsServer() {
			return nil, nil, fmt.Errorf("replay part %q is a client replay, not a server replay", path)
		}
		parts = append(parts, part)
	}

	sort.Slice(parts, func(i, j int) bool {
		if parts[i].header.ReplayPartNumber != parts[j].header.ReplayPartNumber {
			return parts[i].header.ReplayPartNumber < parts[j].header.ReplayPartNumber
		}
		return parts[i].path < parts[j].path
	})
	incomplete, completenessNote, err := validateReplayDirectoryParts(parts, opts.AllowIncomplete)
	if err != nil {
		return nil, nil, err
	}

	last := parts[len(parts)-1]
	results, err := readReplayDirectoryResults(last)
	if err != nil {
		return nil, nil, fmt.Errorf("reading results from replay part %q: %w", last.path, err)
	}

	partNumbers := make([]byte, len(parts))
	paths := make([]string, len(parts))
	streamParts := make([]replayDirectoryPart, len(parts))
	for i := range parts {
		partNumbers[i] = parts[i].header.ReplayPartNumber
		paths[i] = parts[i].path
		streamParts[i] = parts[i]
		streamParts[i].settings = nil
	}

	ret := &ReplayReader{
		Header:       parts[0].header,
		Settings:     append([]byte(nil), parts[0].settings...),
		Results:      results,
		PacketStream: &replayDirectoryStream{parts: streamParts},
	}
	info := &ReplayDirectoryInfo{
		SessionID:        parts[0].header.SessionID,
		PartNumbers:      partNumbers,
		Paths:            paths,
		Incomplete:       incomplete,
		CompletenessNote: completenessNote,
	}
	return ret, info, nil
}

func replayDirectoryFiles(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("opening replay directory %q: %w", dir, err)
	}
	defer f.Close()

	var paths []string
	for {
		entries, err := f.ReadDir(64)
		for _, entry := range entries {
			name := entry.Name()
			if !strings.EqualFold(filepath.Ext(name), ".wrpl") {
				continue
			}
			if len(paths) >= maxReplayDirectoryParts {
				return nil, fmt.Errorf("replay directory %q contains more than %d .wrpl files", dir, maxReplayDirectoryParts)
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("replay entry %q is a symbolic link", filepath.Join(dir, name))
			}
			if !entry.Type().IsRegular() {
				return nil, fmt.Errorf("replay entry %q is not a regular file", filepath.Join(dir, name))
			}
			paths = append(paths, filepath.Join(dir, name))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("reading replay directory %q: %w", dir, err)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("replay directory %q contains no .wrpl files", dir)
	}
	return paths, nil
}

func inspectReplayDirectoryPart(path string) (replayDirectoryPart, error) {
	var part replayDirectoryPart
	f, err := os.Open(path)
	if err != nil {
		return part, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return part, fmt.Errorf("stating replay part: %w", err)
	}
	if !info.Mode().IsRegular() {
		return part, errors.New("not a regular file")
	}

	rpl, err := OpenReplay(f, true, true, false)
	if err != nil {
		if rpl != nil {
			_ = rpl.Close()
		}
		return part, err
	}
	if rpl == nil || rpl.PacketStream == nil {
		if rpl != nil {
			_ = rpl.Close()
		}
		return part, errors.New("replay has no packet stream")
	}
	defer rpl.Close()

	resultsOffset := int64(rpl.Header.ResultsBlkOffset)
	if resultsOffset < 0 {
		return part, fmt.Errorf("invalid negative results offset %d", resultsOffset)
	}
	if resultsOffset > 0 {
		if resultsOffset < int64(replayHeaderSize) || resultsOffset > info.Size() {
			return part, fmt.Errorf("results offset %d is outside file size %d", resultsOffset, info.Size())
		}
	}

	lastPacket, sawEnd, err := scanReplayPackets(rpl.PacketStream)
	if err != nil {
		return part, err
	}
	part.path = path
	part.header = rpl.Header
	part.size = info.Size()
	if rpl.Header.ReplayPartNumber == 0 {
		part.settings = append([]byte(nil), rpl.Settings...)
	}
	part.lastPacket = lastPacket
	part.sawEnd = sawEnd
	return part, nil
}

func validateReplayDirectoryParts(parts []replayDirectoryPart, allowIncomplete bool) (bool, string, error) {
	if len(parts) == 0 {
		return false, "", errors.New("server replay directory has no parts")
	}
	if len(parts) > maxReplayDirectoryParts {
		return false, "", fmt.Errorf("server replay has more than %d parts", maxReplayDirectoryParts)
	}

	first := parts[0]
	if first.header.SessionID == 0 {
		return false, "", errors.New("server replay has session ID 0")
	}
	if !first.header.IsServer() {
		return false, "", fmt.Errorf("replay part %q is a client replay", first.path)
	}

	partsByNumber := make(map[byte]string, len(parts))
	for i, part := range parts {
		if part.header.SessionID != first.header.SessionID {
			return false, "", fmt.Errorf("replay set mixes sessions %016x and %016x (%q)", first.header.SessionID, part.header.SessionID, part.path)
		}
		if part.header.Version != first.header.Version {
			return false, "", fmt.Errorf("replay set mixes versions %#x and %#x (%q)", first.header.Version, part.header.Version, part.path)
		}
		if !bytes.Equal(part.header.Raw_Level[:], first.header.Raw_Level[:]) {
			return false, "", fmt.Errorf("replay set mixes levels in %q", part.path)
		}
		if !part.header.IsServer() {
			return false, "", fmt.Errorf("replay part %q is a client replay", part.path)
		}
		if previous, ok := partsByNumber[part.header.ReplayPartNumber]; ok {
			return false, "", fmt.Errorf("duplicate replay part %d in %q and %q", part.header.ReplayPartNumber, previous, part.path)
		}
		partsByNumber[part.header.ReplayPartNumber] = part.path

		if i < len(parts)-1 && part.sawEnd {
			return false, "", fmt.Errorf("replay part %d contains an end marker but more parts follow", part.header.ReplayPartNumber)
		}
	}

	if _, ok := partsByNumber[0]; !ok {
		return false, "", errors.New("server replay set has no part 0")
	}
	if err := validateReplayPartNumbers(parts); err != nil {
		return false, "", err
	}

	incomplete := false
	completenessNote := ""
	for i, part := range parts {
		isLast := i == len(parts)-1
		if !isLast && part.lastPacket != replayPacketNextSegment {
			return false, "", fmt.Errorf("replay part %d ends with packet type %d, expected NextSegment (%d)", part.header.ReplayPartNumber, part.lastPacket, replayPacketNextSegment)
		}
		if isLast {
			if part.sawEnd && part.lastPacket == replayPacketEndMarker {
				continue
			}
			if allowIncomplete {
				incomplete = true
				lastPacket := fmt.Sprintf("packet type %d", part.lastPacket)
				if part.lastPacket == replayPacketNextSegment {
					lastPacket = fmt.Sprintf("NextSegment(%d)", replayPacketNextSegment)
				}
				completenessNote = fmt.Sprintf("incomplete: final replay part %d has no EndMarker and ends with %s; the replay tail may be missing", part.header.ReplayPartNumber, lastPacket)
				continue
			}
			if part.lastPacket == replayPacketNextSegment {
				return false, "", fmt.Errorf("replay part %d ends with NextSegment; the final replay part is missing", part.header.ReplayPartNumber)
			}
			return false, "", fmt.Errorf("replay part %d does not end with EndMarker (%d)", part.header.ReplayPartNumber, replayPacketEndMarker)
		}
	}
	return incomplete, completenessNote, nil
}

func validateReplayPartNumbers(parts []replayDirectoryPart) error {
	// Odd parts form the main sequence. Even parts are optional snapshots and
	// may be absent, as they are in older server replay sets. A snapshot may
	// trail the last main part, but cannot skip a main part to reach a later
	// part number.
	nextOdd := 1
	maxOdd := 0
	oddSequenceValid := true
	for _, part := range parts {
		number := int(part.header.ReplayPartNumber)
		if number == 0 {
			continue
		}
		if number%2 == 1 {
			if number != nextOdd {
				oddSequenceValid = false
			}
			nextOdd = number + 2
			maxOdd = number
		}
	}
	if oddSequenceValid {
		allPartsValid := true
		for _, part := range parts {
			number := int(part.header.ReplayPartNumber)
			if number > 0 && number%2 == 0 && number > maxOdd+1 {
				allPartsValid = false
			}
		}
		if allPartsValid {
			return nil
		}
	}
	if parts[0].header.ReplayPartNumber != 0 {
		return fmt.Errorf("server replay starts with part %d, expected 0", parts[0].header.ReplayPartNumber)
	}

	var numbers []string
	for _, part := range parts {
		numbers = append(numbers, fmt.Sprintf("%d", part.header.ReplayPartNumber))
	}
	return fmt.Errorf("replay part numbers have a gap in the main sequence: %s", strings.Join(numbers, ","))
}

func scanReplayPackets(r io.Reader) (lastPacket byte, sawEnd bool, err error) {
	br := bufio.NewReaderSize(r, 32<<10)
	foundPacket := false
	for {
		packetSize, readErr := packet.ReadVariableLengthSize(br)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if !foundPacket {
					return 0, false, errors.New("packet stream is empty")
				}
				return lastPacket, sawEnd, nil
			}
			return 0, false, fmt.Errorf("reading packet size: %w", readErr)
		}
		if packetSize == 0 {
			continue
		}
		if packetSize < 2 {
			return 0, false, fmt.Errorf("packet size %d is smaller than the packet header", packetSize)
		}
		if foundPacket && sawEnd {
			return 0, false, errors.New("packet follows EndMarker")
		}

		var header [2]byte
		if _, err := io.ReadFull(br, header[:]); err != nil {
			return 0, false, fmt.Errorf("reading packet header: %w", err)
		}
		packetType := header[0]
		headerSize := uint32(2)
		if header[0]&0b00010000 != 0 {
			packetType ^= 0b00010000
		} else {
			headerSize = 6
			if packetSize < headerSize {
				return 0, false, fmt.Errorf("packet size %d is smaller than a timestamped packet header", packetSize)
			}
		}
		payloadSize := int64(packetSize - headerSize)
		if payloadSize > packet.MaxPacketPayloadSize {
			return 0, false, fmt.Errorf("packet payload size %d exceeds limit %d", payloadSize, packet.MaxPacketPayloadSize)
		}
		// The two header bytes have already been consumed. Skip the timestamp,
		// when present, together with the payload.
		remainingBody := int64(packetSize - 2)
		if _, err := io.CopyN(io.Discard, br, remainingBody); err != nil {
			return 0, false, fmt.Errorf("reading packet payload: %w", err)
		}
		lastPacket = packetType
		foundPacket = true
		if packetType == replayPacketEndMarker {
			sawEnd = true
		}
	}
}

func readReplayDirectoryResults(part replayDirectoryPart) ([]byte, error) {
	f, err := os.Open(part.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != part.size {
		return nil, fmt.Errorf("replay file size changed from %d to %d", part.size, info.Size())
	}
	rpl, err := OpenReplay(f, false, false, false)
	if err != nil {
		return nil, err
	}
	if !sameReplayDirectoryHeader(rpl.Header, part.header) {
		return nil, errors.New("replay header changed after validation")
	}
	offset := int64(rpl.Header.ResultsBlkOffset)
	if offset == 0 {
		return nil, nil
	}
	if offset < int64(replayHeaderSize) || offset > info.Size() {
		return nil, fmt.Errorf("results offset %d is outside file size %d", offset, info.Size())
	}
	size := info.Size() - offset
	if size > maxReplayResultsSize {
		return nil, fmt.Errorf("results block is %d bytes, maximum is %d", size, maxReplayResultsSize)
	}
	buf := make([]byte, int(size))
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seeking to results: %w", err)
	}
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, fmt.Errorf("reading results: %w", err)
	}
	return buf, nil
}

func sameReplayDirectoryHeader(a, b WRPLHeader) bool {
	return a.Magic == b.Magic &&
		a.Version == b.Version &&
		a.SessionID == b.SessionID &&
		a.ReplayPartNumber == b.ReplayPartNumber &&
		bytes.Equal(a.Raw_Level[:], b.Raw_Level[:])
}

type replayDirectoryStream struct {
	mu      sync.Mutex
	parts   []replayDirectoryPart
	next    int
	current io.ReadCloser
	file    *os.File
	failed  error
	closed  bool
}

func (s *replayDirectoryStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, io.ErrClosedPipe
	}
	if len(p) == 0 {
		return 0, nil
	}
	if s.failed != nil {
		return 0, s.failed
	}

	for {
		if s.current == nil {
			if s.next >= len(s.parts) {
				return 0, io.EOF
			}
			if err := s.openNext(); err != nil {
				s.failed = err
				return 0, err
			}
		}

		n, err := s.current.Read(p)
		if err == io.EOF {
			closeErr := s.closeCurrent()
			if n > 0 {
				if closeErr != nil {
					s.failed = closeErr
				}
				return n, nil
			}
			if closeErr != nil {
				s.failed = closeErr
				return 0, closeErr
			}
			continue
		}
		if err != nil {
			s.failed = err
			_ = s.closeCurrent()
			return n, err
		}
		if n > 0 {
			return n, nil
		}
	}
}

func (s *replayDirectoryStream) openNext() error {
	part := s.parts[s.next]
	s.next++

	f, err := os.Open(part.path)
	if err != nil {
		return fmt.Errorf("opening replay part %q: %w", part.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stating replay part %q: %w", part.path, err)
	}
	if info.Size() != part.size {
		_ = f.Close()
		return fmt.Errorf("replay part %q size changed from %d to %d", part.path, part.size, info.Size())
	}

	rpl, err := OpenReplay(f, false, true, false)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("opening replay part %q: %w", part.path, err)
	}
	if rpl == nil || rpl.PacketStream == nil {
		if rpl != nil {
			_ = rpl.Close()
		}
		_ = f.Close()
		return fmt.Errorf("replay part %q has no packet stream", part.path)
	}
	if !sameReplayDirectoryHeader(rpl.Header, part.header) {
		_ = rpl.Close()
		_ = f.Close()
		return fmt.Errorf("replay part %q header changed after validation", part.path)
	}
	s.current = rpl.PacketStream
	s.file = f
	return nil
}

func (s *replayDirectoryStream) closeCurrent() error {
	var errs []error
	if s.current != nil {
		if err := s.current.Close(); err != nil {
			errs = append(errs, err)
		}
		s.current = nil
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil {
			errs = append(errs, err)
		}
		s.file = nil
	}
	return errors.Join(errs...)
}

func (s *replayDirectoryStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.parts = nil
	return s.closeCurrent()
}
