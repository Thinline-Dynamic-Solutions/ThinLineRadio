// Copyright (C) 2025-2026 Thinline Dynamic Solutions
//
// stats_api.go — High-performance analytics & call-classification stats engine
// for ThinLine Radio (GET /api/stats).
//
// When Geocoding & Incident Mapping is enabled on the server, incident
// analytics are powered by the classified & geocoded incident records
// (using calls_incident_pinned_ts_idx in ~12ms). Servers without incident
// mapping enabled automatically fall back to transcript keyword classification.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	statsCacheFreshTTL = 20 * time.Second
	statsCacheStaleTTL = 180 * time.Second
)

type statsCacheEntry struct {
	at         time.Time
	payload    []byte
	refreshing bool
}

var statsCache = struct {
	sync.Mutex
	items       map[string]*statsCacheEntry
	warmerOnce  sync.Once
}{
	items: make(map[string]*statsCacheEntry),
}

type statsSystemItem struct {
	Id    int64  `json:"id"`
	Label string `json:"label"`
}

type statsMinuteBucket struct {
	Minute int64 `json:"minute"`
	Count  int   `json:"count"`
}

type statsHourBucket struct {
	Hour      int `json:"hour"`
	Count     int `json:"count"`
	Incidents int `json:"incidents"`
}

