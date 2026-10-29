package main

import (
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func missileAssociationTestTable() *missileModelTable {
	table := missileModelTestTable(nil)
	table.Aliases = map[string]missileModelAliasEntry{
		"cn_pl12": {
			Model: "PL-12", InternalName: "cn_pl12", Confidence: "confirmed",
			Source: "datamine cn_pl12.blkx: guidanceType=radar, radarSeeker.active=true",
		},
		"cn_pl12_default": {
			Model: "PL-12", InternalName: "cn_pl12_default", Confidence: "confirmed",
			Source: "datamine cn_pl12.blkx: guidanceType=radar, radarSeeker.active=true",
		},
		"us_aim_120a": {
			Model: "AIM-120A", InternalName: "us_aim_120a", Confidence: "confirmed",
			Source: "datamine us_aim_120a.blkx: guidanceType=radar, radarSeeker.active=true",
		},
		"jp_aam3": {
			Model: "AAM-3", InternalName: "jp_aam3", Confidence: "confirmed",
			Source: "datamine jp_aam3.blkx: guidanceType=optical, opticalSeeker (IR AAM)",
		},
		"su_r_73": {
			Model: "R-73", InternalName: "su_r_73", Confidence: "confirmed",
			Source: "datamine su_r_73.blkx: guidanceType=optical, opticalSeeker (IR AAM)",
		},
		"su_r_27er": {
			Model: "R-27ER", InternalName: "su_r_27er", Confidence: "confirmed",
			Source: "datamine su_r_27er.blkx: guidanceType=radar, radarSeeker.active absent (SARH)",
		},
	}
	return table
}

func missileAssociationTestTrack(trackID, ownerEID, parentUID uint64, weaponID, trigger uint32, template string, generation uint64, ammo ...groundmeta.StorageWeapon) *track {
	source := missileNameTestSource(0, 2000, 129+uint64(weaponID), trigger, template)
	source.ParentUID = parentUID
	source.HasParent = true
	source.OwnerEID = ownerEID
	source.ID = generation<<22 | 0x123
	source.OwnerWeapons = append([]groundmeta.StorageWeapon(nil), ammo...)
	return missileNameTestTrack(trackID, source, true)
}

func missileAssociationIRMotion() *projectiles.RawMotion {
	return &projectiles.RawMotion{
		Complete: true, HasTail: true, HasSeekerBlock: true,
		SeekerBitLength: 283, SeekerRawBits: make([]byte, (283+7)/8),
	}
}

func missileAssociationRadarMotion() *projectiles.RawMotion {
	raw := make([]byte, (607+7)/8)
	raw[1] = 0xe0 // Radar mode bits 8..10 = 111 (track).
	return &projectiles.RawMotion{
		Complete: true, HasTail: true, HasSeekerBlock: true,
		SeekerBitLength: 607, SeekerRawBits: raw,
	}
}

func missileAssociationAttachSeeker(tr *track, motion *projectiles.RawMotion) {
	tr.samples = append(tr.samples, acmiSample{
		t:          tr.samples[len(tr.samples)-1].t + 10,
		projectile: &projectiles.Sample{FromSync: true, RawMotion: motion},
	})
}

func TestMissileModelAssociationsUniqueOwnerAmmunition(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "cn_pl12_default", Bullet: "", Count: 2})
	applyMissileModelNames([]*track{tr}, nil)
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 1 {
		t.Fatalf("adopted = %d, want 1", adopted)
	}
	info := tr.missileModel
	if info == nil || info.Model != "PL-12" || info.InternalName != "cn_pl12" ||
		info.Source != missileAssociationSourceMatched || info.Confidence != "confirmed" {
		t.Fatalf("unique owner model = %+v", info)
	}
	if tr.name != "PL-12" {
		t.Fatalf("display name = %q, want PL-12", tr.name)
	}
}

func TestMissileModelAssociationUnrecognizedStorePreventsFalseUniqueness(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 1, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "cn_pl12", Count: 2},
		groundmeta.StorageWeapon{Launcher: "unmapped_missile", Count: 2})
	applyMissileModelNames([]*track{tr}, nil)
	if got := applyMissileModelAssociations([]*track{tr}, table); got != 0 || tr.missileModel.Model != "" {
		t.Fatalf("unrecognized mixed loadout was treated as unique: %d, %+v", got, tr.missileModel)
	}
}

func TestMissileModelAssociationDoesNotPromoteWeakAlias(t *testing.T) {
	table := missileAssociationTestTable()
	entry := table.Aliases["cn_pl12"]
	entry.Confidence = "low"
	table.Aliases = map[string]missileModelAliasEntry{"cn_pl12": entry}
	tr := missileAssociationTestTrack(1, 1, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "cn_pl12", Count: 2})
	applyMissileModelNames([]*track{tr}, nil)
	if got := applyMissileModelAssociations([]*track{tr}, table); got != 0 || tr.missileModel.Model != "" {
		t.Fatalf("weak alias was promoted to a confirmed model: %d, %+v", got, tr.missileModel)
	}
}

