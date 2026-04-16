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

import { Component, EventEmitter, Input, OnDestroy, OnInit, Output } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Subject, Subscription } from 'rxjs';
import { debounceTime, distinctUntilChanged } from 'rxjs/operators';
import { RdioScannerAlert, RdioScannerCall, RdioScannerService, RdioScannerTranscript } from '../rdio-scanner';
import { AlertsService } from './alerts.service';
import { AlertSoundService } from '../alert-sound.service';
import { SettingsService } from '../settings/settings.service';
import { TranscriptAnnotation, renderAnnotatedTranscript } from '../transcript-utils';

/** Main board hosts separate tabs; each instance uses one mode. */
export type RdioScannerAlertsPanelMode = 'alertsAndPreferences' | 'transcripts' | 'stats';

interface IncidentSubcategory {
    label: string;
    count: number;
}

interface IncidentCategory {
    category: string;
    count: number;
    subcategories: IncidentSubcategory[];
}

interface StatsData {
    availableSystems: Array<{ id: number; label: string }>;
    callsPerMinute: Array<{ minute: number; count: number }>;
    topTalkgroups: Array<{ label: string; count: number }>;
    callsByHour: Array<{ hour: number; count: number }>;
    topDepartmentsByTone: Array<{ label: string; count: number }>;
    totalCallsToday: number;
    callsLastMinute: number;
    callsLastHour: number;
    incidentSummary: IncidentCategory[];
    generatedAt: number;
}

@Component({
    selector: 'rdio-scanner-alerts',
    styleUrls: ['./alerts.component.scss'],
    templateUrl: './alerts.component.html',
})
export class RdioScannerAlertsComponent implements OnDestroy, OnInit {
    /** Compact “recent alerts” rail for the Current tab (no tabs / transcript UI). */
    @Input() boardEmbed = false;
    @Input() boardEmbedMax = 12;
    @Output() openFullAlerts = new EventEmitter<void>();

    /**
     * `alertsAndPreferences` — inner tabs Alerts + Preferences only (main Board “Alerts” tab).
     * `transcripts` / `stats` — single full-page panel (separate main Board tabs).
     */
    @Input() panelMode: RdioScannerAlertsPanelMode = 'alertsAndPreferences';

    /**
     * Classic/legacy sidenav: add a third inner tab “Transcripts” (full transcript list) next to Alerts / Preferences.
     * Main board keeps a separate top-level Transcripts tab — leave this false there to avoid duplication.
     */
    @Input() includeTranscriptsTab = false;

    alerts: RdioScannerAlert[] = [];
    transcripts: RdioScannerTranscript[] = [];
    loading = false;
    loadingTranscripts = false;
    limit = 50;
    transcriptOffset = 0;
    activeTab: 'alerts' | 'preferences' | 'transcripts' = 'alerts';

    // Stats
    stats: StatsData | null = null;
    loadingStats = false;
    statsError = '';
    selectedSystemId: number | null = null;
    expandedIncidentCategory: string | null = null;
    private statsRefreshInterval: any;
    private pin?: string;
    
    // Filter properties
    filterSystemId?: number;
    filterTalkgroupId?: number;
    filterDateFrom?: string; // YYYY-MM-DD format for date input
    filterDateTo?: string; // YYYY-MM-DD format for date input
    filterSearch: string = '';
    availableSystems: Array<{id: number, label: string}> = [];
    availableTalkgroups: Array<{id: number, label: string, systemId: number}> = [];
    
    // Cached grouped alerts to avoid recalculation on every change detection
    allAlertGroups: Array<{key: string, alerts: RdioScannerAlert[], latestTimestamp: number, groupType: 'tone' | 'channel'}> = [];

    private searchSubject = new Subject<string>();
    private searchSubscription?: Subscription;

    constructor(
        private rdioScannerService: RdioScannerService,
        private alertsService: AlertsService,
        private alertSoundService: AlertSoundService,
        private settingsService: SettingsService,
        private http: HttpClient,
    ) {
        // Get PIN from localStorage using the service method
        this.pin = this.rdioScannerService.readPin();
    }

