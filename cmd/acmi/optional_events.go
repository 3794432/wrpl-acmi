package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

const (
	optionalEventRetention  = 1 << 16
	optionalMaxStringBytes  = 4 << 10
	optionalMaxFieldBits    = optionalMaxStringBytes * 8
	optionalMaxSlotBytes    = 4 << 20
	optionalMaxSlotMessages = 4 << 10
)

type optionalStoredEvent struct {
	t           uint32
	text        string
	playerSlot  int
	playerID    uint64
	playerName  string
	playerKnown bool
}

type optionalRosterEntry struct {
	known    bool
	playerID uint64
	name     string
	team     byte
}

type optionalRosterChange struct {
	t     uint32
	entry optionalRosterEntry
}

type optionalEventParser struct {
	enabled map[string]bool
	slot    *packetslot.PacketSlotParser

	events  []optionalStoredEvent
	Skipped uint64

	roster            [256]optionalRosterEntry
	rosterLog         [256][]optionalRosterChange
	rosterHistory     int
	haveFirstSlotTime bool
	firstSlotTime     uint32
}

// optionalEventRetention is deliberately finite: a malformed or adversarial
// replay must not turn optional bookmarks into an unbounded heap allocation.
func newOptionalEventParser(enabled map[string]bool, slot *packetslot.PacketSlotParser) *optionalEventParser {
	p := &optionalEventParser{enabled: make(map[string]bool, 4), slot: slot}
	for _, category := range []string{"chat", "join", "damage", "award"} {
		if enabled[category] {
			p.enabled[category] = true
		}
	}
	if slot != nil {
		for index, player := range slot.Players {
			if entry, ok := optionalEntryFromPlayer(player); ok {
				p.roster[index] = entry
				p.recordRosterChange(index, 0, entry)
			}
		}
	}
	return p
}

func (p *optionalEventParser) Name() string { return "optional-events" }

func (p *optionalEventParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	out := map[byte][][]packet.ParsingCondition{}
	if p.enabled["chat"] {
		out[3] = nil
	}
	if p.enabled["join"] {
		out[4] = append(out[4], [][]packet.ParsingCondition{{
			packet.NewParsingCondition(0, 0x02), packet.NewParsingCondition(1, 0x58),
			packet.NewParsingCondition(2, 0x2d), packet.NewParsingCondition(3, 0xf0),
		}}...)
	}
	if p.enabled["award"] {
		out[4] = append(out[4], [][]packet.ParsingCondition{{
			packet.NewParsingCondition(0, 0x02), packet.NewParsingCondition(1, 0x58),
			packet.NewParsingCondition(2, 0x78), packet.NewParsingCondition(3, 0xf0),
		}}...)
	}
	if p.enabled["damage"] {
		out[4] = append(out[4], [][]packet.ParsingCondition{
			{
				packet.NewParsingCondition(0, 0x02), packet.NewParsingCondition(1, 0x58),
				packet.NewParsingCondition(2, 0x56), packet.NewParsingCondition(3, 0xf0),
			},
			{
				packet.NewParsingCondition(0, 0x02), packet.NewParsingCondition(1, 0x58),
				packet.NewParsingCondition(2, 0x57), packet.NewParsingCondition(3, 0xf1),
			},
		}...)
	}
	return out
}

func (p *optionalEventParser) Parse(pk *packet.Packet) (any, error) {
	if pk == nil {
		return nil, nil
	}
	switch pk.PacketType {
	case 3:
		if !p.enabled["chat"] {
			return nil, nil
		}
		if err := p.parseChat(pk); err != nil {
			p.Skipped++
			return nil, err
		}
	case 4:
		if len(pk.PacketPayload) < 4 {
			return nil, nil
		}
		payload := pk.PacketPayload
		switch {
		case payload[0] == 0x02 && payload[1] == 0x58 && payload[2] == 0x2d && payload[3] == 0xf0:
			if !p.enabled["join"] {
				return nil, nil
			}
			if err := p.parseSlot(pk); err != nil {
				p.Skipped++
				return nil, err
			}
		case payload[0] == 0x02 && payload[1] == 0x58 && payload[2] == 0x78 && payload[3] == 0xf0:
			if !p.enabled["award"] {
				return nil, nil
			}
			if err := p.parseAward(pk); err != nil {
				p.Skipped++
				return nil, err
			}
		case p.enabled["damage"] && payload[0] == 0x02 && payload[1] == 0x58 &&
			((payload[2] == 0x56 && payload[3] == 0xf0) || (payload[2] == 0x57 && payload[3] == 0xf1)):
			if err := p.parseDamage(pk); err != nil {
				p.Skipped++
				return nil, err
			}
		}
	}
	return nil, nil
}

