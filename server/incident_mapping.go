// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rdio-scanner/server/mapping"
)

// IncidentMappingConfig is per-system or per-talkgroup mapping settings (JSON column).
type IncidentMappingConfig struct {
	Enabled         bool    `json:"enabled"`
	Inherit         bool    `json:"inherit"` // talkgroup: inherit system streets/places
	GeoCity         string  `json:"geoCity"`
	GeoLat          float64 `json:"geoLat"`
	GeoLon          float64 `json:"geoLon"`
	GeoRadiusMiles  float64 `json:"geoRadiusMiles"`
	LocationContext string  `json:"locationContext"`
	GeoState        string  `json:"geoState"` // optional explicit US state; derived from LocationContext when blank
	CoverageAddress string  `json:"coverageAddress"`
	CoverageNature  string  `json:"coverageNature"`
	// ExtractAddressWithGemini asks Gemini (when it is the STT provider) to
	// also return a short scene address / place name, which is then sent to
	// the geocode gateway instead of the full transcript when present.
	ExtractAddressWithGemini bool `json:"extractAddressWithGemini"`
}

func parseIncidentMappingConfig(raw string) IncidentMappingConfig {
	cfg := IncidentMappingConfig{Inherit: true}
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return cfg
	}
	_ = json.Unmarshal([]byte(raw), &cfg)
	return cfg
}

