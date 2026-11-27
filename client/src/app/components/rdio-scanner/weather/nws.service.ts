/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Injectable, OnDestroy } from '@angular/core';
import {
    BehaviorSubject,
    Observable,
    Subject,
    Subscription,
    catchError,
    forkJoin,
    map,
    of,
    switchMap,
    tap,
    timer,
} from 'rxjs';
import { RdioScannerEvent } from '../rdio-scanner';
import { RdioScannerService } from '../rdio-scanner.service';
import { SettingsService } from '../settings/settings.service';

export interface NwsForecastPeriod {
    name: string;
    startTime: string;
    temperature: number | null;
    temperatureUnit: string;
    shortForecast: string;
    icon: string;
    isDaytime: boolean;
    windSpeed: string;
    probabilityOfPrecipitation: number | null;
}

export interface NwsWeatherBundle {
    zipCode: string;
    locationLabel: string;
    lat: number;
    lon: number;
    currentTemp: number | null;
    currentTempUnit: string;
    currentConditions: string;
    source: 'nws' | 'open-meteo';
    hourly: NwsForecastPeriod[];
    daily: NwsForecastPeriod[];
}

export interface NwsLayerToggles {
    radar: boolean;
    alerts: boolean;
}

export interface NwsSevereAlert {
    id: string;
    event: string;
    headline: string;
    severity: string;
    area: string;
}

const NWS_HEADERS = new HttpHeaders({
    Accept: 'application/geo+json',
});

const ZIP_PATTERN = /^\d{5}(-\d{4})?$/;
const MAX_LOCATION_LENGTH = 80;

// WMO weather interpretation codes used by Open-Meteo.
const WMO_CONDITIONS: Record<number, string> = {
    0: 'Clear', 1: 'Mostly Clear', 2: 'Partly Cloudy', 3: 'Overcast',
    45: 'Fog', 48: 'Freezing Fog',
    51: 'Light Drizzle', 53: 'Drizzle', 55: 'Heavy Drizzle', 56: 'Freezing Drizzle', 57: 'Freezing Drizzle',
    61: 'Light Rain', 63: 'Rain', 65: 'Heavy Rain', 66: 'Freezing Rain', 67: 'Freezing Rain',
    71: 'Light Snow', 73: 'Snow', 75: 'Heavy Snow', 77: 'Snow Grains',
    80: 'Rain Showers', 81: 'Rain Showers', 82: 'Heavy Showers', 85: 'Snow Showers', 86: 'Heavy Snow Showers',
    95: 'Thunderstorms', 96: 'Thunderstorms, Hail', 99: 'Thunderstorms, Hail',
};
const CACHE_TTL_MS = 10 * 60 * 1000;
const ALERTS_CACHE_TTL_MS = 3 * 60 * 1000;

interface ZipCoords {
    lat: number;
    lon: number;
    label: string;
    countryCode?: string;
}

@Injectable()
export class NwsService implements OnDestroy {
    private readonly zipStorageKey = 'tlr-weather-zip';
    private readonly layersStorageKey = 'tlr-weather-layers';
    private readonly weatherCachePrefix = 'tlr-weather-cache-';
    private readonly coordsCachePrefix = 'tlr-weather-coords-';

    private readonly weather$ = new BehaviorSubject<NwsWeatherBundle | null>(null);
    private readonly loading$ = new BehaviorSubject(false);
    private readonly error$ = new BehaviorSubject('');
    private readonly zipCode$ = new BehaviorSubject('');
    private readonly layers$ = new BehaviorSubject<NwsLayerToggles>({ radar: false, alerts: false });
    private readonly fetchZip$ = new Subject<string>();

    private refreshSub?: Subscription;
    private configSub?: Subscription;
    private fetchSub?: Subscription;
    private settingsLoaded = false;
    private settingsSnapshot: Record<string, unknown> = {};
    private alertsCache: { at: number; data: GeoJSON.FeatureCollection } | null = null;
    private severeAlertsCache: { at: number; key: string; data: NwsSevereAlert[] } | null = null;

