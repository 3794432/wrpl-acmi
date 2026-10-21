package projectiles

import (
	"fmt"
	"math"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/danet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	ecsp "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/pierrec/lz4/v4"
)

const maxDecompressedECS = 256 << 20

// Projectile combines creation metadata published in a Rocket or Bomb
// component with the lifetime boundary carried by an ECS destruction packet.
type Projectile struct {
	ID        uint64
	OwnerEID  uint64
	ParentUID uint64
	HasParent bool
	// Kind is "bomb" only for a construction carrying a proven Bomb component.
	// Rocket constructions keep the empty value so existing missile consumers
	// continue to see their established behavior.
	Kind     string
	Name     string
	Template string
	// TemplateComponents is construction schema only: component names declared
	// by the template, in template order. It does not imply that a value was
	// present or decoded in this construction instance.
	TemplateComponents []string
	LeadingULEB        uint64
	WeaponRef          uint32
	SpawnSample        Sample
	EndTime            uint32
	HasEnd             bool
	// Flight is nil unless this exact construction instance has a proven,
	// correctly decoded proj__ Point3 value. The current packet has no bounded
	// Rocket body, so missile constructions leave this nil.
	Flight *FlightEvidence
}

func (p *Projectile) HasTemplateComponent(name string) bool {
	if p == nil {
		return false
	}
	for _, component := range p.TemplateComponents {
		if component == name {
			return true
		}
	}
	return false
}

// MetadataParser consumes ECS construction and destruction packets. It owns a
// separate ECS registry because template parsing has to see each definition in
// packet order; a second, independent ECS parser may have already consumed a
// copy of the same blocks.
type MetadataParser struct {
	// Tracks resolves an ECS EID to its latest construction instance. EIDs can
	// be reconstructed, so History retains every instance in packet order.
	Tracks  map[uint64]*Projectile
	History map[uint64][]*Projectile

	maps      ecsp.ComponentHashMaps
	ecs       *ecsp.PacketECSParser
	reserved  bool
	ownerUIDs map[uint64]uint64
}

func NewMetadataParser(maps ecsp.ComponentHashMaps, reserved bool) *MetadataParser {
	return &MetadataParser{
		Tracks:    make(map[uint64]*Projectile),
		History:   make(map[uint64][]*Projectile),
		maps:      maps,
		ecs:       ecsp.NewPacketECSParser(maps),
		reserved:  reserved,
		ownerUIDs: make(map[uint64]uint64),
	}
}

func (p *MetadataParser) Name() string { return "projectile_metadata" }

func (p *MetadataParser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	return map[byte][][]packet.ParsingCondition{6: nil}
}

func (p *MetadataParser) Parse(pk *packet.Packet) (any, error) {
	p.ensureState()
	if pk == nil {
		return []*Projectile{}, nil
	}
	if len(pk.PacketPayload) == 0 {
		return []*Projectile{}, nil
	}

	created := make([]*Projectile, 0)
	switch pk.PacketPayload[0] {
	case 0x24:
		blob := pk.PacketPayload[1:]
		if err := p.parseConstructions(blob, pk.CurrentTime, &created); err != nil {
			return created, err
		}
	case 0x25:
		blob, err := decompressLZ4(pk.PacketPayload[1:])
		if err != nil {
			return created, fmt.Errorf("decompressing ECS construction packet: %w", err)
		}
		if err := p.parseConstructions(blob, pk.CurrentTime, &created); err != nil {
			return created, err
		}
	case 0x26:
		if err := p.parseDestruction(pk.PacketPayload, pk.CurrentTime); err != nil {
			return created, err
		}
	}
	return created, nil
}

func (p *MetadataParser) ensureState() {
	if p.Tracks == nil {
		p.Tracks = make(map[uint64]*Projectile)
	}
	if p.History == nil {
		p.History = make(map[uint64][]*Projectile)
	}
	if p.ownerUIDs == nil {
		p.ownerUIDs = make(map[uint64]uint64)
	}
	if p.ecs == nil {
		p.ecs = ecsp.NewPacketECSParser(p.maps)
	}
}

func (p *MetadataParser) parseConstructions(blob []byte, time uint32, created *[]*Projectile) error {
	if len(blob) == 0 {
		return fmt.Errorf("missing ECS construction count")
	}
	count := int(blob[0]) + 1
	// The count byte can request at most 256 frames, but every frame also
	// needs an EID and a size prefix. Rejecting a count larger than the whole
	// remainder keeps the loop bounded by bytes that actually arrived.
	if count > len(blob) {
		return fmt.Errorf("ECS construction count %d exceeds %d remaining bytes", count, len(blob))
	}
	r := danet.NewBitReader(blob[1:])
	for i := 0; i < count; i++ {
		eid, err := packet.ReadEID(r)
		if err != nil {
			return fmt.Errorf("reading construction %d EID: %w", i, err)
		}
		block, err := readBoundedBlock(r)
		if err != nil {
			return fmt.Errorf("reading construction %d block: %w", i, err)
		}
		if projectile := p.parseConstruction(eid, block, time); projectile != nil {
			*created = append(*created, projectile)
		}
	}
	return nil
}