func (cfg IncidentMappingConfig) JSON() string {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func resolveIncidentMappingConfig(system *System, talkgroup *Talkgroup) IncidentMappingConfig {
	sysCfg := IncidentMappingConfig{}
	if system != nil {
		sysCfg = system.IncidentMapping
	}
	if talkgroup == nil {
		return sysCfg
	}
	tgCfg := talkgroup.IncidentMapping
	if tgCfg.Inherit {
		merged := sysCfg
		if tgCfg.Enabled || talkgroup.IncidentMapping.Enabled {
			merged.Enabled = tgCfg.Enabled
		}
		if strings.TrimSpace(tgCfg.GeoCity) != "" {
			merged.GeoCity = tgCfg.GeoCity
			merged.GeoLat = tgCfg.GeoLat
			merged.GeoLon = tgCfg.GeoLon
			merged.GeoRadiusMiles = tgCfg.GeoRadiusMiles
			merged.LocationContext = tgCfg.LocationContext
			merged.GeoState = tgCfg.GeoState
		}
		if strings.TrimSpace(tgCfg.CoverageAddress) != "" {
			merged.CoverageAddress = tgCfg.CoverageAddress
			merged.CoverageNature = tgCfg.CoverageNature
		}
		// ExtractAddressWithGemini stays on the system value while inheriting.
		return merged
	}
	if !tgCfg.Enabled && !sysCfg.Enabled {
		return tgCfg
	}
	if !tgCfg.Enabled && sysCfg.Enabled {
		out := sysCfg
		out.Inherit = false
		return out
	}
	return tgCfg
}

func toneSetHasGeo(ts *ToneSet) bool {
	return ts != nil && strings.TrimSpace(ts.GeoCity) != "" && ts.GeoLat != 0 && ts.GeoRadiusMiles > 0
}

func talkgroupHasToneSets(tg *Talkgroup) bool {
	return tg != nil && len(tg.ToneSets) > 0
}

func matchedToneSetID(toneSeq *ToneSequence) string {
	if toneSeq == nil {
		return ""
	}
	if toneSeq.MatchedToneSet != nil && strings.TrimSpace(toneSeq.MatchedToneSet.Id) != "" {
		return toneSeq.MatchedToneSet.Id
	}
	if len(toneSeq.MatchedToneSets) > 0 && toneSeq.MatchedToneSets[0] != nil {
		return toneSeq.MatchedToneSets[0].Id
	}
	return ""
}

func toneSetByID(tg *Talkgroup, id string) *ToneSet {
	if tg == nil || id == "" {
		return nil
	}
	for i := range tg.ToneSets {
		if tg.ToneSets[i].Id == id {
			return &tg.ToneSets[i]
		}
	}
	return nil
}

func incidentMappingFromToneSet(ts *ToneSet) IncidentMappingConfig {
	cfg := IncidentMappingConfig{Enabled: true}
	cfg.GeoCity = strings.TrimSpace(ts.GeoCity)
	cfg.GeoLat = ts.GeoLat
	cfg.GeoLon = ts.GeoLon
	cfg.GeoRadiusMiles = ts.GeoRadiusMiles
	cfg.LocationContext = strings.TrimSpace(ts.LocationContext)
	if cfg.LocationContext == "" {
		cfg.LocationContext = cfg.GeoCity
	}
	cfg.GeoState = mapping.DeriveState(cfg.LocationContext, "", cfg.GeoCity)
	return cfg
}

// resolveIncidentMappingForCall picks geo bounds for incident mapping. When a
// tone set with geo matches, its jurisdiction replaces the parent talkgroup's
// geo entirely. When a tone set matches but has no geo, the parent talkgroup
// geo is skipped and only the system geo is used.
// toneSetFallbackMaxRadiusMi caps county-wide system geo when a tone set
// matched but has no configured jurisdiction — avoids homonym pins 8+ mi away.
const toneSetFallbackMaxRadiusMi = 5.0

// homeMaxRadiusSafetyBufferMi is added on top of the resolved, admin-
// configured coverage radius to form the absolute outer ceiling
// (GeoOptions.HomeMaxRadiusMi) that PinOutsideCoverage enforces no matter
// what any downstream override does to BoundsRadiusMi. Generous enough to
// never reject a real dispatch near the edge of coverage, but nowhere close
// to large enough to admit a same-named street a county or two away.
const homeMaxRadiusSafetyBufferMi = 25.0

// resolveMatchedToneSet prefers the talkgroup's configured tone-set record
// (full geo bounds) over the embedded matchedToneSet snapshot stored on calls.
func resolveMatchedToneSet(talkgroup *Talkgroup, toneSeq *ToneSequence) *ToneSet {
	matchedID := matchedToneSetID(toneSeq)
	if matched := toneSetByID(talkgroup, matchedID); matched != nil {
		return matched
	}
	if toneSeq == nil || toneSeq.MatchedToneSet == nil {
		return nil
	}
	embedded := toneSeq.MatchedToneSet
	if cfg := toneSetByID(talkgroup, embedded.Id); cfg != nil {
		return cfg
	}
	return embedded
}

// incidentMappingNeedsToneMatch reports when mapping should wait for tone
// detection or pending-tone attachment to finish writing matchedToneSet into
// the database.
func incidentMappingNeedsToneMatch(controller *Controller, call *Call) bool {
	if call == nil || call.Talkgroup == nil {
		return false
	}
	if !call.Talkgroup.ToneDetectionEnabled || len(call.Talkgroup.ToneSets) == 0 {
		return false
	}
	if matchedToneSetID(call.ToneSequence) != "" {
		return false
	}
	// Pending tones from a prior tone-only call attach to the first voiced call
	// on this talkgroup; wait for that write even when this clip has HasTones=false.
	if call.HasTones {
		return true
	}
	return controller != nil && controller.pendingTonesAwaitingVoice(call) != nil
}

// pendingTonesAwaitingVoice returns in-memory pending tones for call's talkgroup
// when a tone-only page is waiting to attach to the next voiced dispatch.
func (controller *Controller) pendingTonesAwaitingVoice(call *Call) *PendingToneSequence {
	if controller == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return nil
	}
	systemId := call.System.Id
	talkgroupId := call.Talkgroup.Id
	if systemId == 0 || talkgroupId == 0 {
		return nil
	}
	key := fmt.Sprintf("%d:%d", systemId, talkgroupId)
	controller.pendingTonesMutex.Lock()
	pending := controller.pendingTones[key]
	controller.pendingTonesMutex.Unlock()
	if pending == nil || pending.ToneSequence == nil {
		return nil
	}
	if matchedToneSetID(pending.ToneSequence) == "" {
		return nil
	}
	return pending
}

