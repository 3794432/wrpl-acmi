package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	inspectordata "github.com/maxsupermanhd/wrpl-inspector/v3/data"
	"github.com/maxsupermanhd/wrpl-inspector/v3/internal/terrainmap"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet"
	packetecs2 "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/ecs2"
	packetfm "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/fm"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/groundmeta"
	packetkill "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/kill"
	packetmovement "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/movement"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/projectiles"
	packetslot "github.com/maxsupermanhd/wrpl-inspector/v3/wrpl/packet/parser/slot"
)

var (
	inPath               = flag.String("in", "", "input .wrpl replay or server replay shard directory")
	outPath              = flag.String("out", "", "output .acmi (default: input name with .acmi or .partial.acmi)")
	allowIncomplete      = flag.Bool("allow-incomplete", true, "export available server replay parts when the tail is missing; set false to require a complete replay")
	mapIDOverride        = flag.String("map-id", "", "explicit map id: canonical name, WarThunder.Geo1.<name>, or WarThunder.Atlas1.<name>")
	terrainLayoutName    = flag.String("terrain-layout", "geographic", "terrain placement: geographic (default, reviewed georeferences with atlas fallback) or atlas")
	alt0                 = flag.Float64("alt", 0, "altitude offset in meters")
	hashPath             = flag.String("ecshashes", "embedded", "ECS component hash map: embedded (default), empty to disable, or a JSON path")
	namesPath            = flag.String("names", "", "optional JSON map: air:<uid> / ground:<eid> / missile:<eid> -> display name")
	listPath             = flag.String("list", "", "optional CSV listing of tracks and current names")
	missileFlightLogPath = flag.String("missile-flight-log", "", "optional CSV of missile flight parameters and raw motion evidence")
	missileModelsSpec    = flag.String("missile-models", "embedded", "missile model table: embedded (default), off, or a schema-1 JSON path")
	missileGuidanceSpec  = flag.String("missile-guidance", "embedded", "missile guidance table: embedded (default), off, or a schema-1 JSON path")
	missileSeekers       = flag.Bool("missile-seeker", true, "export recorded post-launch seeker states and supported native Tacview lock properties")
	countermeasures      = flag.Bool("countermeasures", true, "export aircraft countermeasure releases as Misc+Decoy events and tracks (real sync, or an estimated short trajectory when no sync exists)")
	countermeasureLog    = flag.String("countermeasure-log", "", "optional CSV of every trigger-14 countermeasure construction")
	radarLocks           = flag.Bool("radar-locks", true, "export War Thunder radar target designations as Tacview LockedTarget properties")
	radarModes           = flag.Bool("radar-modes", true, "export recorded aircraft sensor modes and supported native Tacview radar properties")
	radarLockLog         = flag.String("radar-lock-log", "", "optional CSV of every radar lock state change")
	groundUnits          = flag.Bool("ground-units", true, "export recorded GroundModelPositions surface-unit tracks")
	groundModels         = flag.Bool("ground-models", true, "identify surface-unit models from explicit replay metadata")
	estimateTelemetry    = flag.Bool("estimate-telemetry", true, "estimate aircraft telemetry from position and attitude (default on; set false to disable; zero wind, ISA; not recorded game telemetry)")
	timelineBookmarks    = flag.Bool("bookmarks", false, "opt-in highlighted bookmarks for destruction, weapon releases, countermeasures and radar designation changes")
	timelineEventsSpec   = flag.String("events", defaultTimelineEvents, "message categories: kill,chat,join,damage,radar by default; award is opt-in; all enables every category; none disables")
	recordingAuthor      = flag.String("author", "wrpl-acmi", "recording author/operator for ACMI file properties")
	acmiFormatVersion    = flag.String("acmi-version", "2.1", "ACMI file version: 2.1 (default for compatibility) or 2.2 (opt-in)")
	airfields            = flag.Bool("airfields", true, "export source-configured map runways, validating conditional airport variants with replay radars")
)

// Atlas anchors are selected from the shared catalog, never from manual flags.
var atlasLatitude, atlasLongitude float64

type terrainLayout string

const (
	terrainLayoutAtlas      terrainLayout = "atlas"
	terrainLayoutGeographic terrainLayout = "geographic"
)

func parseTerrainLayout(value string) (terrainLayout, error) {
	switch layout := terrainLayout(strings.ToLower(strings.TrimSpace(value))); layout {
	case terrainLayoutAtlas, terrainLayoutGeographic:
		return layout, nil
	default:
		return "", fmt.Errorf("invalid -terrain-layout %q: use geographic or atlas", value)
	}
}

func currentTerrainLayout() (terrainLayout, error) {
	return parseTerrainLayout(*terrainLayoutName)
}

func applyAtlasReference(mapID string) error {
	atlasLatitude, atlasLongitude = 0, 0
	if mapID == "" {
		return nil
	}
	latitude, longitude, ok := terrainmap.AtlasReference(mapID)
	if !ok {
		return fmt.Errorf("atlas reference missing for map %q", mapID)
	}
	atlasLatitude, atlasLongitude = latitude, longitude
	return nil
}

type trackKey struct {
	air          bool
	missile      bool
	bomb         bool
	bombWire     bool
	groundEntity bool
	decoy        bool
	id           uint64
	sortie       uint32 // distinguishes successive source units using the same replay UID
}

// missileTrack is the parser-to-writer adapter. ID is the full generation EID,
// rather than an entity-array index, so recycled indices remain distinct.
type missileTrack struct {
	ID        uint64
	Name      string
	ParentUID uint64
	HasParent bool
	Samples   []acmiSample
	EndTime   uint32
	HasEnd    bool
	Source    *projectiles.Projectile
}

type acmiSample struct {
	t          uint32
	x          float64
	y          float64
	z          float64
	roll       float64
	pitch      float64
	yaw        float64
	projectile *projectiles.Sample
}

type track struct {
	key                  trackKey
	entity               *packetecs2.Entity
	samples              []acmiSample
	name                 string
	parent               *track
	end                  uint32
	hasEnd               bool
	retired              bool // replacement without a confirmed destruction report
	projectileSource     *projectiles.Projectile
	countermeasure       countermeasureIdentity
	missileModel         *missileModelInfo
	missileGuidance      *missileGuidanceInfo
	bombPositionOnly     bool
	groundModel          *groundModelInfo
	groundTeam           int32
	groundTeamSource     string
	groundStatic         bool
	groundModelsDisabled bool
	// estimated marks a countermeasure track reconstructed from a release
	// point and the parent aircraft velocity, not from WeaponSync telemetry.
	estimated         bool
	estimatedVelocity bool
}

var errNoInput = errors.New("no input replay specified")

func main() {
	if err := runACMIMain(); err != nil {
		if errors.Is(err, errNoInput) {
			flag.Usage()
			os.Exit(2)
		}
		fatal(err)
	}
}

