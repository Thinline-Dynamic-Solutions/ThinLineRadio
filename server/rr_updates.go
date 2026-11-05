// Copyright (C) 2025 Thinline Dynamic Solutions
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
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Radio Reference updates: a system linked to a Radio Reference system is compared
// with it every week. Nothing changes until an admin picks items from the review.
// A main system's review covers its own talkgroups and the subsystem talkgroups that
// route from it; a subsystem with its own link is reviewed on its own.

const (
	rrUpdateInterval  = 7 * 24 * time.Hour
	rrUpdateFirstWait = 10 * time.Minute
	rrUpdateTick      = time.Hour
)

type rrFields struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	Group string `json:"group"`
	Tag   string `json:"tag"`
}

type rrReviewItem struct {
	SystemId    uint64   `json:"systemId"`
	SystemLabel string   `json:"systemLabel"`
	Ref         uint     `json:"ref"`
	Category    string   `json:"category"`
	Current     rrFields `json:"current"`
	Proposed    rrFields `json:"proposed"`
	Changes     []string `json:"changes"`
	Reasons     []string `json:"reasons,omitempty"`
}

type rrTarget struct {
	Id    uint64 `json:"id"`
	Label string `json:"label"`
}

type rrReview struct {
	SystemId     uint64            `json:"systemId"`
	SystemLabel  string            `json:"systemLabel"`
	RRSystemId   int               `json:"rrSystemId"`
	RRSystemName string            `json:"rrSystemName"`
	CheckedAt    int64             `json:"checkedAt"`
	Error        string            `json:"error,omitempty"`
	Talkgroups   int               `json:"talkgroups"`
	RRTalkgroups int               `json:"rrTalkgroups"`
	Fill         []rrReviewItem    `json:"fill"`
	Differ       []rrReviewItem    `json:"differ"`
	Missing      []rrReviewItem    `json:"missing"`
	NotInRR      []rrReviewItem    `json:"notInRR"`
	Targets      []rrTarget        `json:"targets"`
	GroupMap     map[string]string `json:"groupMap"`
}

type rrUpdateState struct {
	mu      sync.Mutex
	running map[uint64]bool
}

var rrUpdates = &rrUpdateState{running: map[uint64]bool{}}

func (s *rrUpdateState) start(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *rrUpdateState) done(id uint64) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

func (s *rrUpdateState) isRunning(id uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[id]
}

func migrateRadioReferenceUpdates(db *Database) error {
	_, err := db.Sql.Exec(`CREATE TABLE IF NOT EXISTS "radioReferenceReviews" (
		"systemId" bigint NOT NULL PRIMARY KEY,
		"rrSystemId" integer NOT NULL DEFAULT 0,
		"checkedAt" bigint NOT NULL DEFAULT 0,
		"review" text NOT NULL DEFAULT '{}',
		"rrTalkgroups" text NOT NULL DEFAULT '[]'
	)`)
	if err != nil {
		return fmt.Errorf("migrateRadioReferenceUpdates: %w", err)
	}
	return nil
}

// ── comparison ──────────────────────────────────────────────────────────────

var rrNumericLabel = regexp.MustCompile(`^\s*\d+\s*$`)

