package main

import (
	"math"
	"strings"
	"testing"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

func TestMissileNameKillParserRequiresPresentUIDAndWeapon(t *testing.T) {
	parser := newMissileNameKillParser()
	valid := missileNameTestKillPacket(t, 1234, true, 77, true, "cn_pl12")
	if _, err := parser.Parse(valid); err != nil {
		t.Fatalf("valid kill parse error: %v", err)
	}
	if len(parser.Kills) != 1 {
		t.Fatalf("valid kill records = %d, want 1", len(parser.Kills))
	}
	got := parser.Kills[0]
	if got.Time != 1234 || got.KillerUID != 77 || got.Weapon != "cn_pl12" {
		t.Fatalf("parsed kill = %+v", got)
	}

	for name, pk := range map[string]*packet.Packet{
		"missing killer UID": missileNameTestKillPacket(t, 2000, false, 0, true, "cn_pl12"),
		"missing weapon":     missileNameTestKillPacket(t, 2000, true, 77, false, ""),
		"empty weapon":       missileNameTestKillPacket(t, 2000, true, 77, true, ""),
		"short packet":       {PacketType: 4, PacketPayload: []byte{0x02, 0x58, 0x58, 0xf0}},
	} {
		before := len(parser.Kills)
		if _, err := parser.Parse(pk); err != nil {
			t.Fatalf("%s returned an export-blocking error: %v", name, err)
		}
		if len(parser.Kills) != before {
			t.Fatalf("%s produced a kill record", name)
		}
	}
	if parser.Skipped < 4 {
		t.Fatalf("skipped records = %d, want malformed records counted", parser.Skipped)
	}
}

func TestMissileWeaponIDRejectsLeadingOverflowBounds(t *testing.T) {
	tests := []struct {
		name    string
		leading uint64
		want    uint32
		ok      bool
	}{
		{name: "below base", leading: 128},
		{name: "base", leading: 129, want: 0, ok: true},
		{name: "last uint32", leading: 129 + uint64(math.MaxUint32), want: math.MaxUint32, ok: true},
		{name: "uint32 overflow", leading: 130 + uint64(math.MaxUint32)},
		{name: "uint64 max", leading: ^uint64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := missileWeaponIDFromLeading(tt.leading)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("decode(%d) = %d/%v, want %d/%v", tt.leading, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestMissileModelUniqueKillAndLeadingGroupPropagation(t *testing.T) {
	directSource := missileNameTestSource(77, 1020, 161, 8, "rocket+arh_shell")
	direct := missileNameTestTrack(1, directSource, true)
	propagatedSource := missileNameTestSource(88, 3000, 161, 8, "rocket+arh_shell")
	propagated := missileNameTestTrack(2, propagatedSource, true)

	applyMissileModelNames([]*track{direct, propagated}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1040, KillerUID: 77, Weapon: "cn_pl12_default"},
	}})

	if direct.missileModel == nil || direct.missileModel.Model != "PL-12" || direct.name != "PL-12" {
		t.Fatalf("direct match = %+v name=%q", direct.missileModel, direct.name)
	}
	if direct.missileModel.Source != "KillReportMatched" || direct.missileModel.Confidence != "Matched" {
		t.Fatalf("direct source/confidence = %+v", direct.missileModel)
	}
	if direct.missileModel.WeaponID != 32 || !direct.missileModel.HasWeaponID || direct.missileModel.InternalName != "cn_pl12" {
		t.Fatalf("direct evidence = %+v", direct.missileModel)
	}
	if propagated.missileModel == nil || propagated.missileModel.Model != "PL-12" || propagated.name != "PL-12" {
		t.Fatalf("same-leading propagation = %+v name=%q", propagated.missileModel, propagated.name)
	}
	if propagated.missileModel.Source != "ReplayWeaponIDFromKillReport" || propagated.missileModel.Confidence != "Inferred" {
		t.Fatalf("propagated source/confidence = %+v", propagated.missileModel)
	}
	if directSource.Name != "Missile" || propagatedSource.Name != "Missile" {
		t.Fatal("applyMissileModelNames changed Projectile.Name")
	}
	props := strings.Join(missileModelProperties(direct), ",")
	for _, want := range []string{
		"WT_WeaponID=32", "WT_MissileModel=PL-12", "WT_MissileInternalName=cn_pl12",
		"WT_MissileModelSource=KillReportMatched", "WT_MissileModelConfidence=Matched",
	} {
		if !strings.Contains(props, want) {
			t.Fatalf("properties %q missing %q", props, want)
		}
	}
}

func TestMissileModelPropagationRequiresExactTemplate(t *testing.T) {
	direct := missileNameTestTrack(1, missileNameTestSource(77, 1000, 161, 8, "rocket+arh_shell"), true)
	sameTemplate := missileNameTestTrack(2, missileNameTestSource(88, 3000, 161, 8, "rocket+arh_shell"), true)
	differentTemplate := missileNameTestTrack(3, missileNameTestSource(99, 4000, 161, 8, "rocket"), true)
	applyMissileModelNames([]*track{direct, sameTemplate, differentTemplate}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1020, KillerUID: 77, Weapon: "cn_pl12"},
	}})
	if direct.missileModel.Model != "PL-12" || sameTemplate.missileModel.Model != "PL-12" {
		t.Fatalf("exact-template group was not named: direct=%+v sibling=%+v", direct.missileModel, sameTemplate.missileModel)
	}
	if differentTemplate.missileModel == nil || differentTemplate.missileModel.Model != "" || differentTemplate.name != "AAM (unk)" {
		t.Fatalf("same ID leaked across templates: %+v name=%q", differentTemplate.missileModel, differentTemplate.name)
	}
}

