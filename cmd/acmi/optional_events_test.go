package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetdamage "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/damage"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

func TestOptionalEventsChatDispatchAndDisabledCategories(t *testing.T) {
	payload := optionalChatPayload("Alice", "Ready for takeoff", 1)
	chat := &packet.Packet{CurrentTime: 1250, PacketType: 3, PacketPayload: payload}

	parser := newOptionalEventParser(map[string]bool{"chat": true}, nil)
	matcher := packet.NewParserMatcher([]packet.PacketParser{parser})
	results := matcher.Match(chat)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("chat dispatch = %+v", results)
	}
	events := parser.timeline(nil)
	if len(events) != 1 || events[0].t != 1250 {
		t.Fatalf("timeline = %+v", events)
	}
	if !strings.Contains(events[0].text, "Chat [All] Alice: Ready for takeoff") {
		t.Fatalf("chat text = %q", events[0].text)
	}

	off := newOptionalEventParser(map[string]bool{"award": true}, nil)
	if matched := packet.NewParserMatcher([]packet.PacketParser{off}).Match(chat); len(matched) != 0 {
		t.Fatalf("disabled chat matched: %+v", matched)
	}
	if _, err := off.Parse(chat); err != nil || off.Skipped != 0 || len(off.timeline(nil)) != 0 {
		t.Fatalf("off chat changed state: skipped=%d err=%v", off.Skipped, err)
	}
}

func TestOptionalEventsMalformedChatIsCounted(t *testing.T) {
	parser := newOptionalEventParser(map[string]bool{"chat": true}, nil)
	pk := &packet.Packet{CurrentTime: 1, PacketType: 3, PacketPayload: []byte{20, 'A', 'l'}}
	if _, err := parser.Parse(pk); err == nil {
		t.Fatal("truncated chat was accepted")
	}
	if parser.Skipped != 1 || len(parser.timeline(nil)) != 0 {
		t.Fatalf("malformed chat: skipped=%d events=%d", parser.Skipped, len(parser.timeline(nil)))
	}
}

func TestOptionalEventsAwardUsesExactPlayerLink(t *testing.T) {
	slot := &packetslot.PacketSlotParser{}
	slot.Players[2] = &packetslot.Player{Uid: packetslot.UID{Player_id: 42, Name: "Bob"}, Team: 1}
	payload := []byte{0x02, 0x58, 0x78, 0xf0, 7, 0x00, 0x3e, 2, 0, 0, 0}
	payload = append(payload, byte(len("veteran")))
	payload = append(payload, []byte("veteran")...)
	pk := &packet.Packet{CurrentTime: 100, PacketType: 4, PacketPayload: payload}
	parser := newOptionalEventParser(map[string]bool{"award": true}, slot)
	matcher := packet.NewParserMatcher([]packet.PacketParser{parser})
	if results := matcher.Match(pk); len(results) != 1 || results[0].Err != nil {
		t.Fatalf("award dispatch = %+v", results)
	}

	entity := &packetecs2.Entity{}
	entity.Data.AddComponent(&packetecs2.Component{Value: int32(2)}, "unit__playerId")
	tracks := []*track{{key: trackKey{air: true, id: 77}, entity: entity, samples: []acmiSample{{t: 90}}}}
	events := parser.timeline(tracks)
	if len(events) != 1 || len(events[0].keys) != 1 || events[0].keys[0].id != 77 {
		t.Fatalf("award track link = %+v", events)
	}
	if !strings.Contains(events[0].text, "Award: veteran to Bob (slot 2, uid 42)") {
		t.Fatalf("award text = %q", events[0].text)
	}

	// A track outside its recorded lifetime must not be revived by the event.
	tracks[0].samples = []acmiSample{{t: 101}}
	if got := parser.timeline(tracks); len(got) != 1 || len(got[0].keys) != 0 {
		t.Fatalf("inactive track was linked: %+v", got)
	}
}

