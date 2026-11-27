// Copyright (C) 2025 Thinline Dynamic Solutions
//
// Keeping incident pins inside the counties a system covers, using the county
// boundaries imported for the map overlay.

package main

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

const pinCountyCacheTTL = 10 * time.Minute

// countyShape holds a county outline as polygons of rings of [lon, lat].
type countyShape struct {
	minLat, minLon, maxLat, maxLon float64
	polygons                       [][][][2]float64
}

var pinCountyCache = struct {
	sync.Mutex
	at     time.Time
	shapes map[string]*countyShape
}{shapes: map[string]*countyShape{}}

func parseCountyGeometry(raw string) [][][][2]float64 {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if json.Unmarshal([]byte(raw), &g) != nil {
		return nil
	}
	switch g.Type {
	case "Polygon":
		var p [][][2]float64
		if json.Unmarshal(g.Coordinates, &p) == nil {
			return [][][][2]float64{p}
		}
	case "MultiPolygon":
		var mp [][][][2]float64
		if json.Unmarshal(g.Coordinates, &mp) == nil {
			return mp
		}
	}
	return nil
}

// pointInPolygon uses the even-odd rule over every ring, so holes count as outside.
func pointInPolygon(lat, lon float64, rings [][][2]float64) bool {
	inside := false
	for _, ring := range rings {
		for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
			xi, yi := ring[i][0], ring[i][1]
			xj, yj := ring[j][0], ring[j][1]
			if (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
				inside = !inside
			}
		}
	}
	return inside
}

func (s *countyShape) contains(lat, lon float64) bool {
	if lat < s.minLat || lat > s.maxLat || lon < s.minLon || lon > s.maxLon {
		return false
	}
	for _, p := range s.polygons {
		if pointInPolygon(lat, lon, p) {
			return true
		}
	}
	return false
}

func (ms *MappingStore) countyShapes(geoids []string) map[string]*countyShape {
	pinCountyCache.Lock()
	defer pinCountyCache.Unlock()
	if time.Since(pinCountyCache.at) > pinCountyCacheTTL {
		pinCountyCache.shapes = map[string]*countyShape{}
		pinCountyCache.at = time.Now()
	}
	missing := []string{}
	for _, id := range geoids {
		if _, ok := pinCountyCache.shapes[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 && ms != nil && ms.db != nil && ms.db.Sql != nil {
		rows, err := ms.db.Sql.Query(
			`SELECT "geoid", "geometry", "minLat", "minLon", "maxLat", "maxLon" FROM "mappingBoundaries" WHERE "layerType" = 'county' AND "geoid" = ANY($1)`,
			pq.StringArray(missing),
		)
		if err == nil {
			for rows.Next() {
				var id, geom string
				s := &countyShape{}
				if rows.Scan(&id, &geom, &s.minLat, &s.minLon, &s.maxLat, &s.maxLon) != nil {
					continue
				}
				if s.polygons = parseCountyGeometry(geom); len(s.polygons) > 0 {
					pinCountyCache.shapes[id] = s
				}
			}
			rows.Close()
		}
		// Remember counties with no boundary so they aren't queried on every call.
		for _, id := range missing {
			if _, ok := pinCountyCache.shapes[id]; !ok {
				pinCountyCache.shapes[id] = nil
			}
		}
	}
	out := map[string]*countyShape{}
	for _, id := range geoids {
		if s := pinCountyCache.shapes[id]; s != nil {
			out[id] = s
		}
	}
	return out
}

// PinInsideCounties reports whether lat/lon falls in any of the counties.
// known is false when none of them have an imported boundary, in which case
// the pin should not be rejected.
func (ms *MappingStore) PinInsideCounties(lat, lon float64, geoids []string) (inside bool, known bool) {
	shapes := ms.countyShapes(geoids)
	if len(shapes) == 0 {
		return false, false
	}
	for _, s := range shapes {
		if s.contains(lat, lon) {
			return true, true
		}
	}
	return false, true
}

// ListCounties returns the imported county boundaries for the admin picker.
func (ms *MappingStore) ListCounties() ([]map[string]string, error) {
	rows, err := ms.db.Sql.Query(`SELECT "geoid", "name", "stateFips" FROM "mappingBoundaries" WHERE "layerType" = 'county' ORDER BY "stateFips", "name"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var geoid, name, fips string
		if err := rows.Scan(&geoid, &name, &fips); err != nil {
			return nil, err
		}
		out = append(out, map[string]string{"geoid": geoid, "name": name, "state": stateNameForFIPS(fips)})
	}
	return out, rows.Err()
}

func normalizePinCounties(v any) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	switch list := v.(type) {
	case []any:
		for _, x := range list {
			if s, ok := x.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, s := range list {
			add(s)
		}
	}
	return out
}
