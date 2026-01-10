// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

type Tag struct {
	Id    uint64
	Label string
	Order uint
	Color string
}

func NewTag() *Tag {
	return &Tag{}
}

func (tag *Tag) FromMap(m map[string]any) *Tag {
	// Handle both "id" and "_id" fields for backward compatibility
	if v, ok := m["id"].(float64); ok {
		tag.Id = uint64(v)
	} else if v, ok := m["_id"].(float64); ok {
		tag.Id = uint64(v)
	}

	switch v := m["label"].(type) {
	case string:
		tag.Label = v
	}

	switch v := m["order"].(type) {
	case float64:
		tag.Order = uint(v)
	}

	switch v := m["color"].(type) {
	case string:
		tag.Color = v
	}

	return tag
}

func (tag *Tag) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"id":    tag.Id,
		"label": tag.Label,
	}

	if tag.Order > 0 {
		m["order"] = tag.Order
	}

	if tag.Color != "" {
		m["color"] = tag.Color
	}

	return json.Marshal(m)
}

type Tags struct {
	List  []*Tag
	mutex sync.RWMutex
}

func NewTags() *Tags {
	return &Tags{
		List:  []*Tag{},
		mutex: sync.RWMutex{},
	}
}

func (tags *Tags) FromMap(f []any) *Tags {
	tags.mutex.Lock()
	defer tags.mutex.Unlock()

	tags.List = []*Tag{}

	for _, r := range f {
		switch m := r.(type) {
		case map[string]any:
			tag := NewTag().FromMap(m)
			tags.List = append(tags.List, tag)
		}
	}

	return tags
}

func (tags *Tags) GetTagById(id uint64) (tag *Tag, ok bool) {
	tags.mutex.RLock()
	defer tags.mutex.RUnlock()

	for _, tag := range tags.List {
		if tag.Id == id {
			return tag, true
		}
	}

	return nil, false
}

func (tags *Tags) GetTagByLabel(label string) (tag *Tag, ok bool) {
	tags.mutex.RLock()
	defer tags.mutex.RUnlock()

	for _, tag := range tags.List {
		if tag.Label == label {
			return tag, true
		}
	}

	return nil, false
}

func (tags *Tags) GetTagsData(systemsMap *SystemsMap) []Tag {
	var list = []Tag{}

	for _, systemMap := range *systemsMap {
		switch talkgroupsMap := systemMap["talkgroups"].(type) {
		case TalkgroupsMap:
			for _, talkgroupMap := range talkgroupsMap {
				switch label := talkgroupMap["tag"].(type) {
				case string:
					if tag, ok := tags.GetTagByLabel(label); ok {
						add := true
						for _, l := range list {
							if l == *tag {
								add = false
								break
							}
						}
						if add {
							list = append(list, *tag)
						}
					}
				}
			}
		}
	}

	return list
}

func (tags *Tags) GetTagsMap(systemsMap *SystemsMap) TagsMap {
	tagsMap := TagsMap{}

	for _, system := range *systemsMap {
		var (
			fSystemId     = system["id"]
			fTalkgroups   = system["talkgroups"]
			systemId      uint
			talkgroupsMap TalkgroupsMap
		)

		switch v := fSystemId.(type) {
		case uint:
			systemId = v
		}

		switch v := fTalkgroups.(type) {
		case TalkgroupsMap:
			talkgroupsMap = v
		}

		for _, talkgroup := range talkgroupsMap {
			var (
				fTalkgroupTag = talkgroup["tag"]
				fTalkgroupId  = talkgroup["id"]
				talkgroupTag  string
				talkgroupId   uint
			)

			switch v := fTalkgroupTag.(type) {
			case string:
				talkgroupTag = v
			}

			switch v := fTalkgroupId.(type) {
			case uint:
				talkgroupId = v
			}

			tag, ok := tags.GetTagByLabel(talkgroupTag)
			if !ok {
				continue
			}

			if tagsMap[tag.Label] == nil {
				tagsMap[tag.Label] = map[uint][]uint{}
			}

			if tagsMap[tag.Label][systemId] == nil {
				tagsMap[tag.Label][systemId] = []uint{}
			}

			found := false
			for _, id := range tagsMap[tag.Label][systemId] {
				if id == talkgroupId {
					found = true
					break
				}
			}
			if !found {
				tagsMap[tag.Label][systemId] = append(tagsMap[tag.Label][systemId], talkgroupId)
			}
		}
	}

	return tagsMap
}