func rrSame(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

type rrLocal struct {
	system *System
	tg     *Talkgroup
	fields rrFields
	groups []string
}

// rrAutoReasons says why a talkgroup still looks like auto-populate made it.
func rrAutoReasons(l rrLocal) []string {
	var r []string
	if rrNumericLabel.MatchString(l.fields.Label) {
		r = append(r, "label is just the ID")
	}
	if rrNumericLabel.MatchString(l.fields.Name) {
		r = append(r, "name is just the ID")
	}
	if l.fields.Tag == "" || rrSame(l.fields.Tag, "Untagged") {
		r = append(r, "untagged")
	}
	if len(l.groups) == 0 || (len(l.groups) == 1 && rrSame(l.groups[0], "Unknown")) {
		r = append(r, "group Unknown")
	}
	return r
}

// rrLearn maps each Radio Reference value to the label this server
// most often uses for it on talkgroups that were named by hand.
func rrLearn(locals []rrLocal, rrByRef map[uint]RadioReferenceTalkgroup, rrValue func(RadioReferenceTalkgroup) string, localValues func(rrLocal) []string) map[string]string {
	counts := map[string]map[string]int{}
	for _, l := range locals {
		r, ok := rrByRef[l.tg.TalkgroupRef]
		if !ok || rrValue(r) == "" || len(rrAutoReasons(l)) > 0 {
			continue
		}
		for _, v := range localValues(l) {
			if v == "" {
				continue
			}
			if counts[rrValue(r)] == nil {
				counts[rrValue(r)] = map[string]int{}
			}
			counts[rrValue(r)][v]++
		}
	}
	learned := map[string]string{}
	for key, byValue := range counts {
		best, bestN := "", 0
		for v, n := range byValue {
			if n > bestN || (n == bestN && v < best) {
				best, bestN = v, n
			}
		}
		learned[key] = best
	}
	return learned
}

// rrProposed keeps Radio Reference's own tag rather than a learned one: servers often
// tag by county, and learning would then give every talkgroup on the network the tag
// of whichever county was named by hand.
func rrProposed(r RadioReferenceTalkgroup, groups map[string]string) rrFields {
	p := rrFields{Label: strings.TrimSpace(r.AlphaTag), Name: strings.TrimSpace(r.Description), Group: r.Group, Tag: strings.TrimSpace(r.Tag)}
	if g, ok := groups[r.Group]; ok {
		p.Group = g
	}
	return p
}

func rrChanges(l rrLocal, p rrFields) []string {
	var c []string
	if p.Label != "" && !rrSame(l.fields.Label, p.Label) {
		c = append(c, "label")
	}
	if p.Name != "" && !rrSame(l.fields.Name, p.Name) {
		c = append(c, "name")
	}
	inGroup := false
	for _, g := range l.groups {
		inGroup = inGroup || rrSame(g, p.Group)
	}
	if p.Group != "" && !inGroup {
		c = append(c, "group")
	}
	if p.Tag != "" && !rrSame(l.fields.Tag, p.Tag) {
		c = append(c, "tag")
	}
	return c
}

// rrScope lists the systems a review covers and which of their talkgroups count.
func (controller *Controller) rrScope(linked *System) ([]*System, []rrLocal) {
	systems := []*System{linked}
	include := map[*System]func(*Talkgroup) bool{linked: func(*Talkgroup) bool { return true }}
	if linked.IsMain {
		controller.Systems.mutex.RLock()
		for _, sub := range controller.Systems.List {
			if sub.isUnder(linked.Id) && sub.RadioReference.SystemId == 0 {
				s := sub
				systems = append(systems, s)
				include[s] = func(tg *Talkgroup) bool { return s.routesFrom(linked.Id, tg.TalkgroupRef) }
			}
		}
		controller.Systems.mutex.RUnlock()
	}

	var locals []rrLocal
	for _, s := range systems {
		for _, tg := range s.Talkgroups.List {
			if tg == nil || !include[s](tg) {
				continue
			}
			l := rrLocal{system: s, tg: tg, fields: rrFields{Label: tg.Label, Name: tg.Name}}
			for _, id := range tg.GroupIds {
				if g, ok := controller.Groups.GetGroupById(id); ok {
					l.groups = append(l.groups, g.Label)
				}
			}
			l.fields.Group = strings.Join(l.groups, " + ")
			if t, ok := controller.Tags.GetTagById(tg.TagId); ok {
				l.fields.Tag = t.Label
			}
			locals = append(locals, l)
		}
	}
	return systems, locals
}

func (controller *Controller) buildRRReview(linked *System, rrList []RadioReferenceTalkgroup) *rrReview {
	systems, locals := controller.rrScope(linked)
	rrByRef := map[uint]RadioReferenceTalkgroup{}
	for _, r := range rrList {
		if r.ID > 0 {
			rrByRef[uint(r.ID)] = r
		}
	}

	review := &rrReview{
		SystemId:     linked.Id,
		SystemLabel:  linked.Label,
		RRSystemId:   linked.RadioReference.SystemId,
		RRSystemName: linked.RadioReference.SystemName,
		Talkgroups:   len(locals),
		RRTalkgroups: len(rrByRef),
		Fill:         []rrReviewItem{},
		Differ:       []rrReviewItem{},
		Missing:      []rrReviewItem{},
		NotInRR:      []rrReviewItem{},
	}
	for _, s := range systems {
		review.Targets = append(review.Targets, rrTarget{Id: s.Id, Label: s.Label})
	}

	groups := rrLearn(locals, rrByRef, func(r RadioReferenceTalkgroup) string { return r.Group }, func(l rrLocal) []string { return l.groups })
	review.GroupMap = map[string]string{}
	for category, g := range groups {
		if !rrSame(category, g) {
			review.GroupMap[category] = g
		}
	}

	present := map[uint]bool{}
	categoryHomes := map[string]map[*System]int{}
	for _, l := range locals {
		present[l.tg.TalkgroupRef] = true
		r, ok := rrByRef[l.tg.TalkgroupRef]
		item := rrReviewItem{SystemId: l.system.Id, SystemLabel: l.system.Label, Ref: l.tg.TalkgroupRef, Current: l.fields}
		if !ok {
			review.NotInRR = append(review.NotInRR, item)
			continue
		}
		item.Category = r.Group
		if categoryHomes[r.Group] == nil {
			categoryHomes[r.Group] = map[*System]int{}
		}
		categoryHomes[r.Group][l.system]++

		item.Proposed = rrProposed(r, groups)
		if item.Changes = rrChanges(l, item.Proposed); len(item.Changes) == 0 {
			continue
		}
		if item.Reasons = rrAutoReasons(l); len(item.Reasons) > 0 {
			review.Fill = append(review.Fill, item)
		} else {
			review.Differ = append(review.Differ, item)
		}
	}

	// A county slice of a statewide network only uses some of its categories, so only
	// those categories count as missing. Each goes to the system that holds most of
	// that category already.
	for _, r := range rrByRef {
		homes, used := categoryHomes[r.Group]
		if present[uint(r.ID)] || !used {
			continue
		}
		var home *System
		best := 0
		for s, n := range homes {
			if n > best || (n == best && home != nil && s.Order < home.Order) {
				home, best = s, n
			}
		}
		p := rrProposed(r, groups)
		if p.Name == "" {
			p.Name = p.Label
		}
		review.Missing = append(review.Missing, rrReviewItem{SystemId: home.Id, SystemLabel: home.Label, Ref: uint(r.ID), Category: r.Group, Proposed: p, Changes: []string{"label", "name", "group", "tag"}})
	}

	for _, list := range [][]rrReviewItem{review.Fill, review.Differ, review.Missing, review.NotInRR} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].SystemLabel != list[j].SystemLabel {
				return list[i].SystemLabel < list[j].SystemLabel
			}
			return list[i].Ref < list[j].Ref
		})
	}
	return review
}