func runACMIMain() (retErr error) {
	flag.Parse()
	enabledEvents, err := parseTimelineEvents(*timelineEventsSpec)
	if err != nil {
		return err
	}
	if err := validateACMIVersion(*acmiFormatVersion); err != nil {
		return err
	}
	inputPath, err := resolveCLIInput(*inPath, flag.Args())
	if err != nil {
		return err
	}
	if inputPath == "" {
		return errNoInput
	}
	*inPath = inputPath

	guidance, err := loadMissileGuidance(*missileGuidanceSpec)
	if err != nil {
		return err
	}
	missileModelTable, err := loadMissileModels(*missileModelsSpec)
	if err != nil {
		return err
	}
	layout, err := parseTerrainLayout(*terrainLayoutName)
	if err != nil {
		return err
	}
	*terrainLayoutName = string(layout)
	if *mapIDOverride != "" {
		normalized, err := terrainmap.NormalizeMapID(*mapIDOverride)
		if err != nil {
			return err
		}
		*mapIDOverride = normalized
	}

	input, err := openReplayInputWithOptions(*inPath, *allowIncomplete)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := input.Close(); closeErr != nil {
			closeErr = fmt.Errorf("closing replay input: %w", closeErr)
			if retErr == nil {
				retErr = closeErr
			} else {
				retErr = errors.Join(retErr, closeErr)
			}
		}
	}()

	if *outPath == "" {
		incomplete := input.isDirectory() && input.directory.Incomplete
		*outPath = defaultACMIOutputNameForState(*inPath, input.isDirectory(), incomplete)
	}
	inputPaths := []string{input.path}
	if input.isDirectory() {
		inputPaths = input.directory.Paths
	}
	if err := checkOutputDoesNotOverwriteInput(*outPath, inputPaths); err != nil {
		return err
	}

	replaySHA := ""
	if (guidance != nil && len(guidance.Replays) > 0) || (missileModelTable != nil && len(missileModelTable.Replays) > 0) {
		if input.isDirectory() {
			replaySHA, err = hashReplayDirectory(input.directory)
		} else {
			replaySHA, err = hashMissileGuidanceReplay(*inPath)
		}
		if err != nil {
			return err
		}
	}
	if input.isDirectory() {
		fmt.Printf("input: server replay directory, session %016x, parts %d, incomplete=%t\n",
			input.directory.SessionID, len(input.directory.PartNumbers), input.directory.Incomplete)
		if input.directory.Incomplete {
			fmt.Fprintf(os.Stderr, "WARNING: incomplete server replay; exporting the available portion as a partial ACMI: %s\n",
				replayIncompleteReason(input.directory))
		}
	}
	replay := input.reader

	resolvedMapID, err := resolveReplayMapID(replay.Header, *mapIDOverride)
	if err != nil {
		return err
	}
	if err := applyAtlasReference(resolvedMapID); err != nil {
		return err
	}
	if resolvedMapID == "" {
		fmt.Fprintln(os.Stderr, "warning: replay map id is unknown; no terrain reference, T uses local coordinates (use -map-id to override)")
	}
	if layout == terrainLayoutGeographic && resolvedMapID != "" {
		profile, ok := terrainmap.GeographicReference(resolvedMapID)
		if !ok || !profile.Enabled {
			fmt.Fprintf(os.Stderr, "terrain: %s uses Atlas fallback (%s)\n", resolvedMapID, geographicFallbackReason(profile, ok))
		} else {
			fmt.Printf("terrain: %s uses reviewed geographic placement (%s; %s)\n", resolvedMapID, profile.Confidence, profile.Method)
		}
	}

	chms, err := readHashes(*hashPath)
	if err != nil {
		return err
	}
	var ecs *packetecs2.PacketECSParser
	var mgr *packetecs2.EntityManager
	var missileMetadata *projectiles.MetadataParser
	var groundMetadata *groundmeta.Parser
	if chms != nil {
		ecs = packetecs2.NewPacketECSParser(*chms)
		ecs.ConstructionReservedByte = replay.Header.Version == 0x00018C1C
		mgr = &ecs.Mgr
		missileMetadata = projectiles.NewMetadataParser(*chms, replay.Header.Version == 0x00018C1C)
		if *groundUnits || *airfields {
			groundMetadata = groundmeta.NewParser(*chms, replay.Header.Version == 0x00018C1C)
		}
	}
	slot := &packetslot.PacketSlotParser{}
	fm := &packetfm.PacketFlightModelParser{KeepResults: true, ECS: mgr}
	paths := packetmovement.NewPositionRetainerParser()
	if *groundUnits {
		paths = packetmovement.NewPositionRetainerParserWithECS(ecs)
	}
	missilePositions := projectiles.NewPositionParser()
	missileNames := newMissileNameKillParser()
	aircraftDeaths := newAircraftDestructionParser()
	aircraftSpawns := newAircraftSpawnParser(ecs)
	kills := &packetkill.PacketKillParser{KeepKills: true, ECS: mgr, PathsGround: paths, PathsAir: fm}
	optionalEvents := newOptionalEventParser(enabledEvents, slot)
	parsers := []packet.PacketParser{}
	if ecs != nil {
		parsers = append(parsers, ecs)
	}
	parsers = append(parsers, slot, fm, paths)
	if missileMetadata != nil {
		parsers = append(parsers, missileMetadata)
	}
	parsers = append(parsers, missilePositions, missileNames, aircraftDeaths, aircraftSpawns)
	if enabledEvents["kill"] {
		parsers = append(parsers, kills)
	}
	if enabledEvents["chat"] || enabledEvents["join"] || enabledEvents["damage"] || enabledEvents["award"] {
		parsers = append(parsers, optionalEvents)
	}
	if groundMetadata != nil {
		parsers = append(parsers, groundMetadata)
	}
	if _, err := packet.ParsePackets(packet.NewPacketStreamReader(replay.PacketStream), parsers); err != nil {
		return err
	}

	tracks := collectTracks(fm, paths, mgr, slot, aircraftSpawns)
	destroyedAircraft := applyAircraftDestructions(tracks, aircraftDeaths)
	tracks = appendMissileTracks(tracks, decodedMissileTracks(missileMetadata, missilePositions))
	bombs := decodedBombTracks(missileMetadata, missilePositions)
	applyMissileModelNamesWithAliases(tracks, missileNames, missileModelTable.killAliases())
	applyMissileModelsForReplay(tracks, missileModelTable, replaySHA)
	applyMissileModelAssociations(tracks, missileModelTable)
	applyMissileGuidanceForReplay(tracks, guidance, replaySHA)
	cmExport := prepareCountermeasures(missileMetadata, missilePositions, tracks, *countermeasures)
	tracks = cmExport.Tracks
	// Bombs use the next IDs after the established aircraft, missile and
	// countermeasure tracks so this addition cannot renumber existing objects.
	tracks = appendBombTracks(tracks, bombs)
	if *groundUnits {
		tracks = appendGroundTracks(tracks, paths.GroundPaths, paths.GroundEntities, paths.GroundEnds, groundMetadata)
	}
	if groundMetadata != nil && *groundUnits {
		tracks = appendStaticGroundTracks(tracks, groundMetadata)
		applyGroundTeamEvidence(tracks, groundMetadata.Models)
		if *groundModels {
			applyGroundModelEvidence(tracks, replayGroundModelEvidence(groundMetadata.Models))
		}
		tracks = applyGroundIdentityHistory(tracks, groundMetadata, *groundModels)
		applyGroundKillLifetimes(tracks, kills.Kills)
	}
	var radarExport *radarLockExport
	if *radarLocks || enabledEvents["radar"] {
		radarExport = newRadarLockExport(fm)
	}
	var radarModesExport *radarModeExport
	if *radarModes && replay.Header.Version == 0x00018C1C {
		radarModesExport = newRadarModeExport(fm)
	}
	if *missileFlightLogPath != "" {
		if err := writeMissileFlightLog(*missileFlightLogPath, tracks); err != nil {
			return err
		}
		fmt.Printf("wrote missile flight log %s\n", *missileFlightLogPath)
	}
	if *countermeasures && *countermeasureLog != "" {
		if err := writeCountermeasureLog(*countermeasureLog, cmExport.Records); err != nil {
			return err
		}
		fmt.Printf("wrote countermeasure log %s\n", *countermeasureLog)
	}
	if len(tracks) == 0 && len(cmExport.Events) == 0 {
		return fmt.Errorf("no tracks found in replay")
	}
	names, err := readNames(*namesPath)
	if err != nil {
		return err
	}
	var airportMarkers []airfieldMarker
	if *airfields {
		airportMarkers = prepareAirfields(replay.Header, resolvedMapID, groundMetadata)
	}
	if *listPath != "" {
		if err := writeTrackList(*listPath, tracks, slot, names); err != nil {
			return err
		}
		fmt.Printf("wrote track list %s\n", *listPath)
	}
	source := acmiSourceContext{directory: input.directory}
	messageEvents := optionalEvents.timeline(tracks, names)
	attributedKills := 0
	if enabledEvents["kill"] {
		killEvents := buildKillTimeline(tracks, kills.Kills, slot, names)
		attributedKills = len(killEvents)
		messageEvents = append(messageEvents, killEvents...)
	}
	fmt.Printf("timeline: %d kill packets, %d attributed, %d omitted; %d messages before radar; skipped optional packets %d\n", len(kills.Kills), attributedKills, len(kills.Kills)-attributedKills, len(messageEvents), optionalEvents.Skipped)
	if err := writeACMIWithContext(*outPath, replay.Header, tracks, slot, names, cmExport.Events, resolvedMapID, source, acmiWriterContext{radarLocks: radarExport, radarModes: radarModesExport, suppressSeekerProperties: !*missileSeekers, estimateTelemetry: *estimateTelemetry, airfields: airportMarkers, timeline: messageEvents, radarMessages: enabledEvents["radar"], suppressRadarProperties: !*radarLocks, eventCategories: *timelineEventsSpec}); err != nil {
		return err
	}
	if radarExport != nil && *radarLockLog != "" {
		if err := writeRadarLockLog(*radarLockLog, radarExport, tracks, slot, names); err != nil {
			return err
		}
		fmt.Printf("wrote radar lock log %s\n", *radarLockLog)
	}
	samples := 0
	aircraft, identified := 0, 0
	missiles, missileSamples := 0, 0
	missileModels := 0
	bombsExported, bombSamples := 0, 0
	bombPositionMarkers := 0
	decoys, decoySamples := 0, 0
	groundUnitsExported, groundSamples := 0, 0
	groundModelsIdentified := 0
	estimatedDecoys, estimatedSamples := 0, 0
	for _, tr := range tracks {
		if isGroundTrack(tr) {
			groundUnitsExported++
			groundSamples += len(tr.samples)
			if tr.groundModel != nil {
				groundModelsIdentified++
			}
		}
		sampleCount := len(tr.samples)
		if tr.key.air && tr.hasEnd {
			sampleCount = sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t > tr.end })
		}
		samples += sampleCount
		if tr.key.missile {
			missiles++
			missileSamples += len(tr.samples)
			if tr.missileModel != nil && tr.missileModel.Model != "" {
				missileModels++
			}
		}
		if tr.key.bomb {
			bombsExported++
			bombSamples += len(tr.samples)
			if tr.bombPositionOnly {
				bombPositionMarkers++
			}
		}
		if tr.key.decoy {
			decoys++
			decoySamples += len(tr.samples)
			if tr.estimated {
				estimatedDecoys++
				estimatedSamples += len(tr.samples)
			}
		}
		if tr.key.air {
			aircraft++
			if trackName(tr, slot, names) != fmt.Sprintf("air:%d", tr.key.id) {
				identified++
			}
		}
	}
	fmt.Printf("wrote %s: %d tracks, %d samples\n", *outPath, len(tracks), samples)
	if len(airportMarkers) > 0 {
		fmt.Printf("airfield markers exported: %d (%d airports)\n", len(airportMarkers), countAirportMarkers(airportMarkers))
		runways := 0
		for _, marker := range airportMarkers {
			if marker.Role == "Airport" && marker.HasHeading && marker.Length > 0 {
				runways++
			}
		}
		fmt.Printf("airfield runways: %d source-configured, %d facility-only anchors\n", runways, countAirportMarkers(airportMarkers)-runways)
	}
	fmt.Printf("aircraft names identified: %d/%d\n", identified, aircraft)
	fmt.Printf("aircraft spawn reports: %d (%d UIDs)\n", aircraftSpawns.count, len(aircraftSpawns.normalized()))
	fmt.Printf("aircraft removed after destruction: %d\n", destroyedAircraft)
	fmt.Printf("ground/surface units exported: %d (%d recorded positions)\n", groundUnitsExported, groundSamples)
	fmt.Printf("ground models identified: %d/%d\n", groundModelsIdentified, groundUnitsExported)
	fmt.Printf("missiles exported: %d (%d recorded positions)\n", missiles, missileSamples)
	fmt.Printf("missile models identified: %d/%d\n", missileModels, missiles)
	fmt.Printf("bombs exported: %d (%d recorded positions)\n", bombsExported, bombSamples)
	if bombPositionMarkers > 0 {
		fmt.Printf("bomb release-position markers: %d (no recorded flight trajectory)\n", bombPositionMarkers)
		fmt.Fprintln(os.Stderr, "warning: some bombs have construction positions only; these are labelled release-position markers, not flight trajectories")
	}
	fmt.Printf("countermeasures exported: %d tracks (%d recorded positions), %d release events, %d construction records\n",
		decoys, decoySamples, len(cmExport.Events), cmExport.Constructions)
	fmt.Printf("countermeasure estimated trajectories: %d tracks (%d synthesized positions)\n",
		estimatedDecoys, estimatedSamples)
	cmFlares, cmChaff, cmFallback := 0, 0, 0
	for _, record := range cmExport.Records {
		if record.Kind == "Flare" {
			cmFlares++
		} else {
			cmChaff++
		}
		if record.TypeConfidence == "fallback" {
			cmFallback++
		}
	}
	fmt.Printf("countermeasure types: %d Flare, %d Chaff (%d unresolved releases defaulted to Chaff)\n", cmFlares, cmChaff, cmFallback)
	if radarExport != nil {
		lockedSyncs, transitions := radarExport.stats()
		fmt.Printf("radar locks exported: %d aircraft, %d locked syncs, %d state changes\n",
			radarExport.aircraft, lockedSyncs, transitions)
	}
	if identified < aircraft {
		fmt.Fprintln(os.Stderr, "warning: some aircraft names are unresolved; use -list to inspect them and -names to supply overrides")
	}
	return nil
}

