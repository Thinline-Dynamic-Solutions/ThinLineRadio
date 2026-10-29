// Live checks shown before Approve & Send. The server enforces the same rules
// in training_submit_rules.go and will refuse the collector upload.

const STREET_FULL: Record<string, string> = {
    AVE: 'avenue', BLVD: 'boulevard', RD: 'road', LN: 'lane', ST: 'street', DR: 'drive',
    RT: 'route', RTE: 'route',
    N: 'north', S: 'south', E: 'east', W: 'west',
    NE: 'northeast', NW: 'northwest', SE: 'southeast', SW: 'southwest',
};

const NOT_STREET_PREV: Record<string, boolean> = {
    TO: true, AT: true, IN: true, FROM: true, OF: true, BY: true, THE: true, AND: true,
    FOR: true, WITH: true, A: true, AN: true, IS: true, ON: true, MARY: true, LUKE: true, JAMES: true,
};

const HWY_PREV: Record<string, boolean> = {
    I: true, IH: true, US: true, SR: true, ROUTE: true, RT: true, RTE: true, HWY: true,
    HIGHWAY: true, HW: true, STATE: true, COUNTY: true, CR: true, FM: true, MILE: true,
};

const STREET_WORDS: Record<string, boolean> = {
    STREET: true, AVENUE: true, ROAD: true, DRIVE: true, LANE: true, BOULEVARD: true,
    ST: true, AVE: true, RD: true, BLVD: true, LN: true, DR: true,
};

const NUMBER_WORDS: Record<string, boolean> = {
    ZERO: true, ONE: true, TWO: true, THREE: true, FOUR: true, FIVE: true, SIX: true,
    SEVEN: true, EIGHT: true, NINE: true, TEN: true, ELEVEN: true, TWELVE: true,
    THIRTEEN: true, FOURTEEN: true, FIFTEEN: true, SIXTEEN: true, SEVENTEEN: true,
    EIGHTEEN: true, NINETEEN: true, TWENTY: true, THIRTY: true, FORTY: true, FIFTY: true,
    SIXTY: true, SEVENTY: true, EIGHTY: true, NINETY: true, HUNDRED: true, THOUSAND: true,
};

const NON_SPEECH_WORDS: Record<string, boolean> = {
    NO: true, NOT: true, VOICE: true, VOICES: true, AUDIO: true, SPEECH: true, WORD: true,
    WORDS: true, SPOKEN: true, TONE: true, TONES: true, ONLY: true, JUST: true, SILENCE: true,
    SILENT: true, STATIC: true, SOUND: true, SOUNDS: true, NONE: true, NOTHING: true,
    EMPTY: true, BEEP: true, BEEPS: true, OR: true, AND: true,
};

const NON_SPEECH_MARKERS: Record<string, boolean> = {
    VOICE: true, VOICES: true, AUDIO: true, SPEECH: true, WORD: true, WORDS: true,
    TONE: true, TONES: true, SILENCE: true, SILENT: true, STATIC: true, NONE: true,
    NOTHING: true, EMPTY: true, BEEP: true, BEEPS: true,
};

const TRAINING_MAX_SECONDS = 30;

export function trainingSubmitIssues(transcript: string, durationSeconds: number | null): string[] {
    const issues: string[] = [];
    if (durationSeconds != null && durationSeconds > TRAINING_MAX_SECONDS) {
        issues.push(
            `This clip is ${formatSeconds(durationSeconds)} seconds long. Only clips of 30 seconds or less can be sent for training.`,
        );
    }

    const normalized = normalizeTrainingCheck(transcript);
    if (!normalized) {
        return issues;
    }
    if (isNonSpeechLabel(normalized)) {
        issues.push(
            `This transcript is a label ("${normalized.toLowerCase()}"), not spoken words. Clips with no voice or only tones cannot be sent.`,
        );
    }
    const abbrevs = abbreviationParts(normalized);
    if (abbrevs.length) {
        issues.push(`Spell out abbreviations: ${abbrevs.join(', ')}.`);
    }
    const numbers = numberWordHits(normalized);
    if (numbers.length) {
        issues.push(`Write numbers as digits, not words: ${numbers.join(', ')}.`);
    }
    return issues;
}

function formatSeconds(seconds: number): string {
    const s = seconds.toFixed(1);
    return s.endsWith('.0') ? s.slice(0, -2) : s;
}