// ── storage ─────────────────────────────────────────────────────────────────

func (controller *Controller) saveRRReview(review *rrReview, rrList []RadioReferenceTalkgroup) error {
	reviewJSON, err := json.Marshal(review)
	if err != nil {
		return err
	}
	listJSON := "[]"
	if rrList != nil {
		b, err := json.Marshal(rrList)
		if err != nil {
			return err
		}
		listJSON = string(b)
		_, err = controller.Database.Sql.Exec(`INSERT INTO "radioReferenceReviews" ("systemId", "rrSystemId", "checkedAt", "review", "rrTalkgroups") VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT ("systemId") DO UPDATE SET "rrSystemId" = EXCLUDED."rrSystemId", "checkedAt" = EXCLUDED."checkedAt", "review" = EXCLUDED."review", "rrTalkgroups" = EXCLUDED."rrTalkgroups"`,
			review.SystemId, review.RRSystemId, review.CheckedAt, string(reviewJSON), listJSON)
		return err
	}
	// A failed check keeps the last Radio Reference list.
	_, err = controller.Database.Sql.Exec(`INSERT INTO "radioReferenceReviews" ("systemId", "rrSystemId", "checkedAt", "review") VALUES ($1, $2, $3, $4)
		ON CONFLICT ("systemId") DO UPDATE SET "rrSystemId" = EXCLUDED."rrSystemId", "checkedAt" = EXCLUDED."checkedAt", "review" = EXCLUDED."review"`,
		review.SystemId, review.RRSystemId, review.CheckedAt, string(reviewJSON))
	return err
}