func readHashes(spec string) (*packetecs2.ComponentHashMaps, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "":
		return nil, nil
	case "embedded":
		chms, err := packetecs2.ReadComponentHashMaps(bytes.NewReader(inspectordata.ECSHashesJSON()))
		if err != nil {
			return nil, fmt.Errorf("parsing embedded ECS component hash map: %w", err)
		}
		return chms, nil
	default:
		f, err := os.Open(spec)
		if err != nil {
			return nil, fmt.Errorf("opening ECS component hash map %q: %w", spec, err)
		}
		defer f.Close()
		chms, err := packetecs2.ReadComponentHashMaps(f)
		if err != nil {
			return nil, fmt.Errorf("parsing ECS component hash map %q: %w", spec, err)
		}
		return chms, nil
	}
}

func resolveReplayMapID(header wrpl.WRPLHeader, override string) (string, error) {
	if override != "" {
		return terrainmap.NormalizeMapID(override)
	}
	mapID, err := terrainmap.ResolveReplay(
		terrainmap.HeaderString(header.Raw_Level[:]),
		terrainmap.HeaderString(header.Raw_LocName[:]),
	)
	if errors.Is(err, terrainmap.ErrUnknown) {
		return "", nil
	}
	return mapID, err
}

func collectTracks(fm *packetfm.PacketFlightModelParser, paths *packetmovement.PositionRetainerParser, mgr *packetecs2.EntityManager, slot *packetslot.PacketSlotParser, spawnParsers ...*aircraftSpawnParser) []*track {
	spawnRecords := map[uint64][]aircraftSpawn{}
	if len(spawnParsers) > 0 {
		spawnRecords = spawnParsers[0].normalized()
	}
	byKey := map[trackKey]*track{}
	get := func(k trackKey) *track {
		tr := byKey[k]
		if tr == nil {
			tr = &track{key: k}
			byKey[k] = tr
		}
		return tr
	}
	activeAircraft := map[uint64]*track{}
	if fm != nil {
		for _, frame := range fm.Results {
			for _, entry := range frame.Entries {
				if entry == nil || entry.Data == nil || (entry.Data.Unk0 && entry.Data.Unk1) {
					continue
				}
				tr := activeAircraft[entry.UID]
				records := spawnRecords[entry.UID]
				if len(records) > 0 {
					index := aircraftSpawnIndex(records, frame.CurrentTime)
					key := trackKey{air: true, id: entry.UID, sortie: uint32(index)}
					tr = get(key)
					if index+1 < len(records) && records[index+1].Time > 0 {
						tr.end, tr.hasEnd, tr.retired = records[index+1].Time-1, true, true
					}
					// A creation may arrive after the first sync. Within an explicit
					// spawn interval, retain the recorded metadata that supersedes it.
					if records[index].Entity != nil {
						tr.entity = records[index].Entity
					} else if entry.ResolvedEntity != nil {
						tr.entity = entry.ResolvedEntity
					}
				} else if tr == nil {
					tr = get(trackKey{air: true, id: entry.UID})
					activeAircraft[entry.UID] = tr
				} else if entry.ResolvedEntity != nil && tr.entity != nil && entry.ResolvedEntity != tr.entity {
					key := tr.key
					key.sortie++
					if frame.CurrentTime > 0 {
						tr.end, tr.hasEnd, tr.retired = frame.CurrentTime-1, true, true
					}
					tr = get(key)
					activeAircraft[entry.UID] = tr
				}
				if tr.entity == nil {
					tr.entity = entry.ResolvedEntity
				}
				heading, attitude, bank := packetfm.UnpackEuler(entry.Data.EulerBytes)
				tr.samples = append(tr.samples, acmiSample{
					t:     frame.CurrentTime,
					x:     float64(entry.Data.PosX),
					y:     float64(entry.Data.PosY),
					z:     float64(entry.Data.PosZ),
					roll:  aircraftRollToACMI(float64(bank) * 180 / math.Pi),
					pitch: float64(attitude) * 180 / math.Pi,
					yaw:   aircraftHeadingToACMI(float64(heading) * 180 / math.Pi),
				})
			}
		}
	}
	if paths != nil {
		for eid, path := range paths.Paths {
			tr := get(trackKey{air: false, id: eid})
			if tr.entity == nil && mgr != nil {
				tr.entity = mgr.Entities[groundEntityIndex(eid)]
			}
			for i, p := range path {
				yaw := 0.0
				if i+1 < len(path) {
					x := path[i+1].X - p.X
					z := path[i+1].Z - p.Z
					if x != 0 || z != 0 {
						yaw = math.Atan2(x, z) * 180 / math.Pi
					}
				}
				tr.samples = append(tr.samples, acmiSample{t: p.Time, x: p.X, y: p.Y, z: p.Z, yaw: yaw})
			}
		}
	}

	tracks := make([]*track, 0, len(byKey))
	for _, tr := range byKey {
		if len(tr.samples) == 0 {
			continue
		}
		sort.Slice(tr.samples, func(i, j int) bool { return tr.samples[i].t < tr.samples[j].t })
		tracks = append(tracks, tr)
	}
	sort.Slice(tracks, func(i, j int) bool {
		ki, kj := trackKindOrder(tracks[i].key), trackKindOrder(tracks[j].key)
		if ki != kj {
			return ki < kj
		}
		if tracks[i].key.id != tracks[j].key.id {
			return tracks[i].key.id < tracks[j].key.id
		}
		return tracks[i].key.sortie < tracks[j].key.sortie
	})
	_ = slot
	return tracks
}