func TestMissileModelAssociationsMixedLoadoutKeepsCandidatesOnly(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket", 1,
		groundmeta.StorageWeapon{Launcher: "jp_aam3", Count: 2},
		groundmeta.StorageWeapon{Launcher: "su_r_73", Count: 2})
	applyMissileModelNames([]*track{tr}, nil)
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 0 {
		t.Fatalf("mixed loadout adopted %d models", adopted)
	}
	info := tr.missileModel
	if info.Model != "" || info.InternalName != "" {
		t.Fatalf("mixed loadout selected a model: %+v", info)
	}
	want := []string{"AAM-3", "R-73"}
	if strings.Join(info.ModelCandidates, " / ") != strings.Join(want, " / ") {
		t.Fatalf("candidates = %#v, want %#v", info.ModelCandidates, want)
	}
	if tr.name != "AAM (unk)" {
		t.Fatalf("mixed loadout changed fallback name to %q", tr.name)
	}
}

func TestMissileModelAssociationsFilterMixedLoadoutByTemplateGuidance(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "cn_pl12", Count: 2},
		groundmeta.StorageWeapon{Launcher: "jp_aam3", Count: 2})
	applyMissileModelNames([]*track{tr}, nil)
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 1 {
		t.Fatalf("ARH template filter adopted = %d, want 1", adopted)
	}
	if tr.missileModel.Model != "PL-12" || tr.missileModel.InternalName != "cn_pl12" {
		t.Fatalf("ARH template filter = %+v", tr.missileModel)
	}
}

func TestMissileModelAssociationsIRSeekerDisambiguatesMixedIRAndSARH(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket", 1,
		groundmeta.StorageWeapon{Launcher: "jp_aam3", Count: 2},
		groundmeta.StorageWeapon{Launcher: "su_r_27er", Count: 2})
	missileAssociationAttachSeeker(tr, missileAssociationIRMotion())
	applyMissileModelNames([]*track{tr}, nil)
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 1 {
		t.Fatalf("IR seeker filter adopted = %d, want 1", adopted)
	}
	if tr.missileModel.Model != "AAM-3" || tr.missileModel.InternalName != "jp_aam3" {
		t.Fatalf("IR seeker filter = %+v", tr.missileModel)
	}
}

func TestMissileModelAssociationsRadarSeekerDoesNotChooseBetweenARHModels(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "us_aim_120a", Count: 2},
		groundmeta.StorageWeapon{Launcher: "cn_pl12", Count: 2})
	missileAssociationAttachSeeker(tr, missileAssociationRadarMotion())
	applyMissileModelNames([]*track{tr}, nil)
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 0 {
		t.Fatalf("radar-only evidence selected a model: %d", adopted)
	}
	if tr.missileModel.Model != "" || strings.Join(tr.missileModel.ModelCandidates, " / ") != "AIM-120A / PL-12" {
		t.Fatalf("radar candidates = %+v", tr.missileModel)
	}
}

func TestMissileModelAssociationsRejectConflictingSeekerKinds(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "cn_pl12", Count: 2})
	missileAssociationAttachSeeker(tr, missileAssociationIRMotion())
	missileAssociationAttachSeeker(tr, missileAssociationRadarMotion())
	applyMissileModelNames([]*track{tr}, nil)
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 0 {
		t.Fatalf("conflicting IR/radar evidence adopted %d models", adopted)
	}
	if tr.missileModel.Model != "" {
		t.Fatalf("conflicting seeker evidence produced %q", tr.missileModel.Model)
	}
}

