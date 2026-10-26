package main

import (
	"fmt"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
	"strings"
	"testing"

	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
)

const groundIdentityTestUID = uint64(42)

func TestGroundIdentityHistoryModelOptOutKeepsIdentityAndCoalition(t *testing.T) {
	info := groundIdentityTestInfo(42, 1000, 100, "ussr_t_80bvm", 1)
	info.Entity = &packetecs2.Entity{Template: "tank"}
	info.Entity.Data.AddComponent(&packetecs2.Component{Value: "ussr_t_80bvm"}, "unit__className")
	metadata := &groundmeta.Parser{All: []groundmeta.Info{info}}
	tracks := applyGroundIdentityHistory([]*track{groundIdentityTestTrack(42, []uint32{100, 200})}, metadata, false)
	if len(tracks) != 1 || tracks[0].entity != info.Entity || tracks[0].groundModel != nil || trackCoalition(tracks[0], nil) != "Blue" {
		t.Fatal("model opt-out discarded source identity or coalition")
	}
	if name := trackName(tracks[0], nil, nil); name != "Ground unit 42" {
		t.Fatalf("model opt-out leaked %q", name)
	}
}

func TestGroundIdentityKillReportsEndOnlyTheExactVictimLifetime(t *testing.T) {
	old := groundIdentityTestTrack(42, []uint32{100, 200, 300})
	old.hasEnd = true
	old.end = 350
	old.retired = true
	next := groundIdentityTestTrack(42, []uint32{400, 500})
	next.key.sortie = 1
	air, _ := newTestTrack(true, 77, "F-16C", "Pilot", 1)
	air.samples = []acmiSample{{t: 100}}
	applyGroundKillLifetimes([]*track{old, next, air}, []packetkill.KillEntry{{CurrentTime: 250, HasVictimUID: true, VictimUid: 42}, {CurrentTime: 260, HasVictimUID: true, VictimUid: 42}, {CurrentTime: 200, HasVictimUID: true, VictimUid: 77}})
	if old.end != 250 || old.retired || !old.hasEnd {
		t.Fatal("kill did not replace later retirement time")
	}
	if next.hasEnd || air.hasEnd {
		t.Fatal("kill crossed a lifetime or aircraft domain")
	}
}