// appendMissileTracks adapts decoded missile records into writer tracks. Parent
// links are resolved only against an actual aircraft-source UID. Missile samples
// after an explicit end are discarded, and tracks without recorded samples are
// omitted. With no explicit end, the final sample is the recording cutoff used
// by the ACMI writer for object deletion.
func appendMissileTracks(tracks []*track, inputs []missileTrack) []*track {
	out := append([]*track(nil), tracks...)
	existing := make(map[trackKey]bool, len(tracks))
	for _, tr := range tracks {
		if tr != nil {
			existing[tr.key] = true
		}
	}

	added := make([]*track, 0, len(inputs))
	for _, in := range inputs {
		samples := append([]acmiSample(nil), in.Samples...)
		sort.SliceStable(samples, func(i, j int) bool { return samples[i].t < samples[j].t })
		if in.HasEnd {
			end := sort.Search(len(samples), func(i int) bool { return samples[i].t > in.EndTime })
			samples = samples[:end]
		}
		if len(samples) == 0 {
			continue
		}
		key := trackKey{missile: true, id: in.ID}
		if existing[key] {
			continue
		}
		tr := &track{
			key:              key,
			name:             in.Name,
			samples:          samples,
			end:              in.EndTime,
			hasEnd:           in.HasEnd,
			projectileSource: in.Source,
		}
		if in.HasParent {
			parentTime := samples[0].t
			if in.Source != nil {
				parentTime = in.Source.SpawnSample.Time
			}
			tr.parent = aircraftParentAt(tracks, in.ParentUID, parentTime)
		}
		added = append(added, tr)
		existing[key] = true
	}
	sort.Slice(added, func(i, j int) bool { return added[i].key.id < added[j].key.id })
	return append(out, added...)
}

