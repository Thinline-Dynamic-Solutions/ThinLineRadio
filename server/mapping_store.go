// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rdio-scanner/server/mapping"
)

// MappingIntegration holds server-wide incident mapping settings.
type MappingIntegration struct {
	GeocodeCacheMaxAgeDays uint `json:"geocodeCacheMaxAgeDays"`
	AutoLearnKnownPlaces   bool `json:"autoLearnKnownPlaces"`
	MaxGeocodeCandidates   uint `json:"maxGeocodeCandidatesPerCall"`
	OpenAIModel            string `json:"openAIModel"` // empty = use openAIIntegration.model
	// MappingEngine is retained for interface stability with existing config
	// rows but is no longer branched on — mapping always uses the built-in
	// rule-based extractor + outbound geocode chain (Thinline Geocoding API →
	// cache). The legacy OpenAI-extraction + Google/Geocodify pipeline was
	// removed, and the direct Census API fallback was removed too (redundant
	// with the TIGER data already imported into the Thinline Geocoding API).
	MappingEngine string `json:"mappingEngine"`
	// CallNatureOpenAIClassify enables OpenAI to pick a call-nature category when
	// configured phrase matching finds nothing.
	CallNatureOpenAIClassify bool `json:"callNatureOpenAIClassify"`
	// CallNatureOpenAIClassifyConfigured is set when callNatureOpenAIClassify was
	// explicitly saved in admin (so we can default-on for local engine when unset).
	CallNatureOpenAIClassifyConfigured bool `json:"-"`
	// SuppressUnknownNaturePins skips geocoding and drops map pins when the
	// call nature is a catch-all unknown (UNKNOWN PROBLEM, etc.).
	SuppressUnknownNaturePins bool `json:"suppressUnknownNaturePins"`
	// MapBoundariesEnabled shows Census boundary overlays on the incident map.
	MapBoundariesEnabled bool `json:"mapBoundariesEnabled"`
	// MapBoundaryLayers lists enabled overlay layers: county, place, cousub.
	MapBoundaryLayers []string `json:"mapBoundaryLayers"`
	// SendLocationContext appends talkgroup/system incident-mapping location
	// context (LocationContext / GeoCity) to the STT prompt when available.
	SendLocationContext bool `json:"sendLocationContext"`
}

// effectiveCallNatureOpenAIClassify reports whether OpenAI should classify call
// nature when phrase matching fails. Defaults on for the local engine when an
// OpenAI key is configured unless admin explicitly turned the toggle off.
func effectiveCallNatureOpenAIClassify(mi MappingIntegration, openAIKey string) bool {
	if mi.CallNatureOpenAIClassifyConfigured {
		return mi.CallNatureOpenAIClassify
	}
	if mi.engineIsLocal() && strings.TrimSpace(openAIKey) != "" {
		return true
	}
	return mi.CallNatureOpenAIClassify
}

// engineIsLocal reports whether the local rules-based mapping engine is
// selected. Always true — the legacy OpenAI-extraction + Google/Geocodify
// "external" engine was removed, so the local engine is the only one left.
func (mi MappingIntegration) engineIsLocal() bool {
	return true
}

func (mi MappingIntegration) resolvedOpenAIModel(fallback OpenAIIntegration) string {
	m := strings.TrimSpace(mi.OpenAIModel)
	if m == "" {
		return fallback.resolvedChatModel()
	}
	for _, supported := range SupportedOpenAIChatModels {
		if m == supported {
			return m
		}
	}
	return fallback.resolvedChatModel()
}

type MappingStore struct {
	db *Database
}

func NewMappingStore(db *Database) *MappingStore {
	return &MappingStore{db: db}
}

// LoadMappingIntegrationConfig reads server-wide incident mapping settings.
func (ms *MappingStore) LoadMappingIntegrationConfig() MappingIntegration {
	if ms == nil || ms.db == nil || ms.db.Sql == nil {
		return MappingIntegration{}
	}
	var raw string
	if err := ms.db.Sql.QueryRow(`SELECT "value" FROM "options" WHERE "key"='mappingIntegration'`).Scan(&raw); err != nil {
		return MappingIntegration{}
	}
	var cfg MappingIntegration
	if json.Unmarshal([]byte(raw), &cfg) != nil {
		return MappingIntegration{}
	}
	return cfg
}