func TestGroundIdentityHistorySplitsUIDGenerations(t *testing.T) {
	air, slot := newTestTrack(true, 77, "F-16C", "Alpha", 1)
	air.samples = []acmiSample{{t: 100}, {t: 600}}
	missile := &track{
		key:     trackKey{missile: true, id: 9001},
		name:    "AIM-9M",
		samples: []acmiSample{{t: 200}, {t: 300}},
	}
	original := groundIdentityTestTrack(groundIdentityTestUID, []uint32{100, 150, 300, 400, 450, 600})
	original.end, original.hasEnd = 300, true

	oldInfo := groundIdentityTestInfo(groundIdentityTestUID, 1000, 100, "ussr_t_80bvm", 2)
	oldInfo.Source = "ecs:unit__className"
	newInfo := groundIdentityTestInfo(groundIdentityTestUID, 2000, 400, "ussr_zprk_2s6", 1)
	newInfo.Source = "ecs:unit__className+ecs:unit_storage__tank#0x6"
	metadata := &groundmeta.Parser{
		Models: map[uint64]groundmeta.Info{groundIdentityTestUID: newInfo},
		All:    []groundmeta.Info{oldInfo, newInfo},
		Ends:   map[uint64]uint32{1000: 300, 2000: 600},
	}

	beforeIDs := acmiObjectIDs([]*track{air, missile, original})
	out := applyGroundIdentityHistory([]*track{air, missile, original}, metadata, true)
	if len(out) != 4 {
		t.Fatalf("generation split produced %d tracks, want 4: %#v", len(out), out)
	}
	if out[0] != air || out[1] != missile {
		t.Fatal("air or missile track moved, replaced, or reordered")
	}
	afterIDs := acmiObjectIDs(out)
	if afterIDs[air.key] != beforeIDs[air.key] || afterIDs[missile.key] != beforeIDs[missile.key] {
		t.Fatalf("air/missile ACMI ids changed: before %v after %v", beforeIDs, afterIDs)
	}

	oldPiece, newPiece := out[2], out[3]
	if oldPiece.key != original.key || oldPiece.key.sortie != 0 {
		t.Fatalf("first generation did not retain the original ground key: %+v", oldPiece.key)
	}
	if got := groundIdentityTestTimes(oldPiece); !equalTimes(got, []uint32{100, 150, 300}) {
		t.Fatalf("old generation samples = %v, want [100 150 300]", got)
	}
	if !oldPiece.hasEnd || oldPiece.end != 300 || oldPiece.retired {
		t.Fatalf("old generation lifetime = end %d hasEnd %v retired %v, want confirmed end 300", oldPiece.end, oldPiece.hasEnd, oldPiece.retired)
	}
	oldModel := requireGroundModel(t, oldPiece)
	if oldModel.RawModel != "ussr_t_80bvm" || oldModel.Source != oldInfo.Source {
		t.Fatalf("old generation model = %+v, want bare model and source from its own construction", oldModel)
	}
	if oldModel.DisplayName != groundDisplayModelName("ussr_t_80bvm") {
		t.Fatalf("old display name = %q", oldModel.DisplayName)
	}
	if oldPiece.groundTeam != 2 || oldPiece.groundTeamSource != "ecs:team" ||
		trackCoalition(oldPiece, nil) != "Red" {
		t.Fatalf("old generation team = %d/%q coalition %q, want replay team 2 Red", oldPiece.groundTeam, oldPiece.groundTeamSource, trackCoalition(oldPiece, nil))
	}

	if newPiece.key.id != original.key.id || newPiece.key.sortie == 0 {
		t.Fatalf("new generation key = %+v, want same UID with sortie > 0", newPiece.key)
	}
	if got := groundIdentityTestTimes(newPiece); !equalTimes(got, []uint32{400, 450, 600}) {
		t.Fatalf("new generation samples = %v, want [400 450 600]", got)
	}
	if !newPiece.hasEnd || newPiece.end != 600 || newPiece.retired {
		t.Fatalf("new generation lifetime = end %d hasEnd %v retired %v, want confirmed end 600", newPiece.end, newPiece.hasEnd, newPiece.retired)
	}
	newModel := requireGroundModel(t, newPiece)
	if newModel.RawModel != "ussr_zprk_2s6" || newModel.Source != newInfo.Source {
		t.Fatalf("new generation model = %+v, want its own construction model/source", newModel)
	}
	if newPiece.groundTeam != 1 || newPiece.groundTeamSource != "ecs:team" ||
		trackCoalition(newPiece, nil) != "Blue" {
		t.Fatalf("new generation team = %d/%q coalition %q, want replay team 1 Blue", newPiece.groundTeam, newPiece.groundTeamSource, trackCoalition(newPiece, nil))
	}

	data := renderTestACMI(t, out, slot, nil)
	if strings.Count(data, "WT_UnitUID=") != 2 ||
		!strings.Contains(data, "WT_UnitUID=42") ||
		!strings.Contains(data, "WT_GroundModel=ussr_t_80bvm") ||
		!strings.Contains(data, "WT_GroundModel=ussr_zprk_2s6") {
		t.Fatalf("split generations were not exported as separate objects with their own identities:\n%s", data)
	}
	ids := declaredACMIIDs(data)
	if len(ids) != 4 {
		t.Fatalf("declared ACMI objects = %v, want four", ids)
	}
}

