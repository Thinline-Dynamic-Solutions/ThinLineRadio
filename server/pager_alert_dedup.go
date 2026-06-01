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
	"fmt"
	"sync"
)

// PagerAlertDedup tracks pager-style push (CallKit / Telecom) already sent per
// user+callId. A callId only triggers alerts once per user, so no TTL is needed.
type PagerAlertDedup struct {
	sent  map[string]struct{} // key: "userId:callId"
	mutex sync.Mutex
}

func NewPagerAlertDedup() *PagerAlertDedup {
	return &PagerAlertDedup{
		sent: make(map[string]struct{}),
	}
}

// TryClaim reports whether this user+call may include pager_alert in a push.
// The first caller wins; later alerts for the same call still send regular pushes.
func (p *PagerAlertDedup) TryClaim(userId, callId uint64) bool {
	key := fmt.Sprintf("%d:%d", userId, callId)

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if _, ok := p.sent[key]; ok {
		return false
	}
	p.sent[key] = struct{}{}
	return true
}