// normalizeStreetName canonicalizes a street name for local lookups.
// produce the same key for OSM's full names ("West Clifton Boulevard") and the
// abbreviated forms in dispatch transcripts ("W Clifton Blvd"), so both import
// and lookup go through mapping.CanonicalStreetName (route/type/directional
// normalization).
func normalizeStreetName(s string) string {
	return mapping.CanonicalStreetName(s)
}

func (ms *MappingStore) LoadScope(systemId, talkgroupId uint64, inherit bool) *mapping.ScopeData {
	scope := &mapping.ScopeData{}
	scope.KnownStreets = ms.loadKnownStreets(systemId, talkgroupId, inherit)
	scope.Corrections = ms.loadCorrections(systemId, talkgroupId, inherit)
	scope.KnownPlaces = ms.loadKnownPlaces(systemId, talkgroupId, inherit)
	return scope
}

func (ms *MappingStore) loadKnownStreets(systemId, talkgroupId uint64, inherit bool) []string {
	seen := map[string]bool{}
	var out []string
	appendRows := func(tgFilter string, tgArg interface{}) {
		q := `SELECT "streetName" FROM "mappingKnownStreets" WHERE "systemId" = $1 AND ` + tgFilter + ` ORDER BY "streetName"`
		if ms.db.Config.DbType != DbTypePostgresql {
			q = strings.ReplaceAll(q, "$1", "?")
		}
		var rows *sql.Rows
		var err error
		if tgArg == nil {
			if ms.db.Config.DbType == DbTypePostgresql {
				rows, err = ms.db.Sql.Query(q, systemId)
			} else {
				rows, err = ms.db.Sql.Query(q, systemId)
			}
		} else {
			if ms.db.Config.DbType == DbTypePostgresql {
				rows, err = ms.db.Sql.Query(q, systemId, tgArg)
			} else {
				rows, err = ms.db.Sql.Query(q, systemId, tgArg)
			}
		}
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				key := strings.ToUpper(strings.TrimSpace(name))
				if key != "" && !seen[key] {
					seen[key] = true
					out = append(out, name)
				}
			}
		}
	}
	if inherit {
		appendRows(`"talkgroupId" IS NULL`, nil)
	}
	if talkgroupId > 0 {
		appendRows(`"talkgroupId" = $2`, talkgroupId)
	}
	return out
}

func (ms *MappingStore) loadCorrections(systemId, talkgroupId uint64, inherit bool) []mapping.StreetCorrection {
	var out []mapping.StreetCorrection
	byBad := map[string]mapping.StreetCorrection{}
	load := func(tgNull bool, tgId uint64) {
		var q string
		var rows *sql.Rows
		var err error
		if tgNull {
			q = `SELECT "badName", "correctName" FROM "mappingStreetCorrections" WHERE "systemId" = $1 AND "talkgroupId" IS NULL`
			rows, err = ms.db.Sql.Query(q, systemId)
		} else {
			q = `SELECT "badName", "correctName" FROM "mappingStreetCorrections" WHERE "systemId" = $1 AND "talkgroupId" = $2`
			rows, err = ms.db.Sql.Query(q, systemId, tgId)
		}
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var bad, correct string
			if rows.Scan(&bad, &correct) == nil {
				byBad[strings.ToUpper(bad)] = mapping.StreetCorrection{BadName: bad, CorrectName: correct}
			}
		}
	}
	if inherit {
		load(true, 0)
	}
	if talkgroupId > 0 {
		load(false, talkgroupId)
	}
	for _, c := range byBad {
		out = append(out, c)
	}
	return out
}