func TestGroundIdentityHistorySameEIDFormattingOnly(t *testing.T) {
	if !groundmeta.SameModelIdentity("ussr_t_80bvm", "tankModels/ussr_t_80bvm") {
		t.Fatal("bare id and its tankModels qualification were treated as different models")
	}
	if groundmeta.SameModelIdentity("ussr_t_80bvm", "tankModels/ussr_zprk_2s6") {
		t.Fatal("different base models were treated as the same identity")
	}

	first := groundIdentityTestInfo(groundIdentityTestUID, 1000, 100, "ussr_t_80bvm", 1)
	second := first
	second.ConstructionTime = 400
	second.RawModel = "tankModels/ussr_t_80bvm"
	second.ClassModel = "tankModels/ussr_t_80bvm"
	second.StorageModel = "tankModels/ussr_t_80bvm"
	metadata := &groundmeta.Parser{
		Models: map[uint64]groundmeta.Info{groundIdentityTestUID: second},
		All:    []groundmeta.Info{first, second},
	}
	original := groundIdentityTestTrack(groundIdentityTestUID, []uint32{100, 150, 300, 400, 450, 600})

	out := applyGroundIdentityHistory([]*track{original}, metadata, true)
	if len(out) != 1 {
		t.Fatalf("same-EID prefix formatting created %d generations, want 1", len(out))
	}
	if out[0].key.sortie != 0 {
		t.Fatalf("formatting-only history changed sortie to %d", out[0].key.sortie)
	}
	if got := groundIdentityTestTimes(out[0]); !equalTimes(got, []uint32{100, 150, 300, 400, 450, 600}) {
		t.Fatalf("formatting-only history dropped samples: %v", got)
	}
	model := requireGroundModel(t, out[0])
	if groundModelKey(model.RawModel) != "ussr_t_80bvm" {
		t.Fatalf("formatting-only history lost the model: %+v", model)
	}
}

func TestGroundIdentityHistoryRejectsSameEIDModelChange(t *testing.T) {
	first := groundIdentityTestInfo(groundIdentityTestUID, 1000, 100, "ussr_t_80bvm", 1)
	second := groundIdentityTestInfo(groundIdentityTestUID, 1000, 400, "ussr_zprk_2s6", 1)
	second.Conflict = true
	metadata := &groundmeta.Parser{
		Models: map[uint64]groundmeta.Info{groundIdentityTestUID: second},
		All:    []groundmeta.Info{first, second},
	}
	original := groundIdentityTestTrack(groundIdentityTestUID, []uint32{100, 150, 300, 400, 450, 600})

	out := applyGroundIdentityHistory([]*track{original}, metadata, true)
	if len(out) != 1 {
		t.Fatalf("same-EID model change split %d tracks; identity cannot be assigned per generation", len(out))
	}
	if out[0].groundModel != nil {
		t.Fatalf("same-EID model change received model %+v, want refusal", out[0].groundModel)
	}
	if got := groundIdentityTestTimes(out[0]); !equalTimes(got, []uint32{100, 150, 300, 400, 450, 600}) {
		t.Fatalf("refused identity lost samples: %v", got)
	}
	if name := trackName(out[0], nil, nil); name != "Ground unit 42" {
		t.Fatalf("refused identity name = %q, want explicit unknown", name)
	}
}

func TestGroundIdentityHistoryRejectsClassStorageContradiction(t *testing.T) {
	info := groundIdentityTestInfo(groundIdentityTestUID, 1000, 100, "ussr_t_80bvm", 1)
	info.ClassModel = "ussr_t_80bvm"
	info.StorageModel = "tankModels/us_m1a2_sep2_abrams"
	// The parser would normally set ClassConflict, but the history function must
	// also refuse a real field contradiction on its own evidence.
	info.ClassConflict = false
	if groundmeta.SameModelIdentity(info.ClassModel, info.StorageModel) {
		t.Fatal("test fixture does not contain a real class/storage contradiction")
	}
	metadata := &groundmeta.Parser{
		Models: map[uint64]groundmeta.Info{groundIdentityTestUID: info},
		All:    []groundmeta.Info{info},
	}
	original := groundIdentityTestTrack(groundIdentityTestUID, []uint32{100, 150, 300})

	out := applyGroundIdentityHistory([]*track{original}, metadata, true)
	if len(out) != 1 || out[0].groundModel != nil {
		t.Fatalf("class/storage contradiction produced model %+v", out[0].groundModel)
	}
}