func TestOptionalEventsDamageBoundedAdapterMatchesLibrary(t *testing.T) {
	fields := []optionalTestField{
		{number: 0, value: []byte{0}, size: 8},
		{number: 1, value: []byte{0x11, 0x22}, size: 16},
		{number: 2, value: []byte{2, 0, 0, 0}, size: 32},
		{number: 3, value: append([]byte{7}, []byte("F-16C-1")...), size: 64},
		{number: 4, value: []byte{0x33, 0x44}, size: 16},
		{number: 5, value: []byte{1}, size: 1},
	}
	payload := append([]byte{0x02, 0x58, 0x56, 0xf0}, buildOptionalIDField32(fields)...)
	pk := &packet.Packet{CurrentTime: 300, PacketType: 4, PacketPayload: payload}

	// Verify that the fixture follows the existing library's on-wire format.
	if _, err := (&packetdamage.CriticalDamageParser{}).Parse(pk); err != nil {
		t.Fatalf("libreary damage fixture: %v", err)
	}

	slot := &packetslot.PacketSlotParser{}
	slot.Players[2] = &packetslot.Player{Uid: packetslot.UID{Player_id: 42, Name: "Bob"}}
	parser := newOptionalEventParser(map[string]bool{"damage": true}, slot)
	if _, err := parser.Parse(pk); err != nil {
		t.Fatalf("damage parse: %v", err)
	}
	events := parser.timeline(nil)
	if len(events) != 1 || events[0].text != "Unknown vehicle (Unknown player) critical damaged by F-16C-1 (Bob)" {
		t.Fatalf("damage text = %+v", events)
	}
	for _, diagnostic := range []string{"slot", "uid", "raw", "fire"} {
		if strings.Contains(strings.ToLower(events[0].text), diagnostic) {
			t.Fatalf("damage text retained %q diagnostic: %q", diagnostic, events[0].text)
		}
	}

	malformed := &packet.Packet{CurrentTime: 301, PacketType: 4, PacketPayload: append([]byte{0x02, 0x58, 0x57, 0xf1}, 0, 0, 0x7e)}
	if _, err := parser.Parse(malformed); err == nil {
		t.Fatal("truncated damage was accepted")
	}
	if parser.Skipped != 1 {
		t.Fatalf("malformed damage skipped = %d", parser.Skipped)
	}
}

func TestOptionalEventsSlotRosterSuppressesBootstrapAndTracksExplicitChanges(t *testing.T) {
	slot := &packetslot.PacketSlotParser{}
	parser := newOptionalEventParser(map[string]bool{"join": true}, slot)

	first := optionalSlotPacket(t, 10, 0, 100, "Alice", 1)
	if _, err := slot.Parse(first); err != nil {
		t.Fatalf("library slot fixture: %v", err)
	}
	if got := slot.Players[0]; got == nil || got.Uid.Name != "Alice" {
		t.Fatalf("library slot player = %+v", got)
	}
	if _, err := parser.Parse(first); err != nil {
		t.Fatalf("bootstrap slot: %v", err)
	}
	if got := parser.timeline(nil); len(got) != 0 {
		t.Fatalf("initial roster was reported as joins: %+v", got)
	}

	if _, err := parser.Parse(optionalSlotPacket(t, 20, 1, 200, "Bob", 2)); err != nil {
		t.Fatalf("join slot: %v", err)
	}
	if _, err := parser.Parse(optionalSlotPacket(t, 30, 0, 300, "Carol", 1)); err != nil {
		t.Fatalf("replacement slot: %v", err)
	}
	events := parser.timeline(nil)
	if len(events) != 3 {
		t.Fatalf("slot events = %+v", events)
	}
	if events[0].t != 20 || !strings.Contains(events[0].text, "Player joined: Bob (slot 1, uid 200)") {
		t.Fatalf("late join = %+v", events[0])
	}
	if events[1].t != 30 || !strings.Contains(events[1].text, "Player joined: Carol (slot 0, uid 300)") ||
		!strings.Contains(events[2].text, "Player left: Alice (slot 0, uid 100)") {
		t.Fatalf("slot replacement = %+v", events)
	}

	// Repeated identical roster updates are not new join/leave events.
	if _, err := parser.Parse(optionalSlotPacket(t, 40, 1, 200, "Bob", 2)); err != nil {
		t.Fatalf("repeat slot: %v", err)
	}
	if len(parser.timeline(nil)) != 3 {
		t.Fatalf("repeated identity produced an event: %+v", parser.timeline(nil))
	}
}