func TestMissileModelRejectsDifferentLeadingInSameWindow(t *testing.T) {
	first := missileNameTestTrack(1, missileNameTestSource(77, 1000, 161, 8, "rocket+arh_shell"), true)
	second := missileNameTestTrack(2, missileNameTestSource(77, 1050, 162, 8, "rocket+arh_shell"), true)
	applyMissileModelNames([]*track{first, second}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1020, KillerUID: 77, Weapon: "cn_pl12"},
	}})
	for _, tr := range []*track{first, second} {
		if tr.missileModel == nil || tr.missileModel.Model != "" {
			t.Fatalf("near-simultaneous different leading produced a model: %+v", tr.missileModel)
		}
	}
}

func TestMissileModelConflictingWeaponsRollbackWholeLeadingGroup(t *testing.T) {
	first := missileNameTestTrack(1, missileNameTestSource(77, 1000, 161, 8, "rocket+arh_shell"), true)
	second := missileNameTestTrack(2, missileNameTestSource(88, 3000, 161, 8, "rocket+arh_shell"), true)
	applyMissileModelNames([]*track{first, second}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1020, KillerUID: 77, Weapon: "cn_pl12"},
		{Time: 3020, KillerUID: 88, Weapon: "us_aim_120a_default"},
	}})
	for _, tr := range []*track{first, second} {
		if tr.missileModel == nil || tr.missileModel.Model != "" {
			t.Fatalf("conflicting leading group retained a model: %+v", tr.missileModel)
		}
	}
}

func TestMissileModelDoesNotLeakAcrossApplyCallsOrUseUnknownAlias(t *testing.T) {
	tr := missileNameTestTrack(1, missileNameTestSource(77, 1000, 161, 8, "rocket+arh_shell"), true)
	applyMissileModelNames([]*track{tr}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1020, KillerUID: 77, Weapon: "cn_pl12"},
	}})
	if tr.missileModel.Model != "PL-12" {
		t.Fatalf("first apply model = %q", tr.missileModel.Model)
	}
	applyMissileModelNames([]*track{tr}, newMissileNameKillParser())
	if tr.missileModel == nil || tr.missileModel.Model != "" || tr.name != "AAM (ARH)" {
		t.Fatalf("empty replay leaked a model: %+v name=%q", tr.missileModel, tr.name)
	}

	applyMissileModelNames([]*track{tr}, &missileNameKillParser{Kills: []missileNameKill{
		{Time: 1020, KillerUID: 77, Weapon: "cn_pl12_extra"},
	}})
	if tr.missileModel.Model != "" {
		t.Fatalf("unknown alias string was used as evidence: %+v", tr.missileModel)
	}
}