func trackKindOrder(key trackKey) int {
	if key.decoy {
		return 4
	}
	if key.bomb {
		return 3
	}
	if key.missile {
		return 2
	}
	if key.air {
		return 1
	}
	return 0
}

func groundEntityIndex(key uint64) uint32 {
	eid := ((key & 0xff) << 22) | (key >> 8)
	return uint32(packetecs2.EntityID(uint32(eid)).Index())
}

// acmiObjectIDs assigns one hexadecimal Tacview object id per track. The ids
// depend only on track order, so optional properties cannot renumber objects.
func acmiObjectIDs(tracks []*track) map[trackKey]string {
	ids := map[trackKey]string{}
	for i, tr := range tracks {
		if tr == nil {
			continue
		}
		ids[tr.key] = fmt.Sprintf("%X", i+1)
	}
	return ids
}

func writeACMI(path string, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string) error {
	return writeACMIWithEvents(path, header, tracks, slot, names, nil)
}

func writeACMIWithEvents(path string, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string, events []acmiEvent) error {
	return writeACMIWithEventsMapID(path, header, tracks, slot, names, events, "")
}

func writeACMIWithEventsMapID(path string, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string, events []acmiEvent, mapID string) error {
	return writeACMIWithEventsMapIDAndSource(path, header, tracks, slot, names, events, mapID, acmiSourceContext{})
}

func writeACMIWithEventsMapIDAndSource(path string, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string, events []acmiEvent, mapID string, source acmiSourceContext) error {
	return writeACMIWithContext(path, header, tracks, slot, names, events, mapID, source, acmiWriterContext{})
}

// acmiWriterContext carries optional exporters without mutating package globals.
// A zero context writes the object stream without optional radar state.
type acmiWriterContext struct {
	radarLocks               *radarLockExport
	radarModes               *radarModeExport
	suppressSeekerProperties bool
	estimateTelemetry        bool
	recordingTime            time.Time
	airfields                []airfieldMarker
	timeline                 []timelineEvent
	radarMessages            bool
	suppressRadarProperties  bool
	eventCategories          string
}