// loadCallExtractedAddress reads the Gemini short address persisted on the call.
func loadCallExtractedAddress(controller *Controller, callID uint64) string {
	if controller == nil || controller.Database == nil || controller.Database.Sql == nil || callID == 0 {
		return ""
	}
	var addr string
	err := controller.Database.Sql.QueryRow(
		`SELECT COALESCE("extractedAddress", '') FROM "calls" WHERE "callId" = $1`, callID,
	).Scan(&addr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(addr)
}

// callForIncidentMapping reloads a call for mapping and, when the talkgroup
// expects tone detection, waits briefly for matchedToneSet to land in the DB.
func (controller *Controller) callForIncidentMapping(callID uint64, transcript string) *Call {
	if controller == nil || controller.Calls == nil {
		return nil
	}
	const maxAttempts = 24 // up to ~12s — tone detection is usually <1s
	interval := 500 * time.Millisecond
	var last *Call
	for attempt := 0; attempt < maxAttempts; attempt++ {
		dbCall, err := controller.Calls.GetCall(callID)
		if err == nil && dbCall != nil {
			last = dbCall
			if strings.TrimSpace(transcript) != "" {
				last.Transcript = transcript
			}
			if !incidentMappingNeedsToneMatch(controller, last) {
				return last
			}
		}
		if attempt+1 < maxAttempts {
			time.Sleep(interval)
		}
	}
	return last
}

// remapIncidentIfTranscriptReady re-runs incident mapping after tone detection
// when transcription already finished using county-wide geo bounds.
func (controller *Controller) remapIncidentIfTranscriptReady(callID uint64) {
	if controller == nil || controller.IncidentMappingQueue == nil || callID == 0 {
		return
	}
	call, err := controller.Calls.GetCall(callID)
	if err != nil || call == nil {
		return
	}
	if strings.TrimSpace(call.Transcript) == "" {
		return
	}
	if !controller.isVoiceForToneAlerts(call.Transcript) {
		return
	}
	controller.IncidentMappingQueue.ProcessCall(call, call.Transcript)
}

// spokenLocalityIsMutualAidDestination reports whether the dispatch-named
// community matches a configured mutual-aid peer jurisdiction, which may not be
// present in the imported boundary layer but is still a real place.
func spokenLocalityIsMutualAidDestination(geo *mapping.GeoOptions, spoken string) bool {
	if geo == nil {
		return false
	}
	s := strings.ToUpper(strings.TrimSpace(spoken))
	if s == "" {
		return false
	}
	for _, d := range geo.MutualAidDestinations {
		label := strings.ToUpper(strings.TrimSpace(d.CityLabel))
		if label != "" && (label == s || strings.Contains(label, s) || strings.Contains(s, label)) {
			return true
		}
	}
	return false
}

func resolveIncidentMappingForCall(system *System, talkgroup *Talkgroup, toneSeq *ToneSequence) (IncidentMappingConfig, bool, bool) {
	base := resolveIncidentMappingConfig(system, talkgroup)
	if !base.Enabled {
		return base, false, false
	}

	matched := resolveMatchedToneSet(talkgroup, toneSeq)

	if matched != nil && toneSetHasGeo(matched) {
		out := incidentMappingFromToneSet(matched)
		out.Enabled = base.Enabled
		out.CoverageAddress = base.CoverageAddress
		out.CoverageNature = base.CoverageNature
		out.ExtractAddressWithGemini = base.ExtractAddressWithGemini
		return out, true, true
	}

	// A tone set matched but has no geo of its own — use the tightest available
	// bounds (talkgroup jurisdiction when explicit, else system) with a capped
	// radius so learned/embedded tones never widen coverage county-wide.
	if matched != nil && talkgroupHasToneSets(talkgroup) {
		return toneFallbackTightestBounds(base, system.IncidentMapping), false, true
	}

	// Tone-enabled talkgroups without a matched tone set must not fall back to a
	// county-wide radius that accepts cross-border homonyms (e.g. Chestnut Ridge
	// in PA when Warren OH dispatch is still resolving tones). Same tightest-plus-
	// cap policy as a matched-but-geoless tone set.
	if matched == nil && talkgroup != nil && talkgroupHasToneSets(talkgroup) && talkgroup.ToneDetectionEnabled {
		return toneFallbackTightestBounds(base, system.IncidentMapping), false, true
	}

	return base, false, false
}

// toneFallbackTightestBounds selects coverage for a tone-enabled talkgroup whose
// active tone set carries no jurisdiction. It prefers an explicit talkgroup
// jurisdiction (smaller service area) over the county-wide system geo.
//
// Both an explicit talkgroup jurisdiction and the system's configured radius are
// the admin's statement of the real service area, so both are honored as
// configured. Cross-border homonyms (the original reason for a tight cap) are
// already rejected by the configured-state filter, and intra-state homonyms are
// disambiguated by the scoped inner-disc proximity scoring — a county-wide fire
// dispatch (e.g. "78 FD DISP") must be able to pin addresses across its whole
// county, not just near the county seat. The 5-mile default applies only when no
// radius is configured at all.
func toneFallbackTightestBounds(base, systemCfg IncidentMappingConfig) IncidentMappingConfig {
	out := systemCfg
	if !base.Inherit && base.GeoLat != 0 && base.GeoRadiusMiles > 0 {
		out = base
	}
	out.Enabled = base.Enabled
	out.CoverageAddress = base.CoverageAddress
	out.CoverageNature = base.CoverageNature
	out.ExtractAddressWithGemini = base.ExtractAddressWithGemini
	if out.GeoRadiusMiles <= 0 {
		out.GeoRadiusMiles = toneSetFallbackMaxRadiusMi
	}
	return out
}

func buildGeoOptions(cfg IncidentMappingConfig) *mapping.GeoOptions {
	geo := &mapping.GeoOptions{}
	if cfg.GeoCity != "" && cfg.GeoRadiusMiles > 0 && cfg.GeoLat != 0 {
		geo.BoundsLat = cfg.GeoLat
		geo.BoundsLon = cfg.GeoLon
		geo.BoundsRadiusMi = cfg.GeoRadiusMiles
		geo.CityHint = cfg.GeoCity
	}
	if strings.TrimSpace(cfg.LocationContext) != "" {
		geo.LocationContext = cfg.LocationContext
	} else if geo.CityHint != "" {
		geo.LocationContext = geo.CityHint
	}
	if st := strings.TrimSpace(cfg.GeoState); st != "" {
		geo.State = st
	} else {
		geo.State = mapping.DeriveState(cfg.LocationContext, "", geo.CityHint)
	}
	return geo
}

func mutualAidDestinationsForSystem(sys *System) []mapping.MutualAidDestination {
	if sys == nil || sys.Talkgroups == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []mapping.MutualAidDestination
	for _, tg := range sys.Talkgroups.List {
		if tg == nil {
			continue
		}
		cfg := tg.IncidentMapping
		if cfg.GeoLat == 0 || cfg.GeoRadiusMiles <= 0 || strings.TrimSpace(cfg.GeoCity) == "" {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(cfg.GeoCity))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, mapping.MutualAidDestination{
			CityLabel: cfg.GeoCity,
			Lat:       cfg.GeoLat,
			Lon:       cfg.GeoLon,
			RadiusMi:  cfg.GeoRadiusMiles,
		})
	}
	return out
}