func (controller *Controller) loadRRReview(systemId uint64) (*rrReview, []RadioReferenceTalkgroup, error) {
	var reviewJSON, listJSON string
	var rrSystemId int
	err := controller.Database.Sql.QueryRow(`SELECT "rrSystemId", "review", "rrTalkgroups" FROM "radioReferenceReviews" WHERE "systemId" = $1`, systemId).Scan(&rrSystemId, &reviewJSON, &listJSON)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	review := &rrReview{}
	if err := json.Unmarshal([]byte(reviewJSON), review); err != nil {
		return nil, nil, err
	}
	var list []RadioReferenceTalkgroup
	_ = json.Unmarshal([]byte(listJSON), &list)
	// The cached list only counts while the link still points at the same system.
	if system, ok := controller.Systems.GetSystemById(systemId); !ok || system.RadioReference.SystemId != rrSystemId {
		list = nil
	}
	return review, list, nil
}

// ── checks ──────────────────────────────────────────────────────────────────

func (controller *Controller) runRRCheck(systemId uint64) {
	if !rrUpdates.start(systemId) {
		return
	}
	defer rrUpdates.done(systemId)

	system, ok := controller.Systems.GetSystemById(systemId)
	if !ok || system.RadioReference.SystemId == 0 {
		return
	}
	opts := controller.Options
	rrSystemId := system.RadioReference.SystemId

	var rrList []RadioReferenceTalkgroup
	var err error
	if !opts.RadioReferenceEnabled || opts.RadioReferenceUsername == "" {
		err = fmt.Errorf("Radio Reference isn't set up in Options")
	} else {
		rr := NewRadioReferenceService(opts.RadioReferenceUsername, opts.RadioReferencePassword, opts.RadioReferenceAPIKey)
		rrList, err = rr.GetAllTalkgroupsForSystem(rrSystemId)
		if err == nil && len(rrList) == 0 {
			err = fmt.Errorf("Radio Reference returned no talkgroups for system %d", rrSystemId)
		}
	}

	// Reload: the system may have been saved while Radio Reference was downloading.
	if system, ok = controller.Systems.GetSystemById(systemId); !ok {
		return
	}
	var review *rrReview
	if err != nil {
		previous, _, _ := controller.loadRRReview(systemId)
		review = &rrReview{}
		if previous != nil {
			review = previous
		}
		review.SystemId, review.SystemLabel = system.Id, system.Label
		review.RRSystemId, review.RRSystemName = rrSystemId, system.RadioReference.SystemName
		review.Error = err.Error()
		controller.Logs.LogEvent(LogLevelWarn, fmt.Sprintf("radio reference update for %s: %v", system.Label, err))
	} else {
		review = controller.buildRRReview(system, rrList)
		controller.Logs.LogEvent(LogLevelInfo, fmt.Sprintf("radio reference update for %s: %d to fill in, %d named differently, %d missing",
			system.Label, len(review.Fill), len(review.Differ), len(review.Missing)))
	}
	review.CheckedAt = time.Now().UnixMilli()
	if err := controller.saveRRReview(review, rrList); err != nil {
		controller.Logs.LogEvent(LogLevelError, fmt.Sprintf("radio reference update for %s: save: %v", system.Label, err))
	}
}