func TestMissileModelFallbackAndDecoyIsolation(t *testing.T) {
	arh := missileNameTestTrack(1, missileNameTestSource(77, 1000, 0, 8, "rocket+arh_shell"), true)
	aam := missileNameTestTrack(2, missileNameTestSource(77, 1000, 0, 8, "rocket"), true)
	agm := missileNameTestTrack(3, missileNameTestSource(77, 1000, 0, 7, "rocket"), true)
	decoy := missileNameTestTrack(4, missileNameTestSource(77, 1000, 0, 14, "rocket"), false)
	decoy.name = "Existing decoy"
	decoy.key = trackKey{decoy: true, id: 4}

	applyMissileModelNames([]*track{arh, aam, agm, decoy}, newMissileNameKillParser())
	wants := map[*track]string{arh: "AAM (ARH)", aam: "AAM (unk)", agm: "AGM (unknown)"}
	for tr, want := range wants {
		if tr.name != want || tr.missileModel == nil || tr.missileModel.Model != "" {
			t.Fatalf("fallback name=%q model=%+v, want name=%q and empty model", tr.name, tr.missileModel, want)
		}
	}
	if decoy.name != "Existing decoy" || decoy.missileModel != nil || missileModelProperties(decoy) != nil {
		t.Fatalf("decoy was modified: name=%q model=%+v props=%v", decoy.name, decoy.missileModel, missileModelProperties(decoy))
	}
}

func missileNameTestSource(parent, end uint32, leading uint64, trigger uint32, template string) *projectiles.Projectile {
	return &projectiles.Projectile{
		Name: "Missile", Template: template, LeadingULEB: leading,
		ParentUID: uint64(parent), HasParent: true,
		WeaponRef: trigger << 16, EndTime: end, HasEnd: true,
	}
}

func missileNameTestTrack(id uint64, source *projectiles.Projectile, hasEnd bool) *track {
	return &track{
		key: trackKey{missile: true, id: id}, name: source.Name,
		projectileSource: source, end: source.EndTime, hasEnd: hasEnd,
		samples: []acmiSample{{t: source.SpawnSample.Time}},
	}
}

type missileNameTestBits struct {
	data []byte
	used int
}

func (w *missileNameTestBits) write(value uint32, count int) {
	for bit := count - 1; bit >= 0; bit-- {
		if w.used&7 == 0 {
			w.data = append(w.data, 0)
		}
		if value>>bit&1 != 0 {
			w.data[w.used>>3] |= byte(1) << (7 - (w.used & 7))
		}
		w.used++
	}
}

func missileNameTestULEB(value uint32) []byte {
	var out []byte
	for {
		part := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			part |= 0x80
		}
		out = append(out, part)
		if value == 0 {
			return out
		}
	}
}

func missileNameTestKillPacket(t *testing.T, time uint32, includeKiller bool, killer uint16, includeWeapon bool, weapon string) *packet.Packet {
	t.Helper()
	if len(weapon) > 7 {
		t.Fatalf("test weapon %q does not fit the fixed-size test field", weapon)
	}
	fields := uint32(0)
	body := make([]byte, 0)
	if includeKiller {
		fields |= 1 << 4
		body = append(body, byte(killer), byte(killer>>8))
	}
	if includeWeapon {
		fields |= 1 << 10
		body = append(body, byte(len(weapon)))
		body = append(body, []byte(weapon)...)
		body = append(body, make([]byte, 7-len(weapon))...)
	}
	header := make([]byte, 2)
	header = append(header, missileNameTestULEB(fields)...)
	offset := len(header) + len(body)
	if offset > int(^uint16(0)) {
		t.Fatal("test packet offset overflow")
	}
	header[0], header[1] = byte(offset), byte(offset>>8)

	payload := []byte{0x02, 0x58, 0x58, 0xf0}
	payload = append(payload, header...)
	payload = append(payload, body...)
	var sizes missileNameTestBits
	if includeKiller {
		sizes.write(3, 3) // 16-bit field
	}
	if includeWeapon {
		sizes.write(5, 3) // 64-bit field
	}
	payload = append(payload, sizes.data...)
	return &packet.Packet{PacketType: 4, CurrentTime: time, PacketPayload: payload}
}