func writeACMIWithContext(path string, header wrpl.WRPLHeader, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string, events []acmiEvent, mapID string, source acmiSourceContext, ctx acmiWriterContext) error {
	if err := validateACMIVersion(*acmiFormatVersion); err != nil {
		return err
	}
	layout, err := currentTerrainLayout()
	if err != nil {
		return err
	}
	if mapID != "" {
		mapID, err = terrainmap.NormalizeMapID(mapID)
		if err != nil {
			return err
		}
	}
	previousGeographicProfile := activeGeographicProfile
	activeGeographicProfile = nil
	if layout == terrainLayoutGeographic {
		if profile, ok := terrainmap.GeographicReference(mapID); ok && profile.Enabled {
			activeGeographicProfile = &profile
		}
	}
	defer func() { activeGeographicProfile = previousGeographicProfile }()
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	events = append([]acmiEvent(nil), events...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		if events[i].parentID != events[j].parentID {
			return events[i].parentID < events[j].parentID
		}
		return events[i].text < events[j].text
	})
	first := acmiReferenceBase(tracks, events)
	ref := time.Now().UTC()
	if header.StartTime > 0 {
		ref = time.Unix(int64(header.StartTime), 0).UTC().Add(time.Duration(first) * time.Millisecond)
	}

	fmt.Fprintln(w, "FileType=text/acmi/tacview")
	fmt.Fprintln(w, "FileVersion="+*acmiFormatVersion)
	fmt.Fprintf(w, "0,ReferenceTime=%s\n", ref.Format("2006-01-02T15:04:05Z"))
	// T is already absolute longitude, latitude, and altitude. Keeping the
	// global reference at zero prevents Tacview from adding the anchor twice.
	fmt.Fprintln(w, "0,ReferenceLongitude=0.0000000")
	fmt.Fprintln(w, "0,ReferenceLatitude=0.0000000")
	fmt.Fprintln(w, "0,ReferenceAltitude=0.0")
	fmt.Fprintln(w, "0,DataSource=War Thunder replay via wrpl-inspector")
	for _, property := range acmiMetadata(header, mapID, layout, ctx) {
		fmt.Fprintf(w, "0,%s\n", property)
	}
	for _, property := range source.properties() {
		fmt.Fprintf(w, "0,%s\n", property)
	}
	for _, property := range terrainGlobalProperties(layout, mapID) {
		fmt.Fprintf(w, "0,%s\n", property)
	}

	ids := acmiObjectIDs(tracks)
	if ctx.radarLocks != nil {
		ctx.radarLocks.prepare(tracks, ids)
	}
	if ctx.radarModes != nil {
		ctx.radarModes.prepare(tracks, ids)
	}
	properties := map[trackKey][]string{}
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		id := ids[tr.key]
		props := acmiTrackProperties(tr, slot, names, ids)
		props = append(props, groundHeadingProperties(tr)...)
		properties[tr.key] = props
		if !tr.key.missile && !tr.key.bomb && !tr.key.decoy && !tr.key.groundEntity {
			fmt.Fprintf(w, "%s,%s\n", id, strings.Join(props, ","))
		}
	}

	updates := map[uint32][]string{}
	deletions := map[uint32][]string{}
	timesSeen := map[uint32]bool{}
	times := make([]uint32, 0)
	for _, tr := range tracks {
		if tr == nil {
			continue
		}
		id := ids[tr.key]
		if tr.key.missile {
			end, ok := missileTrackEnd(tr)
			if !ok || tr.samples[0].t > end {
				continue
			}
			flight := enrichRecordedMissileFlight(tr.samples)
			for i, s := range tr.samples {
				if s.t > end {
					break
				}
				position := acmiMissilePositionValue(s)
				flightProps := missileFlightPropertiesWithINS(flight[i], s)
				if !ctx.suppressSeekerProperties {
					flightProps = append(flightProps, missileSeekerProperties(tr, s)...)
					for j, property := range flightProps {
						if strings.HasPrefix(property, "Label=") {
							flightProps[j] += " " + missileSeekerDisplayName(s)
						}
					}
				}
				line := fmt.Sprintf("%s,T=%s,%s", id, position, strings.Join(flightProps, ","))
				if i == 0 {
					props := append([]string(nil), properties[tr.key]...)
					props = append(props, "T="+position)
					props = append(props, flightProps...)
					line = id + "," + strings.Join(props, ",")
				}
				if !timesSeen[s.t] {
					timesSeen[s.t] = true
					times = append(times, s.t)
				}
				updates[s.t] = append(updates[s.t], line)
			}
			if !timesSeen[end] {
				timesSeen[end] = true
				times = append(times, end)
			}
			deletions[end] = append(deletions[end], "-"+id)
			continue
		}
		if tr.key.decoy {
			end, ok := decoyTrackEnd(tr)
			if !ok || tr.samples[0].t > end {
				continue
			}
			for i, s := range tr.samples {
				if s.t > end {
					break
				}
				position := acmiPositionValue(s)
				line := fmt.Sprintf("%s,T=%s", id, position)
				if i == 0 {
					props := append([]string(nil), properties[tr.key]...)
					props = append(props, "T="+position)
					line = id + "," + strings.Join(props, ",")
				}
				if !timesSeen[s.t] {
					timesSeen[s.t] = true
					times = append(times, s.t)
				}
				updates[s.t] = append(updates[s.t], line)
			}
			if !timesSeen[end] {
				timesSeen[end] = true
				times = append(times, end)
			}
			deletions[end] = append(deletions[end], "-"+id)
			continue
		}
		if tr.key.bomb {
			end, ok := bombTrackEnd(tr)
			if !ok || tr.samples[0].t > end {
				continue
			}
			for i, s := range tr.samples {
				if s.t > end {
					break
				}
				position := acmiBombPositionValue(s)
				line := fmt.Sprintf("%s,T=%s", id, position)
				if i == 0 {
					props := append([]string(nil), properties[tr.key]...)
					props = append(props, "T="+position)
					line = id + "," + strings.Join(props, ",")
				}
				if !timesSeen[s.t] {
					timesSeen[s.t] = true
					times = append(times, s.t)
				}
				updates[s.t] = append(updates[s.t], line)
			}
			if !timesSeen[end] {
				timesSeen[end] = true
				times = append(times, end)
			}
			deletions[end] = append(deletions[end], "-"+id)
			continue
		}
		if tr.key.groundEntity {
			for i, s := range tr.samples {
				if tr.hasEnd && s.t > tr.end {
					break
				}
				position := groundPositionValue(tr, i)
				line := id + ",T=" + position
				if i == 0 {
					props := append([]string(nil), properties[tr.key]...)
					line = id + "," + strings.Join(append(props, "T="+position), ",")
				}
				if !timesSeen[s.t] {
					timesSeen[s.t] = true
					times = append(times, s.t)
				}
				updates[s.t] = append(updates[s.t], line)
			}
			if tr.hasEnd && len(tr.samples) > 0 && tr.end >= tr.samples[0].t {
				if !timesSeen[tr.end] {
					timesSeen[tr.end] = true
					times = append(times, tr.end)
				}
				deletions[tr.end] = append(deletions[tr.end], "-"+id)
				if !tr.retired {
					updates[tr.end] = append(updates[tr.end], "0,Event=Destroyed|"+id)
				}
			}
			continue
		}
		var telemetry [][]string
		if tr.key.air && ctx.estimateTelemetry {
			samples := tr.samples
			if tr.hasEnd {
				samples = samples[:sort.Search(len(samples), func(i int) bool { return samples[i].t > tr.end })]
			}
			telemetry = aircraftTelemetryProperties(samples)
		}
		for i, s := range tr.samples {
			if tr.hasEnd && s.t > tr.end {
				break
			}
			if !timesSeen[s.t] {
				timesSeen[s.t] = true
				times = append(times, s.t)
			}
			line := fmt.Sprintf("%s,T=%s", id, acmiPositionValue(s))
			if isGroundTrack(tr) {
				line = fmt.Sprintf("%s,T=%s", id, groundPositionValue(tr, i))
			}
			if i < len(telemetry) {
				line += "," + strings.Join(telemetry[i], ",")
			}
			if ctx.radarLocks != nil && !ctx.suppressRadarProperties && tr.key.air {
				if extra := ctx.radarLocks.lineProperties(tr.key.id, s.t, tr); len(extra) > 0 {
					line += "," + strings.Join(extra, ",")
				}
			}
			if ctx.radarModes != nil && tr.key.air {
				if extra := ctx.radarModes.lineProperties(tr.key.id, s.t, tr); len(extra) > 0 {
					line += "," + strings.Join(extra, ",")
				}
			}
			updates[s.t] = append(updates[s.t], line)
		}
		if (tr.key.air || isGroundTrack(tr)) && tr.hasEnd && len(tr.samples) > 0 && tr.end >= tr.samples[0].t {
			if !timesSeen[tr.end] {
				timesSeen[tr.end] = true
				times = append(times, tr.end)
			}
			if !tr.retired {
				updates[tr.end] = append(updates[tr.end], "0,Event=Destroyed|"+id)
			}
			deletions[tr.end] = append(deletions[tr.end], "-"+id)
		}
	}
	if ctx.radarLocks != nil && !ctx.suppressRadarProperties {
		for _, extra := range ctx.radarLocks.extraLines() {
			if !timesSeen[extra.t] {
				timesSeen[extra.t] = true
				times = append(times, extra.t)
			}
			updates[extra.t] = append(updates[extra.t], extra.line)
		}
	}
	if ctx.radarModes != nil {
		for _, extra := range ctx.radarModes.extraLines() {
			if !timesSeen[extra.t] {
				timesSeen[extra.t] = true
				times = append(times, extra.t)
			}
			updates[extra.t] = append(updates[extra.t], extra.line)
		}
	}
	for _, event := range events {
		if !timesSeen[event.t] {
			timesSeen[event.t] = true
			times = append(times, event.t)
		}
		updates[event.t] = append(updates[event.t], acmiEventLine(event, ids, tracks))
	}
	messageEvents := append([]timelineEvent(nil), ctx.timeline...)
	if ctx.radarMessages {
		messageEvents = append(messageEvents, radarTimeline(ctx.radarLocks, slot, names)...)
	}
	for t, lines := range timelineLines(messageEvents, tracks, ids) {
		if !timesSeen[t] {
			timesSeen[t] = true
			times = append(times, t)
		}
		updates[t] = append(updates[t], lines...)
	}
	if *timelineBookmarks {
		for t, lines := range acmiBookmarks(tracks, events, ctx.radarLocks, ids, slot, names) {
			if !timesSeen[t] {
				timesSeen[t] = true
				times = append(times, t)
			}
			updates[t] = append(updates[t], lines...)
		}
	}
	for t, lines := range airfieldLines(ctx.airfields, len(tracks)) {
		if !timesSeen[t] {
			timesSeen[t] = true
			times = append(times, t)
		}
		updates[t] = append(updates[t], lines...)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	for _, t := range times {
		fmt.Fprintf(w, "#%.3f\n", float64(int64(t)-int64(first))/1000)
		for _, line := range updates[t] {
			fmt.Fprintln(w, line)
		}
		for _, line := range deletions[t] {
			fmt.Fprintln(w, line)
		}
	}
	return nil
}

