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

// callWithPresentedTalkgroup returns a shallow copy of call attributed to
// talkgroupRef with patches cleared so the client plays it as a normal single
// talkgroup (no PATCH fan-in UI). Audio bytes are shared (read-only on emit).
func callWithPresentedTalkgroup(call *Call, talkgroupRef uint) *Call {
	if call == nil || call.System == nil || talkgroupRef == 0 {
		return nil
	}
	tg, ok := call.System.Talkgroups.GetTalkgroupByRef(talkgroupRef)
	if !ok || tg == nil {
		return nil
	}
	cp := *call
	cp.Talkgroup = tg
	cp.TalkgroupId = talkgroupRef
	cp.Patches = []uint{}
	return &cp
}

// presentCallForClient picks at most one talkgroup for this client from the
// call's primary ∪ patches that are on in their livefeed (prefer primary),
// checks ACL against that talkgroup, and returns a call payload to emit.
// Returns nil when the client should not receive the call.
func (controller *Controller) presentCallForClient(call *Call, livefeed *Livefeed, user *User, link *ListenLink) *Call {
	if controller == nil || call == nil || livefeed == nil {
		return nil
	}

	refs := livefeed.EnabledMatchingTalkgroupRefs(call)
	if len(refs) == 0 {
		return nil
	}

	restricted := controller.requiresUserAuth() || (user == nil && link != nil)

	allowed := func(c *Call) bool {
		if !restricted {
			return true
		}
		if user != nil {
			return controller.userHasAccess(user, c)
		}
		if controller.anonymousAllowed(link) {
			return controller.featuresForAnonymous(link).LiveFeed && controller.anonymousHasCallAccess(link, c)
		}
		return false
	}

	// No formal patches: keep existing primary-only payload (still ACL-check).
	if len(call.Patches) == 0 {
		if !allowed(call) {
			return nil
		}
		return call
	}

	for _, ref := range refs {
		presented := callWithPresentedTalkgroup(call, ref)
		if presented == nil {
			continue
		}
		if !allowed(presented) {
			continue
		}
		return presented
	}
	return nil
}
