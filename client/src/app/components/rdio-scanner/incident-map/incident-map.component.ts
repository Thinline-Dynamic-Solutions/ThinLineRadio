/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import {
    ChangeDetectionStrategy,
    ChangeDetectorRef,
    Component,
    NgZone,
    OnDestroy,
    OnInit,
    AfterViewInit,
} from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Subscription, timer } from 'rxjs';
import * as L from 'leaflet';
import { IncidentRecord } from '../mapping/mapping.types';
import { RdioScannerService } from '../rdio-scanner.service';
import { TagColorService } from '../tag-color.service';
import { IncidentMapBridgeService } from './incident-map-bridge.service';
import { IncidentsService } from './incidents.service';
import { NwsLayerToggles, NwsService } from '../weather/nws.service';

export type IncidentMapStyle = 'voyager' | 'dark' | 'satellite';

export type IncidentTimePreset = '8h' | '16h' | '24h' | 'custom';

interface IncidentMapStyleOption {
    id: IncidentMapStyle;
    label: string;
}

interface IncidentFilterOption {
    id: number;
    label: string;
}

@Component({
    selector: 'rdio-scanner-incident-map',
    templateUrl: './incident-map.component.html',
    styleUrls: ['./incident-map.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RdioScannerIncidentMapComponent implements OnInit, OnDestroy, AfterViewInit {
    incidents: IncidentRecord[] = [];
    loading = true;
    loadError = '';
    mapReady = false;
    selectedIncident: IncidentRecord | null = null;
    focusingCallId: number | null = null;
    searchQuery = '';
    filterSystemId: number | null = null;
    filterTalkgroupId: number | null = null;
    mapStyle: IncidentMapStyle = 'voyager';
    weatherLayers: NwsLayerToggles = { radar: false, alerts: false };
    radarPlaying = true;
    radarFrameLabel = '';
    timePreset: IncidentTimePreset = '24h';
    customStartDate = '';
    customEndDate = '';
    /** Bumped when list data changes so docked sidebars re-render. */
    sidebarTick = 0;
    /** Stable deduped list for sidebar, markers, and click handlers. */
    private dedupedFilteredIncidents: IncidentRecord[] = [];

    /** System-admin pin correction state (panel over the map). */
    pinEdit: { incident: IncidentRecord; address: string; nature: string; lat: number; lon: number } | null = null;
    pinEditSaving = false;
    movePinArmed = false;
    private pinEditMarker: L.Marker | null = null;

    readonly mapStyleOptions: IncidentMapStyleOption[] = [
        { id: 'voyager', label: 'Voyager' },
        { id: 'dark', label: 'Dark' },
        { id: 'satellite', label: 'Satellite' },
    ];

    readonly timePresetOptions: { id: IncidentTimePreset; label: string }[] = [
        { id: '8h', label: '8h' },
        { id: '16h', label: '16h' },
        { id: '24h', label: '24h' },
        { id: 'custom', label: 'Custom' },
    ];

    private map: L.Map | null = null;
    private markers: L.Marker[] = [];
    private markerCache = new Map<number, { marker: L.Marker; signature: string }>();
    private markerLayerGroup: L.LayerGroup | null = null;
    private boundaryLayerGroup: L.LayerGroup | null = null;
    private readonly boundaryLayerStyles: Record<string, { fill: string; stroke: string; fillOpacity: number; weight: number; strokeOpacity: number }> = {
        county: { fill: '#5B9BD5', stroke: '#0F2744', fillOpacity: 0, weight: 1, strokeOpacity: 0.35 },
        cousub: { fill: '#7BC47F', stroke: '#3D7A3A', fillOpacity: 0.22, weight: 1.25, strokeOpacity: 0.70 },
        place: { fill: '#E8A55C', stroke: '#9A5E1E', fillOpacity: 0.20, weight: 1.25, strokeOpacity: 0.65 },
    };
    private readonly countyOutlineStyle: L.PathOptions = {
        fill: false,
        weight: 2.75,
        color: '#0F2744',
        opacity: 0.92,
        lineJoin: 'round',
        lineCap: 'round',
    };
    private boundaryLoadTimer: ReturnType<typeof setTimeout> | null = null;
    private activeTileLayer: L.TileLayer | null = null;
    private radarLayers: [L.TileLayer, L.TileLayer] | null = null;
    private radarActiveSlot = 0;
    private radarAdvancing = false;
    private radarMapMoving = false;
    private radarResumeAfterMove = false;
    private radarAnimTimer: ReturnType<typeof setInterval> | null = null;
    private radarFrameIndex = 0;
    private readonly radarFrameIds = [
        '900913-m50m',
        '900913-m45m',
        '900913-m40m',
        '900913-m35m',
        '900913-m30m',
        '900913-m25m',
        '900913-m20m',
        '900913-m15m',
        '900913-m10m',
        '900913-m05m',
        '900913',
    ];
    private readonly radarOpacity = 0.55;
    private readonly radarFrameDelayMs = 1400;
    private readonly radarPlayingStorageKey = 'tlr-radar-playing';
    private boundaryCanvasRenderer: L.Canvas | null = null;
    private boundaryRequestId = 0;
    private alertsRequestId = 0;
    private lastBoundaryViewportKey = '';
    private lastAlertsViewportKey = '';
    private readonly boundaryMinZoom = 10;
    private readonly boundaryShadedMinZoom = 10;
    private readonly boundaryPlaceMinZoom = 12;
    private readonly boundaryDebounceMs = 1000;
    private readonly boundaryPanQuantize = 0.4;
    private readonly alertsDebounceMs = 2000;
    private readonly markerRenderDebounceMs = 80;
    private readonly markerViewportPad = 0.12;
    private readonly initialFitMaxZoom = 12;
    private markerRenderTimer: ReturnType<typeof setTimeout> | null = null;
    private markersHiddenDuringMove = false;
    private alertsLayerGroup: L.LayerGroup | null = null;
    private alertsLoadTimer: ReturnType<typeof setTimeout> | null = null;
    private didFitBounds = false;
    private pollSub?: Subscription;
    private eventSub?: Subscription;
    private incidentRefreshTimer: ReturnType<typeof setTimeout> | null = null;
    private tagColorSub?: Subscription;
    private weatherLayersSub?: Subscription;
    private readonly mapStyleStorageKey = 'tlr-incident-map-style';
    private readonly timePresetStorageKey = 'tlr-incident-map-time-preset';
    private readonly startDateStorageKey = 'tlr-incident-map-start-date';
    private readonly endDateStorageKey = 'tlr-incident-map-end-date';
    private readonly incidentFocusZoom = 16;

    constructor(
        private incidentsService: IncidentsService,
        private rdioScannerService: RdioScannerService,
        private tagColorService: TagColorService,
        private snackBar: MatSnackBar,
        private cdr: ChangeDetectorRef,
        private ngZone: NgZone,
        private mapBridge: IncidentMapBridgeService,
        private nwsService: NwsService,
    ) {}

    ngOnInit(): void {
        this.mapStyle = this.loadStoredMapStyle();
        this.weatherLayers = { ...this.nwsService.getLayersValue() };
        this.radarPlaying = this.loadStoredRadarPlaying();
        this.loadStoredTimeFilter();
        this.mapBridge.register(this);
        this.weatherLayersSub = this.nwsService.getLayers().subscribe((layers) => {
            if (layers.radar === this.weatherLayers.radar && layers.alerts === this.weatherLayers.alerts) {
                return;
            }
            this.weatherLayers = { ...layers };
            this.applyWeatherLayers();
            this.cdr.markForCheck();
        });
        this.refreshIncidents();
        this.pollSub = timer(15000, 15000).subscribe(() => this.refreshIncidents());
        this.eventSub = this.rdioScannerService.event.subscribe((event) => {
            if (event.incident) {
                this.scheduleIncidentRefresh();
            } else if (event.alert) {
                // Mapping often finishes seconds after the alert; catch up if INC push is missed.
                this.scheduleIncidentRefresh(2500);
            }
        });
        this.tagColorSub = this.tagColorService.getTagColors().subscribe(() => {
            this.scheduleMarkerRender();
            this.cdr.markForCheck();
        });
    }

    ngAfterViewInit(): void {
        this.ngZone.runOutsideAngular(() => {
            this.initMap();
            this.scheduleMapResize();
        });
        this.mapReady = true;
        this.cdr.markForCheck();
    }

    ngOnDestroy(): void {
        this.mapBridge.unregister(this);
        this.pollSub?.unsubscribe();
        this.eventSub?.unsubscribe();
        if (this.incidentRefreshTimer) {
            clearTimeout(this.incidentRefreshTimer);
            this.incidentRefreshTimer = null;
        }
        this.tagColorSub?.unsubscribe();
        this.weatherLayersSub?.unsubscribe();
        if (this.alertsLoadTimer) {
            clearTimeout(this.alertsLoadTimer);
            this.alertsLoadTimer = null;
        }
        this.stopRadarAnimation();
        this.clearMarkers();
        this.clearWeatherLayers();
        this.clearBoundaryLayer();
        if (this.boundaryLoadTimer) {
            clearTimeout(this.boundaryLoadTimer);
            this.boundaryLoadTimer = null;
        }
        if (this.markerRenderTimer) {
            clearTimeout(this.markerRenderTimer);
            this.markerRenderTimer = null;
        }
        this.removePinEditMarker();
        if (this.map) {
            this.map.remove();
            this.map = null;
        }
    }

    invalidateMapSize(): void {
        this.scheduleMapResize();
        setTimeout(() => this.scheduleMarkerRender(), 120);
    }

    get filteredIncidents(): IncidentRecord[] {
        return this.dedupedFilteredIncidents;
    }

    /** Gates the map's pin correction/removal controls. */
    get isSystemAdmin(): boolean {
        return this.rdioScannerService.isSystemAdmin();
    }

    /** Raw filtered rows before location dedupe (for search across all channels). */
    private filteredIncidentsRaw(): IncidentRecord[] {
        return this.applyIncidentFilters(this.incidents);
    }

    private applyIncidentFilters(rows: IncidentRecord[]): IncidentRecord[] {
        let out = rows;
        if (this.filterSystemId != null) {
            out = out.filter((inc) => inc.systemId === this.filterSystemId);
        }
        if (this.filterTalkgroupId != null) {
            out = out.filter((inc) => inc.talkgroupId === this.filterTalkgroupId);
        }
        const q = this.searchQuery.trim().toLowerCase();
        if (q) {
            out = out.filter((inc) => this.incidentMatchesSearch(inc, q));
        }
        return out;
    }

    get systemFilterOptions(): IncidentFilterOption[] {
        const seen = new Map<number, string>();
        for (const inc of this.incidents) {
            if (!seen.has(inc.systemId)) {
                seen.set(inc.systemId, inc.systemLabel || `System ${inc.systemId}`);
            }
        }
        return Array.from(seen.entries())
            .map(([id, label]) => ({ id, label }))
            .sort((a, b) => a.label.localeCompare(b.label));
    }

    get talkgroupFilterOptions(): IncidentFilterOption[] {
        const seen = new Map<number, string>();
        for (const inc of this.incidents) {
            if (this.filterSystemId != null && inc.systemId !== this.filterSystemId) {
                continue;
            }
            if (!seen.has(inc.talkgroupId)) {
                seen.set(inc.talkgroupId, inc.talkgroupLabel || `TG ${inc.talkgroupId}`);
            }
        }
        return Array.from(seen.entries())
            .map(([id, label]) => ({ id, label }))
            .sort((a, b) => a.label.localeCompare(b.label));
    }

    get hasActiveFilters(): boolean {
        return this.filterSystemId != null ||
            this.filterTalkgroupId != null ||
            this.searchQuery.trim().length > 0;
    }

    get timeRangeLabel(): string {
        if (this.timePreset === 'custom' && this.customStartDate && this.customEndDate) {
            const start = this.parseDateInput(this.customStartDate);
            const end = this.parseDateInput(this.customEndDate);
            if (start && end) {
                const opts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' };
                const yearOpts: Intl.DateTimeFormatOptions = { ...opts, year: 'numeric' };
                if (start.getFullYear() === end.getFullYear()) {
                    return `${start.toLocaleDateString(undefined, opts)} – ${end.toLocaleDateString(undefined, yearOpts)}`;
                }
                return `${start.toLocaleDateString(undefined, yearOpts)} – ${end.toLocaleDateString(undefined, yearOpts)}`;
            }
        }
        switch (this.timePreset) {
            case '8h':
                return 'Last 8 hours';
            case '16h':
                return 'Last 16 hours';
            case 'custom':
                return 'Custom range';
            case '24h':
            default:
                return 'Last 24 hours';
        }
    }

    get maxDateInput(): string {
        return this.formatDateInput(new Date());
    }

    onTimePresetChange(preset: IncidentTimePreset): void {
        this.timePreset = preset;
        if (preset === 'custom' && (!this.customStartDate || !this.customEndDate)) {
            const end = new Date();
            const start = new Date();
            start.setDate(start.getDate() - 7);
            this.customEndDate = this.formatDateInput(end);
            this.customStartDate = this.formatDateInput(start);
        }
        this.didFitBounds = false;
        this.persistTimeFilter();
        this.refreshIncidents();
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    onCustomStartDateChange(value: string): void {
        this.customStartDate = value;
        if (value) {
            this.timePreset = 'custom';
            if (this.customEndDate && value > this.customEndDate) {
                this.customEndDate = value;
            }
        }
        this.persistTimeFilter();
        if (this.customStartDate && this.customEndDate) {
            this.didFitBounds = false;
            this.refreshIncidents();
        }
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    onCustomEndDateChange(value: string): void {
        this.customEndDate = value;
        if (value) {
            this.timePreset = 'custom';
            if (this.customStartDate && value < this.customStartDate) {
                this.customStartDate = value;
            }
        }
        this.persistTimeFilter();
        if (this.customStartDate && this.customEndDate) {
            this.didFitBounds = false;
            this.refreshIncidents();
        }
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    isTimePresetActive(preset: IncidentTimePreset): boolean {
        return this.timePreset === preset;
    }

    showCustomDateInputs(): boolean {
        return this.timePreset === 'custom';
    }

    onSearchChange(value: string): void {
        this.searchQuery = value;
        this.onFiltersChanged();
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    onSystemFilterChange(raw: string | number | null): void {
        this.filterSystemId = raw === '' || raw == null ? null : Number(raw);
        this.filterTalkgroupId = null;
        this.onFiltersChanged();
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    onTalkgroupFilterChange(raw: string | number | null): void {
        this.filterTalkgroupId = raw === '' || raw == null ? null : Number(raw);
        this.onFiltersChanged();
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    clearSearch(): void {
        this.searchQuery = '';
        this.onFiltersChanged();
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    clearAllFilters(): void {
        this.searchQuery = '';
        this.filterSystemId = null;
        this.filterTalkgroupId = null;
        this.onFiltersChanged();
        this.cdr.markForCheck();
        this.sidebarTick++;
    }

    /** Keep map/toolbar focus from scrolling the page and hiding the tab bar. */
    preventFocusScroll(_event: FocusEvent): void {
        const scrollX = window.scrollX;
        const scrollY = window.scrollY;
        requestAnimationFrame(() => window.scrollTo(scrollX, scrollY));
    }

    onWeatherLayerToggle(layer: keyof NwsLayerToggles): void {
        const next = !this.weatherLayers[layer];
        this.nwsService.setLayerToggle(layer, next);
    }

    toggleRadarPlayback(): void {
        this.radarPlaying = !this.radarPlaying;
        this.persistRadarPlaying();
        if (this.weatherLayers.radar) {
            if (this.radarPlaying) {
                this.startRadarAnimation();
            } else {
                this.stopRadarAnimation();
            }
        }
        this.cdr.markForCheck();
    }

    onMapStyleChange(style: IncidentMapStyle): void {
        this.mapStyle = style;
        try {
            localStorage.setItem(this.mapStyleStorageKey, style);
        } catch {
            // ignore storage errors
        }
        this.applyMapStyle();
        this.cdr.markForCheck();
    }

    playIncident(incident: IncidentRecord): void {
        if (!incident?.callId) {
            return;
        }
        this.rdioScannerService.loadAndPlay(incident.callId);
    }

    selectIncident(incident: IncidentRecord): void {
        const resolved = this.resolveIncident(incident.callId) ?? incident;
        this.selectedIncident = resolved;
        this.focusingCallId = resolved.callId;
        this.scrollIncidentIntoView(resolved.callId);
        this.cdr.markForCheck();
        this.sidebarTick++;
        this.ngZone.runOutsideAngular(() => {
            this.focusIncidentOnMap(resolved, () => this.openPopupForIncident(resolved));
        });
    }

    /** Stable key for sidebar list rendering. */
    trackIncidentCallId(_index: number, incident: IncidentRecord): number {
        return incident.callId;
    }

    formatTimestamp(ts: number): string {
        if (!ts) {
            return '';
        }
        return new Date(ts).toLocaleString();
    }

    incidentAddress(incident: IncidentRecord): string {
        if (incident.address) {
            return incident.address;
        }
        if (incident.commonName) {
            return incident.commonName;
        }
        return `Call ${incident.callId}`;
    }

    incidentChannel(incident: IncidentRecord): string {
        const system = incident.systemLabel || `System ${incident.systemId}`;
        const channel = incident.talkgroupLabel || `TG ${incident.talkgroupId}`;
        const primary = `${system} · ${channel}`;
        const extra = incident.relatedChannels?.length || 0;
        if (extra > 0) {
            return `${primary} (+${extra})`;
        }
        return primary;
    }

    /** All underlying calls merged into one map pin (PD + FD tones, etc.). */
    groupedIncidentCalls(incident: IncidentRecord): IncidentRecord[] {
        const ids = new Set<number>([incident.callId, ...(incident.relatedCallIds ?? [])]);
        return this.incidents
            .filter((row) => ids.has(row.callId))
            .sort((a, b) => a.timestamp - b.timestamp);
    }

    /** Number of underlying call records behind the deduped sidebar list. */
    get filteredIncidentRawCount(): number {
        return this.filteredIncidentsRaw().length;
    }

    getIncidentTagColor(incident: IncidentRecord): string {
        if (incident.tagColor?.trim()) {
            return incident.tagColor.trim();
        }
        if (incident.tagLabel?.trim()) {
            return this.tagColorService.getTagColor(incident.tagLabel);
        }
        const config = this.rdioScannerService.getConfig();
        if (!config?.systems?.length) {
            return '#ffffff';
        }
        const system = config.systems.find(
            (s) => s.systemId === incident.systemId || s.id === incident.systemId,
        );
        const talkgroup = system?.talkgroups?.find(
            (tg) => tg.talkgroupId === incident.talkgroupId,
        );
        if (talkgroup?.tag) {
            return this.tagColorService.getTagColor(talkgroup.tag);
        }
        if (talkgroup?.led) {
            return this.tagColorService.getTagColor(talkgroup.led);
        }
        return '#ffffff';
    }

    incidentListBackground(incident: IncidentRecord): string {
        const color = this.getIncidentTagColor(incident);
        const rgb = this.hexToRgb(color);
        if (!rgb) {
            return 'transparent';
        }
        return `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, 0.18)`;
    }

    incidentListSelectedShadow(incident: IncidentRecord): string {
        const color = this.getIncidentTagColor(incident);
        return `inset 3px 0 0 ${color}`;
    }

    incidentNatureTextColor(incident: IncidentRecord): string {
        return this.contrastTextColor(this.getIncidentTagColor(incident));
    }

    private onFiltersChanged(): void {
        this.rebuildFilteredIncidents();
        if (this.selectedIncident) {
            const selectedId = this.selectedIncident.callId;
            const stillVisible = this.filteredIncidents.some(
                (i) => i.callId === selectedId || i.relatedCallIds?.includes(selectedId),
            );
            if (!stillVisible) {
                this.selectedIncident = null;
                this.map?.closePopup();
            }
        }
        this.didFitBounds = false;
        this.scheduleMarkerRender();
    }

    private rebuildFilteredIncidents(): void {
        this.dedupedFilteredIncidents = this.dedupeIncidentsByLocation(
            this.applyIncidentFilters(this.incidents),
        );
    }

    /** Merge same-location dispatches (multi-TG tones) within a short time window. */
    private dedupeIncidentsByLocation(rows: IncidentRecord[]): IncidentRecord[] {
        const windowMs = 15 * 60 * 1000;
        const sorted = [...rows].sort((a, b) => b.timestamp - a.timestamp);
        const out: IncidentRecord[] = [];
        for (const inc of sorted) {
            const existing = this.findDedupeMatch(out, inc, windowMs);
            if (existing) {
                this.mergeIncidentDuplicate(existing, inc);
            } else {
                out.push({ ...inc });
            }
        }
        return out;
    }

    private findDedupeMatch(
        out: IncidentRecord[],
        inc: IncidentRecord,
        windowMs: number,
    ): IncidentRecord | undefined {
        const key = this.incidentLocationKey(inc);
        const addrKey = this.incidentAddressKey(inc);
        return out.find((e) => {
            if (e.systemId !== inc.systemId) {
                return false;
            }
            if (Math.abs(e.timestamp - inc.timestamp) > windowMs) {
                return false;
            }
            if (this.incidentLocationKey(e) === key) {
                return true;
            }
            return !!addrKey && this.incidentAddressKey(e) === addrKey;
        });
    }

    private incidentLocationKey(inc: IncidentRecord): string {
        if (this.incidentHasCoords(inc)) {
            const lat = Number(inc.lat);
            const lon = Number(inc.lon);
            return `${lat.toFixed(5)}:${lon.toFixed(5)}`;
        }
        const addrKey = this.incidentAddressKey(inc);
        if (addrKey) {
            return addrKey;
        }
        return `call:${inc.callId}`;
    }

    private incidentAddressKey(inc: IncidentRecord): string {
        const addr = (inc.address || inc.commonName || '').trim().toUpperCase().replace(/\s+/g, ' ');
        if (!addr) {
            return '';
        }
        return `addr:${inc.systemId}:${addr}`;
    }

    private incidentHasCoords(inc: IncidentRecord): boolean {
        const lat = Number(inc.lat);
        const lon = Number(inc.lon);
        return Number.isFinite(lat) && Number.isFinite(lon) && lat !== 0 && lon !== 0;
    }

    private resolveIncident(callId: number): IncidentRecord | null {
        const deduped = this.findDedupedIncident(callId);
        if (!deduped) {
            return null;
        }
        return this.ensureIncidentCoords(deduped);
    }

    private ensureIncidentCoords(inc: IncidentRecord): IncidentRecord {
        if (this.incidentHasCoords(inc)) {
            return inc;
        }
        const ids = [inc.callId, ...(inc.relatedCallIds ?? [])];
        for (const id of ids) {
            const raw = this.incidents.find((row) => row.callId === id);
            if (raw && this.incidentHasCoords(raw)) {
                return { ...inc, lat: raw.lat, lon: raw.lon };
            }
        }
        return inc;
    }

    private mergeIncidentDuplicate(primary: IncidentRecord, other: IncidentRecord): void {
        if (!this.incidentHasCoords(primary) && this.incidentHasCoords(other)) {
            primary.lat = other.lat;
            primary.lon = other.lon;
        }
        if (!primary.relatedCallIds) {
            primary.relatedCallIds = [];
        }
        if (!primary.relatedChannels) {
            primary.relatedChannels = [];
        }
        primary.relatedCallIds.push(other.callId);
        const channel = this.channelLabel(other);
        if (!primary.relatedChannels.includes(channel)) {
            primary.relatedChannels.push(channel);
        }
        const otherTx = other.transcript?.trim().length || 0;
        const primaryTx = primary.transcript?.trim().length || 0;
        if (otherTx > primaryTx) {
            primary.transcript = other.transcript;
        }
        // Each underlying call keeps its own nature; combined label via incidentNatureLabel().
        if (!primary.nature && other.nature) {
            primary.nature = other.nature;
        }
    }

    /** Unique nature labels across merged calls (e.g. CRASH / MUTUAL AID). */
    incidentNatureLabel(incident: IncidentRecord): string {
        const natures = [
            ...new Set(
                this.groupedIncidentCalls(incident)
                    .map((row) => (row.nature || '').trim())
                    .filter(Boolean),
            ),
        ];
        if (natures.length === 0) {
            return 'UNKNOWN PROBLEM';
        }
        return natures.join(' / ');
    }

    private channelLabel(incident: IncidentRecord): string {
        const system = incident.systemLabel || `System ${incident.systemId}`;
        const channel = incident.talkgroupLabel || `TG ${incident.talkgroupId}`;
        return `${system} · ${channel}`;
    }

    private findDedupedIncident(callId: number): IncidentRecord | null {
        for (const inc of this.filteredIncidents) {
            if (inc.callId === callId) {
                return inc;
            }
            if (inc.relatedCallIds?.includes(callId)) {
                return inc;
            }
        }
        return null;
    }

    private incidentMatchesSearch(incident: IncidentRecord, query: string): boolean {
        const haystack = [
            incident.address,
            incident.commonName,
            incident.nature,
            this.incidentNatureLabel(incident),
            incident.systemLabel,
            incident.talkgroupLabel,
            incident.tagLabel,
            incident.transcript,
            incident.crossStreet1,
            incident.crossStreet2,
            String(incident.callId),
            this.incidentChannel(incident),
        ]
            .filter(Boolean)
            .join(' ')
            .toLowerCase();
        return haystack.includes(query);
    }

    private loadStoredRadarPlaying(): boolean {
        try {
            const stored = localStorage.getItem(this.radarPlayingStorageKey);
            if (stored === '0' || stored === 'false') {
                return false;
            }
        } catch {
            // ignore
        }
        return true;
    }

    private persistRadarPlaying(): void {
        try {
            localStorage.setItem(this.radarPlayingStorageKey, this.radarPlaying ? '1' : '0');
        } catch {
            // ignore
        }
    }

    private loadStoredMapStyle(): IncidentMapStyle {
        try {
            const stored = localStorage.getItem(this.mapStyleStorageKey);
            if (stored === 'voyager' || stored === 'dark' || stored === 'satellite') {
                return stored;
            }
            // Migrate legacy style names.
            if (stored === 'light' || stored === 'roadmap' || stored === 'topo' || stored === 'terrain') {
                return 'voyager';
            }
        } catch {
            // ignore
        }
        return 'voyager';
    }

    private loadStoredTimeFilter(): void {
        try {
            const preset = localStorage.getItem(this.timePresetStorageKey);
            if (preset === '8h' || preset === '16h' || preset === '24h' || preset === 'custom') {
                this.timePreset = preset;
            }
            const start = localStorage.getItem(this.startDateStorageKey);
            const end = localStorage.getItem(this.endDateStorageKey);
            if (start) {
                this.customStartDate = start;
            }
            if (end) {
                this.customEndDate = end;
            }
            if (this.timePreset === 'custom' && (!this.customStartDate || !this.customEndDate)) {
                this.timePreset = '24h';
            }
        } catch {
            // ignore
        }
    }

    private persistTimeFilter(): void {
        try {
            localStorage.setItem(this.timePresetStorageKey, this.timePreset);
            if (this.customStartDate) {
                localStorage.setItem(this.startDateStorageKey, this.customStartDate);
            }
            if (this.customEndDate) {
                localStorage.setItem(this.endDateStorageKey, this.customEndDate);
            }
        } catch {
            // ignore
        }
    }

    private getQueryTimeRange(): { since: number; until: number; limit: number } {
        const now = Date.now();
        if (this.timePreset === 'custom' && this.customStartDate && this.customEndDate) {
            const start = this.parseDateInput(this.customStartDate);
            const end = this.parseDateInput(this.customEndDate);
            if (start && end) {
                start.setHours(0, 0, 0, 0);
                end.setHours(23, 59, 59, 999);
                return {
                    since: start.getTime(),
                    until: end.getTime(),
                    limit: 400,
                };
            }
        }
        const hours = this.timePreset === '8h' ? 8 : this.timePreset === '16h' ? 16 : 24;
        const limit = this.timePreset === '8h' ? 120 : this.timePreset === '16h' ? 200 : 250;
        return {
            since: now - hours * 60 * 60 * 1000,
            until: now,
            limit,
        };
    }

    private formatIncidentLoadError(err: unknown): string {
        const httpErr = err as { status?: number; error?: unknown; message?: string };
        if (typeof httpErr?.error === 'string' && httpErr.error.trim()) {
            const text = httpErr.error.trim();
            if (text.length < 240) {
                return text;
            }
            return text.slice(0, 240) + '…';
        }
        if (httpErr?.error && typeof httpErr.error === 'object') {
            const body = httpErr.error as Record<string, unknown>;
            const msg = body['error'] || body['details'] || body['message'];
            if (typeof msg === 'string' && msg.trim()) {
                return msg.trim();
            }
        }
        if (httpErr?.status === 500) {
            return 'Server error loading map incidents. Deploy the latest server build and ensure incident-mapping migrations have run.';
        }
        if (httpErr?.status === 401 || httpErr?.status === 403) {
            return 'Not authorized to load map incidents. Try signing out and back in.';
        }
        return 'Failed to load map incidents. Try a shorter time range.';
    }

    private parseDateInput(value: string): Date | null {
        if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
            return null;
        }
        const parsed = new Date(`${value}T12:00:00`);
        return Number.isNaN(parsed.getTime()) ? null : parsed;
    }

    private formatDateInput(date: Date): string {
        const y = date.getFullYear();
        const m = String(date.getMonth() + 1).padStart(2, '0');
        const d = String(date.getDate()).padStart(2, '0');
        return `${y}-${m}-${d}`;
    }

    private initMap(): void {
        const el = document.getElementById('tlr-incident-map-canvas');
        if (!el || this.map) {
            return;
        }
        L.DomEvent.disableScrollPropagation(el);
        L.DomEvent.disableClickPropagation(el);
        this.map = L.map(el, {
            center: [39.8283, -98.5795],
            zoom: 4,
            scrollWheelZoom: true,
            zoomAnimation: false,
            fadeAnimation: false,
            preferCanvas: false,
        });
        this.markerLayerGroup = L.layerGroup().addTo(this.map);
        this.boundaryCanvasRenderer = L.canvas({ padding: 0.5 });
        this.map.on('movestart', () => {
            this.radarMapMoving = true;
            this.markersHiddenDuringMove = true;
            this.setMarkerPaneVisible(false);
            this.boundaryRequestId++;
            if (this.boundaryLoadTimer) {
                clearTimeout(this.boundaryLoadTimer);
                this.boundaryLoadTimer = null;
            }
            if (this.alertsLoadTimer) {
                clearTimeout(this.alertsLoadTimer);
                this.alertsLoadTimer = null;
            }
            if (this.markerRenderTimer) {
                clearTimeout(this.markerRenderTimer);
                this.markerRenderTimer = null;
            }
            this.hideRadarDuringMove();
            if (this.radarAnimTimer) {
                this.radarResumeAfterMove = this.radarPlaying;
                this.stopRadarAnimation();
            }
        });
        this.map.on('zoomend', () => {
            this.scheduleBoundaryRefresh();
            this.scheduleMarkerRender();
        });
        this.map.on('click', (e: L.LeafletMouseEvent) => {
            if (!this.movePinArmed || !this.pinEdit) {
                return;
            }
            this.ngZone.run(() => {
                if (!this.pinEdit) {
                    return;
                }
                this.pinEdit.lat = Number(e.latlng.lat.toFixed(6));
                this.pinEdit.lon = Number(e.latlng.lng.toFixed(6));
                this.disarmMovePin();
                this.updatePinEditMarker();
                this.cdr.markForCheck();
            });
        });
        this.map.on('moveend', () => {
            this.radarMapMoving = false;
            this.markersHiddenDuringMove = false;
            this.setMarkerPaneVisible(true);
            this.restoreRadarAfterMove();
            if (this.weatherLayers.alerts) {
                this.scheduleAlertsRefresh();
            }
            if (this.radarResumeAfterMove && this.weatherLayers.radar && this.radarPlaying) {
                this.radarResumeAfterMove = false;
                setTimeout(() => this.startRadarAnimation(), 600);
            }
            this.scheduleBoundaryRefresh();
            this.scheduleMarkerRender();
        });
        this.applyMapStyle();
        this.applyWeatherLayers();
        this.scheduleMapResize();
    }

    private hideRadarDuringMove(): void {
        if (!this.radarLayers) {
            return;
        }
        for (const layer of this.radarLayers) {
            layer.setOpacity(0);
        }
    }

    private restoreRadarAfterMove(): void {
        if (!this.radarLayers || !this.weatherLayers.radar) {
            return;
        }
        const active = this.radarLayers[this.radarActiveSlot];
        active?.setOpacity(this.radarOpacity);
    }

    private scheduleMapResize(): void {
        setTimeout(() => this.map?.invalidateSize(), 0);
    }

    private applyMapStyle(): void {
        const map = this.map;
        if (!map) {
            return;
        }
        if (this.activeTileLayer) {
            map.removeLayer(this.activeTileLayer);
            this.activeTileLayer = null;
        }
        this.activeTileLayer = this.createTileLayer(this.mapStyle);
        this.activeTileLayer.addTo(map);
    }

    private createTileLayer(style: IncidentMapStyle): L.TileLayer {
        const perf = this.tilePerfOptions();
        const url = `/api/map/tiles/${style}/{z}/{x}/{y}.png`;
        const attribution = style === 'satellite'
            ? '&copy; Esri'
            : '&copy; OpenStreetMap &copy; CARTO';
        const maxZoom = style === 'satellite' ? 19 : 18;
        const maxNativeZoom = style === 'satellite' ? 19 : 18;
        return L.tileLayer(url, {
            ...perf,
            maxZoom,
            maxNativeZoom,
            attribution,
        });
    }

    private tilePerfOptions(): L.TileLayerOptions {
        return {
            maxZoom: 18,
            maxNativeZoom: 18,
            updateWhenZooming: false,
            updateWhenIdle: true,
            keepBuffer: 1,
            crossOrigin: true,
            updateInterval: 200,
        };
    }

    private setMarkerPaneVisible(visible: boolean): void {
        const pane = this.markerLayerGroup?.getPane();
        if (!pane) {
            return;
        }
        pane.classList.toggle('tlr-markers-hidden', !visible);
    }

    private scheduleMarkerRender(): void {
        if (!this.map || this.markersHiddenDuringMove) {
            return;
        }
        if (this.markerRenderTimer) {
            clearTimeout(this.markerRenderTimer);
        }
        this.markerRenderTimer = setTimeout(() => {
            this.markerRenderTimer = null;
            this.ngZone.runOutsideAngular(() => this.renderMarkers());
        }, this.markerRenderDebounceMs);
    }

    private getIncidentsInViewport(): IncidentRecord[] {
        const withCoords = this.filteredIncidents.filter((inc) => this.incidentHasCoords(inc));
        const map = this.map;
        if (!map || !this.didFitBounds) {
            return withCoords;
        }
        const bounds = map.getBounds().pad(this.markerViewportPad);
        return withCoords.filter((inc) => bounds.contains([Number(inc.lat), Number(inc.lon)]));
    }

    private incidentMarkerSignature(incident: IncidentRecord): string {
        const color = this.getIncidentTagColor(incident);
        return [
            incident.callId,
            incident.lat,
            incident.lon,
            color,
            incident.nature || '',
            this.incidentNatureLabel(incident),
            this.incidentAddress(incident),
        ].join('|');
    }

    private pruneMarkerCache(visibleIds: Set<number>, allIds: Set<number>): void {
        const group = this.markerLayerGroup;
        for (const [callId, entry] of this.markerCache) {
            if (!allIds.has(callId)) {
                group?.removeLayer(entry.marker);
                this.markerCache.delete(callId);
            } else if (!visibleIds.has(callId) && group?.hasLayer(entry.marker)) {
                group.removeLayer(entry.marker);
            }
        }
    }

    private radarTileOptions(): L.TileLayerOptions {
        return {
            ...this.tilePerfOptions(),
            maxZoom: 12,
            maxNativeZoom: 12,
            className: 'tlr-radar-frame',
            attribution: 'NOAA NEXRAD via Iowa Environmental Mesonet',
        };
    }

    private viewportCacheKey(map: L.Map): string {
        const bounds = map.getBounds();
        const zoom = map.getZoom();
        const q = (value: number): string => {
            const step = this.boundaryPanQuantize;
            return (Math.round(value / step) * step).toFixed(2);
        };
        return `${zoom}:${q(bounds.getWest())},${q(bounds.getSouth())},`
            + `${q(bounds.getEast())},${q(bounds.getNorth())}`;
    }

    /** Debounced refresh after websocket incident/alert signals. */
    private scheduleIncidentRefresh(delayMs = 350): void {
        if (this.incidentRefreshTimer) {
            clearTimeout(this.incidentRefreshTimer);
        }
        this.incidentRefreshTimer = setTimeout(() => {
            this.incidentRefreshTimer = null;
            this.refreshIncidents();
        }, delayMs);
    }

    private refreshIncidents(): void {
        const pin = this.rdioScannerService.readPin();
        const { since, until, limit } = this.getQueryTimeRange();
        this.incidentsService.getIncidents(limit, pin || undefined, since, until).subscribe({
            next: (items) => {
                this.incidents = items || [];
                this.loading = false;
                this.loadError = '';
                this.rebuildFilteredIncidents();
                if (this.selectedIncident) {
                    const selectedId = this.selectedIncident.callId;
                    this.selectedIncident = this.resolveIncident(selectedId);
                    if (!this.selectedIncident) {
                        // The incident aged out (e.g. call-nature force expiry)
                        // — its standalone popup is not bound to the marker, so
                        // close it or it floats over an empty spot.
                        this.map?.closePopup();
                    }
                }
                this.scheduleMarkerRender();
                this.cdr.markForCheck();
                this.sidebarTick++;
            },
            error: (err) => {
                this.loading = false;
                this.loadError = this.formatIncidentLoadError(err);
                this.cdr.markForCheck();
                this.sidebarTick++;
            },
        });
    }

    private renderMarkers(): void {
        const map = this.map;
        const group = this.markerLayerGroup;
        if (!map || !group) {
            return;
        }

        if (!this.didFitBounds) {
            const coords = this.filteredIncidents
                .filter((inc) => this.incidentHasCoords(inc))
                .map((inc) => [Number(inc.lat), Number(inc.lon)] as [number, number]);
            if (coords.length) {
                this.didFitBounds = true;
                map.fitBounds(L.latLngBounds(coords), {
                    maxZoom: this.initialFitMaxZoom,
                    padding: [30, 30],
                    animate: false,
                });
            }
        }

        const visible = this.getIncidentsInViewport();
        const visibleIds = new Set(visible.map((inc) => inc.callId));
        const allIds = new Set(
            this.filteredIncidents
                .filter((inc) => this.incidentHasCoords(inc))
                .map((inc) => inc.callId),
        );
        this.pruneMarkerCache(visibleIds, allIds);

        const attachMarker = (incident: IncidentRecord): void => {
            const latlng: L.LatLngExpression = [Number(incident.lat), Number(incident.lon)];
            const signature = this.incidentMarkerSignature(incident);
            let entry = this.markerCache.get(incident.callId);
            if (!entry || entry.signature !== signature) {
                if (entry) {
                    group.removeLayer(entry.marker);
                }
                const marker = this.createIncidentMarker(incident, latlng);
                marker.on('click', () => {
                    this.ngZone.run(() => {
                        this.selectIncident(incident);
                    });
                });
                entry = { marker, signature };
                this.markerCache.set(incident.callId, entry);
            } else {
                entry.marker.setLatLng(latlng);
            }
            if (!group.hasLayer(entry.marker)) {
                group.addLayer(entry.marker);
            }
        };

        if (!visible.length) {
            this.markers = [];
            return;
        }

        let index = 0;
        const chunkSize = 35;
        const renderChunk = (): void => {
            const end = Math.min(index + chunkSize, visible.length);
            for (; index < end; index++) {
                attachMarker(visible[index]);
            }
            this.markers = visible
                .map((inc) => this.markerCache.get(inc.callId)?.marker)
                .filter((marker): marker is L.Marker => !!marker);

            if (index < visible.length) {
                requestAnimationFrame(renderChunk);
            }
        };
        renderChunk();
    }

    private focusIncidentOnMap(incident: IncidentRecord, onComplete?: () => void): void {
        const map = this.map;
        if (!map || !this.incidentHasCoords(incident)) {
            onComplete?.();
            return;
        }
        map.invalidateSize();
        const lat = Number(incident.lat);
        const lon = Number(incident.lon);
        const targetZoom = Math.max(map.getZoom(), this.incidentFocusZoom);
        map.flyTo([lat, lon], targetZoom, { duration: 0.6 });
        map.once('moveend', () => {
            onComplete?.();
            this.ngZone.run(() => {
                this.focusingCallId = null;
                this.cdr.markForCheck();
            });
        });
    }

    private computeIncidentPinWidth(nature: string, address: string): number {
        const segments = [
            ...nature.replace(/\s+/g, ' ').trim().split('/').map((s) => s.trim()),
            ...address.replace(/\s+/g, ' ').trim().split(/\s+/),
        ].filter(Boolean);
        const longest = segments.reduce((max, s) => Math.max(max, s.length), 0);
        // ~5.5px per char at 10px bold + horizontal padding
        return Math.min(220, Math.max(118, Math.ceil(longest * 5.5) + 16));
    }

    private formatPinNatureHtml(nature: string): string {
        const cleaned = nature.replace(/\s+/g, ' ').trim();
        if (!cleaned) {
            return '';
        }
        if (!cleaned.includes('/')) {
            return this.escapeHtml(cleaned);
        }
        const parts = cleaned.split('/').map((part) => part.trim()).filter(Boolean);
        return parts
            .map((part, index) => {
                const text = this.escapeHtml(part);
                return index < parts.length - 1 ? `${text}/` : text;
            })
            .join('<br>');
    }

    private createIncidentMarker(
        incident: IncidentRecord,
        latlng: L.LatLngExpression,
    ): L.Marker {
        const color = this.getIncidentTagColor(incident);
        const textColor = this.contrastTextColor(color);
        const natureRaw = this.incidentNatureLabel(incident).replace(/\s+/g, ' ').trim();
        const addressRaw = this.incidentAddress(incident).replace(/\s+/g, ' ').trim();
        const natureHtml = this.formatPinNatureHtml(natureRaw);
        const addressHtml = this.escapeHtml(addressRaw);
        const natureTitle = this.escapeHtml(natureRaw);
        const addressTitle = this.escapeHtml(addressRaw);
        const w = this.computeIncidentPinWidth(natureRaw, addressRaw);
        const html = `
            <div class="tlr-incident-pin-wrap" style="width:${w}px;max-width:${w}px;">
                <div class="tlr-incident-pin-card" style="border-color:${color};">
                    <div class="tlr-incident-pin-nature"
                         style="background:${color};color:${textColor};"
                         title="${natureTitle}">${natureHtml}</div>
                    <div class="tlr-incident-pin-address" title="${addressTitle}">${addressHtml}</div>
                </div>
                <div class="tlr-incident-pin-tail" style="border-top-color:${color};" aria-hidden="true"></div>
            </div>`;
        const icon = L.divIcon({
            className: 'tlr-incident-pin-icon',
            html,
            iconSize: [w, 0],
            iconAnchor: [w / 2, 0],
        });
        return L.marker(latlng, { icon, zIndexOffset: 1000 });
    }

    private openPopupForIncident(incident: IncidentRecord): void {
        const map = this.map;
        if (!map || !this.incidentHasCoords(incident)) {
            return;
        }
        const lat = Number(incident.lat);
        const lon = Number(incident.lon);
        const dark = this.mapStyle === 'dark';
        const popupClass = dark ? 'tlr-incident-popup tlr-incident-popup--dark' : 'tlr-incident-popup';
        const address = this.incidentAddress(incident);
        const segments = this.groupedIncidentCalls(incident);
        const multi = segments.length > 1;

        const segmentHtml = segments.map((seg) => {
            const color = this.getIncidentTagColor(seg);
            const textColor = this.contrastTextColor(color);
            const channel = this.escapeHtml(this.channelLabel(seg));
            const nature = seg.nature
                ? `<span class="tlr-map-info__nature-badge" style="background:${color};color:${textColor};">${this.escapeHtml(seg.nature)}</span>`
                : '';
            const time = seg.timestamp
                ? `<div class="tlr-map-info__meta">${this.escapeHtml(this.formatTimestamp(seg.timestamp))}</div>`
                : '';
            const transcript = seg.transcript?.trim()
                ? `<div class="tlr-map-transcript">${this.escapeHtml(seg.transcript.trim())}</div>`
                : '';
            return `
                <div class="tlr-map-info__segment">
                    <div class="tlr-map-info__segment-head">
                        <strong>${channel}</strong>
                        ${nature}
                    </div>
                    ${time}
                    ${transcript}
                    <button type="button" class="tlr-map-info__play" id="tlr-map-play-${seg.callId}">Play Audio</button>
                </div>`;
        }).join('');

        const single = segments[0] ?? incident;
        const singleColor = this.getIncidentTagColor(single);
        const singleText = this.contrastTextColor(singleColor);
        const legacyNature = !multi && single.nature
            ? `<div class="tlr-map-info__nature"><span class="tlr-map-info__nature-badge" style="background:${singleColor};color:${singleText};">${this.escapeHtml(single.nature)}</span></div>`
            : '';
        const legacyChannel = !multi
            ? `<div class="tlr-map-info__meta"><strong>Channel:</strong> ${this.escapeHtml(this.incidentChannel(single))}</div>`
            : '';
        const legacyTime = !multi && single.timestamp
            ? `<div class="tlr-map-info__meta">${this.escapeHtml(this.formatTimestamp(single.timestamp))}</div>`
            : '';
        const legacyTranscript = !multi && single.transcript?.trim()
            ? `<div class="tlr-map-info__transcript-block">
                    <div class="tlr-map-info__transcript-label">Transcript</div>
                    <div class="tlr-map-transcript">${this.escapeHtml(single.transcript.trim())}</div>
               </div>`
            : '';
        const legacyPlay = !multi
            ? `<button type="button" class="tlr-map-info__play" id="tlr-map-play-${single.callId}">Play Audio</button>`
            : '';

        const adminActions = this.isSystemAdmin
            ? `<div class="tlr-map-info__admin">
                    <button type="button" class="tlr-map-info__admin-btn" id="tlr-map-edit-${incident.callId}">Correct Pin</button>
                    <button type="button" class="tlr-map-info__admin-btn tlr-map-info__admin-btn--danger" id="tlr-map-remove-${incident.callId}">Remove Pin</button>
               </div>`
            : '';

        const html = `
            <div class="tlr-map-info${multi ? ' tlr-map-info--multi' : ''}">
                <div class="tlr-map-info__address">${this.escapeHtml(address)}</div>
                ${multi ? `<div class="tlr-map-info__segments">${segmentHtml}</div>` : `${legacyNature}${legacyChannel}${legacyTime}${legacyTranscript}${legacyPlay}`}
                ${adminActions}
            </div>`;
        const popupMaxWidth = multi ? Math.min(920, segments.length * 252 + 48) : 360;
        L.popup({ maxWidth: popupMaxWidth, className: popupClass, autoPan: false })
            .setLatLng([lat, lon])
            .setContent(html)
            .openOn(map);
        window.setTimeout(() => {
            for (const seg of segments) {
                const btn = document.getElementById(`tlr-map-play-${seg.callId}`);
                btn?.addEventListener('click', () => {
                    this.ngZone.run(() => this.playIncident(seg));
                });
            }
            document.getElementById(`tlr-map-edit-${incident.callId}`)?.addEventListener('click', () => {
                this.ngZone.run(() => this.startPinEdit(incident));
            });
            document.getElementById(`tlr-map-remove-${incident.callId}`)?.addEventListener('click', () => {
                this.ngZone.run(() => this.removePin(incident));
            });
        }, 0);
    }

    /** Opens the admin correction panel seeded from the incident. */
    startPinEdit(incident: IncidentRecord): void {
        if (!this.isSystemAdmin) {
            return;
        }
        this.pinEdit = {
            incident,
            address: incident.address || '',
            nature: incident.nature || '',
            lat: Number(incident.lat) || 0,
            lon: Number(incident.lon) || 0,
        };
        this.movePinArmed = false;
        this.map?.closePopup();
        this.updatePinEditMarker();
        this.cdr.markForCheck();
    }

    toggleMovePin(): void {
        this.movePinArmed = !this.movePinArmed;
        const canvas = document.getElementById('tlr-incident-map-canvas');
        canvas?.classList.toggle('pin-move-armed', this.movePinArmed);
        this.cdr.markForCheck();
    }

    cancelPinEdit(): void {
        this.pinEdit = null;
        this.pinEditSaving = false;
        this.disarmMovePin();
        this.removePinEditMarker();
        this.cdr.markForCheck();
    }

    savePinEdit(): void {
        const edit = this.pinEdit;
        if (!edit || this.pinEditSaving) {
            return;
        }
        const changes: { address?: string; nature?: string; lat?: number; lon?: number } = {
            address: edit.address.trim(),
            nature: edit.nature.trim(),
        };
        if (edit.lat !== 0 || edit.lon !== 0) {
            changes.lat = edit.lat;
            changes.lon = edit.lon;
        }
        this.pinEditSaving = true;
        const pin = this.rdioScannerService.readPin();
        this.incidentsService.correctIncidentPin(edit.incident.callId, changes, pin || undefined).subscribe({
            next: () => {
                this.snackBar.open('Pin corrected', '', { duration: 2500 });
                this.cancelPinEdit();
                this.refreshIncidents();
            },
            error: (err) => {
                this.pinEditSaving = false;
                this.snackBar.open(this.pinAdminErrorMessage(err, 'Failed to correct pin'), '', { duration: 4000 });
                this.cdr.markForCheck();
            },
        });
    }

    /** Removes an incident's pin from the map after confirmation. */
    removePin(incident: IncidentRecord): void {
        if (!this.isSystemAdmin) {
            return;
        }
        const label = this.incidentAddress(incident);
        if (!confirm(`Remove the map pin for "${label}"? The call and audio are kept; only the map plot is cleared.`)) {
            return;
        }
        const pin = this.rdioScannerService.readPin();
        this.incidentsService.removeIncidentPin(incident.callId, pin || undefined).subscribe({
            next: () => {
                this.snackBar.open('Pin removed', '', { duration: 2500 });
                if (this.pinEdit?.incident.callId === incident.callId) {
                    this.cancelPinEdit();
                }
                this.map?.closePopup();
                this.refreshIncidents();
            },
            error: (err) => {
                this.snackBar.open(this.pinAdminErrorMessage(err, 'Failed to remove pin'), '', { duration: 4000 });
            },
        });
    }

    private pinAdminErrorMessage(err: unknown, fallback: string): string {
        const status = (err as { status?: number })?.status;
        if (status === 401 || status === 403) {
            return 'System admin sign-in required';
        }
        return fallback;
    }

    private disarmMovePin(): void {
        this.movePinArmed = false;
        document.getElementById('tlr-incident-map-canvas')?.classList.remove('pin-move-armed');
    }

    private updatePinEditMarker(): void {
        this.removePinEditMarker();
        const edit = this.pinEdit;
        const map = this.map;
        if (!edit || !map || (edit.lat === 0 && edit.lon === 0)) {
            return;
        }
        const icon = L.divIcon({
            className: 'tlr-pin-edit-marker',
            html: '<div class="tlr-pin-edit-marker__dot"></div>',
            iconSize: [18, 18],
            iconAnchor: [9, 9],
        });
        this.pinEditMarker = L.marker([edit.lat, edit.lon], { icon, zIndexOffset: 2000 }).addTo(map);
    }

    private removePinEditMarker(): void {
        if (this.pinEditMarker) {
            this.pinEditMarker.remove();
            this.pinEditMarker = null;
        }
    }

    private scrollIncidentIntoView(callId: number): void {
        requestAnimationFrame(() => {
            const el = document.querySelector(`[data-incident-call-id="${callId}"]`) as HTMLElement | null;
            const list = el?.closest('.sidebar-list') as HTMLElement | null;
            if (!el || !list) {
                return;
            }
            const listRect = list.getBoundingClientRect();
            const elRect = el.getBoundingClientRect();
            if (elRect.top < listRect.top) {
                list.scrollTop -= listRect.top - elRect.top;
            } else if (elRect.bottom > listRect.bottom) {
                list.scrollTop += elRect.bottom - listRect.bottom;
            }
        });
    }

    private clearMarkers(): void {
        this.markerLayerGroup?.clearLayers();
        this.markers = [];
        this.markerCache.clear();
    }

    private hexToRgb(hex: string): { r: number; g: number; b: number } | null {
        let normalized = hex.trim().replace('#', '');
        if (normalized.length === 3) {
            normalized = normalized.split('').map((c) => c + c).join('');
        }
        if (normalized.length !== 6) {
            return null;
        }
        const r = parseInt(normalized.slice(0, 2), 16);
        const g = parseInt(normalized.slice(2, 4), 16);
        const b = parseInt(normalized.slice(4, 6), 16);
        if ([r, g, b].some((v) => Number.isNaN(v))) {
            return null;
        }
        return { r, g, b };
    }

    private contrastTextColor(hex: string): string {
        const rgb = this.hexToRgb(hex);
        if (!rgb) {
            return '#ffffff';
        }
        const lum = (0.299 * rgb.r + 0.587 * rgb.g + 0.114 * rgb.b) / 255;
        return lum > 0.62 ? '#1a1a1a' : '#ffffff';
    }

    private escapeHtml(value: string): string {
        return value
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;');
    }

    private scheduleBoundaryRefresh(): void {
        if (this.boundaryLoadTimer) {
            clearTimeout(this.boundaryLoadTimer);
        }
        this.boundaryLoadTimer = setTimeout(() => this.refreshBoundaryLayer(), this.boundaryDebounceMs);
    }

    private refreshBoundaryLayer(): void {
        const map = this.map;
        if (!map) {
            return;
        }
        const zoom = map.getZoom();
        if (zoom < this.boundaryMinZoom) {
            this.lastBoundaryViewportKey = '';
            this.clearBoundaryLayer();
            return;
        }
        const viewportKey = this.viewportCacheKey(map);
        if (viewportKey === this.lastBoundaryViewportKey) {
            return;
        }
        this.lastBoundaryViewportKey = viewportKey;
        const requestId = ++this.boundaryRequestId;
        const bounds = map.getBounds();
        const pin = this.rdioScannerService.readPin();
        const requestLayers = this.boundaryLayersForZoom(zoom);
        this.incidentsService.getMapBoundaries(
            bounds.getWest(),
            bounds.getSouth(),
            bounds.getEast(),
            bounds.getNorth(),
            pin || undefined,
            requestLayers,
        ).subscribe({
            next: (collection) => {
                if (requestId !== this.boundaryRequestId) {
                    return;
                }
                this.ngZone.run(() => {
                    this.clearBoundaryLayer();
                    if (!collection?.enabled || !collection.features?.length) {
                        return;
                    }
                    const group = L.layerGroup();
                    const zoom = map.getZoom();
                    const geoJsonOpts = {
                        smoothFactor: 3,
                        renderer: this.boundaryCanvasRenderer ?? undefined,
                    } as L.GeoJSONOptions;
                    const layerTypes = this.boundaryShadedLayersForZoom(zoom);
                    for (const layerType of layerTypes) {
                        const features = collection.features.filter(
                            (f) => f?.properties?.layer === layerType,
                        );
                        if (!features.length) {
                            continue;
                        }
                        const layer = L.geoJSON(
                            { type: 'FeatureCollection', features } as any,
                            {
                                ...geoJsonOpts,
                                style: (feature) => this.boundaryStyle(feature),
                            },
                        );
                        group.addLayer(layer);
                        layer.bringToBack();
                    }
                    const countyFeatures = collection.features.filter(
                        (f) => f?.properties?.layer === 'county',
                    );
                    if (countyFeatures.length) {
                        const outlineLayer = L.geoJSON(
                            { type: 'FeatureCollection', features: countyFeatures } as any,
                            {
                                ...geoJsonOpts,
                                style: () => this.countyOutlineStyle,
                            },
                        );
                        group.addLayer(outlineLayer);
                    }
                    group.addTo(map);
                    this.boundaryLayerGroup = group;
                });
            },
            error: () => {
                this.ngZone.run(() => this.clearBoundaryLayer());
            },
        });
    }

    private boundaryLayersForZoom(zoom: number): string[] {
        const layers = ['county'];
        if (zoom >= this.boundaryShadedMinZoom) {
            layers.push('cousub');
        }
        if (zoom >= this.boundaryPlaceMinZoom) {
            layers.push('place');
        }
        return layers;
    }

    private boundaryShadedLayersForZoom(zoom: number): string[] {
        const layers: string[] = [];
        if (zoom >= this.boundaryShadedMinZoom) {
            layers.push('cousub');
        }
        if (zoom >= this.boundaryPlaceMinZoom) {
            layers.push('place');
        }
        return layers;
    }

    private boundaryStyle(feature?: { properties?: Record<string, unknown> }): L.PathOptions {
        const layerType = String(feature?.properties?.['layer'] ?? 'cousub');
        const palette = this.boundaryLayerStyles[layerType] ?? this.boundaryLayerStyles['cousub'];
        return {
            fillColor: palette.fill,
            fillOpacity: palette.fillOpacity,
            color: palette.stroke,
            weight: palette.weight,
            opacity: palette.strokeOpacity,
        };
    }

    private clearBoundaryLayer(): void {
        if (this.boundaryLayerGroup) {
            this.boundaryLayerGroup.remove();
            this.boundaryLayerGroup = null;
        }
    }

    private applyWeatherLayers(): void {
        const map = this.map;
        if (!map) {
            return;
        }
        if (this.weatherLayers.radar) {
            this.ensureRadarLayers(map);
            if (this.radarPlaying && !this.radarAnimTimer && !this.radarMapMoving) {
                this.startRadarAnimation();
            }
        } else {
            this.stopRadarAnimation();
            this.destroyRadarLayers(map);
            this.radarFrameLabel = '';
        }

        if (this.weatherLayers.alerts) {
            this.scheduleAlertsRefresh();
        } else {
            this.clearAlertsLayer();
        }
        this.cdr.markForCheck();
    }

    private ensureRadarLayers(map: L.Map): void {
        if (this.radarLayers) {
            return;
        }
        this.radarFrameIndex = this.radarFrameIds.length - 1;
        const url = this.radarTileUrl(this.radarFrameIds[this.radarFrameIndex]);
        const opts = this.radarTileOptions();
        const visible = L.tileLayer(url, { ...opts, opacity: this.radarOpacity });
        const hidden = L.tileLayer(url, { ...opts, opacity: 0 });
        visible.addTo(map);
        hidden.addTo(map);
        this.radarLayers = [visible, hidden];
        this.radarActiveSlot = 0;
        this.updateRadarFrameLabel();
    }

    private destroyRadarLayers(map: L.Map): void {
        if (!this.radarLayers) {
            return;
        }
        for (const layer of this.radarLayers) {
            if (map.hasLayer(layer)) {
                map.removeLayer(layer);
            }
        }
        this.radarLayers = null;
        this.radarActiveSlot = 0;
        this.radarAdvancing = false;
    }

    private advanceRadarFrame(): void {
        const layers = this.radarLayers;
        if (!layers || this.radarAdvancing || this.radarMapMoving) {
            return;
        }
        const nextIndex = (this.radarFrameIndex + 1) % this.radarFrameIds.length;
        const inactiveSlot = 1 - this.radarActiveSlot;
        const activeLayer = layers[this.radarActiveSlot];
        const inactiveLayer = layers[inactiveSlot];
        const nextUrl = this.radarTileUrl(this.radarFrameIds[nextIndex]);

        this.radarAdvancing = true;
        let settled = false;
        const complete = (): void => {
            if (settled) {
                return;
            }
            settled = true;
            inactiveLayer.setOpacity(this.radarOpacity);
            activeLayer.setOpacity(0);
            this.radarActiveSlot = inactiveSlot;
            this.radarFrameIndex = nextIndex;
            this.radarAdvancing = false;
            this.updateRadarFrameLabel();
            this.ngZone.run(() => this.cdr.markForCheck());
        };

        inactiveLayer.setOpacity(0);
        inactiveLayer.setUrl(nextUrl);
        inactiveLayer.once('load', complete);
        window.setTimeout(complete, 2000);
    }

    private radarTileUrl(frameId: string): string {
        return `/api/map/tiles/radar/${frameId}/{z}/{x}/{y}.png`;
    }

    private startRadarAnimation(): void {
        this.stopRadarAnimation();
        if (!this.radarLayers || !this.weatherLayers.radar || this.radarMapMoving) {
            return;
        }
        this.radarAnimTimer = setInterval(() => {
            if (!this.radarAdvancing && !this.radarMapMoving) {
                this.advanceRadarFrame();
            }
        }, this.radarFrameDelayMs);
    }

    private stopRadarAnimation(): void {
        if (this.radarAnimTimer) {
            clearInterval(this.radarAnimTimer);
            this.radarAnimTimer = null;
        }
    }

    private updateRadarFrameLabel(): void {
        const frameId = this.radarFrameIds[this.radarFrameIndex] ?? '900913';
        if (frameId === '900913') {
            this.radarFrameLabel = 'Now';
            return;
        }
        const match = frameId.match(/-m(\d{2})m$/);
        this.radarFrameLabel = match ? `${Number(match[1])}m ago` : '';
    }

    private scheduleAlertsRefresh(): void {
        if (!this.weatherLayers.alerts) {
            return;
        }
        if (this.alertsLoadTimer) {
            clearTimeout(this.alertsLoadTimer);
        }
        this.alertsLoadTimer = setTimeout(() => this.refreshAlertsLayer(), this.alertsDebounceMs);
    }

    private refreshAlertsLayer(): void {
        const map = this.map;
        if (!map || !this.weatherLayers.alerts) {
            return;
        }
        const viewportKey = this.viewportCacheKey(map);
        if (viewportKey === this.lastAlertsViewportKey && this.alertsLayerGroup) {
            return;
        }
        this.lastAlertsViewportKey = viewportKey;
        const requestId = ++this.alertsRequestId;
        const bounds = map.getBounds();
        this.nwsService.fetchActiveAlerts().subscribe({
            next: (collection) => {
                if (requestId !== this.alertsRequestId) {
                    return;
                }
                this.ngZone.run(() => {
                    this.clearAlertsLayer();
                    const features = (collection?.features ?? []).filter((feature) => {
                        if (!feature?.geometry) {
                            return false;
                        }
                        return this.featureIntersectsBounds(feature, bounds);
                    });
                    if (!features.length) {
                        return;
                    }
                    const group = L.layerGroup();
                    const geoLayer = L.geoJSON(
                        { type: 'FeatureCollection', features } as any,
                        {
                            smoothFactor: 2,
                            renderer: this.boundaryCanvasRenderer ?? undefined,
                            style: (feature) => this.alertStyle(feature),
                            onEachFeature: (feature, pathLayer) => {
                                const props = feature?.properties as Record<string, unknown> | undefined;
                                const event = String(props?.['event'] ?? 'Alert');
                                const headline = String(props?.['headline'] ?? '');
                                const severity = String(props?.['severity'] ?? '');
                                pathLayer.bindPopup(
                                    `<div class="tlr-alert-popup"><strong>${this.escapeHtml(event)}</strong>`
                                    + (headline ? `<div>${this.escapeHtml(headline)}</div>` : '')
                                    + (severity ? `<div class="tlr-alert-popup__meta">${this.escapeHtml(severity)}</div>` : '')
                                    + `</div>`,
                                );
                                pathLayer.bindTooltip(this.escapeHtml(event), { sticky: true, opacity: 0.92 });
                            },
                        } as L.GeoJSONOptions,
                    );
                    group.addLayer(geoLayer);
                    group.addTo(map);
                    this.alertsLayerGroup = group;
                });
            },
            error: () => {
                this.ngZone.run(() => this.clearAlertsLayer());
            },
        });
    }

    private alertStyle(feature?: { properties?: Record<string, unknown> }): L.PathOptions {
        const severity = String(feature?.properties?.['severity'] ?? '').toLowerCase();
        let color = '#ff9100';
        let fillOpacity = 0.18;
        if (severity === 'extreme') {
            color = '#d500f9';
            fillOpacity = 0.24;
        } else if (severity === 'severe') {
            color = '#ff1744';
            fillOpacity = 0.22;
        } else if (severity === 'moderate') {
            color = '#ffea00';
            fillOpacity = 0.16;
        } else if (severity === 'minor') {
            color = '#00e676';
            fillOpacity = 0.12;
        }
        return {
            color,
            weight: 2,
            opacity: 0.85,
            fillColor: color,
            fillOpacity,
        };
    }

    private clearAlertsLayer(): void {
        if (this.alertsLayerGroup) {
            this.alertsLayerGroup.remove();
            this.alertsLayerGroup = null;
        }
    }

    private featureIntersectsBounds(feature: GeoJSON.Feature, mapBounds: L.LatLngBounds): boolean {
        const bbox = this.computeFeatureBBox(feature.geometry);
        if (!bbox) {
            return true;
        }
        const [south, west, north, east] = bbox;
        const featureBounds = L.latLngBounds([south, west], [north, east]);
        return mapBounds.intersects(featureBounds);
    }

    private computeFeatureBBox(
        geometry: GeoJSON.Geometry | null | undefined,
    ): [number, number, number, number] | null {
        if (!geometry) {
            return null;
        }
        let south = Infinity;
        let west = Infinity;
        let north = -Infinity;
        let east = -Infinity;
        const extend = (lat: number, lon: number): void => {
            if (lat < south) {
                south = lat;
            }
            if (lat > north) {
                north = lat;
            }
            if (lon < west) {
                west = lon;
            }
            if (lon > east) {
                east = lon;
            }
        };
        const walk = (coords: unknown): void => {
            if (!Array.isArray(coords)) {
                return;
            }
            if (typeof coords[0] === 'number' && typeof coords[1] === 'number') {
                extend(coords[1], coords[0]);
                return;
            }
            for (const part of coords) {
                walk(part);
            }
        };
        if (geometry.type === 'Polygon' || geometry.type === 'MultiPolygon') {
            walk((geometry as GeoJSON.Polygon | GeoJSON.MultiPolygon).coordinates);
        } else if (geometry.type === 'Point') {
            const [lon, lat] = (geometry as GeoJSON.Point).coordinates;
            extend(lat, lon);
        } else {
            return null;
        }
        if (!Number.isFinite(south)) {
            return null;
        }
        return [south, west, north, east];
    }

    private clearWeatherLayers(): void {
        this.stopRadarAnimation();
        const map = this.map;
        if (map) {
            this.destroyRadarLayers(map);
        }
        this.radarFrameLabel = '';
        this.clearAlertsLayer();
    }
}
