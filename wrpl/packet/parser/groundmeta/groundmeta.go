// Package groundmeta builds an independent UID-to-unit-model registry from
// ECS construction metadata. It intentionally runs its own ECS parser so the
// existing aircraft/weapon model is not changed by missing server components.
package groundmeta

import (
	"fmt"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	ecs "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
)

const maxGroundUID = 1 << 11

var groundStorageComponents = []string{
	"unit_storage__tank",
	"unit_storage__light_vehicle",
	"unit_storage__fortification",
	"unit_storage__ship",
	"unit_storage__walker",
	"unit_storage__infantry_troop",
	"unit_storage__human",
	"unit_storage__barrage_balloon",
}

// Info is the explicit metadata observed for one replay UID. RawModel is the
// exact unit__className/model string; empty RawModel means the ground entity
// was present but no model field could be decoded.
type Info struct {
	UID          uint16
	EID          uint64
	RawModel     string
	ClassModel   string
	StorageModel string
	MissionName  string
	Source       string
	Kind         string

	Team       *int32
	TeamSource string
	UnitType   *int32

	Storage       string
	ClassConflict bool
	Conflict      bool
	Entity        *ecs.Entity
}

// Parser is a packet.PacketParser for packet type 6. Models is keyed by the
// real replay unit UID, not by ECS entity index or a synthetic key.
type Parser struct {
	Models map[uint64]Info
	All    []Info

	ParsedPackets  int
	GroundEntities int
	InvalidUIDs    int
	MissingUIDs    int
	UnknownModels  int
	Conflicts      int
	ClassConflicts int
	ParseErrors    int
	LastError      error

	ecs            *ecs.PacketECSParser
	conflicts      map[uint64]bool
	classConflicts map[uint64]bool
}

// NewParser creates an independent ECS reader. constructionReserved must be
// true for replay version 0x00018C1C, matching PacketECSParser.
func NewParser(maps ecs.ComponentHashMaps, constructionReserved bool) *Parser {
	e := ecs.NewPacketECSParser(maps)
	e.ConstructionReservedByte = constructionReserved
	e.AllowMissingPODDataComponents = true
	return &Parser{
		Models: map[uint64]Info{},
		ecs:    e,
	}
}

func (p *Parser) Name() string { return "groundmeta" }

func (p *Parser) ParsesMatching() map[byte][][]packet.ParsingCondition {
	return map[byte][][]packet.ParsingCondition{6: nil}
}

// Parse consumes one ECS packet. The returned value is the current registry
// size; callers should read Models rather than rely on the return value.
func (p *Parser) Parse(pk *packet.Packet) (any, error) {
	if p == nil || pk == nil {
		return nil, nil
	}
	if p.ecs == nil {
		return nil, fmt.Errorf("groundmeta parser is not initialized")
	}
	raw, err := p.ecs.Parse(pk)
	p.ecs.Messages = p.ecs.Messages[:0]
	if err != nil {
		p.ParseErrors++
		p.LastError = err
		return nil, fmt.Errorf("groundmeta ECS packet %d: %w", pk.Seq, err)
	}
	p.ParsedPackets++
	dat, ok := raw.(*ecs.ParsedPacketECS)
	if !ok || dat == nil {
		return len(p.Models), nil
	}
	for _, msg := range dat.Messages {
		if msg == nil {
			continue
		}
		for _, parsed := range msg.Parsed {
			entity, ok := parsed.(*ecs.Entity)
			if !ok || entity == nil {
				continue
			}
			p.ObserveEntity(msg.EID, entity)
		}
	}
	return len(p.Models), nil
}