func (p *optionalEventParser) timeline(tracks []*track) []timelineEvent {
	type trackLink struct {
		track *track
		slot  int
	}
	links := make([]trackLink, 0, len(tracks))
	for _, tr := range tracks {
		if tr == nil || tr.entity == nil {
			continue
		}
		pid, ok := packetecs2.GetObjectData[int32](&tr.entity.Data, "unit__playerId")
		if !ok || pid < 0 || pid >= 256 {
			continue
		}
		links = append(links, trackLink{track: tr, slot: int(pid)})
	}

	out := make([]timelineEvent, 0, len(p.events))
	for _, stored := range p.events {
		text := cleanOptionalText(stored.text, optionalMaxStringBytes)
		if text == "" {
			continue
		}
		event := timelineEvent{t: stored.t, text: text}
		if stored.playerSlot >= 0 && stored.playerSlot < 256 {
			seen := map[trackKey]bool{}
			for _, link := range links {
				if link.slot != stored.playerSlot || !optionalTrackActiveAt(link.track, stored.t) {
					continue
				}
				entry, ok := p.optionalRosterAt(link.slot, stored.t)
				if !ok || !optionalSamePlayer(entry, optionalRosterEntry{
					known: stored.playerKnown, playerID: stored.playerID, name: stored.playerName,
				}) {
					continue
				}
				if !seen[link.track.key] {
					event.keys = append(event.keys, link.track.key)
					seen[link.track.key] = true
				}
			}
		}
		out = append(out, event)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].t != out[j].t {
			return out[i].t < out[j].t
		}
		return out[i].text < out[j].text
	})
	return out
}

func optionalTrackActiveAt(tr *track, t uint32) bool {
	if tr == nil || len(tr.samples) == 0 || t < tr.samples[0].t {
		return false
	}
	return !tr.hasEnd || t <= tr.end
}

func (p *optionalEventParser) optionalRosterAt(slot int, t uint32) (optionalRosterEntry, bool) {
	if slot < 0 || slot >= len(p.rosterLog) {
		return optionalRosterEntry{}, false
	}
	var entry optionalRosterEntry
	found := false
	for _, change := range p.rosterLog[slot] {
		if change.t > t {
			break
		}
		entry, found = change.entry, true
	}
	return entry, found
}

func optionalSamePlayer(a, b optionalRosterEntry) bool {
	if a.known != b.known {
		return false
	}
	if !a.known {
		return true
	}
	if a.playerID != 0 || b.playerID != 0 {
		return a.playerID == b.playerID
	}
	return a.name == b.name
}

func (p *optionalEventParser) addEvent(event optionalStoredEvent) {
	if len(p.events) >= optionalEventRetention {
		p.Skipped++
		return
	}
	p.events = append(p.events, event)
}

func (p *optionalEventParser) parseChat(pk *packet.Packet) error {
	r := bytes.NewReader(pk.PacketPayload)
	sender, err := readOptionalLenString(r)
	if err != nil {
		return fmt.Errorf("chat sender: %w", err)
	}
	content, err := readOptionalLenString(r)
	if err != nil {
		return fmt.Errorf("chat content: %w", err)
	}
	channel, err := r.ReadByte()
	if err != nil {
		return fmt.Errorf("chat channel: %w", err)
	}
	if sender == "" {
		sender = "unknown player"
	}
	if content == "" {
		content = "(empty message)"
	}
	playerSlot := p.exactPlayerSlotByName(sender)
	event := optionalStoredEvent{
		t:          pk.CurrentTime,
		text:       fmt.Sprintf("Chat [%s] %s: %s", optionalChatChannel(channel), sender, content),
		playerSlot: playerSlot,
	}
	if playerSlot >= 0 {
		if player := p.playerSnapshot(playerSlot); player != nil {
			event.playerID, event.playerName, event.playerKnown = player.playerID, player.name, player.known
		}
	}
	p.addEvent(event)
	return nil
}