func TestMissileModelAssociationsSeekerKindBlocksPropagation(t *testing.T) {
	table := missileAssociationTestTable()
	anchor := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket", 1)
	target := missileAssociationTestTrack(2, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket", 1)
	for _, tr := range []*track{anchor, target} {
		applyMissileModelNames([]*track{tr}, nil)
	}
	anchor.missileModel.Model = "AAM-3"
	anchor.missileModel.InternalName = "jp_aam3"
	anchor.missileModel.Source = "KillReportMatched"
	anchor.missileModel.Confidence = "Matched"
	missileAssociationAttachSeeker(target, missileAssociationRadarMotion())

	if adopted := applyMissileModelAssociations([]*track{anchor, target}, table); adopted != 0 {
		t.Fatalf("IR model propagated to radar evidence: %d", adopted)
	}
	if target.missileModel.Model != "" {
		t.Fatalf("seeker constraint did not block propagation: %+v", target.missileModel)
	}
}

func TestMissileModelAssociationsWithoutAmmunitionDoNothing(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	applyMissileModelNames([]*track{tr}, nil)
	before := *tr.missileModel
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 0 {
		t.Fatalf("no-ammunition track adopted %d models", adopted)
	}
	if tr.missileModel.Model != "" || len(tr.missileModel.ModelCandidates) != 0 || tr.name != "AAM (ARH)" {
		t.Fatalf("no-ammunition track changed: %+v name=%q", tr.missileModel, tr.name)
	}
	if tr.missileModel.Source != before.Source || tr.missileModel.Confidence != before.Confidence {
		t.Fatalf("no-ammunition track changed confidence: before=%+v after=%+v", before, tr.missileModel)
	}
}

func TestMissileModelAssociationsRequireExplicitAliases(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "cn_pl12_slot3", Count: 2})
	applyMissileModelNames([]*track{tr}, nil)
	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 0 {
		t.Fatalf("decorated slot name adopted %d models", adopted)
	}
	if tr.missileModel.Model != "" || len(tr.missileModel.ModelCandidates) != 0 {
		t.Fatalf("decorated slot name became evidence: %+v", tr.missileModel)
	}
}

