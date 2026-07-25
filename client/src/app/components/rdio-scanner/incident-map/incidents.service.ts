/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { Observable } from 'rxjs';
import { map } from 'rxjs/operators';
import { IncidentRecord, MapBoundaryCollection } from '../mapping/mapping.types';

@Injectable()
export class IncidentsService {
    private baseUrl: string | null = null;

    constructor(private http: HttpClient) {}

    setBaseUrl(url: string | null): void {
        this.baseUrl = url ? url.replace(/\/$/, '') : null;
    }

    private getFullUrl(path: string): string {
        return this.baseUrl ? `${this.baseUrl}${path}` : path;
    }

    getIncidents(limit = 300, pin?: string, since?: number, until?: number): Observable<IncidentRecord[]> {
        const cappedLimit = Math.min(Math.max(limit, 1), 500);
        let url = `${this.getFullUrl('/api/incidents')}?limit=${cappedLimit}`;
        if (since && since > 0) {
            url += `&since=${since}`;
        }
        if (until && until > 0) {
            url += `&until=${until}`;
        }
        if (pin) {
            url += `&pin=${encodeURIComponent(pin)}`;
        }
        const headers = pin ? new HttpHeaders().set('Authorization', `Bearer ${pin}`) : undefined;
        return this.http.get<{ incidents: IncidentRecord[] }>(url, { headers }).pipe(
            map(res => res?.incidents || []),
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