func (p *optionalEventParser) exactPlayerSlotByName(name string) int {
	if name == "" {
		return -1
	}
	found := -1
	var players [256]*packetslot.Player
	if p.slot != nil {
		players = p.slot.Players
	} else {
		for index, entry := range p.roster {
			if entry.known {
				players[index] = &packetslot.Player{Uid: packetslot.UID{Player_id: entry.playerID, Name: entry.name}, Team: entry.team}
			}
		}
	}
	for index, player := range players {
		if player == nil {
			continue
		}
		if cleanOptionalText(player.Uid.Name, optionalMaxStringBytes) != name {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = index
	}
	return found
}

func (p *optionalEventParser) parseAward(pk *packet.Packet) error {
	payload := pk.PacketPayload
	if len(payload) < 12 {
		return errors.New("truncated award packet")
	}
	pos := 4
	awardType := payload[pos]
	pos++
	pos += 2 // fixed 00 3e
	playerSlot := int(payload[pos])
	pos++
	pos += 3 // fixed 00 00 00
	if pos >= len(payload) {
		return errors.New("truncated award name")
	}
	nameLength := int(payload[pos])
	pos++
	if nameLength > len(payload)-pos {
		return errors.New("award name exceeds packet")
	}
	awardName := cleanOptionalText(string(payload[pos:pos+nameLength]), optionalMaxStringBytes)
	if awardName == "" {
		awardName = "unnamed award"
	}
	player := p.playerSnapshot(playerSlot)
	text := fmt.Sprintf("Award: %s to %s; type=%d", awardName, optionalPlayerDescription(playerSlot, player), awardType)
	event := optionalStoredEvent{t: pk.CurrentTime, text: text, playerSlot: playerSlot}
	if player != nil {
		event.playerID, event.playerName, event.playerKnown = player.playerID, player.name, player.known
	}
	p.addEvent(event)
	return nil
}

type optionalPlayerSnapshot = optionalRosterEntry

func (p *optionalEventParser) playerSnapshot(index int) *optionalPlayerSnapshot {
	if index < 0 || index >= 256 {
		return nil
	}
	if p.slot != nil {
		if entry, ok := optionalEntryFromPlayer(p.slot.Players[index]); ok {
			return &entry
		}
	}
	if entry := p.roster[index]; entry.known {
		return &entry
	}
	return nil
}

func (p *optionalEventParser) parseDamage(pk *packet.Packet) error {
	payload := pk.PacketPayload
	if len(payload) < 4 {
		return errors.New("truncated damage packet")
	}
	fields, err := decodeOptionalIDField32(payload[4:], map[uint8]bool{1: true, 2: true, 3: true, 4: true, 5: true})
	if err != nil {
		return fmt.Errorf("damage fields: %w", err)
	}
	var offendedUID uint16
	hasOffended := false
	if field, ok := fields[1]; ok {
		if len(field) < 2 {
			return errors.New("damage target UID is truncated")
		}
		offendedUID, hasOffended = binary.LittleEndian.Uint16(field), true
	}
	var playerPID uint32
	hasPID := false
	if field, ok := fields[2]; ok {
		if len(field) < 4 {
			return errors.New("damage player PID is truncated")
		}
		playerPID, hasPID = binary.LittleEndian.Uint32(field), true
	}
	vehicle := ""
	if field, ok := fields[3]; ok {
		read, err := readOptionalLenBytes(field)
		if err != nil {
			return fmt.Errorf("damage vehicle: %w", err)
		}
		vehicle = cleanOptionalText(string(read), optionalMaxStringBytes)
	}
	fire := false
	if field, ok := fields[5]; ok {
		if len(field) < 1 {
			return errors.New("damage fire flag is truncated")
		}
		fire = field[0]&1 != 0
	}

	kind := "Critical"
	if payload[2] == 0x57 {
		kind = "Severe"
	}
	playerSlot := -1
	var player *optionalPlayerSnapshot
	if hasPID && playerPID < 256 {
		playerSlot = int(playerPID)
		player = p.playerSnapshot(playerSlot)
	}
	attacker := optionalPlayerDescription(playerSlot, player)
	target := "unresolved target"
	if hasOffended {
		target = fmt.Sprintf("rawUID %d", offendedUID)
	}
	text := fmt.Sprintf("%s damage: attacker %s; target %s", kind, attacker, target)
	if vehicle != "" {
		text += "; vehicle " + vehicle
	}
	if kind == "Critical" {
		text += fmt.Sprintf("; fire=%t", fire)
	}
	event := optionalStoredEvent{t: pk.CurrentTime, text: text, playerSlot: playerSlot}
	if player != nil {
		event.playerID, event.playerName, event.playerKnown = player.playerID, player.name, player.known
	}
	p.addEvent(event)
	return nil
}

func optionalPlayerDescription(index int, player *optionalPlayerSnapshot) string {
	if index < 0 {
		return "unresolved player"
	}
	if player != nil && player.name != "" {
		return fmt.Sprintf("%s (slot %d, uid %d)", player.name, index, player.playerID)
	}
	if player != nil && player.playerID != 0 {
		return fmt.Sprintf("uid %d (slot %d)", player.playerID, index)
	}
	return fmt.Sprintf("slot %d", index)
}

func optionalEntryFromPlayer(player *packetslot.Player) (optionalPlayerSnapshot, bool) {
	if player == nil {
		return optionalPlayerSnapshot{}, false
	}
	name := cleanOptionalText(player.Uid.Name, optionalMaxStringBytes)
	if player.Uid.Player_id == 0 && name == "" {
		return optionalPlayerSnapshot{}, false
	}
	return optionalPlayerSnapshot{known: true, playerID: player.Uid.Player_id, name: name, team: player.Team}, true
}

func readOptionalLenString(r *bytes.Reader) (string, error) {
	raw, err := readOptionalLenBytesReader(r)
	if err != nil {
		return "", err
	}
	return cleanOptionalText(string(raw), optionalMaxStringBytes), nil
}

func readOptionalLenBytesReader(r *bytes.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil reader")
	}
	length, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if int(length) > r.Len() {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, int(length))
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

func readOptionalLenBytes(data []byte) ([]byte, error) {
	if len(data) < 1 {
		return nil, io.ErrUnexpectedEOF
	}
	length := int(data[0])
	if length > len(data)-1 {
		return nil, io.ErrUnexpectedEOF
	}
	return data[1 : 1+length], nil
}

func cleanOptionalText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if limit <= 0 {
		return ""
	}
	var out strings.Builder
	for _, char := range value {
		if char == 0 {
			continue
		}
		if char == '\n' || char == '\r' || char == '\t' || unicode.IsControl(char) {
			char = ' '
		}
		size := utf8.RuneLen(char)
		if size < 0 {
			size = 1
		}
		if out.Len()+size > limit {
			break
		}
		out.WriteRune(char)
	}
	return strings.TrimSpace(out.String())
}

