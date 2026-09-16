// Copyright (C) 2026 Thinline Dynamic Solutions
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
	"regexp"
	"strings"
	"unicode"
)

// Built-in blocked terms. Matching is whole-word only so dispatch language
// (assault, assignment, passenger, Hancock, Dickson) is not masked.
var defaultProfanityWords = []string{
	"ass", "asses", "asshole", "assholes", "dumbass", "dumbasses", "jackass", "jackasses",
	"asshat", "asshats", "asswipe", "asswipes", "assclown", "assclowns",
	"arse", "arses", "arsehole", "arseholes",
	"bastard", "bastards",
	"bitch", "bitches", "bitching", "bitchy",
	"bollocks",
	"cock", "cocks", "cocksucker", "cocksuckers",
	"cunt", "cunts",
	"dick", "dicks", "dickhead", "dickheads", "dickwad", "dickwads", "dickweed", "dickweeds",
	"douche", "douches", "douchebag", "douchebags",
	"fuck", "fucks", "fucked", "fucker", "fuckers", "fucking", "fuckin",
	"fuckhead", "fuckheads", "fuckface", "fuckfaces", "fuckwit", "fuckwits",
	"fuckboy", "fuckboys", "dumbfuck", "dumbfucks", "clusterfuck", "clusterfucks",
	"motherfucker", "motherfuckers", "motherfucking",
	"goddamn", "goddamned", "goddammit", "goddamnit",
	"jackoff", "jackoffs", "jerkoff", "jerkoffs",
	"blowjob", "blowjobs", "handjob", "handjobs",
	"dildo", "dildos",
	"piss", "pisses", "pissed", "pissing",
	"pussy", "pussies",
	"shit", "shits", "shitty", "shitting", "shite",
	"bullshit", "bullshitting", "dipshit", "dipshits", "shithead", "shitheads",
	"skank", "skanks",
	"slut", "sluts",
	"twat", "twats",
	"wank", "wanker", "wankers",
	"whore", "whores",
	"faggot", "faggots",
	"nigger", "niggers", "nigga", "niggas", "sandnigger", "sandniggers",
	"beaner", "beaners",
	"paki", "pakis",
	"raghead", "ragheads", "towelhead", "towelheads",
	"jigaboo", "jigaboos",
	"darkie", "darkies",
	"zipperhead", "zipperheads",
	"polack", "polacks",
	"dago", "dagos",
	"kike", "kikes", "heeb", "heebs", "yid", "yids",
	"spic", "spics",
	"wetback", "wetbacks",
	"chink", "chinks",
	"gook", "gooks",
	"honky", "honkies",
	"injun", "injuns",
	"redskin", "redskins",
	"retard", "retards", "retarded",
	"spaz", "spazzes", "spastic", "spastics",
	"mongoloid", "mongoloids",
	"tranny", "trannies", "shemale", "shemales",
}

var defaultProfanityRe *regexp.Regexp

func init() {
	defaultProfanityRe = compileProfanityWords(defaultProfanityWords)
}

func compileProfanityWords(words []string) *regexp.Regexp {
	escaped := make([]string, 0, len(words))
	seen := make(map[string]struct{}, len(words))
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		escaped = append(escaped, regexp.QuoteMeta(w))
	}
	if len(escaped) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(escaped, "|") + `)\b`)
}

// FilterProfanity masks blocked words in text. Extra words are matched the same
// way (whole-word, case-insensitive). Replacement keeps the first letter.
func FilterProfanity(text string, extra []string) string {
	if text == "" {
		return text
	}
	out := text
	if defaultProfanityRe != nil {
		out = defaultProfanityRe.ReplaceAllStringFunc(out, maskProfanityMatch)
	}
	if extraRe := compileProfanityWords(extra); extraRe != nil {
		out = extraRe.ReplaceAllStringFunc(out, maskProfanityMatch)
	}
	return out
}

func maskProfanityMatch(match string) string {
	runes := []rune(match)
	if len(runes) == 0 {
		return match
	}
	if len(runes) == 1 {
		if unicode.IsLetter(runes[0]) {
			return "*"
		}
		return match
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-1)
}

// IsProfanityFilterEnabled is on by default so existing servers that never
// saved the flag still mask transcripts and notifications.
func (cfg TranscriptionConfig) IsProfanityFilterEnabled() bool {
	if cfg.ProfanityFilterEnabled == nil {
		return true
	}
	return *cfg.ProfanityFilterEnabled
}

func (controller *Controller) applyTranscriptProfanityFilter(text string) string {
	if text == "" {
		return text
	}
	if controller == nil || controller.Options == nil {
		return FilterProfanity(text, nil)
	}
	cfg := controller.Options.TranscriptionConfig
	if !cfg.IsProfanityFilterEnabled() {
		return text
	}
	return FilterProfanity(text, cfg.ProfanityFilterExtraWords)
}