// incidentMappingMaxConcurrency bounds concurrent ProcessCall executions.
// Each call does a synchronous OpenAI nature-classify request plus a chain of
// local/external geocode attempts; callers spawn ProcessCall from an
// unbounded `go func(){...}()` per transcribed call (see transcription_queue.go),
// so during high-volume bursts (60-100+ calls/min across several systems)
// dozens of these could run at once with nothing limiting them. That fan-out
// was observed pushing individual calls to 60-400s of "extract+nature-classify"
// time even though no single stage's own logic (3 retries x 5s timeout on the
// OpenAI call) should ever take that long on its own — the calls were simply
// piling up and contending for OpenAI rate limits / outbound sockets / CPU
// simultaneously. Capping concurrency here trades a bounded queue wait for
// eliminating that self-inflicted pileup.
const incidentMappingMaxConcurrency = 6

type IncidentMappingQueue struct {
	controller *Controller
	sem        chan struct{}
}

func NewIncidentMappingQueue(controller *Controller) *IncidentMappingQueue {
	return &IncidentMappingQueue{
		controller: controller,
		sem:        make(chan struct{}, incidentMappingMaxConcurrency),
	}
}

// collectNatureKeywords returns every keyword from every configured Keyword
// List. These drive incident-nature detection when call-nature categories are
// not configured. Prefer collectCallNatureData when available.
func (q *IncidentMappingQueue) collectNatureKeywords() []string {
	if q == nil || q.controller == nil || q.controller.KeywordListsCache == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, list := range q.controller.KeywordListsCache.GetAllLists() {
		if list == nil {
			continue
		}
		for _, kw := range list.Keywords {
			k := strings.ToUpper(strings.TrimSpace(kw))
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func (q *IncidentMappingQueue) collectCallNatureData() CallNatureMatchData {
	if q != nil && q.controller != nil && q.controller.CallNaturesCache != nil {
		q.controller.Options.mutex.Lock()
		mi := q.controller.Options.MappingIntegration
		openAIKey := strings.TrimSpace(q.controller.Options.OpenAIIntegration.APIKey)
		q.controller.Options.mutex.Unlock()
		openAI := effectiveCallNatureOpenAIClassify(mi, openAIKey)
		data := q.controller.CallNaturesCache.MatchData(openAI)
		if len(data.Labels) > 0 {
			return data
		}
	}
	legacy := q.collectNatureKeywords()
	return CallNatureMatchData{
		Labels:        legacy,
		MatchTerms:    legacy,
		PhraseToLabel: map[string]string{},
	}
}

func (q *IncidentMappingQueue) ProcessCall(call *Call, transcript string) {
	if q == nil || q.controller == nil || call == nil {
		return
	}
	if strings.TrimSpace(transcript) == "" {
		return
	}
	if call.System == nil || call.Talkgroup == nil {
		return
	}
	if !q.controller.isVoiceForToneAlerts(transcript) {
		return
	}

	// Bound concurrent ProcessCall executions — see incidentMappingMaxConcurrency.
	if q.sem != nil {
		semWaitStart := time.Now()
		q.sem <- struct{}{}
		defer func() { <-q.sem }()
		if semWait := time.Since(semWaitStart); semWait > 3*time.Second {
			q.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
				"incident mapping call %d: waited %.1fs for a mapping worker slot (cap=%d)",
				call.Id, semWait.Seconds(), incidentMappingMaxConcurrency))
		}
	}

	// Stage timing — ProcessCall has no external calls of its own until the
	// address-geocode lookup below, but slow calls have been observed to take
	// 60-90s with the eventual external geocode request itself logged as
	// near-instant (see mappingGeocodeExternalLog). These stage markers
	// pinpoint which of extract/nature-classify (mapping.Process) vs the
	// address-geocode lookup loop actually burns the time, instead of
	// guessing from the single start/end log line.
	funcStart := time.Now()
	stageStart := funcStart
	logSlowStage := func(stage string) time.Time {
		now := time.Now()
		if d := now.Sub(stageStart); d > 5*time.Second {
			q.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
				"incident mapping call %d: %s took %.1fs", call.Id, stage, d.Seconds()))
		}
		return now
	}

	cfg, toneSetGeo, skipTalkgroupGeo := resolveIncidentMappingForCall(call.System, call.Talkgroup, call.ToneSequence)
	if !cfg.Enabled {
		return
	}

	q.controller.Options.mutex.Lock()
	mapInteg := q.controller.Options.MappingIntegration
	openAI := q.controller.Options.OpenAIIntegration
	q.controller.Options.mutex.Unlock()

	openAIKey := strings.TrimSpace(openAI.APIKey)
	localEngine := mapInteg.engineIsLocal()

	// Always merge the system-level gazetteer (OSM streets, known places,
	// corrections) into the talkgroup scope. The talkgroup's incident-mapping
	// "inherit" flag only governs its geo settings (city center / radius), not
	// the base map — a talkgroup with its own city center still needs the full
	// system street data to geocode against. Tone-set geo uses system scope with
	// tone-set center bounds (parent talkgroup geo is never used when tones match).
	store := NewMappingStore(q.controller.Database)
	tgScope := uint64(0)
	if !toneSetGeo && !skipTalkgroupGeo && call.Talkgroup != nil && !call.Talkgroup.IncidentMapping.Inherit {
		tgScope = call.Talkgroup.Id
	}
	scope := store.LoadScope(call.System.Id, tgScope, true)
	geo := buildGeoOptions(cfg)
	// Lock in the resolved, admin-configured bounds as an unconditional outer
	// ceiling BEFORE any dynamic override (mutual aid, dispatch locality hint,
	// tone-set fallback) gets a chance to rewrite BoundsLat/BoundsRadiusMi for
	// its own request-scoped purpose — see the HomeLat/HomeLon/HomeMaxRadiusMi
	// doc comment on GeoOptions for why this exists as a second, independent
	// check rather than trusting Bounds alone.
	if geo.BoundsLat != 0 && geo.BoundsRadiusMi > 0 {
		geo.HomeLat = geo.BoundsLat
		geo.HomeLon = geo.BoundsLon
		geo.HomeMaxRadiusMi = geo.BoundsRadiusMi + homeMaxRadiusSafetyBufferMi
	}
	// Nominatim add-on: TLR now talks to the nominatim-gateway service
	// DIRECTLY, using the gateway_url the relay reported on its last
	// /api/geocode/status poll (see relay_account.go) — the relay itself
	// only handles billing/allow-list sync, not individual queries anymore.
	// Only wire this up when the cached poll says the subscription is
	// actually active AND a gateway URL has been learned — otherwise skip
	// the round trip entirely (no external address geocoding for this call).
	if q.controller.NominatimAccessAllowed() {
		geo.NominatimDirectURL = q.controller.NominatimGatewayURLSnapshot()
		q.controller.Options.mutex.Lock()
		geo.NominatimAPIKey = strings.TrimSpace(q.controller.Options.RelayServerAPIKey)
		q.controller.Options.mutex.Unlock()
	}
	geo.MutualAidDestinations = mutualAidDestinationsForSystem(call.System)
	geo = mapping.ApplyMutualAidGeoOverride(geo, transcript)
	// Transcript-mined city hints (ApplyDispatchLocalityGeoHint) are disabled —
	// geocode bias is talkgroup / tone-set bounds only. Mutual aid may still
	// set DispatchSpokenLocality; resolve that to a boundary disc when possible
	// so destination pins outside the home radius stay in coverage. Anything
	// that is not a configured mutual-aid destination and has no imported
	// boundary is dropped so it cannot gate house-level resolution.
	if spoken := mapping.DispatchSpokenLocalityFromGeo(geo); spoken != "" {
		resolved := false
		if geo.BoundsLat != 0 {
			if slat, slon, sradius, ok := store.BoundaryCentroidForLocality(spoken, geo.BoundsLat, geo.BoundsLon); ok {
				geo.SpokenLocalityLat = slat
				geo.SpokenLocalityLon = slon
				geo.SpokenLocalityRadiusMi = sradius
				resolved = true
			}
		}
		if !resolved && !spokenLocalityIsMutualAidDestination(geo, spoken) {
			geo.DispatchSpokenLocality = ""
		}
	}

	toneLabel := call.Talkgroup.Label
	if call.ToneSequence != nil && call.ToneSequence.MatchedToneSet != nil {
		toneLabel = call.ToneSequence.MatchedToneSet.Label
	}

	engine := "local"

	// Incident nature uses configured call-nature categories (labels + phrases),
	// falling back to legacy keyword lists when none are seeded.
	natureData := q.collectCallNatureData()

	stageStart = logSlowStage("setup (scope/mutual-aid) before mapping.Process")

	out := mapping.Process(mapping.ProcessInput{
		Transcript:                transcript,
		ToneSetLabel:              toneLabel,
		OpenAIKey:                 openAIKey,
		OpenAIModel:               mapInteg.resolvedOpenAIModel(openAI),
		Scope:                     scope,
		Geo:                       geo,
		CoverageAddress:           cfg.CoverageAddress,
		CoverageNature:            cfg.CoverageNature,
		NatureCodes:               natureData.Labels,
		NatureMatchTerms:          natureData.MatchTerms,
		NaturePhraseToLabel:       natureData.PhraseToLabel,
		CallNatureOpenAIClassify:  natureData.OpenAIClassify,
		SuppressUnknownNaturePins: mapInteg.SuppressUnknownNaturePins,
		Engine:                    engine,
	})

	stageStart = logSlowStage("extract+nature-classify (mapping.Process)")

	if out.Primary != nil {
		inheritPeerAgencyIncidentPin(store, call.Id, call.System.Id, call.Timestamp.UnixMilli(), transcript, geo, scope, out.Primary)
	}

	if out.Primary == nil {
		return
	}

	source := out.Source
	status := out.Status

	// Full dispatch text for gateway street-scan / transcript geocode.
	geo.DispatchTranscript = strings.TrimSpace(out.Cleaned)
	if geo.DispatchTranscript == "" {
		geo.DispatchTranscript = transcript
	}

	// Gemini short address may fill an empty card, but never replace the full
	// dispatch sent to the gateway — a wrong short string starves street-scan /
	// addr-index recovery that needs the original transcript.
	geminiAddr := strings.TrimSpace(call.ExtractedAddress)
	if geminiAddr == "" {
		geminiAddr = loadCallExtractedAddress(q.controller, call.Id)
		call.ExtractedAddress = geminiAddr
	}
	if cfg.ExtractAddressWithGemini && geminiAddr != "" &&
		strings.TrimSpace(out.Primary.Address) == "" {
		out.Primary.Address = geminiAddr
		if status == "skipped" || status == "failed" {
			status = "failed"
		}
	}

	// Street pins come from Nominatim. Process may only pre-pin known-place
	// POIs (no local OSM geometry). Clear any stale non-known_place local pin
	// so Nominatim always owns house/street placement.
	if localEngine && out.Primary.Lat != "" && source != "known_place" {
		out.Primary.Lat = ""
		out.Primary.Lon = ""
		source = ""
	}

	// Gateway owns geocoding: ONE POST /transcript with the full dispatch.
	// "skipped" is reserved for no-location pre-screen; empty extract uses
	// "failed" so we still attempt recovery. Also allow gateway when the
	// transcript still looks location-bearing even if status stayed skipped
	// (older Process paths / peer-agency edge cases).
	//
	// Nature is already classified in mapping.Process above. When the admin
	// toggle is on, catch-all UNKNOWN PROBLEM natures skip the gateway so we
	// do not spend geocoding quota on unclassified traffic.
	geocodedStreetNamed := false
	// Blank nature displays as UNKNOWN PROBLEM on the map — suppress the same way.
	natureBlankOrUnknown := strings.TrimSpace(out.Primary.NatureDesc) == "" ||
		mapping.IsDefaultUnknownNatureLabel(out.Primary.NatureDesc)
	skipUnknownGeocode := mapInteg.SuppressUnknownNaturePins && natureBlankOrUnknown
	gatewayConfigured := strings.TrimSpace(geo.NominatimDirectURL) != "" &&
		strings.TrimSpace(geo.NominatimAPIKey) != ""
	gatewayText := strings.TrimSpace(geo.DispatchTranscript)
	if gatewayText == "" {
		gatewayText = strings.TrimSpace(out.Primary.Address)
	}
	locationLikely := mapping.TranscriptLikelyHasLocation(gatewayText, scope) ||
		strings.TrimSpace(out.Primary.Address) != ""
	allowGateway := status != "skipped" || locationLikely
	if skipUnknownGeocode {
		mapping.SuppressUnclassifiedPinOnAlert(out.Primary, &status)
		q.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf(
			"incident mapping call %d skipped geocode: unknown nature %q",
			call.Id, strings.TrimSpace(out.Primary.NatureDesc)))
	} else if out.Primary.Lat == "" && allowGateway && gatewayConfigured && gatewayText != "" && locationLikely {
		hit := mapping.GeocodeRelayNominatimFromTranscript(
			geo.NominatimDirectURL, geo.NominatimAPIKey, gatewayText, geo)
		store.logGeocodeExternal(call.System.Id, "relay_nominatim_transcript",
			hit.Query, hit.Lat, hit.Lon, hit.DisplayName, hit.OK, hit.Detail)
		if hit.OK {
			mapping.ApplyTranscriptGeocodeHit(out.Primary, hit)
			source = "relay_nominatim"
			status = "geocoded"
			out.Query = hit.Query
			geocodedStreetNamed = strings.TrimSpace(hit.StreetName) != ""
			stageStart = logSlowStage("transcript→gateway geocode")
		}
	}

	if strings.TrimSpace(out.Primary.NatureDesc) == "" && strings.TrimSpace(out.Primary.Address) == "" {
		if err := store.ClearCallIncident(call.Id); err != nil {
			q.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("incident mapping clear call %d: %v", call.Id, err))
		} else {
			q.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("incident mapping call %d skipped: no address or nature", call.Id))
			q.broadcastIncidentUpdate(call, nil, "skipped")
		}
		return
	}

	// Local geocode cache only — never open a second external geocode path
	// when the gateway is configured (or at all for multi-query /search).
	if !skipUnknownGeocode && out.Primary.Lat == "" && strings.TrimSpace(out.Primary.Address) != "" {
		qNorm := mapping.GeocodeQueryAddress(out.Primary)
		if qNorm == "" {
			qNorm = out.Primary.Address
		}
		if lat, lon, _, ok := store.LookupGeocodeCache(call.System.Id, qNorm); ok && !mapping.PinOutsideCoverage(lat, lon, geo) {
			out.Primary.Lat = fmt.Sprintf("%.6f", lat)
			out.Primary.Lon = fmt.Sprintf("%.6f", lon)
			source = "cache"
			status = "local"
		}
	}

	// Final guard: never persist coordinates outside talkgroup/system coverage.
	if out.Primary.Lat != "" && out.Primary.Lon != "" {
		var lat, lon float64
		fmt.Sscanf(out.Primary.Lat, "%f", &lat)
		fmt.Sscanf(out.Primary.Lon, "%f", &lon)
		if mapping.PinOutsideCoverage(lat, lon, geo) {
			out.Primary.Lat = ""
			out.Primary.Lon = ""
			if status == "geocoded" || status == "local" {
				status = "failed"
			}
		}
	}

	// Gateway transcript geocode already chose house + street + pin. Do not
	// re-align the card from STT — that fought fuzzy adopts (Everett Hall→
	// Cortland Hull, STAMBALL→Stambaugh). Coverage checks above still apply.
	if !(source == "relay_nominatim" && geocodedStreetNamed) &&
		!mapping.TranscriptIsPeerAgencySquadRequest(transcript) {
		geocodedStreetNamed = mapping.FinalizeIncidentCardAddress(out.Primary, transcript, scope, geo, geocodedStreetNamed)
	}
	if mapInteg.SuppressUnknownNaturePins {
		mapping.SuppressUnclassifiedPinOnAlert(out.Primary, &status)
	}
	if mapping.TranscriptIsAdministrativeLocationReference(transcript) &&
		mapping.IsDefaultUnknownNatureLabel(out.Primary.NatureDesc) {
		out.Primary.Address = ""
	}
	if out.Primary.Lat == "" && out.Primary.Lon == "" && strings.TrimSpace(out.Primary.Address) != "" &&
		(status == "geocoded" || status == "local" || status == "approximate") {
		status = "failed"
	}
	mapping.ClearIncidentLocationUnlessGeocoded(out.Primary, &status)

	if mapInteg.AutoLearnKnownPlaces && out.Primary.Lat != "" && strings.TrimSpace(out.Primary.Address) != "" &&
		mapping.AddressQualifiesForAutoLearn(out.Primary.Address, transcript) {
		purged := store.PurgeBadAutoLearnPlaces(call.System.Id, out.Primary, scope, geo)
		if purged > 0 {
			q.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("incident mapping call %d purged %d stale auto_learn pin(s)", call.Id, purged))
		}
		var lat, lon float64
		fmt.Sscanf(out.Primary.Lat, "%f", &lat)
		fmt.Sscanf(out.Primary.Lon, "%f", &lon)
		if (lat != 0 || lon != 0) && !mapping.PinOutsideCoverage(lat, lon, geo) {
			learnAddr := out.Primary.Address
			var tgPtr *uint64
			if call.Talkgroup != nil && tgScope > 0 && !toneSetGeo && !skipTalkgroupGeo {
				id := call.Talkgroup.Id
				tgPtr = &id
			}
			_ = store.UpsertKnownPlace(call.System.Id, tgPtr, learnAddr, lat, lon, "", "auto_learn")
		}
	}

	if err := store.SaveCallIncident(call.Id, out.Primary, out.Extras, status, source, out.Query); err != nil {
		q.controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("incident mapping save call %d: %v", call.Id, err))
		return
	}

	if elapsed := time.Since(funcStart); elapsed > 5*time.Second {
		q.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("incident mapping call %d status=%s source=%s addr=%q elapsed=%.1fs",
			call.Id, status, source, out.Primary.Address, elapsed.Seconds()))
	} else {
		q.controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("incident mapping call %d status=%s source=%s addr=%q",
			call.Id, status, source, out.Primary.Address))
	}
	q.broadcastIncidentUpdate(call, out.Primary, status)
}