// acmiReferenceBase preserves the legacy aircraft and missile origin. Bomb,
// decoy or event times earlier than that origin are written as negative
// offsets. Bombs and decoys only define the origin when a replay has no legacy
// track at all, with existing decoy behavior taking precedence.
func acmiReferenceBase(tracks []*track, events []acmiEvent) uint32 {
	first := uint32(math.MaxUint32)
	for _, tr := range tracks {
		if tr == nil || tr.key.bomb || tr.key.decoy || tr.key.groundEntity {
			continue
		}
		end, endpointOK := missileTrackEnd(tr)
		for _, s := range tr.samples {
			if tr.key.missile && (!endpointOK || s.t > end) {
				continue
			}
			if s.t < first {
				first = s.t
			}
		}
	}
	if first != math.MaxUint32 {
		return first
	}
	for _, tr := range tracks {
		if tr == nil || !tr.key.decoy {
			continue
		}
		end, ok := decoyTrackEnd(tr)
		if !ok || tr.samples[0].t > end {
			continue
		}
		for _, s := range tr.samples {
			if s.t > end {
				break
			}
			if s.t < first {
				first = s.t
			}
		}
	}
	if first == math.MaxUint32 {
		for _, tr := range tracks {
			if tr == nil || !tr.key.bomb {
				continue
			}
			end, ok := bombTrackEnd(tr)
			if !ok || tr.samples[0].t > end {
				continue
			}
			for _, s := range tr.samples {
				if s.t > end {
					break
				}
				if s.t < first {
					first = s.t
				}
			}
		}
	}
	if first == math.MaxUint32 {
		for _, tr := range tracks {
			if tr == nil || !tr.key.groundEntity {
				continue
			}
			for _, sample := range tr.samples {
				if (!tr.hasEnd || sample.t <= tr.end) && sample.t < first {
					first = sample.t
				}
			}
		}
	}
	for _, event := range events {
		if event.t < first {
			first = event.t
		}
	}
	if first == math.MaxUint32 {
		return 0
	}
	return first
}

func acmiTrackProperties(tr *track, slot *packetslot.PacketSlotParser, names map[string]string, ids map[trackKey]string) []string {
	coalition := trackCoalition(tr, slot)
	color := coalition
	if color == "Neutral" {
		color = "White"
	}
	typ := groundTrackType(tr)
	if tr.key.air {
		typ = "Air+FixedWing"
	}
	if tr.key.missile {
		typ = "Weapon+Missile"
	}
	if tr.key.bomb {
		typ = "Weapon+Bomb"
	}
	if tr.key.decoy {
		typ = countermeasureType(tr)
	}
	name := trackName(tr, slot, names)
	props := []string{
		"Name=" + escapeACMIValue(groundACMIName(tr, name)),
		"CallSign=" + escapeACMIValue(name),
	}
	if pilot := trackPilot(tr, slot); pilot != "" {
		props = append(props, "Pilot="+escapeACMIValue(pilot))
	}
	if tr.parent != nil {
		if parentID := ids[tr.parent.key]; parentID != "" {
			props = append(props, "Parent="+parentID)
		}
	}
	props = append(props,
		"Type="+typ,
		"Coalition="+coalition,
		"Color="+color,
	)
	props = append(props, presentationProperties(tr, coalition)...)
	if tr.groundStatic {
		props = append(props, "WT_GroundPositionSource=ECSConstructionTransform", "WT_GroundStatic=1")
	}
	if tr.key.groundEntity {
		if tr.key.id&groundUnitUIDNamespace != 0 {
			props = append(props, "WT_IdentitySource=ReplayUnitUID", "WT_UnitUID="+strconv.FormatUint(tr.key.id&^groundUnitUIDNamespace, 10))
		} else {
			props = append(props, "WT_IdentitySource=ECS", "WT_EntityID="+strconv.FormatUint(tr.key.id, 16))
		}
	}
	props = append(props, groundModelProperties(tr)...)
	props = append(props, groundTeamProperties(tr)...)
	props = append(props, countermeasureIdentityProperties(tr)...)
	if tr.estimated {
		props = append(props, countermeasureEstimatedProperties(tr)...)
	}
	if tr.key.bomb {
		if tr.bombPositionOnly {
			props = append(props, "WT_BombPositionSource=Construction", "WT_BombTrajectoryAvailable=0", "Label=Release position only")
		} else {
			props = append(props, "WT_BombPositionSource=WeaponSync", "WT_BombTrajectoryAvailable=1")
		}
	}
	if tr.key.missile {
		props = append(props, missileModelProperties(tr)...)
		props = append(props, missileGuidanceProperties(tr)...)
	}
	return props
}

func acmiPositionValue(s acmiSample) string {
	lat, lon := terrainPosition(s.x, s.z)
	return fmt.Sprintf("%.7f|%.7f|%.2f|%.2f|%.2f|%.2f", lon, lat, *alt0+s.y, s.roll, s.pitch, s.yaw)
}

func acmiBombPositionValue(s acmiSample) string {
	lat, lon := terrainPosition(s.x, s.z)
	return fmt.Sprintf("%.7f|%.7f|%.2f", lon, lat, *alt0+s.y)
}

// aircraftRollToACMI converts War Thunder's packed FM bank to Tacview roll.
// The FM bank sign is opposite Tacview's convention, where positive roll is
// right wing down. Magnitude and the zero crossing are preserved.
func aircraftRollToACMI(bankDeg float64) float64 {
	return -bankDeg
}

// aircraftHeadingToACMI converts the packed aircraft heading axis to Tacview's
// clockwise heading. Ground headings are already derived clockwise from north.
func aircraftHeadingToACMI(heading float64) float64 {
	heading = math.Mod(heading+90, 360)
	if heading < 0 {
		heading += 360
	}
	return heading
}

func missileTrackEnd(tr *track) (uint32, bool) {
	if tr == nil || !tr.key.missile || len(tr.samples) == 0 {
		return 0, false
	}
	if tr.hasEnd {
		return tr.end, true
	}
	// A replay ending is also the cutoff for a missile whose source does not
	// provide an explicit destruction timestamp. No synthetic sample is added.
	return tr.samples[len(tr.samples)-1].t, true
}

func bombTrackEnd(tr *track) (uint32, bool) {
	if tr == nil || !tr.key.bomb || len(tr.samples) == 0 {
		return 0, false
	}
	if tr.hasEnd {
		return tr.end, true
	}
	return tr.samples[len(tr.samples)-1].t, true
}

func localToLatLon(x, z, lat, lon float64) (float64, float64) {
	const metersPerDegree = 111320.0
	lat += z / metersPerDegree
	cosLat := math.Cos(lat * math.Pi / 180)
	if math.Abs(cosLat) < 1e-6 {
		cosLat = 1
	}
	lon += x / (metersPerDegree * cosLat)
	return lat, lon
}

func readNames(path string) (map[string]string, error) {
	if path == "" {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("parsing names JSON: %w", err)
	}
	return names, nil
}