func TestGroundIdentityHistoryKeepsMissingModelUnknown(t *testing.T) {
	info := groundIdentityTestInfo(groundIdentityTestUID, 1000, 100, "", 1)
	info.ClassModel, info.StorageModel, info.Source = "", "", "unknown"
	metadata := &groundmeta.Parser{
		Models: map[uint64]groundmeta.Info{groundIdentityTestUID: info},
		All:    []groundmeta.Info{info},
	}
	original := groundIdentityTestTrack(groundIdentityTestUID, []uint32{100, 150, 300})

	out := applyGroundIdentityHistory([]*track{original}, metadata, true)
	if len(out) != 1 || out[0].groundModel != nil {
		t.Fatalf("missing model was invented: %+v", out[0].groundModel)
	}
	if name := trackName(out[0], nil, nil); name != "Ground unit 42" {
		t.Fatalf("missing model name = %q, want explicit unknown", name)
	}
}

func TestGroundIdentityHistoryRetiredGenerationHasNoDestroyedEvent(t *testing.T) {
	oldInfo := groundIdentityTestInfo(groundIdentityTestUID, 1000, 100, "ussr_t_80bvm", 1)
	newInfo := groundIdentityTestInfo(groundIdentityTestUID, 2000, 400, "ussr_zprk_2s6", 1)
	metadata := &groundmeta.Parser{
		Models: map[uint64]groundmeta.Info{groundIdentityTestUID: newInfo},
		All:    []groundmeta.Info{oldInfo, newInfo},
		// No destruction for the first construction: the next birth retires it.
		Ends: map[uint64]uint32{2000: 600},
	}
	original := groundIdentityTestTrack(groundIdentityTestUID, []uint32{100, 150, 300, 400, 450, 600})

	out := applyGroundIdentityHistory([]*track{original}, metadata, true)
	if len(out) != 2 {
		t.Fatalf("undead UID replacement produced %d tracks, want 2", len(out))
	}
	oldPiece, newPiece := out[0], out[1]
	if !oldPiece.hasEnd || oldPiece.end != 399 || !oldPiece.retired {
		t.Fatalf("retired generation = end %d hasEnd %v retired %v, want end 399 retired", oldPiece.end, oldPiece.hasEnd, oldPiece.retired)
	}
	if !newPiece.hasEnd || newPiece.end != 600 {
		t.Fatalf("new generation end = %d/%v, want confirmed 600", newPiece.end, newPiece.hasEnd)
	}

	data := renderTestACMI(t, out, nil, nil)
	ids := acmiObjectIDs(out)
	oldID := ids[oldPiece.key]
	if oldID == "" || oldID == ids[newPiece.key] {
		t.Fatalf("generation ids collide or are missing: old %q new %q", oldID, ids[newPiece.key])
	}
	if strings.Contains(data, "0,Event=Destroyed|"+oldID) {
		t.Fatalf("retired generation %s was reported as Destroyed:\n%s", oldID, data)
	}
	if !strings.Contains(data, "-"+oldID) {
		t.Fatalf("retired generation %s was not deleted:\n%s", oldID, data)
	}
}

func groundIdentityTestInfo(uid uint64, eid uint64, birth uint32, model string, team int32) groundmeta.Info {
	value := team
	return groundmeta.Info{
		UID:                 uint16(uid),
		EID:                 eid,
		RawModel:            model,
		ClassModel:          model,
		StorageModel:        "tankModels/" + model,
		Source:              "ecs:unit__className",
		Storage:             "unit_storage__tank",
		Kind:                "tank",
		Team:                &value,
		TeamSource:          "ecs:team",
		ConstructionTime:    birth,
		HasConstructionTime: true,
	}
}

func groundIdentityTestTrack(uid uint64, times []uint32) *track {
	samples := make([]acmiSample, 0, len(times))
	for _, at := range times {
		samples = append(samples, acmiSample{t: at, x: float64(at)})
	}
	return &track{
		key:     trackKey{groundEntity: true, id: groundUnitUIDNamespace | uid},
		name:    fmt.Sprintf("Ground unit %d", uid),
		samples: samples,
	}
}

func groundIdentityTestTimes(tr *track) []uint32 {
	result := make([]uint32, 0, len(tr.samples))
	for _, sample := range tr.samples {
		result = append(result, sample.t)
	}
	return result
}

func equalTimes(got, want []uint32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func requireGroundModel(t *testing.T, tr *track) *groundModelInfo {
	t.Helper()
	if tr == nil || tr.groundModel == nil {
		t.Fatal("ground model missing")
	}
	return tr.groundModel
}