func TestOptionalEventsCompressedSlotAndMalformedBounds(t *testing.T) {
	plain := optionalSlotMessageData(0, 100, "Alice", 1)
	var compressed bytes.Buffer
	writer, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	e := optionalTestBitWriter{}
	e.variable(uint64(compressed.Len()))
	e.variable(uint64(len(plain)))
	payload := append([]byte{0x02, 0x58, 0x2d, 0xf0, 1}, e.raw()...)
	payload = append(payload, compressed.Bytes()...)
	decoded, err := decodeOptionalSlotPayload(payload)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("compressed slot: err=%v data=%x", err, decoded)
	}

	parser := newOptionalEventParser(map[string]bool{"join": true}, nil)
	badULEB := &packet.Packet{CurrentTime: 1, PacketType: 4, PacketPayload: []byte{0x02, 0x58, 0x2d, 0xf0, 1, 0x80, 0x80, 0x80, 0x80, 0x80}}
	if _, err := parser.Parse(badULEB); err == nil || parser.Skipped != 1 {
		t.Fatalf("bad slot ULEB: err=%v skipped=%d", err, parser.Skipped)
	}
	truncated := &packet.Packet{CurrentTime: 1, PacketType: 4, PacketPayload: []byte{0x02, 0x58, 0x2d, 0xf0, 0, 1, 0, 20, 0}}
	if _, err := parser.Parse(truncated); err == nil || parser.Skipped != 2 {
		t.Fatalf("truncated slot: err=%v skipped=%d", err, parser.Skipped)
	}
}

func TestOptionalEventsRetentionCap(t *testing.T) {
	parser := newOptionalEventParser(map[string]bool{"chat": true}, nil)
	payload := optionalChatPayload("A", "x", 0)
	for index := 0; index < optionalEventRetention+1; index++ {
		pk := &packet.Packet{CurrentTime: uint32(index), PacketType: 3, PacketPayload: payload}
		if _, err := parser.Parse(pk); err != nil {
			t.Fatalf("event %d: %v", index, err)
		}
	}
	if got := len(parser.events); got != optionalEventRetention || parser.Skipped != 1 {
		t.Fatalf("retention: events=%d skipped=%d", got, parser.Skipped)
	}
}

func TestOptionalEventsDamageBodyCannotOverlapDirectory(t *testing.T) {
	// Field 1 claims 16 bits, but its body starts at the size directory.
	if _, err := decodeOptionalIDField32([]byte{3, 0, 2, 0x60, 0, 0}, map[uint8]bool{1: true}); err == nil {
		t.Fatal("accepted a field body inside its size directory")
	}
}

func TestOptionalEventsSlotTeamUpdatePreservesUIDInSamePacket(t *testing.T) {
	uidMessage := optionalSlotMessage(0, 100, "Alice", 1)
	// One field: team (index 9, encoded with 4 bits), size 8 bits.
	teamMessage := []byte{6, 0, 1, 0x40, 2, 0x90, 0x40}
	data := optionalTestBitWriter{}
	data.u16(2)
	for _, message := range [][]byte{uidMessage, teamMessage} {
		data.u16(uint16(2 + len(message)))
		data.u16(0x7000)
		data.bytes(message)
	}
	updates, err := decodeOptionalSlotMessages(data.raw())
	if err != nil {
		t.Fatal(err)
	}
	got := updates[0]
	if !got.uidPresent || got.playerID != 100 || got.name != "Alice" || !got.teamPresent || got.team != 2 {
		t.Fatalf("merged roster update lost identity: %+v", got)
	}
}

