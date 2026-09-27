/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
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

import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { BehaviorSubject, Observable, of } from 'rxjs';
import { tap } from 'rxjs/operators';
import { RdioScannerService } from '../rdio-scanner.service';

@Injectable()
export class SettingsService {
    private readonly apiUrl = '/api/settings';
    private readonly settings$ = new BehaviorSubject<Record<string, unknown>>({});

    constructor(
        private http: HttpClient,
        private rdioScannerService: RdioScannerService,
    ) {
    }

    watchSettings(): Observable<Record<string, unknown>> {
        return this.settings$.asObservable();
    }

    getSettingsValue(): Record<string, unknown> {
        return this.settings$.value;
    }

    private getAuthHeaders(): HttpHeaders {
        const pin = this.getPin();
        const headers = new HttpHeaders();
        if (pin) {
            return headers.set('Authorization', `Bearer ${pin}`);
        }
        return headers;
    }

    private getPin(): string | undefined {
        try {
            const pin = window?.localStorage?.getItem('rdio-scanner-pin');
            return pin ? window.atob(pin) : undefined;
        } catch {
            return undefined;
        }
    }

    getSettings(): Observable<any> {
        const pin = this.getPin();
        const headers = this.getAuthHeaders();
        
        if (!pin) {
            this.settings$.next({});
            return of({});
        }

        // Include PIN as query parameter or in header
        return this.http.get<any>(`${this.apiUrl}?pin=${encodeURIComponent(pin)}`, { headers }).pipe(
            tap((settings) => this.settings$.next(settings || {})),
        );
    }

    saveSettings(settings: any): Observable<any> {
        const pin = this.getPin();
        const headers = this.getAuthHeaders();
        
        if (!pin) {
            return new Observable(observer => {
                observer.error(new Error('No PIN available. Please log in.'));
                observer.complete();
            });
        }

        // Include PIN as query parameter or in header
        return this.http.post<any>(`${this.apiUrl}?pin=${encodeURIComponent(pin)}`, settings, { headers }).pipe(
            tap(() => this.settings$.next(settings || {})),
        );
    }

    // Check if auto livefeed is enabled
    shouldAutoStartLivefeed(): Observable<boolean> {
        return new Observable(observer => {
            // Check if running as PWA
            const isStandalone = window.matchMedia('(display-mode: standalone)').matches;
            const isIOSStandalone = (window.navigator as any).standalone === true;
            const isFullscreen = window.matchMedia('(display-mode: fullscreen)').matches;
            const isPWA = isStandalone || isIOSStandalone || isFullscreen;

            // Only check settings if running as PWA
            if (!isPWA) {
                observer.next(false);
                observer.complete();
                return;
            }

            // Get settings to check if auto livefeed is enabled
            this.getSettings().subscribe({
                next: (settings) => {
                    const autoLivefeed = settings?.autoLivefeed || false;
                    observer.next(autoLivefeed);
                    observer.complete();
                },
                error: () => {
                    observer.next(false);
                    observer.complete();
                }
            });
        });
    }
}