func writeTrackList(path string, tracks []*track, slot *packetslot.PacketSlotParser, names map[string]string) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	qw := csv.NewWriter(f)
	defer qw.Flush()
	header := []string{"object_id", "kind", "player", "team", "current_name", "start_sec", "end_sec", "samples", "class_name", "parent_source"}
	modelColumns := tracksHaveMissileModelColumns(tracks)
	if modelColumns {
		header = append(header, "weapon_id", "model", "model_source", "model_confidence", "candidates")
	}
	guidanceColumns := tracksHaveMissileGuidance(tracks)
	if guidanceColumns {
		header = append(header, "guidance_type", "guidance_source", "guidance_confidence", "guidance_candidates")
	}
	if err := qw.Write(header); err != nil {
		return err
	}
	for _, tr := range tracks {
		if tr == nil || len(tr.samples) == 0 {
			continue
		}
		kind := trackListKind(tr)
		player := ""
		if tr.entity != nil {
			if p := entityPlayer(tr.entity, slot); p != nil {
				player = p.Uid.Name
			}
		}
		start := tr.samples[0].t
		end := tr.samples[len(tr.samples)-1].t
		sampleCount := len(tr.samples)
		if (tr.key.air || isGroundTrack(tr)) && tr.hasEnd {
			end = tr.end
			sampleCount = sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t > end })
		}
		if tr.key.bomb {
			var ok bool
			end, ok = bombTrackEnd(tr)
			if !ok || start > end {
				continue
			}
			sampleCount = sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t > end })
		}
		if tr.key.missile {
			var ok bool
			end, ok = missileTrackEnd(tr)
			if !ok || start > end {
				continue
			}
			sampleCount = sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t > end })
		}
		if tr.key.decoy {
			var ok bool
			end, ok = decoyTrackEnd(tr)
			if !ok || start > end {
				continue
			}
			sampleCount = sort.Search(len(tr.samples), func(i int) bool { return tr.samples[i].t > end })
		}
		className := ""
		if tr.entity != nil {
			className, _ = packetecs2.GetObjectData[string](&tr.entity.Data, "unit__className")
		}
		parentSource := ""
		if (tr.key.missile || tr.key.bomb || tr.key.decoy) && tr.parent != nil {
			parentSource = fmt.Sprintf("air:%d", tr.parent.key.id)
		}
		row := []string{
			fmt.Sprintf("%s:%d", kind, tr.key.id),
			kind,
			player,
			trackCoalition(tr, slot),
			trackName(tr, slot, names),
			strconv.FormatFloat(float64(start)/1000, 'f', 3, 64),
			strconv.FormatFloat(float64(end)/1000, 'f', 3, 64),
			strconv.Itoa(sampleCount),
			className,
			parentSource,
		}
		if modelColumns {
			if tr.key.missile {
				row = append(row, missileModelCSVValues(tr)...)
			} else {
				row = append(row, "", "", "", "", "")
			}
		}
		if guidanceColumns {
			if tr.key.missile {
				row = append(row, missileGuidanceCSVValues(tr)...)
			} else {
				row = append(row, "", "", "", "")
			}
		}
		if err := qw.Write(row); err != nil {
			return err
		}
	}
	qw.Flush()
	return qw.Error()
}

func trackName(tr *track, slot *packetslot.PacketSlotParser, names map[string]string) string {
	if name := manualTrackName(tr, names); name != "" {
		return name
	}
	if tr.key.decoy {
		name := countermeasureKind(tr.countermeasure)
		if tr.estimated {
			return name + " (estimated)"
		}
		return name
	}
	if tr.key.bomb {
		return bombDisplayName
	}
	if isGroundTrack(tr) && tr.groundModel != nil {
		return tr.groundModel.DisplayName
	}
	if tr.key.missile && strings.TrimSpace(tr.name) != "" {
		if tr.name == "AAM (unknown)" {
			if name := missileGuidanceFallbackName(tr); name != "" {
				return name
			}
		}
		return tr.name
	}
	if tr.entity != nil && !(isGroundTrack(tr) && tr.groundModelsDisabled) {
		if model, ok := packetecs2.GetObjectData[string](&tr.entity.Data, "unit__className"); ok {
			if model = strings.TrimSpace(model); model != "" {
				return displayModelName(model)
			}
		}
	}
	if isGroundTrack(tr) && strings.TrimSpace(tr.name) != "" {
		return tr.name
	}
	if tr.key.missile {
		return fmt.Sprintf("missile:%d", tr.key.id)
	}
	if tr.key.air {
		return fmt.Sprintf("air:%d", tr.key.id)
	}
	return fmt.Sprintf("ground:%d", tr.key.id)
}

func manualTrackName(tr *track, names map[string]string) string {
	if len(names) > 0 {
		if tr.key.bomb {
			return strings.TrimSpace(names[fmt.Sprintf("bomb:%d", tr.key.id)])
		}
		if tr.key.decoy {
			return strings.TrimSpace(names[fmt.Sprintf("countermeasure:%d", tr.key.id)])
		}
		if tr.key.missile {
			return strings.TrimSpace(names[fmt.Sprintf("missile:%d", tr.key.id)])
		}
		base := "ground"
		if tr.key.air {
			base = "air"
		}
		for _, key := range []string{fmt.Sprintf("%s:%d", base, tr.key.id), strconv.FormatUint(tr.key.id, 10), fmt.Sprintf("%X", tr.key.id)} {
			if v := strings.TrimSpace(names[key]); v != "" {
				return v
			}
		}
	}
	return ""
}

func trackPilot(tr *track, slot *packetslot.PacketSlotParser) string {
	if tr.entity == nil {
		return ""
	}
	player := entityPlayer(tr.entity, slot)
	if player == nil {
		return ""
	}
	return strings.TrimSpace(player.Uid.Name)
}

func trackCoalition(tr *track, slot *packetslot.PacketSlotParser) string {
	if tr.key.decoy {
		if tr.parent != nil {
			return trackCoalition(tr.parent, slot)
		}
		return "Neutral"
	}
	if tr.key.missile {
		if tr.parent != nil {
			return trackCoalition(tr.parent, slot)
		}
		return "Neutral"
	}
	if tr.key.bomb {
		if tr.parent != nil {
			return trackCoalition(tr.parent, slot)
		}
		return "Neutral"
	}
	if isGroundTrack(tr) && tr.groundTeamSource != "" {
		switch tr.groundTeam {
		case 1:
			return "Blue"
		case 2:
			return "Red"
		}
	}
	if tr.entity != nil {
		if isGroundTrack(tr) {
			if team, ok := packetecs2.GetObjectData[int32](&tr.entity.Data, "team"); ok {
				switch team {
				case 1:
					return "Blue"
				case 2:
					return "Red"
				}
			}
		}
		if p := entityPlayer(tr.entity, slot); p != nil {
			switch p.Team {
			case 1:
				return "Blue"
			case 2:
				return "Red"
			}
		}
	}
	return "Neutral"
}

func trackListKind(tr *track) string {
	if tr.key.decoy {
		return "countermeasure"
	}
	if tr.key.missile {
		return "missile"
	}
	if tr.key.bomb {
		return "bomb"
	}
	if tr.key.air {
		return "air"
	}
	return "ground"
}

func entityPlayer(e *packetecs2.Entity, slot *packetslot.PacketSlotParser) *packetslot.Player {
	if e == nil || slot == nil {
		return nil
	}
	pid, ok := packetecs2.GetObjectData[int32](&e.Data, "unit__playerId")
	if !ok || pid < 0 || int(pid) >= len(slot.Players) {
		return nil
	}
	return slot.Players[pid]
}

func escapeACMIValue(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case ',':
			b.WriteString(`\,`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