function normalizeTrainingCheck(s: string): string {
    const upper = s.toUpperCase().replace(/&/g, ' AND ');
    let out = '';
    for (let i = 0; i < upper.length; i++) {
        const r = upper[i];
        const isLetter = r >= 'A' && r <= 'Z';
        const isDigit = r >= '0' && r <= '9';
        if (isLetter || isDigit) {
            out += r;
            continue;
        }
        const prev = upper[i - 1];
        const next = upper[i + 1];
        if ((r === '-' || r === '–' || r === '—') && prev >= '0' && prev <= '9' && next >= '0' && next <= '9') {
            continue;
        }
        if (out && !out.endsWith(' ')) {
            out += ' ';
        }
    }
    return out.replace(/\s+/g, ' ').trim();
}

function isNonSpeechLabel(normalized: string): boolean {
    const fields = normalized.split(' ').filter(Boolean);
    if (!fields.length) {
        return false;
    }
    let marker = false;
    for (const w of fields) {
        if (!NON_SPEECH_WORDS[w]) {
            return false;
        }
        if (NON_SPEECH_MARKERS[w]) {
            marker = true;
        }
    }
    return marker;
}

function abbreviationParts(normalized: string): string[] {
    const hits: { pos: number; from: string; to: string }[] = [];

    for (const m of normalized.matchAll(/\b(AVE|BLVD|RD|LN)\b/g)) {
        const word = m[1];
        hits.push({ pos: m.index ?? 0, from: word.toLowerCase(), to: STREET_FULL[word] });
    }
    for (const m of normalized.matchAll(/\b(\d{1,6}\s+)((?:[A-Z']+\s+){1,3}?)(ST|DR)\b/g)) {
        const words = m[2].trim().split(/\s+/).filter(Boolean);
        const last = words[words.length - 1];
        if (!last || NOT_STREET_PREV[last]) {
            continue;
        }
        const suffix = m[3];
        hits.push({
            pos: (m.index ?? 0) + m[0].length - suffix.length,
            from: suffix.toLowerCase(),
            to: STREET_FULL[suffix],
        });
    }
    for (const m of normalized.matchAll(/\b(RTE|RT)\s+\d/g)) {
        hits.push({ pos: m.index ?? 0, from: m[1].toLowerCase(), to: 'route' });
    }
    for (const m of normalized.matchAll(/\b(?:STREET|AVENUE|ROAD|DRIVE|LANE|BOULEVARD|AVE|BLVD|RD|LN|ST|DR)\s+(NE|NW|SE|SW)\b/g)) {
        const word = m[1];
        hits.push({
            pos: (m.index ?? 0) + m[0].lastIndexOf(word),
            from: word.toLowerCase(),
            to: STREET_FULL[word],
        });
    }
    for (const m of normalized.matchAll(/(\d{1,6})\s+(NE|NW|SE|SW|N|E|W|S)\s+(\d+(?:ST|ND|RD|TH)|[A-Z]{3,})\b/g)) {
        const dir = m[2];
        const next = m[3];
        const numPos = m.index ?? 0;
        if (HWY_PREV[wordBefore(normalized, numPos)]) {
            continue;
        }
        if (STREET_WORDS[next]) {
            continue;
        }
        if (dir === 'S' && !/^\d+(?:ST|ND|RD|TH)$/.test(next)) {
            continue;
        }
        hits.push({
            pos: numPos + m[1].length + 1,
            from: dir.toLowerCase(),
            to: STREET_FULL[dir],
        });
    }

    hits.sort((a, b) => a.pos - b.pos);
    const seen: Record<string, boolean> = {};
    const parts: string[] = [];
    for (const h of hits) {
        if (!h.to || seen[h.from]) {
            continue;
        }
        seen[h.from] = true;
        parts.push(`${h.from} → ${h.to}`);
    }
    return parts;
}

function wordBefore(s: string, idx: number): string {
    const prev = s.slice(0, idx).trimEnd();
    if (!prev) {
        return '';
    }
    const i = prev.lastIndexOf(' ');
    return i >= 0 ? prev.slice(i + 1) : prev;
}

function numberWordHits(normalized: string): string[] {
    const seen: Record<string, boolean> = {};
    const words: string[] = [];
    for (const tok of normalized.split(' ')) {
        if (!NUMBER_WORDS[tok]) {
            continue;
        }
        const low = tok.toLowerCase();
        if (seen[low]) {
            continue;
        }
        seen[low] = true;
        words.push(low);
    }
    return words;
}