func (tags *Tags) Read(db *Database) error {
	var (
		err   error
		query string
		rows  *sql.Rows
	)

	tags.mutex.Lock()
	defer tags.mutex.Unlock()

	tags.List = []*Tag{}

	formatError := errorFormatter("tags", "read")

	query = `SELECT "tagId", "label", "order", COALESCE("color", '') FROM "tags"`
	if rows, err = db.Sql.Query(query); err != nil {
		return formatError(err, query)
	}

	for rows.Next() {
		tag := NewTag()

		if err = rows.Scan(&tag.Id, &tag.Label, &tag.Order, &tag.Color); err != nil {
			break
		}

		tags.List = append(tags.List, tag)
	}

	rows.Close()

	if err != nil {
		return formatError(err, "")
	}

	sort.Slice(tags.List, func(i int, j int) bool {
		return tags.List[i].Order < tags.List[j].Order
	})

	return nil
}

func (tags *Tags) Write(db *Database) error {
	var (
		err    error
		query  string
		rows   *sql.Rows
		tagIds = []uint64{}
		tx     *sql.Tx
	)

	tags.mutex.Lock()
	defer tags.mutex.Unlock()

	formatError := errorFormatter("tags", "write")

	if tx, err = db.Sql.Begin(); err != nil {
		return formatError(err, "")
	}

	query = `SELECT "tagId" FROM "tags"`
	if rows, err = tx.Query(query); err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	for rows.Next() {
		var tagId uint64
		if err = rows.Scan(&tagId); err != nil {
			break
		}
		remove := true
		for _, tag := range tags.List {
			if tag.Id == 0 || tag.Id == tagId {
				remove = false
				break
			}
		}
		if remove {
			tagIds = append(tagIds, tagId)
		}
	}

	rows.Close()

	if err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	if len(tagIds) > 0 {
		if b, err := json.Marshal(tagIds); err == nil {
			in := strings.ReplaceAll(strings.ReplaceAll(string(b), "[", "("), "]", ")")
			query = fmt.Sprintf(`DELETE FROM "tags" WHERE "tagId" IN %s`, in)
			if _, err = tx.Exec(query); err != nil {
				tx.Rollback()
				return formatError(err, query)
			}
		}
	}

	for _, tag := range tags.List {
		var count uint
		var existingId uint64

		if tag.Id > 0 {
			query = fmt.Sprintf(`SELECT COUNT(*) FROM "tags" WHERE "tagId" = %d`, tag.Id)
			if err = tx.QueryRow(query).Scan(&count); err != nil {
				break
			}
		} else {
			// Check if a tag with this label already exists (to prevent duplicates)
			query = fmt.Sprintf(`SELECT "tagId" FROM "tags" WHERE "label" = '%s' LIMIT 1`, escapeQuotes(tag.Label))
			err = tx.QueryRow(query).Scan(&existingId)
			if err != nil && err != sql.ErrNoRows {
				// Real error (not just "no rows")
				break
			}
			if existingId > 0 {
				// Tag with this label already exists, update the in-memory tag's ID
				tag.Id = existingId
				count = 1
			}
		}

		if count == 0 {
			if tag.Id > 0 {
				// Preserve the explicit ID when inserting
				query = fmt.Sprintf(`INSERT INTO "tags" ("tagId", "label", "order", "color") VALUES (%d, '%s', %d, '%s')`, tag.Id, escapeQuotes(tag.Label), tag.Order, escapeQuotes(tag.Color))
			} else {
				// Let database assign auto-increment ID
				query = fmt.Sprintf(`INSERT INTO "tags" ("label", "order", "color") VALUES ('%s', %d, '%s')`, escapeQuotes(tag.Label), tag.Order, escapeQuotes(tag.Color))
			}
			if _, err = tx.Exec(query); err != nil {
				break
			}
		} else {
			query = fmt.Sprintf(`UPDATE "tags" SET "label" = '%s', "order" = %d, "color" = '%s' WHERE "tagId" = %d`, escapeQuotes(tag.Label), tag.Order, escapeQuotes(tag.Color), tag.Id)
			if _, err = tx.Exec(query); err != nil {
				break
			}
		}
	}

	if err != nil {
		tx.Rollback()
		return formatError(err, query)
	}

	if err = tx.Commit(); err != nil {
		tx.Rollback()
		return formatError(err, "")
	}

	return nil
}

type TagsMap map[string]map[uint][]uint