func TestMissileModelAssociationsDoNotCrossParents(t *testing.T) {
	table := missileAssociationTestTable()
	anchor := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	sameParent := missileAssociationTestTrack(2, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	otherParent := missileAssociationTestTrack(3, 0x002, 22, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	for _, tr := range []*track{anchor, sameParent, otherParent} {
		applyMissileModelNames([]*track{tr}, nil)
	}
	anchor.missileModel.Model = "PL-12"
	anchor.missileModel.InternalName = "cn_pl12"
	anchor.missileModel.Source = "KillReportMatched"
	anchor.missileModel.Confidence = "Matched"

	if adopted := applyMissileModelAssociations([]*track{anchor, sameParent, otherParent}, table); adopted != 1 {
		t.Fatalf("adopted = %d, want one same-parent propagation", adopted)
	}
	if sameParent.missileModel.Model != "PL-12" || sameParent.missileModel.Source != missileAssociationSourcePropagated {
		t.Fatalf("same-parent propagation = %+v", sameParent.missileModel)
	}
	if otherParent.missileModel.Model != "" || len(otherParent.missileModel.ModelCandidates) != 0 {
		t.Fatalf("same weapon ID crossed to another parent: %+v", otherParent.missileModel)
	}
}

func TestMissileModelAssociationsDoNotCrossHandleGenerations(t *testing.T) {
	table := missileAssociationTestTable()
	anchor := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	sameGeneration := missileAssociationTestTrack(2, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	newHandle := missileAssociationTestTrack(3, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 2)
	newOwner := missileAssociationTestTrack(4, 0x002, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	for _, tr := range []*track{anchor, sameGeneration, newHandle, newOwner} {
		applyMissileModelNames([]*track{tr}, nil)
	}
	anchor.missileModel.Model = "PL-12"
	anchor.missileModel.InternalName = "cn_pl12"
	anchor.missileModel.Source = "KillReportMatched"
	anchor.missileModel.Confidence = "Matched"

	if adopted := applyMissileModelAssociations([]*track{anchor, sameGeneration, newHandle, newOwner}, table); adopted != 1 {
		t.Fatalf("adopted = %d, want only the same generation", adopted)
	}
	if sameGeneration.missileModel.Model != "PL-12" {
		t.Fatalf("same generation did not propagate: %+v", sameGeneration.missileModel)
	}
	if newHandle.missileModel.Model != "" || newOwner.missileModel.Model != "" {
		t.Fatalf("generation boundary leaked: handle=%+v owner=%+v", newHandle.missileModel, newOwner.missileModel)
	}
}

func TestMissileModelAssociationsRejectConflictingGroupAnchors(t *testing.T) {
	table := missileAssociationTestTable()
	first := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	second := missileAssociationTestTrack(2, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	target := missileAssociationTestTrack(3, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1)
	for _, tr := range []*track{first, second, target} {
		applyMissileModelNames([]*track{tr}, nil)
	}
	first.missileModel.Model = "PL-12"
	first.missileModel.InternalName = "cn_pl12"
	first.missileModel.Source = "KillReportMatched"
	first.missileModel.Confidence = "Matched"
	second.missileModel.Model = "AIM-120A"
	second.missileModel.InternalName = "us_aim_120a"
	second.missileModel.Source = "ReplayWeaponIDFromModelTable"
	second.missileModel.Confidence = "confirmed"

	if adopted := applyMissileModelAssociations([]*track{first, second, target}, table); adopted != 0 {
		t.Fatalf("conflicting anchors propagated %d models", adopted)
	}
	if target.missileModel.Model != "" {
		t.Fatalf("conflicting anchors produced %q", target.missileModel.Model)
	}
	if !strings.Contains(target.missileModel.ModelConflict, "owner group") {
		t.Fatalf("group conflict was not recorded: %+v", target.missileModel)
	}
}

func TestMissileModelAssociationsConflictDoesNotReplaceKnownModel(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Launcher: "us_aim_120a", Count: 2})
	applyMissileModelNames([]*track{tr}, nil)
	tr.missileModel.Model = "PL-12"
	tr.missileModel.InternalName = "cn_pl12"
	tr.missileModel.Source = "KillReportMatched"
	tr.missileModel.Confidence = "Matched"
	tr.name = "PL-12"

	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 0 {
		t.Fatalf("conflicting owner evidence adopted %d models", adopted)
	}
	info := tr.missileModel
	if info.Model != "PL-12" || info.InternalName != "cn_pl12" || info.Source != "KillReportMatched" ||
		info.Confidence != "Matched" || tr.name != "PL-12" {
		t.Fatalf("known model was replaced: %+v name=%q", info, tr.name)
	}
	if !strings.Contains(info.ModelConflict, "existing=PL-12") || !strings.Contains(info.ModelConflict, "owner=AIM-120A") {
		t.Fatalf("conflict detail = %q", info.ModelConflict)
	}
	if strings.Join(info.ModelCandidates, " / ") != "AIM-120A" {
		t.Fatalf("conflict candidates = %#v", info.ModelCandidates)
	}
}

func TestMissileModelAssociationsPreserveMatchingKnownModel(t *testing.T) {
	table := missileAssociationTestTable()
	tr := missileAssociationTestTrack(1, 0x001, 11, 35, missileNameArmamentTriggerAAM, "rocket+arh_shell", 1,
		groundmeta.StorageWeapon{Bullet: "cn_pl12_default", Count: 1})
	applyMissileModelNames([]*track{tr}, nil)
	tr.missileModel.Model = "PL-12"
	tr.missileModel.InternalName = "cn_pl12"
	tr.missileModel.Source = "KillReportMatched"
	tr.missileModel.Confidence = "Matched"
	tr.name = "PL-12"

	if adopted := applyMissileModelAssociations([]*track{tr}, table); adopted != 0 {
		t.Fatalf("matching known model adopted = %d", adopted)
	}
	info := tr.missileModel
	if info.Model != "PL-12" || info.InternalName != "cn_pl12" || info.Source != "KillReportMatched" ||
		info.Confidence != "Matched" || info.ModelConflict != "" || tr.name != "PL-12" {
		t.Fatalf("matching known model changed: %+v name=%q", info, tr.name)
	}
}

func TestMissileModelAssociationsIgnoreNonMissiles(t *testing.T) {
	table := missileAssociationTestTable()
	decoy := &track{key: trackKey{decoy: true, id: 9}, name: "Existing decoy"}
	bomb := &track{key: trackKey{bomb: true, id: 10}, name: "Bomb"}
	if adopted := applyMissileModelAssociations([]*track{decoy, bomb}, table); adopted != 0 {
		t.Fatalf("non-missiles adopted %d models", adopted)
	}
	if decoy.missileModel != nil || decoy.name != "Existing decoy" || bomb.missileModel != nil || bomb.name != "Bomb" {
		t.Fatalf("non-missile mutated: decoy=%+v bomb=%+v", decoy, bomb)
	}
}

func TestMissileModelAssociationsRespectKindAndTemplateConstraints(t *testing.T) {
	table := missileAssociationTestTable()
	pm := groundmeta.StorageWeapon{Launcher: "cn_pl12", Count: 2}
	cases := []struct {
		name     string
		trigger  uint32
		template string
		want     string
	}{
		{name: "AAM ARH shell", trigger: missileNameArmamentTriggerAAM, template: "rocket+arh_shell", want: "PL-12"},
		{name: "AAM plain rocket", trigger: missileNameArmamentTriggerAAM, template: "rocket", want: ""},
		{name: "AGM with AAM alias", trigger: missileNameArmamentTriggerAGM, template: "rocket+arh_shell", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := missileAssociationTestTrack(1, 0x001, 11, 35, tc.trigger, tc.template, 1, pm)
			applyMissileModelNames([]*track{tr}, nil)
			applyMissileModelAssociations([]*track{tr}, table)
			if tr.missileModel.Model != tc.want {
				t.Fatalf("model = %q, want %q (%+v)", tr.missileModel.Model, tc.want, tr.missileModel)
			}
		})
	}
}