func TestOptionalEventsBootstrapStartsAtFirstIdentityNotUnrelatedSlot(t *testing.T) {
	p := newOptionalEventParser(map[string]bool{"join": true}, nil)
	// A slot packet without player identities precedes the actual roster.
	if _, err := p.Parse(&packet.Packet{PacketType: 4, CurrentTime: 1, PacketPayload: []byte{2, 0x58, 0x2d, 0xf0, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Parse(optionalSlotPacket(t, 10, 0, 100, "Alice", 1)); err != nil {
		t.Fatal(err)
	}
	if len(p.events) != 0 {
		t.Fatalf("initial roster became a join: %+v", p.events)
	}
	if _, err := p.Parse(optionalSlotPacket(t, 20, 1, 200, "Bob", 2)); err != nil {
		t.Fatal(err)
	}
	if len(p.events) != 1 || !strings.Contains(p.events[0].text, "Bob") {
		t.Fatalf("later join lost: %+v", p.events)
	}
}

func TestOptionalEventsRejectsPlayerSlotBeyondRoster(t *testing.T) {
	pk := optionalSlotPacket(t, 10, 256, 100, "Alice", 1)
	p := newOptionalEventParser(map[string]bool{"join": true}, nil)
	if _, err := p.Parse(pk); err == nil || len(p.events) != 0 {
		t.Fatalf("out-of-range slot accepted: %v", err)
	}
}

type optionalTestField struct {
	number uint8
	value  []byte
	size   int
}

type optionalTestBitWriter struct {
	data []byte
	bits int
}

func (w *optionalTestBitWriter) put(value uint64, count int) {
	for bit := count - 1; bit >= 0; bit-- {
		if w.bits/8 == len(w.data) {
			w.data = append(w.data, 0)
		}
		if value&(uint64(1)<<bit) != 0 {
			w.data[w.bits/8] |= 0x80 >> (w.bits % 8)
		}
		w.bits++
	}
}

func (w *optionalTestBitWriter) byte(value byte) { w.put(uint64(value), 8) }

func (w *optionalTestBitWriter) u16(value uint16) {
	w.byte(byte(value))
	w.byte(byte(value >> 8))
}

func (w *optionalTestBitWriter) bytes(value []byte) {
	for _, b := range value {
		w.byte(b)
	}
}

func (w *optionalTestBitWriter) variable(value uint64) {
	for {
		part := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			part |= 0x80
		}
		w.byte(part)
		if value == 0 {
			return
		}
	}
}

func (w *optionalTestBitWriter) size(value int) {
	switch value {
	case 1:
		w.put(1, 3)
	case 8:
		w.put(2, 3)
	case 16:
		w.put(3, 3)
	case 32:
		w.put(4, 3)
	case 64:
		w.put(5, 3)
	case 96:
		w.put(6, 3)
	case 128:
		w.put(7, 3)
	default:
		w.put(0, 3)
		w.variable(uint64(value))
	}
}

func (w *optionalTestBitWriter) raw() []byte { return append([]byte(nil), w.data...) }

func buildOptionalIDField32(fields []optionalTestField) []byte {
	body := optionalTestBitWriter{}
	mask := uint32(0)
	for _, field := range fields {
		mask |= uint32(1) << field.number
		if field.size%8 == 0 {
			body.bytes(field.value[:field.size/8])
		} else {
			body.put(uint64(field.value[0]), field.size)
		}
	}
	if body.bits%8 != 0 {
		body.put(0, (8-body.bits%8)%8)
	}
	header := optionalTestBitWriter{}
	header.put(0, 16)
	header.variable(uint64(mask))
	directory := optionalTestBitWriter{}
	for _, field := range fields {
		directory.size(field.size)
	}
	offset := len(header.data) + len(body.data)
	out := append(header.data, body.data...)
	out = append(out, directory.data...)
	binary.LittleEndian.PutUint16(out[:2], uint16(offset))
	return out
}

func optionalChatPayload(sender, content string, channel byte) []byte {
	out := []byte{byte(len(sender))}
	out = append(out, []byte(sender)...)
	out = append(out, byte(len(content)))
	out = append(out, []byte(content)...)
	out = append(out, channel)
	return out
}

func optionalSlotPacket(t *testing.T, when uint32, index int, playerID uint64, name string, team byte) *packet.Packet {
	t.Helper()
	message := optionalSlotMessage(index, playerID, name, team)
	data := optionalTestBitWriter{}
	data.u16(1)
	data.u16(uint16(2 + len(message)))
	data.u16(uint16(0x7000 | index))
	data.bytes(message)
	payload := []byte{0x02, 0x58, 0x2d, 0xf0, 0}
	payload = append(payload, data.data...)
	return &packet.Packet{CurrentTime: when, PacketType: 4, PacketPayload: payload}
}

func optionalSlotMessageData(index int, playerID uint64, name string, team byte) []byte {
	message := optionalSlotMessage(index, playerID, name, team)
	data := optionalTestBitWriter{}
	data.u16(1)
	data.u16(uint16(2 + len(message)))
	data.u16(uint16(0x7000 | index))
	data.bytes(message)
	return data.data
}

func optionalSlotMessage(_ int, playerID uint64, name string, team byte) []byte {
	uid := make([]byte, 73)
	binary.LittleEndian.PutUint64(uid[:8], playerID)
	copy(uid[8:], []byte(name))
	body := append(uid, team)
	sizes := optionalTestBitWriter{}
	sizes.size(len(uid) * 8)
	sizes.size(8)
	indices := []byte{2, 9}
	indexStart := 4 + len(body)
	offset := indexStart + len(indices)
	out := make([]byte, offset+len(sizes.data))
	binary.LittleEndian.PutUint16(out[:2], uint16(offset))
	binary.LittleEndian.PutUint16(out[2:4], uint16(0x8000|2))
	copy(out[4:], body)
	copy(out[indexStart:], indices)
	copy(out[offset:], sizes.data)
	return out
}