    ngOnInit(): void {
        this.searchSubscription = this.searchSubject.pipe(
            debounceTime(300),
            distinctUntilChanged(),
        ).subscribe(() => {
            this.transcriptOffset = 0;
            this.loadTranscripts();
        });

        // Refresh PIN from localStorage
        this.pin = this.rdioScannerService.readPin();

        // For the embed rail, paint cached alerts immediately (synchronously) so the
        // LCP element (p.transcript-text) is visible on the very first frame instead
        // of waiting for the HTTP response.
        if (this.boardEmbed) {
            const cached = this.alertsService.getCachedAlerts();
            if (cached.length > 0) {
                this.alerts = cached;
                this.updateGroupedAlerts();
            }
        }

        // Defer all remaining data-loading to a separate task so the tab paint is not blocked.
        // The browser can render the empty shell first, then data arrives in the next task.
        setTimeout(() => {
            if (!this.boardEmbed && (this.panelMode === 'alertsAndPreferences' || this.panelMode === 'transcripts')) {
                this.loadSystemsAndTalkgroups();
            }

            if (this.boardEmbed || this.panelMode !== 'stats') {
                this.loadAlerts(true);
            }
            if (!this.boardEmbed && this.panelMode === 'transcripts') {
                this.loadTranscripts();
            }
            if (!this.boardEmbed && this.panelMode === 'stats') {
                this.loadStats();
                this.startStatsRefreshInterval();
            }
        }, 0);

        this.requestNotificationPermission();

        // Defer subscriptions that emit synchronously (BehaviorSubject) so the
        // initial tab paint is not blocked by grouping/sorting cached alert data.
        setTimeout(() => {
            // Subscribe to shared alerts service for updates
            this.alertsService.alerts$.subscribe(alerts => {
                this.alerts = alerts;
                this.updateGroupedAlerts();
            });

            // Listen for real-time alerts via WebSocket
            this.rdioScannerService.event.subscribe((event: any) => {
                if (event.alert) {
                    if (this.boardEmbed || this.panelMode !== 'stats') {
                        this.loadAlerts(false);
                    }
                    if (!this.boardEmbed && (this.panelMode === 'transcripts' || (this.panelMode === 'alertsAndPreferences' && this.activeTab === 'transcripts'))) {
                        this.loadTranscripts();
                    }
                    if (this.boardEmbed || this.panelMode === 'alertsAndPreferences') {
                        this.showNotification(event.alert);
                        this.playAlertSound();
                    }
                }
                if (event.config && !this.boardEmbed && (this.panelMode === 'alertsAndPreferences' || this.panelMode === 'transcripts')) {
                    this.loadSystemsAndTalkgroups();
                }
            });
        }, 0);
    }

