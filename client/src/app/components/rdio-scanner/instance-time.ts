import { formatDate } from '@angular/common';
import { Inject, LOCALE_ID, Pipe, PipeTransform } from '@angular/core';

/**
 * Time zone of the instance being listened to (Options.timeZone from the server).
 * Empty means "use the browser's zone", which is the behavior before the setting existed.
 */
let instanceZone = '';

export function setInstanceTimeZone(zone: unknown): void {
    const z = typeof zone === 'string' ? zone.trim() : '';
    if (z && !isKnownZone(z)) {
        instanceZone = '';
        return;
    }
    instanceZone = z;
}

export function instanceTimeZone(): string {
    return instanceZone;
}

/** Intl options with the instance zone applied (undefined zone = browser zone). */
export function withInstanceZone(opts: Intl.DateTimeFormatOptions = {}): Intl.DateTimeFormatOptions {
    return instanceZone ? { ...opts, timeZone: instanceZone } : opts;
}

function isKnownZone(zone: string): boolean {
    try {
        new Intl.DateTimeFormat('en-US', { timeZone: zone });
        return true;
    } catch {
        return false;
    }
}

/** UTC offset of `zone` at `date`, as the "+HHMM" string Angular's date formatting accepts. */
export function zoneOffset(date: Date, zone: string): string {
    const parts = new Intl.DateTimeFormat('en-US', {
        timeZone: zone,
        hourCycle: 'h23',
        year: 'numeric',
        month: 'numeric',
        day: 'numeric',
        hour: 'numeric',
        minute: 'numeric',
        second: 'numeric',
    }).formatToParts(date);
    const v: { [type: string]: number } = {};
    for (const p of parts) {
        if (p.type !== 'literal') {
            v[p.type] = Number(p.value);
        }
    }
    const wallAsUtc = Date.UTC(v['year'], v['month'] - 1, v['day'], v['hour'], v['minute'], v['second']);
    const instant = Math.floor(date.getTime() / 1000) * 1000;
    const minutes = Math.round((wallAsUtc - instant) / 60000);
    const sign = minutes < 0 ? '-' : '+';
    const abs = Math.abs(minutes);
    return `${sign}${String(Math.floor(abs / 60)).padStart(2, '0')}${String(abs % 60).padStart(2, '0')}`;
}

/**
 * Like Angular's `date` pipe (same format strings) but in the instance's time zone.
 * Not pure so it follows the zone when the server config arrives or changes.
 */
@Pipe({ name: 'instanceDate', standalone: false, pure: false })
export class InstanceDatePipe implements PipeTransform {
    constructor(@Inject(LOCALE_ID) private locale: string) {}

    transform(value: Date | number | string | null | undefined, format = 'mediumDate'): string | null {
        if (value === null || value === undefined || value === '') {
            return null;
        }
        const date = value instanceof Date ? value : new Date(value);
        if (isNaN(date.getTime())) {
            return null;
        }
        const zone = instanceZone;
        return formatDate(date, format, this.locale, zone ? zoneOffset(date, zone) : undefined);
    }
}