func optionalChatChannel(channel byte) string {
	switch channel {
	case 0:
		return "Team"
	case 1:
		return "All"
	case 2:
		return "Squad"
	case 3:
		return "Direct"
	default:
		return fmt.Sprintf("Channel %d", channel)
	}
}

// decodeOptionalIDField32 validates the complete field directory before
// decoding any value. The library serializer is intentionally not used here:
// malformed compressed sizes can otherwise reach a very large ReadBits call.
func decodeOptionalIDField32(data []byte, wanted map[uint8]bool) (map[uint8][]byte, error) {
	if len(data) < 3 {
		return nil, io.ErrUnexpectedEOF
	}
	offset := int(binary.LittleEndian.Uint16(data[:2]))
	br := danet.NewBitReader(data)
	br.BitOffset = 16
	fieldMask, err := readOptionalCompressed(br, 5)
	if err != nil || fieldMask > 0xffffffff {
		return nil, errors.New("invalid damage field mask")
	}
	startBody := br.BitOffset
	fieldCount := bits.OnesCount32(uint32(fieldMask))
	directoryStart := offset * 8
	if directoryStart < startBody || directoryStart > len(data)*8 {
		return nil, errors.New("damage field directory is outside payload")
	}
	br.BitOffset = directoryStart
	var sizes [32]uint32
	for index := 0; index < fieldCount; index++ {
		sizes[index], err = readOptionalSize(br)
		if err != nil || br.BitOffset > len(data)*8 {
			return nil, errors.New("invalid damage field size directory")
		}
	}

	out := map[uint8][]byte{}
	br.BitOffset = startBody
	ordinal := 0
	for fieldNumber := uint8(0); fieldNumber < 32; fieldNumber++ {
		if fieldMask&(uint64(1)<<fieldNumber) == 0 {
			continue
		}
		size := sizes[ordinal]
		ordinal++
		if size > optionalMaxFieldBits || br.BitOffset+int(size) > directoryStart {
			return nil, errors.New("damage field exceeds payload")
		}
		if wanted[fieldNumber] {
			value, readErr := readOptionalBitSlice(data, br.BitOffset, int(size))
			if readErr != nil {
				return nil, readErr
			}
			out[fieldNumber] = value
		}
		br.BitOffset += int(size)
	}
	return out, nil
}

