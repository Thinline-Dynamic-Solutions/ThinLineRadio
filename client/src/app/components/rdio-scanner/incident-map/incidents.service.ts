/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';
import { map, tap } from 'rxjs/operators';
import { IncidentRecord, MapBoundaryCollection } from '../mapping/mapping.types';

@Injectable()
export class IncidentsService {
    private baseUrl: string | null = null;
    private prefetched24h: { items: IncidentRecord[]; fetchedAt: number; pin?: string } | null = null;

    constructor(private http: HttpClient) {}

    setBaseUrl(url: string | null): void {
        this.baseUrl = url ? url.replace(/\/$/, '') : null;
    }

    private getFullUrl(path: string): string {
        return this.baseUrl ? `${this.baseUrl}${path}` : path;
    }

    /** Pre-warms the default 24h incident list in the background so opening the Map tab is instant. */
    prefetchDefault24h(pin?: string): void {
        if (this.prefetched24h && Date.now() - this.prefetched24h.fetchedAt < 12000 && this.prefetched24h.pin === pin) {
            return;
        }
        const now = Date.now();
        const since = now - 24 * 60 * 60 * 1000;
        this.getIncidents(2000, pin, since, now, null, null, true).subscribe({ error: () => {} });
    }

    consumePrefetched24h(pin?: string, maxAgeMs = 30000): IncidentRecord[] | null {
        if (!this.prefetched24h) {
            return null;
        }
        if (this.prefetched24h.pin !== pin || Date.now() - this.prefetched24h.fetchedAt > maxAgeMs) {
            return null;
        }
        return this.prefetched24h.items;
    }

    getIncidents(
        limit = 300,
        pin?: string,
        since?: number,
        until?: number,
        systemId?: number | null,
        talkgroupId?: number | null,
        isDefault24h = false,
    ): Observable<IncidentRecord[]> {
        const cappedLimit = Math.min(Math.max(limit, 1), 2000);
        let url = `${this.getFullUrl('/api/incidents')}?limit=${cappedLimit}`;
        if (since && since > 0) {
            url += `&since=${since}`;
        }
        if (until && until > 0) {
            url += `&until=${until}`;
        }
        if (systemId && systemId > 0) {
            url += `&systemId=${systemId}`;
        }
        if (talkgroupId && talkgroupId > 0) {
            url += `&talkgroupId=${talkgroupId}`;
        }
        if (pin) {
            url += `&pin=${encodeURIComponent(pin)}`;
        }
        const headers = pin ? new HttpHeaders().set('Authorization', `Bearer ${pin}`) : undefined;
        return this.http.get<{ incidents: IncidentRecord[] }>(url, { headers }).pipe(
            map(res => res?.incidents || []),
            tap((items) => {
                if (isDefault24h || (!systemId && !talkgroupId && cappedLimit >= 1500)) {
                    this.prefetched24h = { items, fetchedAt: Date.now(), pin };
                }
            }),
        );
    }

    /** System-admin correction of an incident pin (address, nature, position). */
    correctIncidentPin(
        callId: number,
        changes: { address?: string; nature?: string; lat?: number; lon?: number },
        pin?: string,
    ): Observable<unknown> {
        let url = `${this.getFullUrl('/api/incidents/pin/')}${callId}`;
        if (pin) {
            url += `?pin=${encodeURIComponent(pin)}`;
        }
        const headers = pin ? new HttpHeaders().set('Authorization', `Bearer ${pin}`) : undefined;
        return this.http.put(url, changes, { headers });
    }

    /** Geocode a typed address for Correct Pin. Several matches are possible. */
    searchPinAddress(
        callId: number,
        query: string,
        pin?: string,
    ): Observable<{ label: string; lat: number; lon: number }[]> {
        let url = `${this.getFullUrl('/api/incidents/pin/')}${callId}/search?q=${encodeURIComponent(query)}`;
        if (pin) {
            url += `&pin=${encodeURIComponent(pin)}`;
        }
        const headers = pin ? new HttpHeaders().set('Authorization', `Bearer ${pin}`) : undefined;
        return this.http.get<{ results: { label: string; lat: number; lon: number }[] }>(url, { headers }).pipe(
            map(res => res?.results || []),
        );
    }

    /** System-admin removal of an incident pin from the map. */
    removeIncidentPin(callId: number, pin?: string): Observable<unknown> {
        let url = `${this.getFullUrl('/api/incidents/pin/')}${callId}`;
        if (pin) {
            url += `?pin=${encodeURIComponent(pin)}`;
        }
        const headers = pin ? new HttpHeaders().set('Authorization', `Bearer ${pin}`) : undefined;
        return this.http.delete(url, { headers });
    }

    getMapBoundaries(
        west: number,
        south: number,
        east: number,
        north: number,
        pin?: string,
        layers?: string[],
    ): Observable<MapBoundaryCollection> {
        let url = `${this.getFullUrl('/api/map/boundaries')}?west=${west}&south=${south}&east=${east}&north=${north}`;
        if (layers?.length) {
            url += `&layers=${encodeURIComponent(layers.join(','))}`;
        }
        if (pin) {
            url += `&pin=${encodeURIComponent(pin)}`;
        }
        const headers = pin ? new HttpHeaders().set('Authorization', `Bearer ${pin}`) : undefined;
        return this.http.get<MapBoundaryCollection>(url, { headers });
    }
}