    get recentAlertsFlat(): RdioScannerAlert[] {
        if (!this.boardEmbed || !this.alerts?.length) {
            return [];
        }
        // Deduplicate by callId — keep the alert with the most keywords for each call
        // so the same call never appears more than once in the embed rail.
        const byCall = new Map<number, RdioScannerAlert>();
        for (const alert of this.alerts) {
            if (alert?.createdAt == null) continue;
            const existing = byCall.get(alert.callId);
            if (!existing) {
                byCall.set(alert.callId, alert);
            } else {
                const existingCount = existing.keywordsMatched ? JSON.parse(existing.keywordsMatched).length : 0;
                const newCount = alert.keywordsMatched ? JSON.parse(alert.keywordsMatched).length : 0;
                if (newCount > existingCount || (!existing.transcript && alert.transcript)) {
                    byCall.set(alert.callId, alert);
                }
            }
        }
        return [...byCall.values()]
            .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0))
            .slice(0, this.boardEmbedMax);
    }
    
    loadSystemsAndTalkgroups(): void {
        const config = this.rdioScannerService.getConfig();
        if (config && config.systems) {
            this.availableSystems = config.systems.map(s => ({
                id: s.id,
                label: s.label || `System ${s.id}`
            }));
            
            // Flatten talkgroups from all systems
            this.availableTalkgroups = [];
            config.systems.forEach(system => {
                if (system.talkgroups) {
                    system.talkgroups.forEach(tg => {
                        this.availableTalkgroups.push({
                            id: tg.id,
                            label: tg.label || tg.name || `Talkgroup ${tg.id}`,
                            systemId: system.id
                        });
                    });
                }
            });
        }
    }
    
    getFilteredTalkgroups(): Array<{id: number, label: string, systemId: number}> {
        if (!this.filterSystemId) {
            return this.availableTalkgroups;
        }
        return this.availableTalkgroups.filter(tg => tg.systemId === this.filterSystemId);
    }
    
    onSystemFilterChange(value: any): void {
        // Convert to number if it's a string, or set to undefined if empty/null
        if (value === '' || value === null || value === undefined || value === 'undefined') {
            this.filterSystemId = undefined;
        } else {
            const numValue = typeof value === 'string' ? parseInt(value, 10) : Number(value);
            this.filterSystemId = isNaN(numValue) ? undefined : numValue;
        }
        // Reset talkgroup filter when system changes
        this.filterTalkgroupId = undefined;
        this.applyFilters();
    }
    
    onTalkgroupFilterChange(value: any): void {
        // Convert to number if it's a string, or set to undefined if empty/null
        if (value === '' || value === null || value === undefined || value === 'undefined') {
            this.filterTalkgroupId = undefined;
        } else {
            const numValue = typeof value === 'string' ? parseInt(value, 10) : Number(value);
            this.filterTalkgroupId = isNaN(numValue) ? undefined : numValue;
        }
        this.applyFilters();
    }
    
    applyFilters(): void {
        this.transcriptOffset = 0;
        this.loadTranscripts();
    }

    onSearchInput(): void {
        this.searchSubject.next(this.filterSearch);
    }
    
    clearFilters(): void {
        this.filterSystemId = undefined;
        this.filterTalkgroupId = undefined;
        this.filterDateFrom = undefined;
        this.filterDateTo = undefined;
        this.filterSearch = '';
        this.applyFilters();
    }
    
    highlightSearchText(text: string, search: string): string {
        if (!search || !text) {
            return text;
        }
        const regex = new RegExp(`(${search.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
        return text.replace(regex, '<mark>$1</mark>');
    }

    renderTranscript(transcript: string, annotations?: TranscriptAnnotation[], search?: string): string {
        return renderAnnotatedTranscript(transcript, annotations, search);
    }

    ngOnDestroy(): void {
        if (this.statsRefreshInterval) {
            clearInterval(this.statsRefreshInterval);
        }
        this.searchSubscription?.unsubscribe();
        this.searchSubject.complete();
    }

    /** When true, classic sidenav shows Alerts | Preferences | Transcripts inner tabs. */
    get showTranscriptsInnerTab(): boolean {
        return this.includeTranscriptsTab && this.isTranscriptionEnabled;
    }

    get isTranscriptionEnabled(): boolean {
        return !!this.rdioScannerService.getConfig()?.options?.transcriptionEnabled;
    }

    setTab(tab: 'alerts' | 'preferences' | 'transcripts'): void {
        if (this.panelMode !== 'alertsAndPreferences') {
            return;
        }
        if (tab === 'transcripts' && !this.showTranscriptsInnerTab) {
            return;
        }
        this.activeTab = tab;
        if (tab === 'alerts') {
            this.loadAlerts(false);
        }
        if (tab === 'transcripts') {
            this.loadTranscripts();
        }
    }

    private startStatsRefreshInterval(): void {
        if (this.statsRefreshInterval) {
            clearInterval(this.statsRefreshInterval);
        }
        this.statsRefreshInterval = setInterval(() => {
            if (this.panelMode === 'stats') {
                this.loadStats();
            }
        }, 30000);
    }

    loadStats(): void {
        this.loadingStats = true;
        this.statsError = '';
        const pin = this.pin;
        const headers = pin ? new HttpHeaders({ 'Authorization': `Bearer ${pin}` }) : new HttpHeaders();
        let url = '/api/stats';
        if (this.selectedSystemId !== null) {
            url += `?systemId=${this.selectedSystemId}`;
        }
        this.http.get<StatsData>(url, { headers }).subscribe({
            next: (data) => {
                this.stats = data;
                this.loadingStats = false;
            },
            error: (_err) => {
                this.statsError = 'Failed to load stats. Please try again.';
                this.loadingStats = false;
            }
        });
    }


    // Helpers for CSS bar charts
    statsMaxCount(items: Array<{ count: number }>): number {
        return items.length ? Math.max(...items.map(i => i.count), 1) : 1;
    }

    statsBarPct(count: number, max: number): number {
        return Math.round((count / max) * 100);
    }

    statsHourLabel(hour: number): string {
        if (hour === 0) return '12a';
        if (hour < 12) return `${hour}a`;
        if (hour === 12) return '12p';
        return `${hour - 12}p`;
    }

    statsCallsPerMinLabel(minute: number): string {
        return new Date(minute).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }

    statsMaxCpm(): number {
        return this.stats ? Math.max(...this.stats.callsPerMinute.map(b => b.count), 1) : 1;
    }

    // Returns every 10th minute label for the x-axis tick marks
    statsCpmLabels(): string[] {
        if (!this.stats) return [];
        return this.stats.callsPerMinute
            .filter((_, i) => i % 10 === 0)
            .map(b => this.statsCallsPerMinLabel(b.minute));
    }

    // Returns 5 evenly-spaced Y-axis tick values from max down to 0
    statsYTicks(items: Array<{ count: number }>): number[] {
        const max = this.statsMaxCount(items);
        const steps = 4;
        return Array.from({ length: steps + 1 }, (_, i) => Math.round(max * (steps - i) / steps));
    }

    statsYTicksCpm(): number[] {
        const max = this.statsMaxCpm();
        const steps = 4;
        return Array.from({ length: steps + 1 }, (_, i) => Math.round(max * (steps - i) / steps));
    }


    nextTranscriptsPage(): void {
        this.transcriptOffset += this.limit;
        this.loadTranscripts();
    }

    prevTranscriptsPage(): void {
        this.transcriptOffset = Math.max(0, this.transcriptOffset - this.limit);
        this.loadTranscripts();
    }


    loadAlerts(forceFullRefresh: boolean = false): void {
        // Refresh PIN before each request
        this.pin = this.rdioScannerService.readPin();
        
        if (!this.pin) {
            console.warn('No PIN available for loading alerts');
            this.loading = false;
            this.alerts = [];
            this.updateGroupedAlerts();
            return;
        }
        
        this.loading = true;
        
        // Use shared service to fetch new alerts incrementally
        this.alertsService.fetchNewAlerts(this.pin, forceFullRefresh).subscribe({
            next: (newAlerts) => {
                // Get all alerts from cache (includes new ones)
                this.alerts = this.alertsService.getCachedAlerts();
                
                this.updateGroupedAlerts();
                
                this.loading = false;
            },
            error: (error) => {
                console.error('Error loading alerts:', error);
                // On error, still try to use cached alerts
                this.alerts = this.alertsService.getCachedAlerts();
                this.updateGroupedAlerts();
                this.loading = false;
            },
        });
    }

    loadTranscripts(): void {
        this.pin = this.rdioScannerService.readPin();
        if (!this.pin) {
            this.transcripts = [];
            return;
        }
        this.loadingTranscripts = true;
        
        // Convert date strings (YYYY-MM-DD) to timestamps (start of day for from, end of day for to)
        let dateFrom: number | undefined;
        let dateTo: number | undefined;
        if (this.filterDateFrom) {
            const date = new Date(this.filterDateFrom + 'T00:00:00');
            dateFrom = Math.floor(date.getTime() / 1000) * 1000;
        }
        if (this.filterDateTo) {
            const date = new Date(this.filterDateTo + 'T23:59:59');
            dateTo = Math.floor(date.getTime() / 1000) * 1000;
        }
        
        this.alertsService.getTranscripts(
            this.limit, 
            this.transcriptOffset, 
            this.pin, 
            this.filterSystemId, 
            this.filterTalkgroupId,
            dateFrom,
            dateTo,
            this.filterSearch
        ).subscribe({
            next: (transcripts) => {
                this.transcripts = (transcripts || []).map((t: any) => {
                    return {
                        ...t,
                        transcript: t.transcript || '',
                    } as RdioScannerTranscript;
                });
                this.loadingTranscripts = false;
            },
            error: (error) => {
                console.error('Error loading transcripts:', error);
                this.transcripts = [];
                this.loadingTranscripts = false;
            },
        });
    }

    getAlertTypeLabel(alert: RdioScannerAlert): string {
        switch (alert.alertType) {
            case 'tone':
                return 'Tone Detected';
            case 'keyword':
                return 'Keyword Match';
            case 'tone+keyword':
                return 'Tone & Keyword';
            default:
                return 'Alert';
        }
    }

    getKeywordsMatched(alert: RdioScannerAlert): string[] {
        if (!alert.keywordsMatched) {
            return [];
        }
        try {
            return JSON.parse(alert.keywordsMatched);
        } catch {
            return [];
        }
    }

    formatTimestamp(timestamp: number): string {
        const date = new Date(timestamp);
        const datePart = date.toLocaleDateString();
        const timePart = date.toLocaleTimeString();
        const spacer = '\u00A0\u00A0\u00A0'; // three non-breaking spaces
        return `${datePart}${spacer}${timePart}`;
    }

    // Update cached grouped alerts (called when alerts change to avoid recalculation on every change detection)
    private updateGroupedAlerts(): void {
        // Group tone alerts by tone set name
        const toneGrouped = new Map<string, RdioScannerAlert[]>();
        
        this.alerts.filter(alert => 
            alert.alertType === 'tone' || alert.alertType === 'tone+keyword'
        ).forEach(alert => {
            // Get tone set name from alert - prefer matchedToneSetName (specific tone set for this alert)
            // then fall back to first tone set from matchedToneSetNames
            let toneSetKey = 'Unknown Tone Set';
            if (alert.matchedToneSetName) {
                toneSetKey = alert.matchedToneSetName;
            } else if (alert.matchedToneSetNames && alert.matchedToneSetNames.length > 0) {
                toneSetKey = alert.matchedToneSetNames[0];
            }
            
            if (!toneGrouped.has(toneSetKey)) {
                toneGrouped.set(toneSetKey, []);
            }
            toneGrouped.get(toneSetKey)!.push(alert);
        });
        
        // Convert to array and find latest timestamp for each group
        const toneGroups = Array.from(toneGrouped.entries()).map(([key, alerts]) => {
            // Find the most recent alert timestamp in this group
            const latestTimestamp = Math.max(...alerts.map(a => a.createdAt || 0));
            return {
                key,
                alerts,
                latestTimestamp,
                groupType: 'tone' as const
            };
        });

        // Group channel alerts by channel (system + talkgroup)
        const channelGrouped = new Map<string, RdioScannerAlert[]>();
        
        this.alerts.filter(alert => alert.alertType === 'keyword').forEach(alert => {
            // Create channel key from system + talkgroup
            const channelKey = `${alert.systemLabel || `System ${alert.systemId}`} / ${alert.talkgroupLabel || alert.talkgroupName || `Talkgroup ${alert.talkgroupId}`}`;
            
            if (!channelGrouped.has(channelKey)) {
                channelGrouped.set(channelKey, []);
            }
            channelGrouped.get(channelKey)!.push(alert);
        });
        
        // Convert to array and find latest timestamp for each group
        const channelGroups = Array.from(channelGrouped.entries()).map(([key, alerts]) => {
            // Find the most recent alert timestamp in this group
            const latestTimestamp = Math.max(...alerts.map(a => a.createdAt || 0));
            return {
                key,
                alerts,
                latestTimestamp,
                groupType: 'channel' as const
            };
        });
        
        // Combine all groups and sort by most recent alert timestamp
        this.allAlertGroups = [...toneGroups, ...channelGroups].sort((a, b) => b.latestTimestamp - a.latestTimestamp);
    }

    // TrackBy functions for efficient change detection
    trackByGroupKey(index: number, group: {key: string, alerts: RdioScannerAlert[], latestTimestamp: number}): string {
        return group.key;
    }

    trackByAlertId(index: number, alert: RdioScannerAlert): number {
        return alert.alertId;
    }

    trackByTranscriptId(index: number, transcript: RdioScannerTranscript): number | string {
        return transcript.callId ?? index;
    }

    playCall(callId: number): void {
        // Trigger call playback
        this.rdioScannerService.loadAndPlay(callId);
    }

    requestNotificationPermission(): void {
        if ('Notification' in window && Notification.permission === 'default') {
            Notification.requestPermission();
        }
    }

    showNotification(alert: RdioScannerAlert): void {
        if ('Notification' in window && Notification.permission === 'granted') {
            const keywords = this.getKeywordsMatched(alert);
            const keywordText = keywords.length > 0 ? `Keywords: ${keywords.join(', ')}` : '';
            const notification = new Notification(
                this.getAlertTypeLabel(alert),
                {
                    body: alert.transcriptSnippet || keywordText || 'Alert detected',
                    icon: '/assets/icons/icon.png',
                    tag: `alert-${alert.alertId}`,
                }
            );
            notification.onclick = () => {
                this.playCall(alert.callId);
                window.focus();
            };
        }
    }

    // ── Incident summary helpers ────────────────────────────────────────────
    toggleIncidentCategory(cat: string): void {
        this.expandedIncidentCategory = this.expandedIncidentCategory === cat ? null : cat;
    }

    getIncidentIcon(category: string): string {
        const icons: { [key: string]: string } = {
            'Fire':          '🔥',
            'Hazmat':        '☣️',
            'Medical / EMS': '🚑',
            'Crime':         '🚔',
            'Traffic':       '🚗',
            'Disturbance':   '⚠️',
        };
        return icons[category] || '📻';
    }

    getIncidentColor(category: string): string {
        const colors: { [key: string]: string } = {
            'Fire':         '#ff5722',
            'Hazmat':       '#ff9800',
            'Medical / EMS':'#00e676',
            'Crime':        '#f44336',
            'Traffic':      '#29b6f6',
            'Disturbance':  '#ce93d8',
        };
        return colors[category] || '#90a4ae';
    }

    private playAlertSound(): void {
        // Get the alert sound setting and play it
        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                const alertSound = settings?.alertSound || 'alert';
                this.alertSoundService.playSound(alertSound);
            },
            error: (error) => {
                console.error('Failed to get alert sound setting:', error);
                // Play default alert sound on error
                this.alertSoundService.playSound('alert');
            }
        });
    }
}