// readBoundedBlock reads a size-prefixed block directly from its containing
// packet. Checking the remaining bytes first avoids allocating before a bad
// size has been validated.
func readBoundedBlock(r *danet.BitReader) ([]byte, error) {
	size, err := r.ReadCompressed()
	if err != nil {
		return nil, err
	}
	if r.BitOffset < 0 || r.BitOffset > len(r.Data)*8 {
		return nil, fmt.Errorf("invalid reader offset %d", r.BitOffset)
	}
	remainingBits := len(r.Data)*8 - r.BitOffset
	if size > uint64(remainingBits/8) {
		return nil, fmt.Errorf("block size %d exceeds remaining payload %d", size, remainingBits/8)
	}
	return r.ReadBytes(int(size))
}

func decompressLZ4(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("empty LZ4 block")
	}

	// ECS packets use a raw LZ4 block, which does not carry its decompressed
	// size. Grow the destination until the block fits, with a hard memory cap.
	size := len(src) * 8
	if size < 8<<10 {
		size = 8 << 10
	}
	if size > maxDecompressedECS {
		size = maxDecompressedECS
	}
	var lastErr error
	for {
		dst := make([]byte, size)
		n, err := lz4.UncompressBlock(src, dst)
		if err == nil {
			return dst[:n], nil
		}
		lastErr = err
		if size >= maxDecompressedECS {
			return nil, fmt.Errorf("LZ4 block exceeds %d-byte limit: %w", maxDecompressedECS, lastErr)
		}
		next := size * 2
		if next <= size || next > maxDecompressedECS {
			next = maxDecompressedECS
		}
		size = next
	}
}

func (p *MetadataParser) parseConstruction(eid uint64, block []byte, time uint32) *Projectile {
	r := danet.NewBitReader(block)
	template, err := p.ecs.ParseECSTemplate(r)
	if err != nil {
		return nil
	}
	if p.reserved {
		reserved, err := r.ReadByte()
		if err != nil || reserved != 0 {
			return nil
		}
	}
	componentCount, err := readComponentCount(r, len(template.Components))
	if err != nil || componentCount > len(template.Components) {
		return nil
	}

	rocketTemplate := isCandidateTemplate(template.Name)
	componentNames := p.templateComponentNames(template)
	previous := -1
	for i := 0; i < componentCount; i++ {
		local, err := readComponentOffset(r, len(template.Components), previous, i == 0)
		if err != nil || local < 0 || local >= len(template.Components) {
			return nil
		}
		previous = local

		componentID := template.Components[local]
		component := p.ecs.ComponentDefs[componentID]
		if component == nil {
			return nil
		}

		if kind, ok := p.projectileComponentKind(component); ok {
			if kind == projectileKindBomb {
				return p.readProjectileConstruction(eid, r, time, template.Name, componentNames, kind)
			}
			if rocketTemplate {
				if local != 0 {
					return nil
				}
				return p.readProjectileConstruction(eid, r, time, template.Name, componentNames, kind)
			}
		}
		if rocketTemplate {
			// The established rocket templates place their Rocket component
			// first. Reaching any other component means this construction does
			// not have the layout this parser knows how to decode.
			return nil
		}

		parser := p.maps.DataComponentParsers[component.Name]
		if parser == nil {
			// The component body has no wire length of its own. The outer block
			// boundary lets us discard this entity and resume at the next frame.
			return nil
		}
		value, err := parser.Parse(r, p.ecs)
		if err != nil {
			return nil
		}
		if name, ok := p.maps.GetDataCompName(component.Name); ok && name == "uid" {
			if uid, ok := uidFromValue(value); ok {
				p.recordOwnerUID(eid, uid)
			}
		}
	}
	return nil
}

func (p *MetadataParser) templateComponentNames(template *ecsp.Template) []string {
	if template == nil {
		return nil
	}
	names := make([]string, 0, len(template.Components))
	for _, id := range template.Components {
		component := p.ecs.ComponentDefs[id]
		if component == nil {
			names = append(names, "")
			continue
		}
		name := ""
		if dataName, ok := p.maps.GetDataCompName(component.Name); ok {
			name = dataName
		} else if typeName, ok := p.maps.GetCompName(component.Type); ok {
			name = typeName
		} else {
			name = fmt.Sprintf("data:0x%08x/type:0x%08x", uint32(component.Name), uint32(component.Type))
		}
		names = append(names, name)
	}
	return names
}

func readComponentCount(r *danet.BitReader, templateComponents int) (int, error) {
	if templateComponents < 256 {
		count, err := r.ReadByte()
		return int(count), err
	}
	count, err := r.ReadCompressed()
	if err != nil {
		return 0, err
	}
	if count > uint64(templateComponents) {
		return 0, fmt.Errorf("component count %d exceeds template size %d", count, templateComponents)
	}
	return int(count), nil
}