type statsLabelCount struct {
	Label       string `json:"label"`
	SystemLabel string `json:"systemLabel,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Color       string `json:"color,omitempty"`
	Count       int    `json:"count"`
}

type statsSystemBreakdown struct {
	Id        int64  `json:"id"`
	Label     string `json:"label"`
	Count     int    `json:"count"`
	Incidents int    `json:"incidents"`
}

type statsIncidentSub struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type statsIncidentCat struct {
	Category      string             `json:"category"`
	Color         string             `json:"color"`
	Count         int                `json:"count"`
	SharePct      float64            `json:"sharePct"`
	Subcategories []statsIncidentSub `json:"subcategories"`
}

type statsNatureRank struct {
	Label    string  `json:"label"`
	Category string  `json:"category"`
	Color    string  `json:"color"`
	Count    int     `json:"count"`
	SharePct float64 `json:"sharePct"`
}

type statsRecentIncident struct {
	CallId         uint64 `json:"callId"`
	Timestamp      int64  `json:"timestamp"`
	Nature         string `json:"nature"`
	Category       string `json:"category"`
	Color          string `json:"color"`
	Address        string `json:"address"`
	SystemLabel    string `json:"systemLabel"`
	TalkgroupLabel string `json:"talkgroupLabel"`
}

var mappedCategoryOrder = []string{
	"Medical / EMS",
	"Fire & Alarms",
	"Traffic & MVA",
	"Law Enforcement",
	"Hazmat & Utilities",
	"Service & Welfare",
}

var mappedCategoryColors = map[string]string{
	"Medical / EMS":      "#10b981",
	"Fire & Alarms":      "#ff5722",
	"Traffic & MVA":      "#38bdf8",
	"Law Enforcement":    "#ef4444",
	"Hazmat & Utilities": "#f59e0b",
	"Service & Welfare":  "#a855f7",
	"Fire":               "#ff5722",
	"Hazmat":             "#f59e0b",
	"Crime":              "#ef4444",
	"Traffic":            "#38bdf8",
	"Disturbance":        "#a855f7",
}

var exactNatureToCategory = map[string]string{
	// Medical / EMS
	"ABDOMINAL PAIN":                   "Medical / EMS",
	"ALARM DROP MEDICAL":               "Medical / EMS",
	"ALLERGIC REACTION":                "Medical / EMS",
	"ALTERED LEVEL OF CONSCIOUSNESS":   "Medical / EMS",
	"BACK PAINS":                       "Medical / EMS",
	"BITE":                             "Medical / EMS",
	"BLEEDING":                         "Medical / EMS",
	"BLOOD PRESSURE":                   "Medical / EMS",
	"BURNS":                            "Medical / EMS",
	"CARDIAC ARREST":                   "Medical / EMS",
	"CHEST PAINS":                      "Medical / EMS",
	"CHOKING":                          "Medical / EMS",
	"COVID 19":                         "Medical / EMS",
	"DIABETIC":                         "Medical / EMS",
	"DIZZINESS":                        "Medical / EMS",
	"DOA":                              "Medical / EMS",
	"DROWNING":                         "Medical / EMS",
	"ELECTROCUTION":                    "Medical / EMS",
	"EMERGENCY MEDICAL ASSISTANCE":     "Medical / EMS",
	"EMS UNKNOWN PROBLEM/UNCLASSIFIED": "Medical / EMS",
	"EVALUATION":                       "Medical / EMS",
	"FALL":                             "Medical / EMS",
	"FRACTURE":                         "Medical / EMS",
	"GENERAL ILLNESS":                  "Medical / EMS",
	"HANGING":                          "Medical / EMS",
	"HEART PROBLEM":                    "Medical / EMS",
	"LIFT ASSIST":                      "Medical / EMS",
	"MASS CASUALTIES INCIDENT":         "Medical / EMS",
	"MENTAL":                           "Medical / EMS",
	"MENTALLY IMPAIRED PERSON":         "Medical / EMS",
	"NAUSEA/VOMITING":                  "Medical / EMS",
	"OB/GYN":                           "Medical / EMS",
	"OVERDOSE":                         "Medical / EMS",
	"POISONING":                        "Medical / EMS",
	"RESPIRATORY ARREST":               "Medical / EMS",
	"SEIZURE":                          "Medical / EMS",
	"SHORTNESS OF BREATH":              "Medical / EMS",
	"STROKE":                           "Medical / EMS",
	"SUICIDE":                          "Medical / EMS",
	"TRAUMATIC INJURY":                 "Medical / EMS",
	"UNCONSCIOUS/FAINTED":              "Medical / EMS",

	// Fire & Alarms
	"AIRCRAFT CRASH":         "Fire & Alarms",
	"ASSIST FIRE DEPARTMENT": "Fire & Alarms",
	"BRUSH/GRASS FIRE":       "Fire & Alarms",
	"BURNING COMPLAINT":      "Fire & Alarms",
	"CHIMNEY FIRE":           "Fire & Alarms",
	"DUMPSTER FIRE":          "Fire & Alarms",
	"ELECTRICAL FIRE":        "Fire & Alarms",
	"ENTRAPMENT/RESCUE":      "Fire & Alarms",
	"FIRE ALARM DROP":        "Fire & Alarms",
	"FIREWORKS COMPLAINT":    "Fire & Alarms",
	"LAKE EMERGENCY":         "Fire & Alarms",
	"MUTUAL AID":             "Fire & Alarms",
	"PUMPS":                  "Fire & Alarms",
	"SMOKE INVESTIGATION":    "Fire & Alarms",
	"STRUCTURE FIRE":         "Fire & Alarms",
	"TRAIN DERAILMENT":       "Fire & Alarms",
	"TRANSFORMER FIRE":       "Fire & Alarms",
	"VEHICLE FIRE":           "Fire & Alarms",

	// Hazmat & Utilities
	"BIOLOGICAL COMPLAINT":           "Hazmat & Utilities",
	"CO DETECTOR":                    "Hazmat & Utilities",
	"DUMPING SOLIDS OR LIQUIDS":      "Hazmat & Utilities",
	"ENVIRONMENTAL CALL":             "Hazmat & Utilities",
	"FUEL SPILL":                     "Hazmat & Utilities",
	"GAS LEAK":                       "Hazmat & Utilities",
	"HAZMAT":                         "Hazmat & Utilities",
	"LIGHT DEPARTMENT CALL":          "Hazmat & Utilities",
	"ODOR INVESTIGATION":             "Hazmat & Utilities",
	"SEWER DEPARTMENT CALL":          "Hazmat & Utilities",
	"STREET DEPARTMENT CALL":         "Hazmat & Utilities",
	"UTILITY PROBLEMS OR COMPLAINTS": "Hazmat & Utilities",
	"WIRES/TREES DOWN":               "Hazmat & Utilities",

	// Traffic & MVA
	"ABANDONED VEHICLE":            "Traffic & MVA",
	"ATV/DIRTBIKE":                 "Traffic & MVA",
	"CRASH PROPERTY DAMAGE":        "Traffic & MVA",
	"CRASH WITH REPORTED INJURIES": "Traffic & MVA",
	"DISABLED VEHICLE":             "Traffic & MVA",
	"HIT SKIP":                     "Traffic & MVA",
	"PURSUIT":                      "Traffic & MVA",
	"RECOVERED VEHICLE":            "Traffic & MVA",
	"REPO":                         "Traffic & MVA",
	"TRAFFIC CONTROL":              "Traffic & MVA",
	"TRAFFIC REGULATION COMPLAINT": "Traffic & MVA",
	"TRAFFIC STOP":                 "Traffic & MVA",

	// Law Enforcement
	"ABDUCTION OR KIDNAPPING":       "Law Enforcement",
	"ACTIVE SHOOTER":                "Law Enforcement",
	"ACTIVE SHOOTER INCIDENT":       "Law Enforcement",
	"ASSAULT":                       "Law Enforcement",
	"ASSIST OFFICER":                "Law Enforcement",
	"ASSIST POLICE DEPARTMENT":      "Law Enforcement",
	"ATTEMPT TO LOCATE":             "Law Enforcement",
	"BE ON THE LOOKOUT":             "Law Enforcement",
	"BOMB THREAT":                   "Law Enforcement",
	"BREAKING AND ENTERING":         "Law Enforcement",
	"BURGLARY":                      "Law Enforcement",
	"CAR JACKING":                   "Law Enforcement",
	"CHILD ENDANGERING":             "Law Enforcement",
	"CRIMINAL DAMAGING":             "Law Enforcement",
	"CRIMINAL MISCHIEF":             "Law Enforcement",
	"CRIMINAL TRESPASS":             "Law Enforcement",
	"DISTURBANCE":                   "Law Enforcement",
	"DOMESTIC":                      "Law Enforcement",
	"DRUG ACTIVITY":                 "Law Enforcement",
	"FIGHT":                         "Law Enforcement",
	"FORGERY":                       "Law Enforcement",
	"HARASSMENT":                    "Law Enforcement",
	"HOMICIDE OR POSSIBLE HOMICIDE": "Law Enforcement",
	"IDENTITY THEFT":                "Law Enforcement",
	"INTOXICATED PERSON":            "Law Enforcement",
	"JUVENILE DELINQUENT OR UNRULY": "Law Enforcement",
	"JUVENILE RUNAWAY":              "Law Enforcement",
	"LOST OR STOLEN PLATE":          "Law Enforcement",
	"MISSING JUVENILE":              "Law Enforcement",
	"MISSING PERSON":                "Law Enforcement",
	"PASSING BAD CHECK(S)":          "Law Enforcement",
	"PERSON WITH GUN":               "Law Enforcement",
	"PERSON WITH KNIFE":             "Law Enforcement",
	"PRISONER ESCAPE":               "Law Enforcement",
	"PROWLER":                       "Law Enforcement",
	"ROBBERY":                       "Law Enforcement",
	"SEX OFFENSE":                   "Law Enforcement",
	"SHOPLIFTING":                   "Law Enforcement",
	"SHOTS BEING HEARD":             "Law Enforcement",
	"SHOTS FIRED":                   "Law Enforcement",
	"SOLICITATION":                  "Law Enforcement",
	"STALKING":                      "Law Enforcement",
	"SUBJECT PURSUIT":               "Law Enforcement",
	"SUBJECT STOP":                  "Law Enforcement",
	"SUSPICIOUS ACTIVITY":           "Law Enforcement",
	"SUSPICIOUS PERSON":             "Law Enforcement",
	"SUSPICIOUS VEHICLE":            "Law Enforcement",
	"TELEPHONE HARASSMENT":          "Law Enforcement",
	"THEFT":                         "Law Enforcement",
	"THEFT FROM VEHICLE":            "Law Enforcement",
	"THREATS":                       "Law Enforcement",
	"UNAUTHORIZED USE OF PROPERTY":  "Law Enforcement",
	"UNAUTHORIZED USE OF VEHICLE":   "Law Enforcement",
	"UNDERAGE DRINKING":             "Law Enforcement",
	"UNSECURED PROPERTY":            "Law Enforcement",
	"UNWANTED PERSON OR PERSONS":    "Law Enforcement",
	"VANDALISM":                     "Law Enforcement",
	"VERBAL DISPUTE":                "Law Enforcement",
}

func categorizeCallNatureLabel(nature string) (string, string) {
	u := strings.ToUpper(strings.TrimSpace(nature))
	if cat, ok := exactNatureToCategory[u]; ok {
		return cat, mappedCategoryColors[cat]
	}
	switch {
	case strings.Contains(u, "FIRE") || strings.Contains(u, "SMOKE") || strings.Contains(u, "BURN") ||
		strings.Contains(u, "ENTRAPMENT") || strings.Contains(u, "EXTRICATION") || strings.Contains(u, "RESCUE"):
		return "Fire & Alarms", mappedCategoryColors["Fire & Alarms"]
	case strings.Contains(u, "PAIN") || strings.Contains(u, "MEDICAL") || strings.Contains(u, "EMS") ||
		strings.Contains(u, "BREATH") || strings.Contains(u, "BLEED") || strings.Contains(u, "CARDIAC") ||
		strings.Contains(u, "HEART") || strings.Contains(u, "STROKE") || strings.Contains(u, "SEIZURE") ||
		strings.Contains(u, "FALL") || strings.Contains(u, "INJURY") || strings.Contains(u, "ILLNESS") ||
		strings.Contains(u, "UNCONSCIOUS") || strings.Contains(u, "FAINT") || strings.Contains(u, "OVERDOSE") ||
		strings.Contains(u, "DIABETIC") || strings.Contains(u, "CHOKING") || strings.Contains(u, "ALLERG") ||
		strings.Contains(u, "LIFT ASSIST") || strings.Contains(u, "SUICID") || strings.Contains(u, "MENTAL") ||
		strings.Contains(u, "VOMIT") || strings.Contains(u, "FRACTURE"):
		return "Medical / EMS", mappedCategoryColors["Medical / EMS"]
	case strings.Contains(u, "CRASH") || strings.Contains(u, "MVA") || strings.Contains(u, "ACCIDENT") ||
		strings.Contains(u, "TRAFFIC") || strings.Contains(u, "VEHICLE") || strings.Contains(u, "HIT SKIP") ||
		strings.Contains(u, "PURSUIT"):
		return "Traffic & MVA", mappedCategoryColors["Traffic & MVA"]
	case strings.Contains(u, "GAS") || strings.Contains(u, "WIRE") || strings.Contains(u, "TREE") ||
		strings.Contains(u, "HAZMAT") || strings.Contains(u, "SPILL") || strings.Contains(u, "ODOR") ||
		strings.Contains(u, "CARBON MONOXIDE") || strings.Contains(u, "CO DETECTOR") || strings.Contains(u, "UTILITY"):
		return "Hazmat & Utilities", mappedCategoryColors["Hazmat & Utilities"]
	case strings.Contains(u, "ASSAULT") || strings.Contains(u, "THEFT") || strings.Contains(u, "BURGLAR") ||
		strings.Contains(u, "ROBBERY") || strings.Contains(u, "SHOT") || strings.Contains(u, "GUN") ||
		strings.Contains(u, "KNIFE") || strings.Contains(u, "DOMESTIC") || strings.Contains(u, "DISTURBANCE") ||
		strings.Contains(u, "SUSPICIOUS") || strings.Contains(u, "TRESPASS") || strings.Contains(u, "THREAT") ||
		strings.Contains(u, "HARASS") || strings.Contains(u, "WANTED") || strings.Contains(u, "WARRANT") ||
		strings.Contains(u, "FIGHT") || strings.Contains(u, "DRUG") || strings.Contains(u, "VANDAL"):
		return "Law Enforcement", mappedCategoryColors["Law Enforcement"]
	default:
		return "Service & Welfare", mappedCategoryColors["Service & Welfare"]
	}
}

func normalizeStatsRange(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "today":
		return "today"
	case "7d", "7days", "week":
		return "7d"
	case "30d", "30days", "month":
		return "30d"
	default:
		return "24h"
	}
}

// StatsHandler handles GET /api/stats — returns call activity and incident classification analytics.
func (api *Api) StatsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.exitWithError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	client := api.getClient(r)
	if client == nil || client.User == nil {
		api.exitWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	statsCache.warmerOnce.Do(func() {
		go api.startStatsBackgroundWarmer()
	})

	systemID := 0
	if sid := strings.TrimSpace(r.URL.Query().Get("systemId")); sid != "" {
		if id, err := strconv.Atoi(sid); err == nil && id > 0 {
			systemID = id
		}
	}
	rangeKey := normalizeStatsRange(r.URL.Query().Get("range"))

	api.Controller.Options.mutex.Lock()
	mappingEnabled := api.Controller.Options.MappingIntegration.IncidentMappingEnabled
	api.Controller.Options.mutex.Unlock()

	cacheKey := fmt.Sprintf("sys=%d|range=%s|map=%v", systemID, rangeKey, mappingEnabled)

	statsCache.Lock()
	entry := statsCache.items[cacheKey]
	if entry != nil && len(entry.payload) > 0 {
		age := time.Since(entry.at)
		if age <= statsCacheFreshTTL {
			payload := entry.payload
			statsCache.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		if age <= statsCacheStaleTTL {
			payload := entry.payload
			if !entry.refreshing {
				entry.refreshing = true
				go func(k string, sid int, rk string, me bool) {
					if data, err := api.computeStatsJSON(sid, rk, me); err == nil {
						statsCache.Lock()
						statsCache.items[k] = &statsCacheEntry{at: time.Now(), payload: data}
						statsCache.Unlock()
					} else {
						statsCache.Lock()
						if e := statsCache.items[k]; e != nil {
							e.refreshing = false
						}
						statsCache.Unlock()
					}
				}(cacheKey, systemID, rangeKey, mappingEnabled)
			}
			statsCache.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
	}
	statsCache.Unlock()

	payload, err := api.computeStatsJSON(systemID, rangeKey, mappingEnabled)
	if err != nil {
		api.exitWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	statsCache.Lock()
	statsCache.items[cacheKey] = &statsCacheEntry{at: time.Now(), payload: payload}
	statsCache.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func (api *Api) startStatsBackgroundWarmer() {
	warm := func() {
		api.Controller.Options.mutex.Lock()
		me := api.Controller.Options.MappingIntegration.IncidentMappingEnabled
		api.Controller.Options.mutex.Unlock()
		for _, rk := range []string{"24h", "today"} {
			key := fmt.Sprintf("sys=0|range=%s|map=%v", rk, me)
			if data, err := api.computeStatsJSON(0, rk, me); err == nil {
				statsCache.Lock()
				statsCache.items[key] = &statsCacheEntry{at: time.Now(), payload: data}
				statsCache.Unlock()
			}
		}
	}
	time.Sleep(2 * time.Second)
	warm()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		warm()
	}
}

func (api *Api) computeStatsJSON(systemID int, rangeKey string, mappingEnabled bool) ([]byte, error) {
	db := api.Controller.Database.Sql
	nowTime := time.Now()
	now := nowTime.UnixMilli()
	_, tzOffsetSec := nowTime.Zone()
	tzOffsetMs := int64(tzOffsetSec) * 1000

	midnightToday := time.Date(nowTime.Year(), nowTime.Month(), nowTime.Day(), 0, 0, 0, 0, nowTime.Location()).UnixMilli()
	oneHourAgo := now - 60*60*1000
	twentyFourHoursAgo := now - 24*60*60*1000

	var windowStart int64
	switch rangeKey {
	case "today":
		windowStart = midnightToday
	case "7d":
		windowStart = now - 7*24*60*60*1000
	case "30d":
		windowStart = now - 30*24*60*60*1000
	default:
		windowStart = twentyFourHoursAgo
	}

	// Cap raw-call table scan window at 7d even if 30d incident window is chosen,
	// so multi-million-row servers stay sub-second on raw call histograms.
	rawCallsWindowStart := windowStart
	if rawCallsWindowStart < now-7*24*60*60*1000 {
		rawCallsWindowStart = now - 7*24*60*60*1000
	}

	systemFilter := ""
	toneSystemFilter := ""
	if systemID > 0 {
		systemFilter = fmt.Sprintf(` AND c."systemId" = %d`, systemID)
		toneSystemFilter = fmt.Sprintf(` AND a."systemId" = %d`, systemID)
	}

	// 0. Available systems from in-memory controller
	var availableSystems []statsSystemItem
	api.Controller.Systems.mutex.RLock()
	for _, sys := range api.Controller.Systems.List {
		lbl := strings.TrimSpace(sys.Label)
		if lbl == "" {
			lbl = fmt.Sprintf("System %d", sys.Id)
		}
		availableSystems = append(availableSystems, statsSystemItem{
			Id:    int64(sys.Id),
			Label: lbl,
		})
	}
	api.Controller.Systems.mutex.RUnlock()
	sort.Slice(availableSystems, func(i, j int) bool {
		return strings.ToLower(availableSystems[i].Label) < strings.ToLower(availableSystems[j].Label)
	})
	if availableSystems == nil {
		availableSystems = []statsSystemItem{}
	}

	var (
		wg sync.WaitGroup

		// 1. 60-minute calls per minute
		callsPerMinute     []statsMinuteBucket
		callsLastMinute    int
		callsLastHour      int
		peakCallsPerMinute int
		avgCallsPerMinute  float64

		// 2. Single-pass raw calls aggregation (totals, by hour, top talkgroups, tags, systems)
		totalCallsRange     int
		totalCallsToday     int
		activeChannelsCount int
		callsByHour         = make([]statsHourBucket, 24)
		peakHour            int
		topTalkgroups       []statsLabelCount
		tagBreakdown        []statsLabelCount
		systemCallCounts    = make(map[uint64]int)

		// 3. Tone dispatches
		topDepartmentsByTone []statsLabelCount
		activeToneDeptsCount int
		totalToneDispatches  int

		// 4. Incident classification & mapping
		classificationMode      = "legacy"
		totalIncidents          int
		mappedIncidentsCount    int
		uniqueIncidentLocations int
		incidentSummary         []statsIncidentCat
		topNatures              []statsNatureRank
		topIncidentChannels     []statsLabelCount
		recentIncidents         []statsRecentIncident
		incidentByHour          = make(map[int]int)
		incidentBySystem        = make(map[uint64]int)
	)

	// Goroutine 1: Last 60 minutes (callsPerMinute, callsLastHour, callsLastMinute, peak/avg)
	wg.Add(1)
	go func() {
		defer wg.Done()
		cpmMap := make(map[int64]int, 64)
		rows, err := db.Query(`
			SELECT (c."timestamp" / 60000) * 60000 AS minute, COUNT(*) AS count
			FROM "calls" c
			WHERE c."timestamp" >= $1`+systemFilter+`
			GROUP BY minute`, oneHourAgo)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var m int64
				var c int
				if rows.Scan(&m, &c) == nil {
					cpmMap[m] = c
				}
			}
		}
		curMin := (now / 60000) * 60000
		startMin := curMin - 59*60000
		callsPerMinute = make([]statsMinuteBucket, 0, 60)
		sum := 0
		peak := 0
		for m := startMin; m <= curMin; m += 60000 {
			cnt := cpmMap[m]
			callsPerMinute = append(callsPerMinute, statsMinuteBucket{Minute: m, Count: cnt})
			sum += cnt
			if cnt > peak {
				peak = cnt
			}
		}
		callsLastHour = sum
		// Use the fuller of current partial minute or previous full minute for live rate
		callsLastMinute = cpmMap[curMin]
		if prev := cpmMap[curMin-60000]; prev > callsLastMinute {
			callsLastMinute = prev
		}
		peakCallsPerMinute = peak
		avgCallsPerMinute = math.Round((float64(sum)/60.0)*10) / 10
	}()

	// Goroutine 2: Single-pass raw calls aggregation (by systemId, talkgroupId, hour, and today flag)
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanStart := rawCallsWindowStart
		if midnightToday < scanStart {
			scanStart = midnightToday
		}
		rows, err := db.Query(`
			SELECT c."systemId", c."talkgroupId",
			       MOD(((c."timestamp" + $2) / 3600000), 24) AS hr,
			       COUNT(*) FILTER (WHERE c."timestamp" >= $1) AS range_cnt,
			       COUNT(*) FILTER (WHERE c."timestamp" >= $3) AS today_cnt
			FROM "calls" c
			WHERE c."timestamp" >= $4`+systemFilter+`
			GROUP BY c."systemId", c."talkgroupId", hr`,
			rawCallsWindowStart, tzOffsetMs, midnightToday, scanStart)
		if err != nil {
			log.Printf("StatsHandler: calls aggregation error: %v", err)
			return
		}
		defer rows.Close()

		type tgKey struct {
			sysID uint64
			tgID  uint64
		}
		tgCounts := make(map[tgKey]int)
		hourCounts := make(map[int]int, 24)

		for rows.Next() {
			var sysID, tgID uint64
			var hr, rCnt, tCnt int
			if rows.Scan(&sysID, &tgID, &hr, &rCnt, &tCnt) != nil {
				continue
			}
			totalCallsToday += tCnt
			if rCnt > 0 {
				totalCallsRange += rCnt
				tgCounts[tgKey{sysID: sysID, tgID: tgID}] += rCnt
				if hr >= 0 && hr < 24 {
					hourCounts[hr] += rCnt
				}
				systemCallCounts[sysID] += rCnt
			}
		}

		for h := 0; h < 24; h++ {
			cnt := hourCounts[h]
			callsByHour[h] = statsHourBucket{Hour: h, Count: cnt}
			if cnt > callsByHour[peakHour].Count {
				peakHour = h
			}
		}

		activeChannelsCount = len(tgCounts)

		type tagAcc struct {
			label string
			color string
			count int
		}
		byTag := make(map[string]*tagAcc)
		allTGs := make([]statsLabelCount, 0, len(tgCounts))

		for k, cnt := range tgCounts {
			sysLabel := fmt.Sprintf("System %d", k.sysID)
			tgLabel := fmt.Sprintf("TG %d", k.tgID)
			tagLabel := "Untagged"
			tagColor := "#64748b"

			if sys, ok := api.Controller.Systems.GetSystemById(k.sysID); ok && sys != nil {
				if strings.TrimSpace(sys.Label) != "" {
					sysLabel = strings.TrimSpace(sys.Label)
				}
				if tg, ok2 := sys.Talkgroups.GetTalkgroupById(k.tgID); ok2 && tg != nil {
					if strings.TrimSpace(tg.Label) != "" {
						tgLabel = strings.TrimSpace(tg.Label)
					} else if strings.TrimSpace(tg.Name) != "" {
						tgLabel = strings.TrimSpace(tg.Name)
					} else if tg.TalkgroupRef > 0 {
						tgLabel = fmt.Sprintf("TG %d", tg.TalkgroupRef)
					}
					if tg.TagId > 0 {
						if tagObj, ok3 := api.Controller.Tags.GetTagById(tg.TagId); ok3 && tagObj != nil {
							if strings.TrimSpace(tagObj.Label) != "" {
								tagLabel = strings.TrimSpace(tagObj.Label)
							}
							if strings.TrimSpace(tagObj.Color) != "" {
								tagColor = strings.TrimSpace(tagObj.Color)
							}
						}
					}
				}
			}

			allTGs = append(allTGs, statsLabelCount{
				Label:       tgLabel,
				SystemLabel: sysLabel,
				Tag:         tagLabel,
				Color:       tagColor,
				Count:       cnt,
			})

			tKey := strings.ToUpper(tagLabel)
			if acc, exists := byTag[tKey]; exists {
				acc.count += cnt
			} else {
				byTag[tKey] = &tagAcc{label: tagLabel, color: tagColor, count: cnt}
			}
		}

		sort.Slice(allTGs, func(i, j int) bool { return allTGs[i].Count > allTGs[j].Count })
		if len(allTGs) > 12 {
			topTalkgroups = allTGs[:12]
		} else {
			topTalkgroups = allTGs
		}

		for _, acc := range byTag {
			tagBreakdown = append(tagBreakdown, statsLabelCount{
				Label: acc.label,
				Color: acc.color,
				Count: acc.count,
			})
		}
		sort.Slice(tagBreakdown, func(i, j int) bool { return tagBreakdown[i].Count > tagBreakdown[j].Count })
	}()

	// Goroutine 3: Tone-out dispatches (fast index scan on alerts_created_idx + in-memory ToneSet lookup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rows, err := db.Query(`
			SELECT a."systemId", a."talkgroupId", COALESCE(a."toneSetId", ''), COUNT(DISTINCT a."callId") AS cnt
			FROM "alerts" a
			WHERE a."createdAt" >= $1
			  AND a."toneDetected" = true`+toneSystemFilter+`
			GROUP BY a."systemId", a."talkgroupId", a."toneSetId"`, rawCallsWindowStart)
		if err != nil {
			log.Printf("StatsHandler: tone query error: %v", err)
			return
		}
		defer rows.Close()

		byDept := make(map[string]int)
		for rows.Next() {
			var sysID, tgID uint64
			var toneSetID string
			var cnt int
			if rows.Scan(&sysID, &tgID, &toneSetID, &cnt) != nil || cnt <= 0 {
				continue
			}
			label := ""
			if sys, ok := api.Controller.Systems.GetSystemById(sysID); ok && sys != nil {
				if tg, ok2 := sys.Talkgroups.GetTalkgroupById(tgID); ok2 && tg != nil {
					if toneSetID != "" {
						for _, ts := range tg.ToneSets {
							if ts.Id == toneSetID && strings.TrimSpace(ts.Label) != "" {
								label = strings.TrimSpace(ts.Label)
								break
							}
						}
					}
					if label == "" {
						if strings.TrimSpace(tg.Label) != "" {
							label = strings.TrimSpace(tg.Label)
						} else if tg.TalkgroupRef > 0 {
							label = fmt.Sprintf("TG %d", tg.TalkgroupRef)
						}
					}
				}
			}
			if label == "" {
				label = fmt.Sprintf("TG %d", tgID)
			}
			byDept[label] += cnt
			totalToneDispatches += cnt
		}

		activeToneDeptsCount = len(byDept)
		depts := make([]statsLabelCount, 0, len(byDept))
		for lbl, cnt := range byDept {
			depts = append(depts, statsLabelCount{Label: lbl, Count: cnt})
		}
		sort.Slice(depts, func(i, j int) bool { return depts[i].Count > depts[j].Count })
		if len(depts) > 12 {
			topDepartmentsByTone = depts[:12]
		} else {
			topDepartmentsByTone = depts
		}
	}()

	// Goroutine 4: Incident Classification & Geocoded Mapping Breakdown
	wg.Add(1)
	go func() {
		defer wg.Done()
		if mappingEnabled {
			rows, err := db.Query(`
				SELECT c."callId", c."systemId", c."talkgroupId", c."timestamp",
				       COALESCE(c."incidentNature", ''),
				       COALESCE(c."incidentAddress", ''),
				       COALESCE(c."incidentCommonName", '')
				FROM "calls" c
				WHERE c."timestamp" >= $1
				  AND c."incidentLat" <> 0 AND c."incidentLon" <> 0
				  AND COALESCE(c."incidentGeocodeStatus", '') NOT IN ('', 'failed', 'skipped')`+systemFilter+`
				ORDER BY c."timestamp" DESC`, windowStart)
			if err == nil {
				defer rows.Close()
				classificationMode = "mapped"

				natureCounts := make(map[string]int)
				type tgIncKey struct {
					sysID uint64
					tgID  uint64
				}
				tgIncCounts := make(map[tgIncKey]int)
				seenAddrWindow := make(map[string]int64)

				for rows.Next() {
					var callID, sysID, tgID uint64
					var ts int64
					var nature, addr, common string
					if rows.Scan(&callID, &sysID, &tgID, &ts, &nature, &addr, &common) != nil {
						continue
					}
					nat := strings.ToUpper(strings.TrimSpace(nature))
					if nat == "" || nat == "UNKNOWN PROBLEM" || nat == "UNKNOWN PROBLEM/UNCLASSIFIED" || nat == "EMS UNKNOWN PROBLEM/UNCLASSIFIED" {
						continue
					}
					mappedIncidentsCount++
					totalIncidents++
					natureCounts[nat]++
					tgIncCounts[tgIncKey{sysID: sysID, tgID: tgID}]++
					incidentBySystem[sysID]++

					hr := int(((ts + tzOffsetMs) / 3600000) % 24)
					if hr < 0 {
						hr += 24
					}
					incidentByHour[hr]++

					locKey := strings.ToUpper(strings.TrimSpace(addr))
					if locKey == "" {
						locKey = strings.ToUpper(strings.TrimSpace(common))
					}
					if locKey != "" {
						if prevTs, seen := seenAddrWindow[locKey]; !seen || (prevTs-ts) > 15*60*1000 {
							uniqueIncidentLocations++
							seenAddrWindow[locKey] = ts
						}
					} else {
						uniqueIncidentLocations++
					}

					if len(recentIncidents) < 12 {
						cat, col := categorizeCallNatureLabel(nat)
						dispAddr := strings.TrimSpace(addr)
						if dispAddr == "" {
							dispAddr = strings.TrimSpace(common)
						}
						sysLbl := fmt.Sprintf("System %d", sysID)
						tgLbl := fmt.Sprintf("TG %d", tgID)
						if sys, ok := api.Controller.Systems.GetSystemById(sysID); ok && sys != nil {
							if strings.TrimSpace(sys.Label) != "" {
								sysLbl = strings.TrimSpace(sys.Label)
							}
							if tg, ok2 := sys.Talkgroups.GetTalkgroupById(tgID); ok2 && tg != nil {
								if strings.TrimSpace(tg.Label) != "" {
									tgLbl = strings.TrimSpace(tg.Label)
								} else if strings.TrimSpace(tg.Name) != "" {
									tgLbl = strings.TrimSpace(tg.Name)
								}
							}
						}
						recentIncidents = append(recentIncidents, statsRecentIncident{
							CallId:         callID,
							Timestamp:      ts,
							Nature:         nat,
							Category:       cat,
							Color:          col,
							Address:        dispAddr,
							SystemLabel:    sysLbl,
							TalkgroupLabel: tgLbl,
						})
					}
				}

				if totalIncidents > 0 {
					catMap := make(map[string]*statsIncidentCat, len(mappedCategoryOrder))
					for _, cName := range mappedCategoryOrder {
						catMap[cName] = &statsIncidentCat{
							Category:      cName,
							Color:         mappedCategoryColors[cName],
							Subcategories: []statsIncidentSub{},
						}
					}

					allNatures := make([]statsNatureRank, 0, len(natureCounts))
					for nat, cnt := range natureCounts {
						catName, col := categorizeCallNatureLabel(nat)
						cat := catMap[catName]
						if cat == nil {
							cat = &statsIncidentCat{Category: catName, Color: col}
							catMap[catName] = cat
						}
						cat.Subcategories = append(cat.Subcategories, statsIncidentSub{Label: nat, Count: cnt})
						cat.Count += cnt

						pct := math.Round((float64(cnt)*100.0/float64(totalIncidents))*10) / 10
						allNatures = append(allNatures, statsNatureRank{
							Label:    nat,
							Category: catName,
							Color:    col,
							Count:    cnt,
							SharePct: pct,
						})
					}

					sort.Slice(allNatures, func(i, j int) bool {
						if allNatures[i].Count != allNatures[j].Count {
							return allNatures[i].Count > allNatures[j].Count
						}
						return allNatures[i].Label < allNatures[j].Label
					})
					if len(allNatures) > 20 {
						topNatures = allNatures[:20]
					} else {
						topNatures = allNatures
					}

					for _, cName := range mappedCategoryOrder {
						cat := catMap[cName]
						if cat != nil && cat.Count > 0 {
							sort.Slice(cat.Subcategories, func(i, j int) bool {
								if cat.Subcategories[i].Count != cat.Subcategories[j].Count {
									return cat.Subcategories[i].Count > cat.Subcategories[j].Count
								}
								return cat.Subcategories[i].Label < cat.Subcategories[j].Label
							})
							cat.SharePct = math.Round((float64(cat.Count)*100.0/float64(totalIncidents))*10) / 10
							incidentSummary = append(incidentSummary, *cat)
						}
					}
					sort.Slice(incidentSummary, func(i, j int) bool {
						return incidentSummary[i].Count > incidentSummary[j].Count
					})

					incChannels := make([]statsLabelCount, 0, len(tgIncCounts))
					for k, cnt := range tgIncCounts {
						sysLbl := fmt.Sprintf("System %d", k.sysID)
						tgLbl := fmt.Sprintf("TG %d", k.tgID)
						if sys, ok := api.Controller.Systems.GetSystemById(k.sysID); ok && sys != nil {
							if strings.TrimSpace(sys.Label) != "" {
								sysLbl = strings.TrimSpace(sys.Label)
							}
							if tg, ok2 := sys.Talkgroups.GetTalkgroupById(k.tgID); ok2 && tg != nil {
								if strings.TrimSpace(tg.Label) != "" {
									tgLbl = strings.TrimSpace(tg.Label)
								} else if strings.TrimSpace(tg.Name) != "" {
									tgLbl = strings.TrimSpace(tg.Name)
								}
							}
						}
						incChannels = append(incChannels, statsLabelCount{
							Label:       tgLbl,
							SystemLabel: sysLbl,
							Count:       cnt,
						})
					}
					sort.Slice(incChannels, func(i, j int) bool { return incChannels[i].Count > incChannels[j].Count })
					if len(incChannels) > 10 {
						topIncidentChannels = incChannels[:10]
					} else {
						topIncidentChannels = incChannels
					}
					return
				}
			}
		}

		// Legacy fallback for servers without geocoding / incident mapping enabled
		classificationMode = "legacy"
		subcategoryToCategory := map[string]string{
			"Structure Fire":       "Fire & Alarms",
			"Brush / Wildland":     "Fire & Alarms",
			"Vehicle Fire":         "Fire & Alarms",
			"Fire Alarm":           "Fire & Alarms",
			"Fire (General)":       "Fire & Alarms",
			"Carbon Monoxide":      "Hazmat & Utilities",
			"Gas Leak":             "Hazmat & Utilities",
			"Chemical / Spill":     "Hazmat & Utilities",
			"Cardiac / Breathing":  "Medical / EMS",
			"Trauma / Injury":      "Medical / EMS",
			"Overdose / Substance": "Medical / EMS",
			"General Medical":      "Medical / EMS",
			"Violent Crime":        "Law Enforcement",
			"Property Crime":       "Law Enforcement",
			"Suspicious":           "Law Enforcement",
			"MVA / Crash":          "Traffic & MVA",
			"Traffic Stop / Plate": "Traffic & MVA",
			"Road Hazard":          "Traffic & MVA",
			"Domestic":             "Law Enforcement",
			"Disturbance":          "Service & Welfare",
		}

		legacyStart := windowStart
		if legacyStart < twentyFourHoursAgo {
			legacyStart = twentyFourHoursAgo
		}

		incidentQuery := `
			WITH filtered_calls AS (
			    SELECT c.transcript
			    FROM calls c
			    WHERE c.timestamp >= $1
			      AND c.transcript != ''
			      AND c."transcriptionStatus" = 'completed'
			      ` + systemFilter + `
			),
			categorized AS (
			    SELECT
			        CASE
			            WHEN transcript ILIKE ANY(ARRAY['%structure fire%','%working fire%','%house fire%','%garage fire%','%apartment fire%','%residential fire%','%commercial fire%','%warehouse fire%','%fully involved%','%smoke showing%','%basement fire%','%attic fire%','%kitchen fire%','%bedroom fire%','%office fire%'])
			                THEN 'Structure Fire'
			            WHEN transcript ILIKE ANY(ARRAY['%brush fire%','%grass fire%','%wildland%','%barn fire%','%field fire%'])
			                THEN 'Brush / Wildland'
			            WHEN transcript ILIKE ANY(ARRAY['%vehicle fire%','%car fire%','%truck fire%'])
			                THEN 'Vehicle Fire'
			            WHEN transcript ILIKE ANY(ARRAY['%fire alarm%','%alarm activation%','%fire investigation%','%rekindle%','%dumpster fire%'])
			                THEN 'Fire Alarm'
			            WHEN transcript ILIKE ANY(ARRAY['%smoke%','%flames%','%burning%'])
			                THEN 'Fire (General)'
			            WHEN transcript ILIKE ANY(ARRAY['%carbon monoxide%','%co alarm%'])
			                THEN 'Carbon Monoxide'
			            WHEN transcript ILIKE ANY(ARRAY['%gas leak%','%natural gas%','%propane%','%ammonia%'])
			                THEN 'Gas Leak'
			            WHEN transcript ILIKE ANY(ARRAY['%hazmat%','%chemical%','%spill%','%unknown substance%','%fumes%','%strange odor%','%unknown odor%'])
			                THEN 'Chemical / Spill'
			            WHEN transcript ILIKE ANY(ARRAY['%cardiac%','%not breathing%','%chest pain%','%respiratory%','%cpr%','%stopped breathing%'])
			                THEN 'Cardiac / Breathing'
			            WHEN transcript ILIKE ANY(ARRAY['%trauma%','%injured%','%bleeding%','%laceration%','%fracture%'])
			                THEN 'Trauma / Injury'
			            WHEN transcript ILIKE ANY(ARRAY['%overdose%','%narcan%','%bad drugs%','%intoxicated%'])
			                THEN 'Overdose / Substance'
			            WHEN transcript ILIKE ANY(ARRAY['%squad%','%patient%','%medical%','%ems%','%ambulance%','%sick%','%medication%','%pass out%','%stomach pain%'])
			                THEN 'General Medical'
			            WHEN transcript ILIKE ANY(ARRAY['%shooting%','%shots fired%','%shot fired%','%gunshot%','%armed%','%assault%','%battery%','%robbery%','%homicide%','%stabbing%','%weapon%','%gun%'])
			                THEN 'Violent Crime'
			            WHEN transcript ILIKE ANY(ARRAY['%stolen%','%theft%','%burglary%','%larceny%','%shoplifting%','%breaking in%','%vandalism%'])
			                THEN 'Property Crime'
			            WHEN transcript ILIKE ANY(ARRAY['%suspicious%','%warrant%'])
			                THEN 'Suspicious'
			            WHEN transcript ILIKE ANY(ARRAY['%accident%','%crash%','%collision%','%mva%','%rollover%'])
			                THEN 'MVA / Crash'
			            WHEN transcript ILIKE ANY(ARRAY['%traffic stop%','%plate%','%registered to%','%comes back to%','%return clean%','%registered owner%'])
			                THEN 'Traffic Stop / Plate'
			            WHEN transcript ILIKE ANY(ARRAY['%erratic%','%speeding%','%road blocked%','%wires down%','%broken down%','%broke down%'])
			                THEN 'Road Hazard'
			            WHEN transcript ILIKE ANY(ARRAY['%domestic%'])
			                THEN 'Domestic'
			            WHEN transcript ILIKE ANY(ARRAY['%disturbance%','%argument%','%fighting%','%irate%','%unwanted%','%trespass%','%refusing%','%wilding out%','%attacking%'])
			                THEN 'Disturbance'
			            ELSE NULL
			        END AS subcategory
			    FROM filtered_calls
			)
			SELECT subcategory, COUNT(*) AS count
			FROM categorized
			WHERE subcategory IS NOT NULL
			GROUP BY subcategory
			ORDER BY count DESC`

		catMap := make(map[string]*statsIncidentCat)
		for _, cName := range mappedCategoryOrder {
			catMap[cName] = &statsIncidentCat{
				Category:      cName,
				Color:         mappedCategoryColors[cName],
				Subcategories: []statsIncidentSub{},
			}
		}

		incRows, err := db.Query(incidentQuery, legacyStart)
		if err != nil {
			log.Printf("StatsHandler: legacy incident query error: %v", err)
			return
		}
		defer incRows.Close()
		for incRows.Next() {
			var subLabel string
			var subCount int
			if incRows.Scan(&subLabel, &subCount) == nil && subCount > 0 {
				totalIncidents += subCount
				if parentCat, ok := subcategoryToCategory[subLabel]; ok {
					if cat, ok2 := catMap[parentCat]; ok2 {
						cat.Subcategories = append(cat.Subcategories, statsIncidentSub{Label: subLabel, Count: subCount})
						cat.Count += subCount
					}
					topNatures = append(topNatures, statsNatureRank{
						Label:    subLabel,
						Category: parentCat,
						Color:    mappedCategoryColors[parentCat],
						Count:    subCount,
					})
				}
			}
		}
		if totalIncidents > 0 {
			for i := range topNatures {
				topNatures[i].SharePct = math.Round((float64(topNatures[i].Count)*100.0/float64(totalIncidents))*10) / 10
			}
			for _, cName := range mappedCategoryOrder {
				if cat := catMap[cName]; cat != nil && cat.Count > 0 {
					cat.SharePct = math.Round((float64(cat.Count)*100.0/float64(totalIncidents))*10) / 10
					incidentSummary = append(incidentSummary, *cat)
				}
			}
			sort.Slice(incidentSummary, func(i, j int) bool {
				return incidentSummary[i].Count > incidentSummary[j].Count
			})
		}
	}()

	wg.Wait()

	// Merge hourly incident counts into callsByHour
	for h := 0; h < 24; h++ {
		callsByHour[h].Incidents = incidentByHour[h]
	}

	// Build systemBreakdown
	var systemBreakdown []statsSystemBreakdown
	for _, sys := range availableSystems {
		uid := uint64(sys.Id)
		cCnt := systemCallCounts[uid]
		iCnt := incidentBySystem[uid]
		if cCnt > 0 || iCnt > 0 {
			systemBreakdown = append(systemBreakdown, statsSystemBreakdown{
				Id:        sys.Id,
				Label:     sys.Label,
				Count:     cCnt,
				Incidents: iCnt,
			})
		}
	}
	sort.Slice(systemBreakdown, func(i, j int) bool {
		if systemBreakdown[i].Count != systemBreakdown[j].Count {
			return systemBreakdown[i].Count > systemBreakdown[j].Count
		}
		return systemBreakdown[i].Incidents > systemBreakdown[j].Incidents
	})
	if len(systemBreakdown) > 12 {
		systemBreakdown = systemBreakdown[:12]
	}

	if topTalkgroups == nil {
		topTalkgroups = []statsLabelCount{}
	}
	if topDepartmentsByTone == nil {
		topDepartmentsByTone = []statsLabelCount{}
	}
	if tagBreakdown == nil {
		tagBreakdown = []statsLabelCount{}
	}
	if systemBreakdown == nil {
		systemBreakdown = []statsSystemBreakdown{}
	}
	if incidentSummary == nil {
		incidentSummary = []statsIncidentCat{}
	}
	if topNatures == nil {
		topNatures = []statsNatureRank{}
	}
	if topIncidentChannels == nil {
		topIncidentChannels = []statsLabelCount{}
	}
	if recentIncidents == nil {
		recentIncidents = []statsRecentIncident{}
	}

	return json.Marshal(map[string]any{
		"availableSystems":        availableSystems,
		"range":                   rangeKey,
		"classificationMode":      classificationMode,
		"callsPerMinute":          callsPerMinute,
		"topTalkgroups":           topTalkgroups,
		"callsByHour":             callsByHour,
		"peakHour":                peakHour,
		"topDepartmentsByTone":    topDepartmentsByTone,
		"tagBreakdown":            tagBreakdown,
		"systemBreakdown":         systemBreakdown,
		"totalCallsToday":         totalCallsToday,
		"totalCallsRange":         totalCallsRange,
		"callsLastMinute":         callsLastMinute,
		"callsLastHour":           callsLastHour,
		"peakCallsPerMinute":      peakCallsPerMinute,
		"avgCallsPerMinute":       avgCallsPerMinute,
		"activeChannelsCount":     activeChannelsCount,
		"activeToneDeptsCount":    activeToneDeptsCount,
		"totalToneDispatches":     totalToneDispatches,
		"totalIncidents":          totalIncidents,
		"mappedIncidentsCount":    mappedIncidentsCount,
		"uniqueIncidentLocations": uniqueIncidentLocations,
		"incidentSummary":         incidentSummary,
		"topNatures":              topNatures,
		"topIncidentChannels":     topIncidentChannels,
		"recentIncidents":         recentIncidents,
		"generatedAt":             now,
	})
}