func (controller *Controller) startRadioReferenceUpdateLoop() {
	time.Sleep(rrUpdateFirstWait)
	for {
		controller.runDueRRChecks()
		time.Sleep(rrUpdateTick)
	}
}

func (controller *Controller) runDueRRChecks() {
	if !controller.Options.RadioReferenceEnabled {
		return
	}
	checked := map[uint64]int64{}
	if rows, err := controller.Database.Sql.Query(`SELECT "systemId", "checkedAt" FROM "radioReferenceReviews"`); err == nil {
		for rows.Next() {
			var id uint64
			var at int64
			if rows.Scan(&id, &at) == nil {
				checked[id] = at
			}
		}
		rows.Close()
	}
	controller.Systems.mutex.RLock()
	var due []uint64
	for _, s := range controller.Systems.List {
		if s.RadioReference.SystemId > 0 && time.Since(time.UnixMilli(checked[s.Id])) >= rrUpdateInterval {
			due = append(due, s.Id)
		}
	}
	controller.Systems.mutex.RUnlock()
	for _, id := range due {
		controller.runRRCheck(id)
	}
}

// ── apply ───────────────────────────────────────────────────────────────────

type rrApplyPick struct {
	SystemId uint64 `json:"systemId"`
	Ref      uint   `json:"ref"`
}

type rrApplyBody struct {
	SystemId uint64        `json:"systemId"`
	Fill     []rrApplyPick `json:"fill"`
	Differ   []rrApplyPick `json:"differ"`
	Missing  []rrApplyPick `json:"missing"` // systemId is where to add it
}

// rrFindGroupId and rrFindTagId match labels loosely, so "Law Dispatch " and
// "law dispatch" are the same thing.
func rrFindGroupId(controller *Controller, label string) (uint64, error) {
	controller.Groups.mutex.RLock()
	for _, g := range controller.Groups.List {
		if rrSame(g.Label, label) && g.Id > 0 {
			controller.Groups.mutex.RUnlock()
			return g.Id, nil
		}
	}
	controller.Groups.mutex.RUnlock()
	return controller.resolveGroupIdForLabel(label)
}

func rrFindTagId(controller *Controller, label string) (uint64, error) {
	controller.Tags.mutex.RLock()
	for _, t := range controller.Tags.List {
		if rrSame(t.Label, label) && t.Id > 0 {
			controller.Tags.mutex.RUnlock()
			return t.Id, nil
		}
	}
	controller.Tags.mutex.RUnlock()
	return controller.resolveTagIdForLabel(label)
}

func (controller *Controller) applyRRFields(tg *Talkgroup, p rrFields, changes []string, replaceGroups bool) error {
	for _, field := range changes {
		switch field {
		case "label":
			tg.Label = p.Label
		case "name":
			tg.Name = p.Name
		case "group":
			id, err := rrFindGroupId(controller, p.Group)
			if err != nil {
				return err
			}
			if replaceGroups {
				tg.GroupIds = []uint64{id}
			} else {
				tg.GroupIds = append(tg.GroupIds, id)
			}
		case "tag":
			id, err := rrFindTagId(controller, p.Tag)
			if err != nil {
				return err
			}
			tg.TagId = id
		}
	}
	return nil
}

