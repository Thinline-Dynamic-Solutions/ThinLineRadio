// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
// Modified by Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Site struct {
	Id          uint64
	Label       string
	Order       uint
	SiteRef     string // Site ID as string to preserve leading zeros (e.g., "001", "021")
	RFSS        uint   // Radio Frequency Sub-System ID
	SystemId    uint64
	Frequencies []float64 // MHz frequencies for this site
	Preferred   bool      // Is this the preferred site for the system?
}

func NewSite() *Site {
	return &Site{}
}

func (site *Site) FromMap(m map[string]any) *Site {
	// Handle both "id" and "_id" fields for backward compatibility
	if v, ok := m["id"].(float64); ok {
		site.Id = uint64(v)
	} else if v, ok := m["_id"].(float64); ok {
		site.Id = uint64(v)
	}

	switch v := m["label"].(type) {
	case string:
		site.Label = v
	}

	switch v := m["order"].(type) {
	case float64:
		site.Order = uint(v)
	}

	switch v := m["siteRef"].(type) {
	case string:
		site.SiteRef = v
	case float64:
		// Convert number to string for backward compatibility
		site.SiteRef = fmt.Sprintf("%d", uint(v))
	}

	switch v := m["rfss"].(type) {
	case float64:
		site.RFSS = uint(v)
	}

	switch v := m["systemId"].(type) {
	case float64:
		site.SystemId = uint64(v)
	}

	// Parse frequencies array
	switch v := m["frequencies"].(type) {
	case []any:
		site.Frequencies = []float64{}
		for _, f := range v {
			switch freq := f.(type) {
			case float64:
				site.Frequencies = append(site.Frequencies, freq)
			}
		}
	}

	// Parse preferred flag
	switch v := m["preferred"].(type) {
	case bool:
		site.Preferred = v
	}

	return site
}

func (site *Site) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"id":       site.Id,
		"label":    site.Label,
		"siteRef":  site.SiteRef,
		"rfss":     site.RFSS,
		"systemId": site.SystemId,
	}

	if site.Order > 0 {
		m["order"] = site.Order
	}

	// Always include frequencies (even if empty array)
	m["frequencies"] = site.Frequencies
	if site.Frequencies == nil {
		m["frequencies"] = []float64{}
	}

	// Always include preferred flag
	m["preferred"] = site.Preferred

	return json.Marshal(m)
}

type Sites struct {
	List  []*Site
	mutex sync.Mutex
}

func NewSites() *Sites {
	return &Sites{
		List:  []*Site{},
		mutex: sync.Mutex{},
	}
}

func (sites *Sites) FromMap(f []any) *Sites {
	sites.mutex.Lock()
	defer sites.mutex.Unlock()

	sites.List = []*Site{}

	for _, r := range f {
		switch m := r.(type) {
		case map[string]any:
			site := NewSite().FromMap(m)
			sites.List = append(sites.List, site)
		}
	}

	return sites
}

func (sites *Sites) GetSiteById(id uint64) (site *Site, ok bool) {
	sites.mutex.Lock()
	defer sites.mutex.Unlock()

	for _, site := range sites.List {
		if site.Id == id {
			return site, true
		}
	}

	return nil, false
}

func (sites *Sites) GetSiteByLabel(label string) (site *Site, ok bool) {
	sites.mutex.Lock()
	defer sites.mutex.Unlock()

	for _, site := range sites.List {
		if site.Label == label {
			return site, true
		}
	}

	return nil, false
}

func (sites *Sites) GetSiteByRef(ref string) (site *Site, ok bool) {
	sites.mutex.Lock()
	defer sites.mutex.Unlock()

	for _, site := range sites.List {
		if site.SiteRef == ref {
			return site, true
		}
	}

	return nil, false
}

// GetSiteByFrequency finds a site that matches the given frequency (in Hz)
// Frequencies are matched with a tolerance to account for slight variations
func (sites *Sites) GetSiteByFrequency(frequency uint) (site *Site, ok bool) {
	sites.mutex.Lock()
	defer sites.mutex.Unlock()

	if frequency == 0 {
		return nil, false
	}

	// Convert frequency from Hz to MHz for comparison
	freqMHz := float64(frequency) / 1e6

	// Use a tolerance of 0.01 MHz (10 kHz) for matching
	tolerance := 0.01

	for _, site := range sites.List {
		for _, siteFreq := range site.Frequencies {
			// Check if the frequency matches within tolerance
			diff := freqMHz - siteFreq
			if diff < 0 {
				diff = -diff
			}
			if diff <= tolerance {
				return site, true
			}
		}
	}

	return nil, false
}

