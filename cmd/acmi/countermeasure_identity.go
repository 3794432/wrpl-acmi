package main

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
)

//go:embed data/countermeasure_ammo.json
var countermeasureAmmoJSON []byte

type countermeasureAmmoCatalog struct {
	Schema    int                          `json:"schema"`
	Commit    string                       `json:"commit"`
	Launchers map[string]map[string]string `json:"launchers"`
}

var countermeasureAmmo = func() countermeasureAmmoCatalog {
	var catalog countermeasureAmmoCatalog
	if err := json.Unmarshal(countermeasureAmmoJSON, &catalog); err != nil || catalog.Schema != 1 {
		panic("invalid embedded countermeasure ammunition catalogue")
	}
	return catalog
}()

type countermeasureIdentity struct {
	Kind, Source, Confidence, Launcher, Bullet string
}

func effectiveCountermeasureIdentity(identity countermeasureIdentity) countermeasureIdentity {
	if identity.Kind == "Flare" || identity.Kind == "Chaff" {
		return identity
	}
	identity.Kind = "Chaff"
	if identity.Source == "" {
		identity.Source = "NoAmmunitionEvidence"
	}
	identity.Source = "UnknownAsChaff:" + identity.Source
	identity.Confidence = "fallback"
	return identity
}

func countermeasureIdentities(metadata *projectiles.MetadataParser) map[*projectiles.Projectile]countermeasureIdentity {
	out := make(map[*projectiles.Projectile]countermeasureIdentity)
	if metadata == nil {
		return out
	}
	for _, history := range metadata.History {
		for _, source := range history {
			if !isCountermeasureProjectile(source) {
				continue
			}
			out[source] = effectiveCountermeasureIdentity(countermeasureSingleAmmoIdentity(source))
		}
	}
	return out
}

// Only homogeneous ammunition is direct identity evidence. Empty bullet names
// select the launcher's explicit default block. Neither the low WeaponRef
// index nor the order of storage rows selects an ammunition type.
func countermeasureSingleAmmoIdentity(source *projectiles.Projectile) countermeasureIdentity {
	if !isCountermeasureProjectile(source) {
		return countermeasureIdentity{}
	}
	var identity countermeasureIdentity
	for _, ammo := range source.OwnerWeapons {
		if ammo.Count == 0 {
			continue
		}
		bullets, known := countermeasureAmmo.Launchers[ammo.Launcher]
		if !known {
			if strings.Contains(strings.ToLower(ammo.Launcher), "countermeasure") {
				return countermeasureIdentity{Source: "UnknownLauncher"}
			}
			continue
		}
		bullet := ammo.Bullet
		if bullet == "" {
			bullet = "bullet"
		}
		kind := bullets[bullet]
		if kind != "Chaff" && kind != "Flare" {
			return countermeasureIdentity{Source: "UnknownAmmunition"}
		}
		if identity.Kind != "" && identity.Kind != kind {
			return countermeasureIdentity{Source: "MixedAmmunition"}
		}
		identity = countermeasureIdentity{Kind: kind, Source: "OwnerSingleAmmoType+Datamine", Confidence: "confirmed", Launcher: ammo.Launcher, Bullet: bullet}
	}
	if identity.Kind == "" {
		identity.Source = "NoAmmunitionEvidence"
	}
	return identity
}