    constructor(
        private http: HttpClient,
        private settingsService: SettingsService,
        private rdioScannerService: RdioScannerService,
    ) {
        this.fetchSub = this.fetchZip$.pipe(
            switchMap((zip) => this.fetchWeatherInternal(zip)),
        ).subscribe();

        this.loadStoredPrefs();
        this.configSub = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => {
            if (event.config?.userSettings) {
                this.applyServerSettings(event.config.userSettings, false);
            }
        });
        this.refreshSub = timer(10 * 60 * 1000, 10 * 60 * 1000).subscribe(() => {
            const zip = this.zipCode$.value;
            if (zip) {
                this.requestFetch(zip);
            }
        });
    }

    ngOnDestroy(): void {
        this.refreshSub?.unsubscribe();
        this.configSub?.unsubscribe();
        this.fetchSub?.unsubscribe();
    }

    getWeather(): Observable<NwsWeatherBundle | null> {
        return this.weather$.asObservable();
    }

    getLoading(): Observable<boolean> {
        return this.loading$.asObservable();
    }

    getError(): Observable<string> {
        return this.error$.asObservable();
    }

    getZipCode(): Observable<string> {
        return this.zipCode$.asObservable();
    }

    getLayers(): Observable<NwsLayerToggles> {
        return this.layers$.asObservable();
    }

    getLayersValue(): NwsLayerToggles {
        return this.layers$.value;
    }

    setZipCode(zip: string): void {
        const normalized = zip.trim().replace(/\s+/g, ' ');
        if (!normalized || normalized.length > MAX_LOCATION_LENGTH) {
            this.error$.next('Enter a US ZIP code or a city, for example "London, UK".');
            return;
        }
        this.zipCode$.next(normalized);
        this.persistZip(normalized);
        this.requestFetch(normalized);
    }

    setLayerToggle(layer: keyof NwsLayerToggles, enabled: boolean): void {
        const current = this.layers$.value;
        if (current[layer] === enabled) {
            return;
        }
        const next = { ...current, [layer]: enabled };
        this.layers$.next(next);
        this.persistLayers(next);
    }

    fetchSevereAlertsForUserArea(): Observable<NwsSevereAlert[]> {
        const bundle = this.weather$.value;
        const zip = this.zipCode$.value;
        // NWS alerts only cover the US.
        if (bundle?.source === 'open-meteo') {
            return of([]);
        }
        if (bundle?.lat != null && bundle?.lon != null) {
            return this.fetchSevereAlertsAtPoint(bundle.lat, bundle.lon);
        }
        if (!zip || !ZIP_PATTERN.test(zip)) {
            return of([]);
        }
        return this.resolveZip(zip).pipe(
            switchMap((coords) => {
                if (!coords) {
                    return of([]);
                }
                return this.fetchSevereAlertsAtPoint(coords.lat, coords.lon);
            }),
        );
    }

    fetchActiveAlerts(): Observable<GeoJSON.FeatureCollection> {
        const now = Date.now();
        if (this.alertsCache && now - this.alertsCache.at < ALERTS_CACHE_TTL_MS) {
            return of(this.alertsCache.data);
        }
        return this.http.get<GeoJSON.FeatureCollection>(
            'https://api.weather.gov/alerts/active?status=actual',
            { headers: NWS_HEADERS },
        ).pipe(
            tap((data) => {
                this.alertsCache = { at: now, data };
            }),
            catchError(() => of({ type: 'FeatureCollection', features: [] } as GeoJSON.FeatureCollection)),
        );
    }

    private fetchSevereAlertsAtPoint(lat: number, lon: number): Observable<NwsSevereAlert[]> {
        const key = `${lat},${lon}`;
        const now = Date.now();
        if (
            this.severeAlertsCache
            && this.severeAlertsCache.key === key
            && now - this.severeAlertsCache.at < ALERTS_CACHE_TTL_MS
        ) {
            return of(this.severeAlertsCache.data);
        }
        return this.http.get<GeoJSON.FeatureCollection>(
            `https://api.weather.gov/alerts/active?point=${lat},${lon}&status=actual`,
            { headers: NWS_HEADERS },
        ).pipe(
            map((data) => this.parseSevereAlerts(data)),
            tap((alerts) => {
                this.severeAlertsCache = { at: now, key, data: alerts };
            }),
            catchError(() => of([])),
        );
    }

    private parseSevereAlerts(fc: GeoJSON.FeatureCollection): NwsSevereAlert[] {
        const severe = new Set(['extreme', 'severe']);
        return (fc.features ?? [])
            .filter((feature) => {
                const sev = String(feature.properties?.['severity'] ?? '').toLowerCase();
                return severe.has(sev);
            })
            .map((feature) => ({
                id: String(feature.properties?.['id'] ?? feature.id ?? ''),
                event: String(feature.properties?.['event'] ?? 'Alert'),
                headline: String(feature.properties?.['headline'] ?? ''),
                severity: String(feature.properties?.['severity'] ?? ''),
                area: String(feature.properties?.['areaDesc'] ?? ''),
            }))
            .filter((alert) => !!alert.id);
    }

    private loadStoredPrefs(): void {
        let zip = '';
        try {
            zip = localStorage.getItem(this.zipStorageKey) || '';
            if (zip) {
                this.zipCode$.next(zip);
                this.hydrateWeatherCache(zip);
            }
            const layersRaw = localStorage.getItem(this.layersStorageKey);
            if (layersRaw) {
                const parsed = JSON.parse(layersRaw) as Partial<NwsLayerToggles>;
                this.layers$.next({
                    radar: !!parsed.radar,
                    alerts: !!parsed.alerts,
                });
            }
        } catch {
            // ignore
        }

        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                this.settingsLoaded = true;
                this.applyServerSettings(settings || {}, true);
                if (!this.zipCode$.value) {
                    this.seedZipFromAccount();
                } else if (zip) {
                    this.requestFetch(zip);
                }
            },
            error: () => {
                this.settingsLoaded = true;
                if (!zip) {
                    this.seedZipFromAccount();
                } else {
                    this.requestFetch(zip);
                }
            },
        });
    }

    private seedZipFromAccount(): void {
        const pin = this.rdioScannerService.readPin();
        if (!pin || this.zipCode$.value) {
            return;
        }
        this.http.get<{ zipCode?: string }>('/api/account', {
            params: { pin: encodeURIComponent(pin) },
        }).subscribe({
            next: (account) => {
                const accountZip = account?.zipCode?.trim() ?? '';
                if (accountZip && ZIP_PATTERN.test(accountZip) && !this.zipCode$.value) {
                    this.zipCode$.next(accountZip);
                    try {
                        localStorage.setItem(this.zipStorageKey, accountZip);
                    } catch {
                        // ignore
                    }
                    this.requestFetch(accountZip);
                }
            },
            error: () => { /* optional fallback */ },
        });
    }

    private applyServerSettings(settings: Record<string, unknown>, allowFetch: boolean): void {
        this.settingsSnapshot = { ...settings };
        const zip = typeof settings['weatherZipCode'] === 'string' ? settings['weatherZipCode'].trim() : '';
        if (zip && zip.length <= MAX_LOCATION_LENGTH && zip !== this.zipCode$.value) {
            this.zipCode$.next(zip);
            try {
                localStorage.setItem(this.zipStorageKey, zip);
            } catch {
                // ignore
            }
            if (allowFetch) {
                this.requestFetch(zip);
            }
        } else if (allowFetch && zip && this.weather$.value == null) {
            this.hydrateWeatherCache(zip);
            this.requestFetch(zip);
        }

        const layers = settings['weatherLayers'] as Partial<NwsLayerToggles> | undefined;
        if (layers && typeof layers === 'object') {
            const next: NwsLayerToggles = {
                radar: !!layers.radar,
                alerts: !!layers.alerts,
            };
            const current = this.layers$.value;
            if (current.radar !== next.radar || current.alerts !== next.alerts) {
                this.layers$.next(next);
                try {
                    localStorage.setItem(this.layersStorageKey, JSON.stringify(next));
                } catch {
                    // ignore
                }
            }
        }
    }

    private persistZip(zip: string): void {
        try {
            localStorage.setItem(this.zipStorageKey, zip);
        } catch {
            // ignore
        }
        if (!this.settingsLoaded) {
            return;
        }
        const payload = {
            ...this.settingsSnapshot,
            weatherZipCode: zip,
        };
        this.settingsService.saveSettings(payload).subscribe({
            next: () => {
                this.settingsSnapshot = payload;
            },
            error: () => { /* localStorage still holds the preference */ },
        });
    }

    private persistLayers(layers: NwsLayerToggles): void {
        try {
            localStorage.setItem(this.layersStorageKey, JSON.stringify(layers));
        } catch {
            // ignore
        }
        if (!this.settingsLoaded) {
            return;
        }
        const payload = {
            ...this.settingsSnapshot,
            weatherLayers: layers,
        };
        this.settingsService.saveSettings(payload).subscribe({
            next: () => {
                this.settingsSnapshot = payload;
            },
            error: () => { /* localStorage still holds the preference */ },
        });
    }

    private requestFetch(zip: string): void {
        this.fetchZip$.next(zip);
    }

    private fetchWeatherInternal(zip: string): Observable<NwsWeatherBundle | null> {
        this.hydrateWeatherCache(zip);
        const hadData = this.weather$.value != null;
        if (!hadData) {
            this.loading$.next(true);
        }
        this.error$.next('');

        if (!ZIP_PATTERN.test(zip)) {
            return this.resolvePlace(zip).pipe(
                switchMap((coords) => {
                    if (!coords) {
                        this.loading$.next(false);
                        this.error$.next('Could not find that place. Try "City, Country".');
                        return of(null);
                    }
                    return this.fetchOpenMeteo(zip, coords, coords.countryCode === 'US' ? 'F' : 'C');
                }),
                catchError(() => this.weatherUnavailable()),
            );
        }

        return this.resolveZip(zip).pipe(
            switchMap((coords) => {
                if (!coords) {
                    this.loading$.next(false);
                    this.error$.next('Could not locate that ZIP code.');
                    return of(null);
                }
                return this.http.get<any>(
                    `https://api.weather.gov/points/${coords.lat},${coords.lon}`,
                    { headers: NWS_HEADERS },
                ).pipe(
                    switchMap((points) => {
                        const props = points?.properties ?? {};
                        const forecastHourlyUrl = props.forecastHourly as string | undefined;
                        const forecastUrl = props.forecast as string | undefined;
                        const requests: Record<string, Observable<any>> = {};
                        if (forecastHourlyUrl) {
                            requests['hourly'] = this.http.get<any>(forecastHourlyUrl, { headers: NWS_HEADERS });
                        }
                        if (forecastUrl) {
                            requests['forecast'] = this.http.get<any>(forecastUrl, { headers: NWS_HEADERS });
                        }
                        if (!Object.keys(requests).length) {
                            return of({
                                coords,
                                hourly: [] as NwsForecastPeriod[],
                                daily: [] as NwsForecastPeriod[],
                            });
                        }
                        return forkJoin(requests).pipe(
                            map((results) => ({
                                coords,
                                hourly: this.parsePeriods(results['hourly']?.properties?.periods).slice(0, 5),
                                daily: this.pickDailyPeriods(this.parsePeriods(results['forecast']?.properties?.periods)),
                            })),
                        );
                    }),
                    map((data) => {
                        return this.publishWeather({
                            zipCode: zip,
                            locationLabel: coords.label,
                            lat: coords.lat,
                            lon: coords.lon,
                            currentTemp: data.hourly[0]?.temperature ?? null,
                            currentTempUnit: data.hourly[0]?.temperatureUnit || 'F',
                            currentConditions: data.hourly[0]?.shortForecast ?? '',
                            source: 'nws',
                            hourly: data.hourly,
                            daily: data.daily,
                        });
                    }),
                    catchError(() => this.fetchOpenMeteo(zip, coords, 'F')),
                );
            }),
            catchError(() => this.weatherUnavailable()),
        );
    }

    private publishWeather(bundle: NwsWeatherBundle): NwsWeatherBundle {
        this.weather$.next(bundle);
        this.writeWeatherCache(bundle.zipCode, bundle);
        this.loading$.next(false);
        return bundle;
    }

    private weatherUnavailable(): Observable<null> {
        this.loading$.next(false);
        if (!this.weather$.value) {
            this.error$.next('Weather data is temporarily unavailable.');
        }
        return of(null);
    }

    private resolvePlace(query: string): Observable<ZipCoords | null> {
        const key = `${this.coordsCachePrefix}place:${query.toLowerCase()}`;
        try {
            const cached = localStorage.getItem(key);
            if (cached) {
                const parsed = JSON.parse(cached) as ZipCoords;
                if (parsed?.lat != null && parsed?.lon != null) {
                    return of(parsed);
                }
            }
        } catch {
            // ignore
        }
        // The geocoder matches a place name, so "Paris, France" searches for
        // "Paris" and prefers results whose country or region matches the rest.
        const parts = query.split(',').map((p) => p.trim()).filter(Boolean);
        const name = parts[0] || query;
        const hints = parts.slice(1).map((p) => p.toLowerCase()).map((p) => (p === 'uk' ? 'gb' : p));
        return this.http.get<any>('https://geocoding-api.open-meteo.com/v1/search', {
            params: { name, count: hints.length ? 10 : 1, language: 'en', format: 'json' },
        }).pipe(
            map((body) => {
                const results: any[] = Array.isArray(body?.results) ? body.results : [];
                const matches = (r: any) => hints.every((h) => [r?.country, r?.country_code, r?.admin1, r?.admin2]
                    .some((v) => typeof v === 'string' && v.toLowerCase().startsWith(h)));
                const place = results.find(matches) ?? results[0];
                const lat = Number(place?.latitude);
                const lon = Number(place?.longitude);
                if (!place || !Number.isFinite(lat) || !Number.isFinite(lon)) {
                    return null;
                }
                const label = [place.name, place.admin1, place.country]
                    .filter((v, i, all) => typeof v === 'string' && v && all.indexOf(v) === i)
                    .join(', ');
                const coords: ZipCoords = { lat, lon, label: label || query, countryCode: String(place.country_code || '') };
                try {
                    localStorage.setItem(key, JSON.stringify(coords));
                } catch {
                    // ignore
                }
                return coords;
            }),
            catchError(() => of(null)),
        );
    }

    private fetchOpenMeteo(zip: string, coords: ZipCoords, unit: 'F' | 'C'): Observable<NwsWeatherBundle | null> {
        return this.http.get<any>('https://api.open-meteo.com/v1/forecast', {
            params: {
                latitude: coords.lat,
                longitude: coords.lon,
                current: 'temperature_2m,weather_code',
                hourly: 'temperature_2m,precipitation_probability,weather_code',
                daily: 'temperature_2m_max,weather_code',
                temperature_unit: unit === 'F' ? 'fahrenheit' : 'celsius',
                timezone: 'auto',
                forecast_days: 3,
            },
        }).pipe(
            map((body) => {
                const conditions = (code: unknown) => WMO_CONDITIONS[Number(code)] ?? '';
                const num = (v: unknown) => (typeof v === 'number' ? v : null);
                // Times are local to the place, without an offset.
                const nowHour = String(body?.current?.time ?? '').slice(0, 13);
                const h = body?.hourly ?? {};
                const times: string[] = Array.isArray(h.time) ? h.time : [];
                let start = times.findIndex((t) => t.slice(0, 13) >= nowHour);
                if (start < 0) {
                    start = 0;
                }
                const hourly: NwsForecastPeriod[] = times.slice(start, start + 5).map((t, i) => ({
                    name: '',
                    startTime: t,
                    temperature: num(h.temperature_2m?.[start + i]),
                    temperatureUnit: unit,
                    shortForecast: conditions(h.weather_code?.[start + i]),
                    icon: '',
                    isDaytime: true,
                    windSpeed: '',
                    probabilityOfPrecipitation: num(h.precipitation_probability?.[start + i]),
                }));
                const d = body?.daily ?? {};
                const days: string[] = Array.isArray(d.time) ? d.time : [];
                const daily: NwsForecastPeriod[] = days.slice(0, 3).map((day, i) => ({
                    name: '',
                    startTime: `${day}T12:00`,
                    temperature: num(d.temperature_2m_max?.[i]),
                    temperatureUnit: unit,
                    shortForecast: conditions(d.weather_code?.[i]),
                    icon: '',
                    isDaytime: true,
                    windSpeed: '',
                    probabilityOfPrecipitation: null,
                }));
                return this.publishWeather({
                    zipCode: zip,
                    locationLabel: coords.label,
                    lat: coords.lat,
                    lon: coords.lon,
                    currentTemp: num(body?.current?.temperature_2m),
                    currentTempUnit: unit,
                    currentConditions: conditions(body?.current?.weather_code),
                    source: 'open-meteo',
                    hourly,
                    daily,
                });
            }),
            catchError(() => this.weatherUnavailable()),
        );
    }

    private hydrateWeatherCache(zip: string): void {
        try {
            const raw = localStorage.getItem(`${this.weatherCachePrefix}${zip}`);
            if (!raw) {
                return;
            }
            const parsed = JSON.parse(raw) as { at: number; bundle: NwsWeatherBundle };
            if (!parsed?.bundle?.source || Date.now() - parsed.at > CACHE_TTL_MS) {
                return;
            }
            this.weather$.next(parsed.bundle);
        } catch {
            // ignore
        }
    }

    private writeWeatherCache(zip: string, bundle: NwsWeatherBundle): void {
        try {
            localStorage.setItem(`${this.weatherCachePrefix}${zip}`, JSON.stringify({
                at: Date.now(),
                bundle,
            }));
        } catch {
            // ignore
        }
    }

    private resolveZip(zip: string): Observable<ZipCoords | null> {
        const baseZip = zip.slice(0, 5);
        try {
            const cached = localStorage.getItem(`${this.coordsCachePrefix}${baseZip}`);
            if (cached) {
                const parsed = JSON.parse(cached) as ZipCoords;
                if (parsed?.lat != null && parsed?.lon != null) {
                    return of(parsed);
                }
            }
        } catch {
            // ignore
        }
        return this.http.get<any>(`https://api.zippopotam.us/us/${baseZip}`).pipe(
            map((body) => {
                const place = body?.places?.[0];
                if (!place) {
                    return null;
                }
                const lat = Number(place.latitude);
                const lon = Number(place.longitude);
                if (!Number.isFinite(lat) || !Number.isFinite(lon)) {
                    return null;
                }
                const city = String(place['place name'] || '').trim();
                const state = String(place['state abbreviation'] || '').trim();
                const label = city && state ? `${city}, ${state}` : zip;
                const coords = { lat, lon, label };
                try {
                    localStorage.setItem(`${this.coordsCachePrefix}${baseZip}`, JSON.stringify(coords));
                } catch {
                    // ignore
                }
                return coords;
            }),
            catchError(() => of(null)),
        );
    }

    private parsePeriods(periods: unknown): NwsForecastPeriod[] {
        if (!Array.isArray(periods)) {
            return [];
        }
        return periods.map((p) => ({
            name: String(p?.name ?? ''),
            startTime: String(p?.startTime ?? ''),
            temperature: typeof p?.temperature === 'number' ? p.temperature : null,
            temperatureUnit: String(p?.temperatureUnit ?? 'F'),
            shortForecast: String(p?.shortForecast ?? ''),
            icon: String(p?.icon ?? ''),
            isDaytime: !!p?.isDaytime,
            windSpeed: String(p?.windSpeed ?? ''),
            probabilityOfPrecipitation: typeof p?.probabilityOfPrecipitation?.value === 'number'
                ? p.probabilityOfPrecipitation.value
                : null,
        }));
    }

    private pickDailyPeriods(periods: NwsForecastPeriod[]): NwsForecastPeriod[] {
        const days: NwsForecastPeriod[] = [];
        const seen = new Set<string>();
        for (const period of periods) {
            if (!period.startTime) {
                continue;
            }
            const dayKey = period.startTime.slice(0, 10);
            if (seen.has(dayKey)) {
                continue;
            }
            seen.add(dayKey);
            days.push(period);
            if (days.length >= 3) {
                break;
            }
        }
        return days;
    }
}