func (admin *Admin) applyRRReview(body rrApplyBody) (*rrReview, int, error) {
	ctrl := admin.Controller
	linked, ok := ctrl.Systems.GetSystemById(body.SystemId)
	if !ok || linked.RadioReference.SystemId == 0 {
		return nil, 0, fmt.Errorf("that system isn't linked to Radio Reference")
	}
	review, rrList, err := ctrl.loadRRReview(body.SystemId)
	if err != nil {
		return nil, 0, err
	}
	if review == nil || rrList == nil {
		return nil, 0, fmt.Errorf("run a check first")
	}

	find := func(list []rrReviewItem, pick rrApplyPick, matchSystem bool) (rrReviewItem, bool) {
		for _, item := range list {
			if item.Ref == pick.Ref && (!matchSystem || item.SystemId == pick.SystemId) {
				return item, true
			}
		}
		return rrReviewItem{}, false
	}
	targets := map[uint64]bool{}
	for _, t := range review.Targets {
		targets[t.Id] = true
	}

	admin.mutex.Lock()
	defer admin.mutex.Unlock()

	applied := 0
	fail := func(err error) (*rrReview, int, error) {
		if readErr := ctrl.Systems.Read(ctrl.Database); readErr != nil {
			ctrl.Logs.LogEvent(LogLevelError, fmt.Sprintf("radio reference apply: reload: %v", readErr))
		}
		return nil, 0, err
	}

	update := func(list []rrReviewItem, picks []rrApplyPick, replaceGroups bool) error {
		for _, pick := range picks {
			item, ok := find(list, pick, true)
			if !ok {
				continue
			}
			system, ok := ctrl.Systems.GetSystemById(item.SystemId)
			if !ok {
				continue
			}
			tg, ok := system.Talkgroups.GetTalkgroupByRef(item.Ref)
			if !ok {
				continue
			}
			if err := ctrl.applyRRFields(tg, item.Proposed, item.Changes, replaceGroups); err != nil {
				return err
			}
			applied++
		}
		return nil
	}
	if err := update(review.Fill, body.Fill, true); err != nil {
		return fail(err)
	}
	if err := update(review.Differ, body.Differ, false); err != nil {
		return fail(err)
	}

	for _, pick := range body.Missing {
		item, ok := find(review.Missing, pick, false)
		if !ok || !targets[pick.SystemId] {
			continue
		}
		system, ok := ctrl.Systems.GetSystemById(pick.SystemId)
		if !ok {
			continue
		}
		if _, exists := system.Talkgroups.GetTalkgroupByRef(item.Ref); exists {
			continue
		}
		maxOrder := uint(0)
		for _, t := range system.Talkgroups.List {
			if t.Order > maxOrder {
				maxOrder = t.Order
			}
		}
		tg := &Talkgroup{TalkgroupRef: item.Ref, Order: maxOrder + 1, AlertsEnabled: system.AutoPopulateAlertsEnabled}
		if err := ctrl.applyRRFields(tg, item.Proposed, item.Changes, true); err != nil {
			return fail(err)
		}
		system.Talkgroups.List = append(system.Talkgroups.List, tg)
		// Added from a main system's review, so it belongs to that main system's network.
		if linked.IsMain && len(system.ParentIds) > 1 {
			if system.TalkgroupNetworks == nil {
				system.TalkgroupNetworks = map[uint]uint64{}
			}
			system.TalkgroupNetworks[item.Ref] = linked.Id
		}
		applied++
	}

	if applied == 0 {
		return review, 0, nil
	}
	ctrl.Systems.mutex.RLock()
	problems := ValidateSystemRouting(ctrl.Systems.List)
	ctrl.Systems.mutex.RUnlock()
	if len(problems) > 0 {
		return fail(fmt.Errorf("%s", strings.Join(problems, " ")))
	}
	if err := ctrl.Systems.Write(ctrl.Database); err != nil {
		return fail(err)
	}
	if err := ctrl.Systems.Read(ctrl.Database); err != nil {
		return nil, 0, err
	}
	go ctrl.EmitConfig()
	ctrl.SyncConfigToFile()

	if linked, ok = ctrl.Systems.GetSystemById(body.SystemId); !ok {
		return nil, applied, nil
	}
	checkedAt := review.CheckedAt
	review = ctrl.buildRRReview(linked, rrList)
	review.CheckedAt = checkedAt
	if err := ctrl.saveRRReview(review, rrList); err != nil {
		return nil, applied, err
	}
	return review, applied, nil
}

// ── HTTP ────────────────────────────────────────────────────────────────────