func (q *IncidentMappingQueue) broadcastIncidentUpdate(call *Call, primary *mapping.CuratedAlert, status string) {
	if q == nil || q.controller == nil || call == nil || call.System == nil || call.Talkgroup == nil {
		return
	}
	payload := map[string]any{
		"callId":                call.Id,
		"systemId":              call.System.Id,
		"talkgroupId":           call.Talkgroup.Id,
		"incidentGeocodeStatus": status,
	}
	if primary != nil {
		var lat, lon float64
		fmt.Sscanf(primary.Lat, "%f", &lat)
		fmt.Sscanf(primary.Lon, "%f", &lon)
		payload["incidentAddress"] = primary.Address
		payload["incidentNature"] = primary.NatureDesc
		payload["incidentLat"] = lat
		payload["incidentLon"] = lon
	}
	q.controller.Clients.EmitIncidentUpdate(q.controller, call, payload)
}

func inheritPeerAgencyIncidentPin(store *MappingStore, callID uint64, systemID uint64, timestampMs int64, transcript string, geo *mapping.GeoOptions, scope *mapping.ScopeData, primary *mapping.CuratedAlert) {
	if store == nil || primary == nil || strings.TrimSpace(primary.Address) != "" {
		return
	}
	if !mapping.TranscriptIsPeerAgencySquadRequest(transcript) {
		return
	}
	hint := mapping.MutualAidJurisdictionHint(transcript, geo)
	addr, lat, lon, sourceTr, ok := store.LookupRecentPeerIncidentPin(systemID, int64(callID), timestampMs, hint)
	if !ok {
		return
	}
	primary.Address = addr
	primary.Lat = fmt.Sprintf("%.6f", lat)
	primary.Lon = fmt.Sprintf("%.6f", lon)
	alignTr := transcript
	if strings.TrimSpace(sourceTr) != "" {
		alignTr = sourceTr
		if scope != nil {
			inhHouse, _ := splitHouseAndStreetMain(primary.Address)
			best := ""
			for _, spoken := range mapping.CollectTranscriptDispatchAddresses(sourceTr, scope) {
				sh, _ := splitHouseAndStreetMain(spoken)
				if inhHouse != "" && sh == inhHouse && len(spoken) > len(best) {
					best = spoken
				}
			}
			if best != "" {
				primary.Address = best
			}
		}
	}
	primary.Address = mapping.AlignAddressSuffixFromTranscript(primary.Address, alignTr)
}