func (ms *MappingStore) loadKnownPlaces(systemId, talkgroupId uint64, inherit bool) []mapping.KnownPlace {
	var out []mapping.KnownPlace
	byKey := map[string]mapping.KnownPlace{}
	load := func(tgNull bool, tgId uint64) {
		var rows *sql.Rows
		var err error
		if tgNull {
			rows, err = ms.db.Sql.Query(`SELECT "placeKey", "displayName", "lat", "lon", "addressHint", "source", "updatedAt" FROM "mappingKnownPlaces" WHERE "systemId" = $1 AND "talkgroupId" IS NULL`, systemId)
		} else {
			rows, err = ms.db.Sql.Query(`SELECT "placeKey", "displayName", "lat", "lon", "addressHint", "source", "updatedAt" FROM "mappingKnownPlaces" WHERE "systemId" = $1 AND "talkgroupId" = $2`, systemId, tgId)
		}
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var p mapping.KnownPlace
			if rows.Scan(&p.PlaceKey, &p.DisplayName, &p.Lat, &p.Lon, &p.AddressHint, &p.Source, &p.UpdatedAt) == nil {
				byKey[p.PlaceKey] = p
			}
		}
	}
	if inherit {
		load(true, 0)
	}
	if talkgroupId > 0 {
		load(false, talkgroupId)
	}
	for _, p := range byKey {
		out = append(out, p)
	}
	return out
}