type rrSummary struct {
	SystemId     uint64 `json:"systemId"`
	SystemLabel  string `json:"systemLabel"`
	IsMain       bool   `json:"isMain"`
	RRSystemId   int    `json:"rrSystemId"`
	RRSystemName string `json:"rrSystemName"`
	CheckedAt    int64  `json:"checkedAt"`
	Running      bool   `json:"running"`
	Error        string `json:"error,omitempty"`
	Fill         int    `json:"fill"`
	Differ       int    `json:"differ"`
	Missing      int    `json:"missing"`
	NotInRR      int    `json:"notInRR"`
}

// RadioReferenceUpdatesHandler serves:
//
//	GET  /api/admin/rr-updates                    one summary per linked system
//	GET  /api/admin/rr-updates/review?systemId=N  the full review for a system
//	POST /api/admin/rr-updates/check  {systemId}  check now (runs in the background)
//	POST /api/admin/rr-updates/apply  {systemId, fill, differ, missing}
func (admin *Admin) RadioReferenceUpdatesHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.ValidateToken(admin.GetAuthorization(r)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	ctrl := admin.Controller
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/rr-updates"), "/")
	writeErr := func(status int, msg string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}

	switch {
	case sub == "" && r.Method == http.MethodGet:
		summaries := []rrSummary{}
		ctrl.Systems.mutex.RLock()
		var linked []*System
		for _, s := range ctrl.Systems.List {
			if s.RadioReference.SystemId > 0 {
				linked = append(linked, s)
			}
		}
		ctrl.Systems.mutex.RUnlock()
		for _, s := range linked {
			sum := rrSummary{SystemId: s.Id, SystemLabel: s.Label, IsMain: s.IsMain, RRSystemId: s.RadioReference.SystemId, RRSystemName: s.RadioReference.SystemName, Running: rrUpdates.isRunning(s.Id)}
			if review, _, err := ctrl.loadRRReview(s.Id); err == nil && review != nil && review.RRSystemId == s.RadioReference.SystemId {
				sum.CheckedAt, sum.Error = review.CheckedAt, review.Error
				sum.Fill, sum.Differ, sum.Missing, sum.NotInRR = len(review.Fill), len(review.Differ), len(review.Missing), len(review.NotInRR)
			}
			summaries = append(summaries, sum)
		}
		json.NewEncoder(w).Encode(map[string]any{"systems": summaries})

	case sub == "review" && r.Method == http.MethodGet:
		id, _ := strconv.ParseUint(r.URL.Query().Get("systemId"), 10, 64)
		review, _, err := ctrl.loadRRReview(id)
		if err != nil {
			writeErr(http.StatusInternalServerError, err.Error())
			return
		}
		system, ok := ctrl.Systems.GetSystemById(id)
		if review != nil && ok && review.RRSystemId != system.RadioReference.SystemId {
			review = nil
		}
		json.NewEncoder(w).Encode(map[string]any{"review": review, "running": rrUpdates.isRunning(id)})

	case sub == "check" && r.Method == http.MethodPost:
		var body struct {
			SystemId uint64 `json:"systemId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(http.StatusBadRequest, "invalid request body")
			return
		}
		system, ok := ctrl.Systems.GetSystemById(body.SystemId)
		if !ok || system.RadioReference.SystemId == 0 {
			writeErr(http.StatusBadRequest, "Pick a Radio Reference system for this system and save it first.")
			return
		}
		if !ctrl.Options.RadioReferenceEnabled {
			writeErr(http.StatusExpectationFailed, "Radio Reference is not enabled")
			return
		}
		go ctrl.runRRCheck(body.SystemId)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"running": true})

	case sub == "apply" && r.Method == http.MethodPost:
		var body rrApplyBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(http.StatusBadRequest, "invalid request body")
			return
		}
		review, applied, err := admin.applyRRReview(body)
		if err != nil {
			writeErr(http.StatusBadRequest, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"review": review, "applied": applied, "systems": ctrl.Systems.List})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