// ObserveEntity can be used by integrations that already parsed an ECS entity
// with AllowMissingPODDataComponents enabled.
func (p *Parser) ObserveEntity(eid uint64, entity *ecs.Entity) (Info, bool) {
	if p == nil || entity == nil {
		return Info{}, false
	}
	storageName, storage, ok := findGroundStorage(&entity.Data)
	if !ok {
		return Info{}, false
	}
	p.GroundEntities++
	uid, ok := ecs.GetObjectData[int32](&entity.Data, "uid")
	if !ok {
		p.MissingUIDs++
		return Info{}, false
	}
	if uid < 0 || uid >= maxGroundUID {
		p.InvalidUIDs++
		return Info{}, false
	}
	info := Info{
		UID:     uint16(uid),
		EID:     eid,
		Kind:    kindOf(entity.Template, storageName),
		Storage: storageName,
		Entity:  entity,
	}
	info.ClassModel, _ = ecs.GetObjectData[string](&entity.Data, "unit__className")
	info.MissionName, _ = ecs.GetObjectData[string](&entity.Data, "unit__missionName")
	storageModel, storageErr := UnitStorageUnitName(storage.Raw)
	if storageErr == nil {
		info.StorageModel = storageModel
	}
	info.RawModel = info.ClassModel
	if info.RawModel == "" {
		info.RawModel = info.StorageModel
	}
	sources := []string{}
	if info.ClassModel != "" {
		sources = append(sources, "ecs:unit__className")
	}
	if info.StorageModel != "" {
		sources = append(sources, "ecs:"+storageName+"#0x6")
	}
	if info.ClassModel != "" && info.StorageModel != "" && info.ClassModel != info.StorageModel {
		info.ClassConflict = true
	}
	info.Source = "unknown"
	if len(sources) > 0 {
		info.Source = strings.Join(sources, "+")
	}
	if team, ok := ecs.GetObjectData[int32](&entity.Data, "team"); ok {
		v := team
		info.Team = &v
		info.TeamSource = "ecs:team"
	}
	if unitType, ok := ecs.GetObjectData[int32](&entity.Data, "unitType"); ok {
		v := unitType
		info.UnitType = &v
	}
	if info.RawModel == "" {
		p.UnknownModels++
	}
	key := uint64(info.UID)
	p.ensureEvidenceMaps()
	if info.ClassConflict {
		if !p.classConflicts[key] {
			p.ClassConflicts++
		}
		p.classConflicts[key] = true
	}
	prev, exists := p.Models[key]
	if exists {
		if prev.RawModel != "" && info.RawModel == "" {
			// Keep the last non-empty identity. A later transient unknown
			// observation must not erase evidence for the same UID.
			info.ClassConflict = p.classConflicts[key]
			info.Conflict = p.conflicts[key]
			p.All = append(p.All, info)
			return prev, true
		}
		if prev.RawModel != "" && info.RawModel != "" && prev.RawModel != info.RawModel {
			if !p.conflicts[key] {
				p.Conflicts++
			}
			p.conflicts[key] = true
		}
	}
	info.Conflict = p.conflicts[key]
	info.ClassConflict = p.classConflicts[key]
	p.Models[key] = info
	p.All = append(p.All, info)
	return info, true
}

func (p *Parser) ensureEvidenceMaps() {
	if p.Models == nil {
		p.Models = map[uint64]Info{}
	}
	if p.conflicts == nil {
		p.conflicts = map[uint64]bool{}
	}
	if p.classConflicts == nil {
		p.classConflicts = map[uint64]bool{}
	}
}

// Lookup returns the latest metadata for a real replay UID.
func (p *Parser) Lookup(uid uint16) (Info, bool) {
	if p == nil {
		return Info{}, false
	}
	info, ok := p.Models[uint64(uid)]
	return info, ok
}

func findGroundStorage(o *ecs.Object) (string, ecs.UnitStorage, bool) {
	for _, name := range groundStorageComponents {
		if v, ok := ecs.GetObjectData[ecs.UnitStorage](o, name); ok {
			return name, v, true
		}
	}
	return "", ecs.UnitStorage{}, false
}

func kindOf(template, storage string) string {
	if template != "" {
		return template
	}
	return strings.TrimPrefix(storage, "unit_storage__")
}