func (sites *Sites) ReadTx(tx *sql.Tx, systemId uint64) error {
	var (
		err   error
		query string
		rows  *sql.Rows
	)

	sites.mutex.Lock()
	defer sites.mutex.Unlock()

	sites.List = []*Site{}

	formatError := errorFormatter("sites", "read")

	query = fmt.Sprintf(`SELECT "siteId", "label", "order", "siteRef", "rfss", "frequencies", "preferred" FROM "sites" WHERE "systemId" = %d`, systemId)
	if rows, err = tx.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		site := NewSite()
		var frequenciesJSON string

		if err = rows.Scan(&site.Id, &site.Label, &site.Order, &site.SiteRef, &site.RFSS, &frequenciesJSON, &site.Preferred); err != nil {
			break
		}

		// Parse frequencies JSON array
		if len(frequenciesJSON) > 0 {
			json.Unmarshal([]byte(frequenciesJSON), &site.Frequencies)
		}
		if site.Frequencies == nil {
			site.Frequencies = []float64{}
		}

		sites.List = append(sites.List, site)
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	sort.Slice(sites.List, func(i int, j int) bool {
		return sites.List[i].Order < sites.List[j].Order
	})

	return nil
}

func (sites *Sites) WriteTx(tx *sql.Tx, systemId uint64) error {
	var (
		err     error
		query   string
		rows    *sql.Rows
		siteIds = []uint64{}
	)

	sites.mutex.Lock()
	defer sites.mutex.Unlock()

	formatError := errorFormatter("sites", "writetx")

	query = fmt.Sprintf(`SELECT "siteId" FROM "sites" WHERE "systemId" = %d`, systemId)
	if rows, err = tx.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		var siteId uint64
		if err = rows.Scan(&siteId); err != nil {
			break
		}
		remove := true
		for _, site := range sites.List {
			if site.Id == 0 || site.Id == siteId {
				remove = false
				break
			}
		}
		if remove {
			siteIds = append(siteIds, siteId)
		}
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	if len(siteIds) > 0 {
		if b, err := json.Marshal(siteIds); err == nil {
			in := strings.ReplaceAll(strings.ReplaceAll(string(b), "[", "("), "]", ")")
			query = fmt.Sprintf(`DELETE FROM "sites" WHERE "siteId" IN %s`, in)
			if _, err = tx.Exec(query); err != nil {
				return formatError(err, query)
			}
		}
	}

	for _, site := range sites.List {
		var count uint

		// Serialize frequencies to JSON
		frequenciesJSON := "[]"
		if len(site.Frequencies) > 0 {
			if b, err := json.Marshal(site.Frequencies); err == nil {
				frequenciesJSON = string(b)
			}
		}

		if site.Id > 0 {
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "sites" WHERE "siteId" = %d`, site.Id)
			if err = tx.QueryRow(query).Scan(&count); err != nil {
				break
			}
		}

		if count == 0 {
			if site.Id > 0 {
				// Preserve the explicit ID when inserting
				query = fmt.Sprintf(`INSERT INTO "sites" ("siteId", "label", "order", "siteRef", "rfss", "systemId", "frequencies", "preferred") VALUES (%d, '%s', %d, '%s', %d, %d, '%s', %t)`, site.Id, escapeQuotes(site.Label), site.Order, escapeQuotes(site.SiteRef), site.RFSS, systemId, frequenciesJSON, site.Preferred)
			} else {
				// Let database assign auto-increment ID
				query = fmt.Sprintf(`INSERT INTO "sites" ("label", "order", "siteRef", "rfss", "systemId", "frequencies", "preferred") VALUES ('%s', %d, '%s', %d, %d, '%s', %t)`, escapeQuotes(site.Label), site.Order, escapeQuotes(site.SiteRef), site.RFSS, systemId, frequenciesJSON, site.Preferred)
			}
			if _, err = tx.Exec(query); err != nil {
				break
			}

		} else {
			query = fmt.Sprintf(`UPDATE "sites" SET "label" = '%s', "order" = %d, "siteRef" = '%s', "rfss" = %d, "frequencies" = '%s', "preferred" = %t where "siteId" = %d`, escapeQuotes(site.Label), site.Order, escapeQuotes(site.SiteRef), site.RFSS, frequenciesJSON, site.Preferred, site.Id)
			if _, err = tx.Exec(query); err != nil {
				break
			}
		}
	}

	if err != nil {
		return formatError(err, query)
	}

	return nil
}