func readOptionalSize(br *danet.BitReader) (uint32, error) {
	hdr, err := br.ReadBits(3)
	if err != nil || len(hdr) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	switch hdr[0] {
	case 1:
		return 1, nil
	case 2:
		return 8, nil
	case 3:
		return 16, nil
	case 4:
		return 32, nil
	case 5:
		return 64, nil
	case 6:
		return 96, nil
	case 7:
		return 128, nil
	}
	size, err := readOptionalCompressed(br, 5)
	if err != nil || size > optionalMaxFieldBits {
		return 0, errors.New("invalid field size")
	}
	return uint32(size), nil
}

func readOptionalCompressed(br *danet.BitReader, maxBytes int) (uint64, error) {
	if br == nil {
		return 0, errors.New("nil bit reader")
	}
	var value uint64
	for count := 0; count < maxBytes; count++ {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		shift := count * 7
		if shift >= 64 {
			return 0, errors.New("compressed integer overflow")
		}
		part := uint64(b & 0x7f)
		if shift > 57 && part > (^uint64(0)>>shift) {
			return 0, errors.New("compressed integer overflow")
		}
		value |= part << shift
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("compressed integer is too long")
}

func readOptionalBitSlice(data []byte, bitOffset, bitCount int) ([]byte, error) {
	if bitOffset < 0 || bitCount < 0 || bitCount > optionalMaxFieldBits || bitOffset+bitCount > len(data)*8 {
		return nil, io.ErrUnexpectedEOF
	}
	if bitCount == 0 {
		return []byte{}, nil
	}
	br := danet.NewBitReader(data)
	br.BitOffset = bitOffset
	return br.ReadBits(bitCount)
}

type optionalSlotUpdate struct {
	uidPresent  bool
	playerID    uint64
	name        string
	teamPresent bool
	team        byte
}

func (p *optionalEventParser) parseSlot(pk *packet.Packet) error {
	data, err := decodeOptionalSlotPayload(pk.PacketPayload)
	if err != nil {
		return err
	}
	updates, err := decodeOptionalSlotMessages(data)
	if err != nil {
		return err
	}
	hasIdentityUpdate := false
	for _, update := range updates {
		hasIdentityUpdate = hasIdentityUpdate || update.uidPresent
	}
	if !p.haveFirstSlotTime && hasIdentityUpdate {
		p.firstSlotTime = pk.CurrentTime
		p.haveFirstSlotTime = true
	}
	initialRoster := !p.haveFirstSlotTime || pk.CurrentTime == p.firstSlotTime
	for index, update := range updates {
		old := p.roster[index]
		if update.uidPresent {
			next := optionalRosterEntry{
				known:    update.playerID != 0 || update.name != "",
				playerID: update.playerID,
				name:     update.name,
				team:     old.team,
			}
			if !initialRoster && !optionalSamePlayer(old, next) {
				if old.known {
					p.addEvent(optionalStoredEvent{
						t:          pk.CurrentTime,
						text:       fmt.Sprintf("Player left: %s", optionalPlayerDescription(index, &optionalPlayerSnapshot{known: true, playerID: old.playerID, name: old.name, team: old.team})),
						playerSlot: index, playerID: old.playerID, playerName: old.name, playerKnown: true,
					})
				}
				if next.known {
					p.addEvent(optionalStoredEvent{
						t:          pk.CurrentTime,
						text:       fmt.Sprintf("Player joined: %s", optionalPlayerDescription(index, &optionalPlayerSnapshot{known: true, playerID: next.playerID, name: next.name, team: next.team})),
						playerSlot: index, playerID: next.playerID, playerName: next.name, playerKnown: true,
					})
				}
			}
			p.roster[index] = next
		}
		if update.teamPresent {
			p.roster[index].team = update.team
		}
		if (update.uidPresent && !optionalSamePlayer(old, p.roster[index])) || (update.teamPresent && old.team != update.team) {
			p.recordRosterChange(index, pk.CurrentTime, p.roster[index])
		}
	}
	return nil
}

func (p *optionalEventParser) recordRosterChange(index int, t uint32, entry optionalRosterEntry) {
	if index < 0 || index >= len(p.rosterLog) || p.rosterHistory >= optionalEventRetention {
		return
	}
	p.rosterLog[index] = append(p.rosterLog[index], optionalRosterChange{t: t, entry: entry})
	p.rosterHistory++
}

// Slot packets contain field updates, not a complete roster snapshot. The
// first observed timestamp is treated as roster bootstrap. A later explicit
// UID addition is a join; a clear or replacement is a leave (and a join when
// replaced). A slot's absence from a later packet never implies a leave.
func decodeOptionalSlotPayload(payload []byte) ([]byte, error) {
	if len(payload) > optionalMaxSlotBytes {
		return nil, errors.New("slot payload exceeds limit")
	}
	if len(payload) < 5 || payload[0] != 0x02 || payload[1] != 0x58 || payload[2] != 0x2d || payload[3] != 0xf0 {
		return nil, errors.New("invalid slot packet header")
	}
	compressed := payload[4] != 0
	dataStart := 4
	if !compressed {
		dataStart++
		return payload[dataStart:], nil
	}
	pos := 5
	compressedSize, err := readOptionalCompressedBytes(payload, &pos, 5)
	if err != nil {
		return nil, fmt.Errorf("slot compressed size: %w", err)
	}
	decompressedSize, err := readOptionalCompressedBytes(payload, &pos, 10)
	if err != nil {
		return nil, fmt.Errorf("slot decompressed size: %w", err)
	}
	if compressedSize == 0 || compressedSize > uint64(len(payload)-pos) || compressedSize > uint64(len(payload)) {
		return nil, errors.New("slot compressed data exceeds payload")
	}
	if decompressedSize > optionalMaxSlotBytes {
		return nil, errors.New("slot decompressed data exceeds limit")
	}
	decoder, err := zstd.NewReader(bytes.NewReader(payload[pos:pos+int(compressedSize)]), zstd.WithDecoderMaxMemory(optionalMaxSlotBytes))
	if err != nil {
		return nil, fmt.Errorf("slot zstd decoder: %w", err)
	}
	defer decoder.Close()
	out, err := io.ReadAll(io.LimitReader(decoder, optionalMaxSlotBytes+1))
	if err != nil {
		return nil, fmt.Errorf("slot zstd data: %w", err)
	}
	if len(out) > optionalMaxSlotBytes || uint64(len(out)) != decompressedSize {
		return nil, errors.New("slot decompressed data has an invalid size")
	}
	return out, nil
}

func decodeOptionalSlotMessages(data []byte) (map[int]optionalSlotUpdate, error) {
	if len(data) < 2 {
		return nil, io.ErrUnexpectedEOF
	}
	messageCount := int(binary.LittleEndian.Uint16(data[:2]))
	if messageCount > optionalMaxSlotMessages {
		return nil, errors.New("slot message count exceeds limit")
	}
	pos := 2
	updates := make(map[int]optionalSlotUpdate)
	for messageIndex := 0; messageIndex < messageCount; messageIndex++ {
		if pos+2 > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		messageLength := int(binary.LittleEndian.Uint16(data[pos : pos+2]))
		pos += 2
		if messageLength <= 0 || messageLength > len(data)-pos {
			return nil, errors.New("slot message length exceeds payload")
		}
		messageStart := pos
		if pos+2 > len(data) {
			return nil, io.ErrUnexpectedEOF
		}
		oid := binary.LittleEndian.Uint16(data[pos : pos+2])
		pos += 2
		if oid != packetslot.INVALID_OBJECT_ID && oid&packetslot.EXT_MASK == packetslot.EXT_MASK {
			if _, err := readOptionalCompressedBytes(data, &pos, 5); err != nil {
				return nil, fmt.Errorf("slot extended object id: %w", err)
			}
		}
		consumed := pos - messageStart
		if consumed > messageLength {
			return nil, errors.New("slot object id exceeds message length")
		}
		messageEnd := messageStart + messageLength
		if oid>>11 == 0xe {
			index := int(oid & 0x7ff)
			if index >= 256 {
				return nil, errors.New("player slot index exceeds roster size")
			}
			update, err := decodeOptionalSlotIdentity(data[pos:messageEnd])
			if err != nil {
				return nil, fmt.Errorf("slot %d: %w", index, err)
			}
			previous := updates[index]
			if previous.uidPresent && update.uidPresent {
				return nil, fmt.Errorf("slot %d has duplicate UID fields", index)
			}
			previous.uidPresent = previous.uidPresent || update.uidPresent
			if update.uidPresent {
				previous.playerID = update.playerID
				previous.name = update.name
			}
			previous.teamPresent = previous.teamPresent || update.teamPresent
			if update.teamPresent {
				previous.team = update.team
			}
			updates[index] = previous
		}
		pos = messageEnd
	}
	return updates, nil
}

func decodeOptionalSlotIdentity(message []byte) (optionalSlotUpdate, error) {
	var update optionalSlotUpdate
	if len(message) < 4 {
		return update, io.ErrUnexpectedEOF
	}
	offset := int(binary.LittleEndian.Uint16(message[:2]))
	count := binary.LittleEndian.Uint16(message[2:4])
	fieldCount := int(count & 0x0fff)
	bitsPerID := int(count >> 12)
	if fieldCount >= 255 || bitsPerID > 16 {
		return update, errors.New("invalid slot field directory")
	}
	if fieldCount == 0 {
		return update, nil
	}
	sizeStart := offset * 8
	bitsForIndices := (bitsPerID*fieldCount + 7) &^ 7
	indexStart := sizeStart - bitsForIndices
	if indexStart < 32 || sizeStart > len(message)*8 {
		return update, errors.New("slot directory is outside message")
	}
	br := danet.NewBitReader(message)
	br.BitOffset = sizeStart
	sizes := make([]uint32, fieldCount)
	for index := range sizes {
		var err error
		sizes[index], err = readOptionalSize(br)
		if err != nil || br.BitOffset > len(message)*8 {
			return update, errors.New("invalid slot field sizes")
		}
	}
	br.BitOffset = indexStart
	indexes := make([]uint16, fieldCount)
	for index := range indexes {
		if bitsPerID == 0 {
			indexes[index] = 0
			continue
		}
		raw, err := br.ReadBits(bitsPerID)
		if err != nil || len(raw) == 0 {
			return update, errors.New("invalid slot field index")
		}
		var value uint16
		if len(raw) >= 2 {
			value = binary.LittleEndian.Uint16(raw[:2])
		} else {
			value = uint16(raw[0])
		}
		indexes[index] = value
	}

	bodyBit := 32
	fieldEnd := indexStart
	for index := range sizes {
		fieldNumber := indexes[index]
		size := int(sizes[index])
		if size > optionalMaxFieldBits || bodyBit+size > fieldEnd {
			return update, errors.New("slot field exceeds body")
		}
		switch fieldNumber {
		case 2: // UID: uint64 player id followed by a fixed 65-byte name.
			if size < 73*8 {
				return update, errors.New("truncated slot UID")
			}
			value, err := readOptionalBitSlice(message, bodyBit, 73*8)
			if err != nil {
				return update, err
			}
			update.uidPresent = true
			update.playerID = binary.LittleEndian.Uint64(value[:8])
			update.name = cleanOptionalText(string(value[8:73]), optionalMaxStringBytes)
		case 9: // team
			if size < 8 {
				return update, errors.New("truncated slot team")
			}
			value, err := readOptionalBitSlice(message, bodyBit, 8)
			if err != nil {
				return update, err
			}
			update.teamPresent, update.team = true, value[0]
		}
		bodyBit += size
	}
	return update, nil
}

func readOptionalCompressedBytes(data []byte, pos *int, maxBytes int) (uint64, error) {
	if pos == nil || *pos < 0 || *pos > len(data) {
		return 0, io.ErrUnexpectedEOF
	}
	var value uint64
	for count := 0; count < maxBytes; count++ {
		if *pos >= len(data) {
			return 0, io.ErrUnexpectedEOF
		}
		b := data[*pos]
		*pos++
		shift := count * 7
		if shift >= 64 {
			return 0, errors.New("compressed integer overflow")
		}
		part := uint64(b & 0x7f)
		if shift > 57 && part > (^uint64(0)>>shift) {
			return 0, errors.New("compressed integer overflow")
		}
		value |= part << shift
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("compressed integer is too long")
}