// LoadMutualAidDestinations returns geocode centers from talkgroup incident-mapping
// configs on a system (used when mutual-aid dispatches name a peer jurisdiction).
func (ms *MappingStore) LoadMutualAidDestinations(systemId uint64) []mapping.MutualAidDestination {
	if ms == nil || ms.db == nil || ms.db.Sql == nil {
		return nil
	}
	rows, err := ms.db.Sql.Query(`SELECT COALESCE("incidentMappingConfig",'{}') FROM talkgroups WHERE "systemId" = $1`, systemId)
	if err != nil {
		return nil
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []mapping.MutualAidDestination
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		cfg := parseIncidentMappingConfig(raw)
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

func (ms *MappingStore) LookupGeocodeCache(systemId uint64, query string) (lat, lon float64, formatted string, ok bool) {
	norm := strings.ToUpper(strings.TrimSpace(query))
	err := ms.db.Sql.QueryRow(`SELECT "lat", "lon", "formattedAddress" FROM "mappingGeocodeCache" WHERE "systemId" = $1 AND "queryNormalized" = $2`,
		systemId, norm).Scan(&lat, &lon, &formatted)
	if err != nil {
		return 0, 0, "", false
	}
	mapping.RecordGeocodeCacheHit()
	_, _ = ms.db.Sql.Exec(`UPDATE "mappingGeocodeCache" SET "hitCount" = "hitCount" + 1, "lastUsedAt" = $1 WHERE "systemId" = $2 AND "queryNormalized" = $3`,
		time.Now().UnixMilli(), systemId, norm)
	return lat, lon, formatted, true
}

func (ms *MappingStore) LookupGeocodeMiss(systemId uint64, query, provider string) bool {
	norm := strings.ToUpper(strings.TrimSpace(query))
	provider = strings.ToLower(strings.TrimSpace(provider))
	if norm == "" || provider == "" {
		return false
	}
	var reason string
	err := ms.db.Sql.QueryRow(`SELECT "reason" FROM "mappingGeocodeMissCache" WHERE "systemId" = $1 AND "queryNormalized" = $2 AND "provider" = $3`,
		systemId, norm, provider).Scan(&reason)
	if err != nil {
		return false
	}
	// Stale bounds-dependent misses must not short-circuit later retries.
	if geocodeMissReasonIsBoundsDependent(reason) {
		_, _ = ms.db.Sql.Exec(`DELETE FROM "mappingGeocodeMissCache" WHERE "systemId" = $1 AND "queryNormalized" = $2 AND "provider" = $3`,
			systemId, norm, provider)
		return false
	}
	now := time.Now().UnixMilli()
	_, _ = ms.db.Sql.Exec(`UPDATE "mappingGeocodeMissCache" SET "hitCount" = "hitCount" + 1, "lastUsedAt" = $1 WHERE "systemId" = $2 AND "queryNormalized" = $3 AND "provider" = $4`,
		now, systemId, norm, provider)
	return true
}

func (ms *MappingStore) lookupGeocodeMissReason(systemId uint64, query, provider string) string {
	norm := strings.ToUpper(strings.TrimSpace(query))
	provider = strings.ToLower(strings.TrimSpace(provider))
	if norm == "" || provider == "" {
		return ""
	}
	var reason string
	err := ms.db.Sql.QueryRow(`SELECT "reason" FROM "mappingGeocodeMissCache" WHERE "systemId" = $1 AND "queryNormalized" = $2 AND "provider" = $3`,
		systemId, norm, provider).Scan(&reason)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(reason)
}

func (ms *MappingStore) SaveGeocodeMiss(systemId uint64, query, provider, reason string) {
	norm := strings.ToUpper(strings.TrimSpace(query))
	provider = strings.ToLower(strings.TrimSpace(provider))
	if norm == "" || provider == "" {
		return
	}
	now := time.Now().UnixMilli()
	_, _ = ms.db.Sql.Exec(`INSERT INTO "mappingGeocodeMissCache" ("systemId", "queryNormalized", "provider", "reason", "hitCount", "lastUsedAt") VALUES ($1,$2,$3,$4,1,$5)
		ON CONFLICT ("systemId", "queryNormalized", "provider") DO UPDATE SET "reason" = EXCLUDED."reason", "hitCount" = "mappingGeocodeMissCache"."hitCount" + 1, "lastUsedAt" = EXCLUDED."lastUsedAt"`,
		systemId, norm, provider, strings.TrimSpace(reason), now)
}

func (ms *MappingStore) logGeocodeExternal(systemId uint64, provider, query string, lat, lon float64, matched string, ok bool, detail string) {
	norm := strings.ToUpper(strings.TrimSpace(query))
	now := time.Now().UnixMilli()
	_, _ = ms.db.Sql.Exec(`INSERT INTO "mappingGeocodeExternalLog" ("systemId", "provider", "queryNormalized", "ok", "lat", "lon", "matchedAddress", "detail", "sentAt") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		systemId, strings.ToLower(strings.TrimSpace(provider)), norm, ok, lat, lon, matched, detail, now)
}

func (ms *MappingStore) DeleteGeocodeCache(systemId uint64, query string) {
	norm := strings.ToUpper(strings.TrimSpace(query))
	if norm == "" {
		return
	}
	_, _ = ms.db.Sql.Exec(`DELETE FROM "mappingGeocodeCache" WHERE "systemId" = $1 AND "queryNormalized" = $2`, systemId, norm)
}

func (ms *MappingStore) SaveGeocodeCache(systemId uint64, query string, lat, lon float64, formatted string) {
	norm := strings.ToUpper(strings.TrimSpace(query))
	if norm == "" {
		return
	}
	now := time.Now().UnixMilli()
	_, _ = ms.db.Sql.Exec(`INSERT INTO "mappingGeocodeCache" ("systemId", "queryNormalized", "lat", "lon", "formattedAddress", "hitCount", "lastUsedAt") VALUES ($1,$2,$3,$4,$5,1,$6)
		ON CONFLICT ("systemId", "queryNormalized") DO UPDATE SET "lat" = EXCLUDED."lat", "lon" = EXCLUDED."lon", "formattedAddress" = EXCLUDED."formattedAddress", "hitCount" = "mappingGeocodeCache"."hitCount" + 1, "lastUsedAt" = EXCLUDED."lastUsedAt"`,
		systemId, norm, lat, lon, formatted, now)
}

func (ms *MappingStore) UpsertKnownPlace(systemId uint64, talkgroupId *uint64, displayName string, lat, lon float64, addressHint, source string) error {
	key := mapping.NormalizePlaceKey(displayName)
	if key == "" {
		return fmt.Errorf("empty place key")
	}
	now := time.Now().UnixMilli()
	var tg interface{}
	if talkgroupId != nil && *talkgroupId > 0 {
		tg = *talkgroupId
	}
	_, err := ms.db.Sql.Exec(`DELETE FROM "mappingKnownPlaces" WHERE "systemId" = $1 AND COALESCE("talkgroupId", 0) = COALESCE($2::bigint, 0) AND "placeKey" = $3`,
		systemId, tg, key)
	if err != nil {
		_, _ = ms.db.Sql.Exec(`DELETE FROM "mappingKnownPlaces" WHERE "systemId" = $1 AND "placeKey" = $2`, systemId, key)
	}
	_, err = ms.db.Sql.Exec(`INSERT INTO "mappingKnownPlaces" ("systemId", "talkgroupId", "placeKey", "displayName", "lat", "lon", "addressHint", "source", "updatedAt")
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		systemId, tg, key, strings.ToUpper(displayName), lat, lon, addressHint, source, now)
	return err
}

func (ms *MappingStore) DeleteAutoLearnKnownPlaceByKey(systemId uint64, placeKey string) error {
	placeKey = strings.TrimSpace(placeKey)
	if placeKey == "" {
		return nil
	}
	_, err := ms.db.Sql.Exec(`DELETE FROM "mappingKnownPlaces" WHERE "systemId" = $1 AND "placeKey" = $2 AND "source" = 'auto_learn'`,
		systemId, placeKey)
	return err
}

func (ms *MappingStore) PurgeBadAutoLearnPlaces(systemId uint64, curated *mapping.CuratedAlert, scope *mapping.ScopeData, geo *mapping.GeoOptions) int {
	if ms == nil || ms.db == nil {
		return 0
	}
	purged := 0
	for _, place := range mapping.CollectAutoLearnPlacesToPurge(scope, curated, geo) {
		if err := ms.DeleteAutoLearnKnownPlaceByKey(systemId, place.PlaceKey); err != nil {
			continue
		}
		purged++
	}
	return purged
}

func (ms *MappingStore) SaveCallIncident(callId uint64, primary *mapping.CuratedAlert, extras []*mapping.CuratedAlert, status, source, query string) error {
	if primary == nil {
		return nil
	}
	additional := "[]"
	if len(extras) > 0 {
		type extraRow struct {
			Address      string  `json:"address"`
			Nature       string  `json:"nature"`
			CommonName   string  `json:"commonName"`
			CrossStreet1 string  `json:"crossStreet1"`
			CrossStreet2 string  `json:"crossStreet2"`
			Lat          float64 `json:"lat"`
			Lon          float64 `json:"lon"`
		}
		rows := make([]extraRow, 0, len(extras))
		for _, e := range extras {
			if e == nil {
				continue
			}
			var lat, lon float64
			fmt.Sscanf(e.Lat, "%f", &lat)
			fmt.Sscanf(e.Lon, "%f", &lon)
			rows = append(rows, extraRow{
				Address: e.Address, Nature: e.NatureDesc, CommonName: e.CommonName,
				CrossStreet1: e.CrossStreet1, CrossStreet2: e.CrossStreet2, Lat: lat, Lon: lon,
			})
		}
		if b, err := json.Marshal(rows); err == nil {
			additional = string(b)
		}
	}
	var lat, lon float64
	fmt.Sscanf(primary.Lat, "%f", &lat)
	fmt.Sscanf(primary.Lon, "%f", &lon)
	now := time.Now().UnixMilli()
	_, err := ms.db.Sql.Exec(`UPDATE "calls" SET
		"incidentAddress" = $1, "incidentCrossStreet1" = $2, "incidentCrossStreet2" = $3,
		"incidentNature" = $4, "incidentCommonName" = $5, "incidentAptUnit" = $6,
		"incidentLat" = $7, "incidentLon" = $8,
		"incidentGeocodeStatus" = $9, "incidentGeocodeSource" = $10, "incidentGeocodeQuery" = $11,
		"incidentMappingProcessedAt" = $12, "incidentAdditional" = $13
		WHERE "callId" = $14`,
		primary.Address, primary.CrossStreet1, primary.CrossStreet2,
		primary.NatureDesc, primary.CommonName, primary.AptUnit,
		lat, lon, status, source, query, now, additional, callId)
	return err
}

// CorrectCallIncident applies a system-admin correction to a call's incident
// card. Only non-nil fields are written; status/source become manual/admin so
// the pin survives the map query filter and is distinguishable from geocoding.
func (ms *MappingStore) CorrectCallIncident(callId uint64, address, nature *string, lat, lon *float64) error {
	sets := []string{`"incidentGeocodeStatus" = 'manual'`, `"incidentGeocodeSource" = 'admin'`}
	args := []any{}
	idx := 1
	add := func(clause string, v any) {
		sets = append(sets, fmt.Sprintf(clause, idx))
		args = append(args, v)
		idx++
	}
	add(`"incidentMappingProcessedAt" = $%d`, time.Now().UnixMilli())
	if address != nil {
		add(`"incidentAddress" = $%d`, *address)
	}
	if nature != nil {
		add(`"incidentNature" = $%d`, *nature)
	}
	if lat != nil {
		add(`"incidentLat" = $%d`, *lat)
	}
	if lon != nil {
		add(`"incidentLon" = $%d`, *lon)
	}
	args = append(args, callId)
	query := fmt.Sprintf(`UPDATE "calls" SET %s WHERE "callId" = $%d`, strings.Join(sets, ", "), idx)
	_, err := ms.db.Sql.Exec(query, args...)
	return err
}

func (ms *MappingStore) ClearCallIncident(callId uint64) error {
	now := time.Now().UnixMilli()
	_, err := ms.db.Sql.Exec(`UPDATE "calls" SET
		"incidentAddress" = '', "incidentCrossStreet1" = '', "incidentCrossStreet2" = '',
		"incidentNature" = '', "incidentCommonName" = '', "incidentAptUnit" = '',
		"incidentLat" = 0, "incidentLon" = 0,
		"incidentGeocodeStatus" = 'skipped', "incidentGeocodeSource" = '', "incidentGeocodeQuery" = '',
		"incidentMappingProcessedAt" = $1, "incidentAdditional" = '[]'
		WHERE "callId" = $2`, now, callId)
	return err
}

// LookupRecentPeerIncidentPin returns the nearest geocoded incident on the same
// system within a short time window — used when a peer-agency squad request
// names no street address (Warren tones to Niles Dunkin crash, call 167191).
func (ms *MappingStore) LookupRecentPeerIncidentPin(systemID uint64, callID int64, callTimestampMs int64, cityHint string) (addr string, lat, lon float64, sourceTranscript string, ok bool) {
	if ms == nil || ms.db == nil || ms.db.Sql == nil {
		return "", 0, 0, "", false
	}
	if callTimestampMs <= 0 && callID > 0 {
		_ = ms.db.Sql.QueryRow(`SELECT "timestamp" FROM calls WHERE "callId"=$1`, callID).Scan(&callTimestampMs)
	}
	if callTimestampMs <= 0 {
		return "", 0, 0, "", false
	}
	const windowMs = int64(20 * 60 * 1000)
	minTs := callTimestampMs - windowMs
	maxTs := callTimestampMs + windowMs
	cityPat := "%"
	if h := strings.TrimSpace(strings.SplitN(cityHint, ",", 2)[0]); h != "" {
		city := strings.TrimSuffix(strings.ToUpper(h), " CITY")
		cityPat = "%" + city + "%"
	}
	err := ms.db.Sql.QueryRow(`SELECT "incidentAddress", "incidentLat", "incidentLon", COALESCE(transcript,'')
		FROM calls
		WHERE "systemId"=$1
		AND "timestamp" BETWEEN $2 AND $3
		AND ($4 = 0 OR "callId" != $4)
		AND "incidentLat" != 0
		AND NULLIF(TRIM("incidentAddress"), '') IS NOT NULL
		AND (
			transcript ILIKE '%DUNKIN%'
			OR transcript ILIKE '%NORTH MAIN%'
			OR transcript ILIKE '%MUTUAL AID%'
			OR "incidentAddress" ILIKE $5
			OR "incidentNature" ILIKE '%MUTUAL%'
			OR "incidentNature" ILIKE '%MVA%'
			OR "incidentNature" ILIKE '%CRASH%'
		)
		ORDER BY
			CASE WHEN "incidentAddress" ILIKE '%NORTH MAIN%' THEN 0
			     WHEN transcript ILIKE '%NORTH MAIN%' OR transcript ILIKE '%DUNKIN%' THEN 1
			     ELSE 2 END,
			ABS("timestamp" - $6)
		LIMIT 1`,
		systemID, minTs, maxTs, callID, cityPat, callTimestampMs,
	).Scan(&addr, &lat, &lon, &sourceTranscript)
	return addr, lat, lon, sourceTranscript, err == nil && strings.TrimSpace(addr) != ""
}

func splitHouseAndStreetMain(addr string) (house, street string) {
	fields := strings.Fields(strings.TrimSpace(addr))
	if len(fields) == 0 {
		return "", ""
	}
	if len(fields[0]) > 0 && fields[0][0] >= '0' && fields[0][0] <= '9' {
		return fields[0], strings.Join(fields[1:], " ")
	}
	return "", strings.Join(fields, " ")
}

func (ms *MappingStore) ListKnownStreets(systemId, talkgroupId uint64) []map[string]any {
	var q string
	var rows *sql.Rows
	var err error
	if talkgroupId > 0 {
		q = `SELECT "mappingKnownStreetId", "streetName", "talkgroupId" FROM "mappingKnownStreets" WHERE "systemId" = $1 AND ("talkgroupId" IS NULL OR "talkgroupId" = $2) ORDER BY "streetName"`
		rows, err = ms.db.Sql.Query(q, systemId, talkgroupId)
	} else {
		q = `SELECT "mappingKnownStreetId", "streetName", "talkgroupId" FROM "mappingKnownStreets" WHERE "systemId" = $1 AND "talkgroupId" IS NULL ORDER BY "streetName"`
		rows, err = ms.db.Sql.Query(q, systemId)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id uint64
		var name string
		var tg sql.NullInt64
		if rows.Scan(&id, &name, &tg) == nil {
			item := map[string]any{"id": id, "streetName": name}
			if tg.Valid {
				item["talkgroupId"] = tg.Int64
			}
			out = append(out, item)
		}
	}
	return out
}

func (ms *MappingStore) ListCorrections(systemId, talkgroupId uint64) []map[string]any {
	var rows *sql.Rows
	var err error
	if talkgroupId > 0 {
		rows, err = ms.db.Sql.Query(`SELECT "mappingStreetCorrectionId", "badName", "correctName", "talkgroupId" FROM "mappingStreetCorrections" WHERE "systemId" = $1 AND ("talkgroupId" IS NULL OR "talkgroupId" = $2)`, systemId, talkgroupId)
	} else {
		rows, err = ms.db.Sql.Query(`SELECT "mappingStreetCorrectionId", "badName", "correctName", "talkgroupId" FROM "mappingStreetCorrections" WHERE "systemId" = $1 AND "talkgroupId" IS NULL`, systemId)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id uint64
		var bad, correct string
		var tg sql.NullInt64
		if rows.Scan(&id, &bad, &correct, &tg) == nil {
			item := map[string]any{"id": id, "badName": bad, "correctName": correct}
			if tg.Valid {
				item["talkgroupId"] = tg.Int64
			}
			out = append(out, item)
		}
	}
	return out
}

func (ms *MappingStore) ListKnownPlacesRows(systemId, talkgroupId uint64) []map[string]any {
	var rows *sql.Rows
	var err error
	if talkgroupId > 0 {
		rows, err = ms.db.Sql.Query(`SELECT "mappingKnownPlaceId", "displayName", "lat", "lon", "addressHint", "source", "talkgroupId" FROM "mappingKnownPlaces" WHERE "systemId" = $1 AND ("talkgroupId" IS NULL OR "talkgroupId" = $2)`, systemId, talkgroupId)
	} else {
		rows, err = ms.db.Sql.Query(`SELECT "mappingKnownPlaceId", "displayName", "lat", "lon", "addressHint", "source", "talkgroupId" FROM "mappingKnownPlaces" WHERE "systemId" = $1 AND "talkgroupId" IS NULL`, systemId)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id uint64
		var name, hint, source string
		var lat, lon float64
		var tg sql.NullInt64
		if rows.Scan(&id, &name, &lat, &lon, &hint, &source, &tg) == nil {
			item := map[string]any{"id": id, "displayName": name, "lat": lat, "lon": lon, "addressHint": hint, "source": source}
			if tg.Valid {
				item["talkgroupId"] = tg.Int64
			}
			out = append(out, item)
		}
	}
	return out
}

func (ms *MappingStore) MappingStats(systemId uint64) map[string]any {
	var streets, corrections, places, cache int
	ms.db.Sql.QueryRow(`SELECT COUNT(*) FROM "mappingKnownStreets" WHERE "systemId" = $1`, systemId).Scan(&streets)
	ms.db.Sql.QueryRow(`SELECT COUNT(*) FROM "mappingStreetCorrections" WHERE "systemId" = $1`, systemId).Scan(&corrections)
	ms.db.Sql.QueryRow(`SELECT COUNT(*) FROM "mappingKnownPlaces" WHERE "systemId" = $1`, systemId).Scan(&places)
	ms.db.Sql.QueryRow(`SELECT COUNT(*) FROM "mappingGeocodeCache" WHERE "systemId" = $1`, systemId).Scan(&cache)
	return map[string]any{
		"streets": streets, "corrections": corrections, "places": places,
		"cacheEntries": cache,
	}
}

func (ms *MappingStore) AddKnownStreet(systemId, talkgroupId uint64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("street name required")
	}
	var tg interface{}
	if talkgroupId > 0 {
		tg = talkgroupId
	}
	_, err := ms.db.Sql.Exec(`INSERT INTO "mappingKnownStreets" ("systemId", "talkgroupId", "streetName", "createdAt") VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
		systemId, tg, strings.ToUpper(name), time.Now().UnixMilli())
	return err
}

func (ms *MappingStore) AddCorrection(systemId, talkgroupId uint64, bad, correct string) error {
	if strings.TrimSpace(bad) == "" || strings.TrimSpace(correct) == "" {
		return fmt.Errorf("bad and correct names required")
	}
	var tg interface{}
	if talkgroupId > 0 {
		tg = talkgroupId
	}
	_, err := ms.db.Sql.Exec(`INSERT INTO "mappingStreetCorrections" ("systemId", "talkgroupId", "badName", "correctName", "createdAt") VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		systemId, tg, strings.ToUpper(bad), strings.ToUpper(correct), time.Now().UnixMilli())
	return err
}

func (ms *MappingStore) DeleteMappingRow(kind string, id uint64) error {
	table := ""
	switch kind {
	case "street":
		table = "mappingKnownStreets"
	case "correction":
		table = "mappingStreetCorrections"
	case "place":
		table = "mappingKnownPlaces"
	default:
		return fmt.Errorf("unknown kind")
	}
	_, err := ms.db.Sql.Exec(fmt.Sprintf(`DELETE FROM "%s" WHERE "%sId" = $1`, table, strings.TrimPrefix(table, "mapping")), id)
	if err != nil {
		// fallback with full column names
		switch kind {
		case "street":
			_, err = ms.db.Sql.Exec(`DELETE FROM "mappingKnownStreets" WHERE "mappingKnownStreetId" = $1`, id)
		case "correction":
			_, err = ms.db.Sql.Exec(`DELETE FROM "mappingStreetCorrections" WHERE "mappingStreetCorrectionId" = $1`, id)
		case "place":
			_, err = ms.db.Sql.Exec(`DELETE FROM "mappingKnownPlaces" WHERE "mappingKnownPlaceId" = $1`, id)
		}
	}
	return err
}