func readComponentOffset(r *danet.BitReader, templateComponents, previous int, first bool) (int, error) {
	var offset uint64
	var err error
	if templateComponents < 256 {
		var value byte
		value, err = r.ReadByte()
		offset = uint64(value)
	} else {
		offset, err = r.ReadCompressed()
	}
	if err != nil {
		return 0, err
	}
	if first {
		if offset > uint64(templateComponents) {
			return 0, fmt.Errorf("component offset %d exceeds template size %d", offset, templateComponents)
		}
		return int(offset), nil
	}
	if previous < 0 || offset > uint64(templateComponents-previous-1) {
		return 0, fmt.Errorf("component delta %d exceeds template size %d", offset, templateComponents)
	}
	return previous + int(offset) + 1, nil
}

const (
	projectileKindRocket = "rocket"
	projectileKindBomb   = "bomb"
)

// projectileComponentKind identifies only components whose construction body
// is the shared RocketSerializer layout. Bomb is a Rocket subclass registered
// with that same serializer, so its body can be decoded with the same reader.
// Unknown components are rejected rather than guessed by template name.
func (p *MetadataParser) projectileComponentKind(component *ecsp.HashedComponent) (string, bool) {
	if component == nil {
		return "", false
	}
	switch p.maps.ComponentNames[uint32(component.Type)] {
	case "Rocket":
		return projectileKindRocket, true
	case "Bomb":
		return projectileKindBomb, true
	}
	if name, ok := p.maps.GetDataCompName(component.Name); ok {
		switch name {
		case "rocket_component":
			return projectileKindRocket, true
		case "bomb_component":
			return projectileKindBomb, true
		}
	}
	return "", false
}

func isCandidateTemplate(name string) bool {
	return name == "rocket" || name == "rocket+arh_shell"
}

// readProjectileConstruction decodes the fixed prefix shared by the
// RocketSerializer layout. ComponentTypesDefs declares `struct Bomb : Rocket`
// and registers Bomb with the same rocket_serializer, so a Bomb construction
// uses this layout rather than a guessed one. The kind selects only identity.
func (p *MetadataParser) readProjectileConstruction(eid uint64, r *danet.BitReader, time uint32, template string, templateComponents []string, kind string) *Projectile {
	leading, err := r.ReadCompressed()
	if err != nil { // leading serialized ULEB value
		return nil
	}
	ownerEID, err := packet.ReadEID(r)
	if err != nil {
		return nil
	}
	_, err = packet.ReadEID(r)
	if err != nil { // secondary EID
		return nil
	}
	if _, err = r.ReadByte(); err != nil {
		return nil
	}
	if _, err = r.ReadU32LE(); err != nil {
		return nil
	}
	weaponRef, err := r.ReadU32LE()
	if err != nil {
		return nil
	}
	coords := [3]float64{}
	for i := range coords {
		bits, err := r.ReadU32LE()
		if err != nil {
			return nil
		}
		coords[i] = float64(math.Float32frombits(bits))
	}
	if !finiteCoordinates(coords[0], coords[1], coords[2]) {
		return nil
	}

	name := "Missile"
	projectileKind := ""
	if kind == projectileKindBomb {
		name = "Bomb"
		projectileKind = projectileKindBomb
	}
	projectile := &Projectile{
		ID:                 eid,
		OwnerEID:           ownerEID,
		Kind:               projectileKind,
		Name:               name,
		Template:           template,
		TemplateComponents: append([]string(nil), templateComponents...),
		LeadingULEB:        leading,
		WeaponRef:          weaponRef,
		SpawnSample: Sample{
			Time: time,
			X:    coords[0],
			Y:    coords[1],
			Z:    coords[2],
		},
	}
	if parentUID, ok := p.ownerUIDs[ownerEID]; ok {
		projectile.ParentUID = parentUID
		projectile.HasParent = true
	}
	p.History[eid] = append(p.History[eid], projectile)
	p.Tracks[eid] = projectile
	return projectile
}

func finiteCoordinates(x, y, z float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) &&
		!math.IsNaN(y) && !math.IsInf(y, 0) &&
		!math.IsNaN(z) && !math.IsInf(z, 0)
}

func uidFromValue(value any) (uint64, bool) {
	switch v := value.(type) {
	case int32:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case uint64:
		return v, true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case uint:
		return uint64(v), true
	default:
		return 0, false
	}
}

func (p *MetadataParser) recordOwnerUID(eid, uid uint64) {
	p.ownerUIDs[eid] = uid
	for _, instances := range p.History {
		for _, projectile := range instances {
			if projectile.OwnerEID == eid {
				projectile.ParentUID = uid
				projectile.HasParent = true
			}
		}
	}
}

func (p *MetadataParser) parseDestruction(payload []byte, time uint32) error {
	if len(payload) < 2 {
		return fmt.Errorf("short ECS destruction packet")
	}
	count := int(payload[1]) + 1
	r := danet.NewBitReader(payload[2:])
	for i := 0; i < count; i++ {
		eid, err := packet.ReadEID(r)
		if err != nil {
			return fmt.Errorf("reading destruction %d EID: %w", i, err)
		}
		instances := p.History[eid]
		for i := len(instances) - 1; i >= 0; i-- {
			if !instances[i].HasEnd {
				instances[i].EndTime = time
				instances[i].HasEnd = true
				break
			}
		}
	}
	return nil
}

var _ packet.PacketParser = (*MetadataParser)(nil)
