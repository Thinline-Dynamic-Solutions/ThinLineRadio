// Copyright (C) 2025 Thinline Dynamic Solutions

package main

import (
	"database/sql"
)

func applyIncidentMappingFromMap(cfg *IncidentMappingConfig, m map[string]any) {
	if cfg == nil || m == nil {
		return
	}
	if v, ok := m["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v, ok := m["inherit"].(bool); ok {
		cfg.Inherit = v
	}
	if v, ok := m["geoCity"].(string); ok {
		cfg.GeoCity = v
	}
	if v, ok := m["geoLat"].(float64); ok {
		cfg.GeoLat = v
	}
	if v, ok := m["geoLon"].(float64); ok {
		cfg.GeoLon = v
	}
	if v, ok := m["geoRadiusMiles"].(float64); ok {
		cfg.GeoRadiusMiles = v
	}
	if v, ok := m["locationContext"].(string); ok {
		cfg.LocationContext = v
	}
	if v, ok := m["coverageAddress"].(string); ok {
		cfg.CoverageAddress = v
	}
	if v, ok := m["coverageNature"].(string); ok {
		cfg.CoverageNature = v
	}
	if v, ok := anyToBool(m["extractAddressWithGemini"]); ok {
		cfg.ExtractAddressWithGemini = v
	}
	if v, ok := m["pinCounties"]; ok {
		cfg.PinCounties = normalizePinCounties(v)
	}
}

func incidentMappingToMap(cfg IncidentMappingConfig) map[string]any {
	return map[string]any{
		"enabled":                    cfg.Enabled,
		"inherit":                    cfg.Inherit,
		"geoCity":                    cfg.GeoCity,
		"geoLat":                     cfg.GeoLat,
		"geoLon":                     cfg.GeoLon,
		"geoRadiusMiles":             cfg.GeoRadiusMiles,
		"locationContext":            cfg.LocationContext,
		"coverageAddress":            cfg.CoverageAddress,
		"coverageNature":             cfg.CoverageNature,
		"extractAddressWithGemini":   cfg.ExtractAddressWithGemini,
		"pinCounties":                normalizePinCounties(cfg.PinCounties),
	}
}

func (systems *Systems) loadIncidentMappingConfigs(db *Database) error {
	rows, err := db.Sql.Query(`SELECT "systemId", "incidentMappingConfig" FROM "systems"`)
	if err != nil {
		return err
	}
	defer rows.Close()
	byId := map[uint64]string{}
	for rows.Next() {
		var id uint64
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		if raw.Valid {
			byId[id] = raw.String
		}
	}
	for _, sys := range systems.List {
		if raw, ok := byId[sys.Id]; ok {
			sys.IncidentMapping = parseIncidentMappingConfig(raw)
		}
	}
	return nil
}

func (systems *Systems) saveIncidentMappingConfigs(db *Database) error {
	for _, sys := range systems.List {
		if sys == nil {
			continue
		}
		if _, err := db.Sql.Exec(`UPDATE "systems" SET "incidentMappingConfig" = $1 WHERE "systemId" = $2`,
			sys.IncidentMapping.JSON(), sys.Id); err != nil {
			return err
		}
		for _, tg := range sys.Talkgroups.List {
			if tg == nil {
				continue
			}
			if _, err := db.Sql.Exec(`UPDATE "talkgroups" SET "incidentMappingConfig" = $1 WHERE "talkgroupId" = $2`,
				tg.IncidentMapping.JSON(), tg.Id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (systems *Systems) loadTalkgroupIncidentMappingConfigs(db *Database) error {
	rows, err := db.Sql.Query(`SELECT "talkgroupId", "incidentMappingConfig" FROM "talkgroups"`)
	if err != nil {
		return err
	}
	defer rows.Close()
	byId := map[uint64]string{}
	for rows.Next() {
		var id uint64
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		if raw.Valid {
			byId[id] = raw.String
		}
	}
	for _, sys := range systems.List {
		for _, tg := range sys.Talkgroups.List {
			if raw, ok := byId[tg.Id]; ok {
				tg.IncidentMapping = parseIncidentMappingConfig(raw)
				if tg.IncidentMapping.Inherit == false && !tg.IncidentMapping.Enabled {
					// keep explicit inherit false
				} else if raw == "{}" {
					tg.IncidentMapping.Inherit = true
				}
			}
		}
	}
	return nil
}
