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

import { RdioScannerCall, RdioScannerUnit } from './rdio-scanner';

/** True when a configured unit row matches a call source / radio ID. */
export function unitMatchesSrc(unit: RdioScannerUnit, src: number): boolean {
    if (typeof unit.unitFrom === 'number' && typeof unit.unitTo === 'number') {
        if (unit.unitFrom > 0 && unit.unitTo > 0 && unit.unitFrom <= src && unit.unitTo >= src) {
            return true;
        }
    }

    const unitRef = unit.unitRef;
    if (typeof unitRef === 'number' && unitRef > 0 && unitRef === src) {
        return true;
    }

    // v6 configs: JSON "id" was the radio unitRef (see server unit.MarshalJSON / issue #172).
    if (!(typeof unitRef === 'number' && unitRef > 0) && unit.id === src) {
        return true;
    }

    return false;
}

export function findUnitLabelForSrc(units: RdioScannerUnit[] | undefined, src: number): string | undefined {
    if (!Array.isArray(units)) {
        return undefined;
    }
    const label = units.find((unit) => unitMatchesSrc(unit, src))?.label?.trim();
    return label && label.length > 0 ? label : undefined;
}

export function resolveUnitLabelForSrc(units: RdioScannerUnit[] | undefined, src: number): string {
    return findUnitLabelForSrc(units, src) ?? String(src);
}

/** Alias plus radio ID, e.g. `Engine 3 | 1234567`, or just the ID when unaliased. */
export function formatUnitDisplay(units: RdioScannerUnit[] | undefined, src: number, tag?: string): string {
    if (!(typeof src === 'number') || src <= 0) {
        const t = tag?.trim();
        return t && t.length > 0 ? t : '—';
    }
    const fromTag = tag?.trim();
    const alias = fromTag && fromTag !== String(src) ? fromTag : findUnitLabelForSrc(units, src);
    if (alias && alias !== String(src)) {
        return `${alias} | ${src}`;
    }
    return String(src);
}

export function formatCallUnitDisplay(call: RdioScannerCall | undefined | null): string {
    if (!call) {
        return '—';
    }
    if (Array.isArray(call.sources) && call.sources.length) {
        const ordered = [...call.sources].sort((a, b) => (a.pos || 0) - (b.pos || 0));
        const parts: string[] = [];
        const seen = new Set<number>();
        for (const s of ordered) {
            if (typeof s.src !== 'number' || s.src <= 0 || seen.has(s.src)) {
                continue;
            }
            seen.add(s.src);
            parts.push(formatUnitDisplay(call.systemData?.units, s.src, s.tag));
        }
        if (parts.length) {
            return parts.join(', ');
        }
        for (const s of ordered) {
            if (typeof s.tag === 'string' && s.tag.trim().length > 0) {
                return s.tag.trim();
            }
        }
    }
    if (typeof call.source === 'number' && call.source > 0) {
        return formatUnitDisplay(call.systemData?.units, call.source);
    }
    return '—';
}
