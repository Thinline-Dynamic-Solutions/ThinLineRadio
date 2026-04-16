/*
 * *****************************************************************************
 * Copyright (C) 2026 Carter Carling <carter@cartercarling.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 * ****************************************************************************
 */

export interface TranscriptAnnotation {
    type: 'unit' | 'channel';
    text: string;
    /** Unicode code-point (rune) offset in the corrected transcript string (inclusive). Compatible with JS string character indices. */
    start: number;
    /** Unicode code-point (rune) offset in the corrected transcript string (exclusive). Compatible with JS string character indices. */
    end: number;
    prefix?: string;
    apparatus?: string;
    number?: string;
    dispatch?: string;
    separator?: string;
    channel?: string;
    fuzzy: boolean;
}

function escapeHtml(s: string): string {
    return s
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}

function applySearchHighlight(s: string, search: string): string {
    if (!search || !s) return s;
    const re = new RegExp(`(${search.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
    return s.replace(re, '<mark>$1</mark>');
}

/**
 * Renders a transcript string as HTML, wrapping recognized units and channels
 * in styled <span> elements using the byte offsets from transcriptAnnotations.
 * Optionally applies search-term highlighting to plain-text segments.
 *
 * Safe to call with no annotations — falls back to escaped plain text with
 * optional search highlighting (identical to the old behavior).
 */
export function renderAnnotatedTranscript(
    transcript: string,
    annotations?: TranscriptAnnotation[],
    searchText?: string,
): string {
    if (!transcript) return '';

    if (!annotations || annotations.length === 0) {
        return applySearchHighlight(escapeHtml(transcript), searchText || '');
    }

    let result = '';
    let lastEnd = 0;

    for (const ann of annotations) {
        // Plain text before this annotation — apply search highlight
        if (ann.start > lastEnd) {
            result += applySearchHighlight(escapeHtml(transcript.slice(lastEnd, ann.start)), searchText || '');
        }

        const cssClass = `transcript-annotation transcript-annotation--${ann.type}` +
            (ann.fuzzy ? ' transcript-annotation--fuzzy' : '');

        result += `<span class="${cssClass}">${escapeHtml(ann.text)}</span>`;

        lastEnd = ann.end;
    }

    // Remaining plain text after the last annotation
    if (lastEnd < transcript.length) {
        result += applySearchHighlight(escapeHtml(transcript.slice(lastEnd)), searchText || '');
    }

    return result;
}
