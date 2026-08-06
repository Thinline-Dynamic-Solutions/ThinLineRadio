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

import { Component, EventEmitter, HostBinding, Input, OnDestroy, OnInit, Output, ChangeDetectionStrategy } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Subject, Subscription, firstValueFrom } from 'rxjs';
import { debounceTime, distinctUntilChanged } from 'rxjs/operators';
import { RdioScannerAlert, RdioScannerCall, RdioScannerService, RdioScannerTranscript } from '../rdio-scanner';
import { AlertsService, RdioScannerSystemAlert } from './alerts.service';
import { AlertSoundService } from '../alert-sound.service';
import { SettingsService } from '../settings/settings.service';
import { TranscriptAnnotation, renderAnnotatedTranscript } from '../transcript-utils';
import { RdioScannerAdminService } from '../admin/admin.service';
import { TranscriptReviewService } from '../transcript-review/transcript-review.service';
import { MatSnackBar } from '@angular/material/snack-bar';

/** Main board hosts separate tabs; each instance uses one mode. */
export type RdioScannerAlertsPanelMode = 'alertsAndPreferences' | 'transcripts' | 'stats';

export type RdioScannerAlertsViewMode = 'mine' | 'system' | 'all';

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
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAlertsComponent implements OnDestroy, OnInit {
    /** Compact “recent alerts” rail for the Current tab (no tabs / transcript UI). */
    @Input() boardEmbed = false;
    @Input() boardEmbedMax = 12;
    @Output() openFullAlerts = new EventEmitter<void>();

    @HostBinding('class.alerts-host-embed')
    get alertsHostEmbed(): boolean {
        return this.boardEmbed;
    }

    /**
     * `alertsAndPreferences` — inner tabs Alerts + Preferences only (main Board “Alerts” tab).
     * `transcripts` / `stats` — single full-page panel (separate main Board tabs).
     */
    @Input() panelMode: RdioScannerAlertsPanelMode = 'alertsAndPreferences';

    alerts: RdioScannerAlert[] = [];
    systemAlerts: RdioScannerSystemAlert[] = [];
    loadingSystemAlerts = false;
    canViewSystemAlerts = false;
    isSystemAdmin = false;
    alertsViewMode: RdioScannerAlertsViewMode = 'mine';
    alertSearch = '';
    transcripts: RdioScannerTranscript[] = [];
    loading = false;
    loadingTranscripts = false;
    limit = 50;
    transcriptOffset = 0;
    activeTab: 'alerts' | 'preferences' = 'alerts';

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
    /** Set when a live alert arrives but we skip auto-refresh to preserve filters / edit state. */
    transcriptsStale = false;
    availableSystems: Array<{id: number, label: string}> = [];
    availableTalkgroups: Array<{id: number, label: string, systemId: number}> = [];
    
    // Cached grouped alerts to avoid recalculation on every change detection
    allAlertGroups: Array<{key: string, alerts: RdioScannerAlert[], latestTimestamp: number, groupType: 'tone' | 'channel'}> = [];

    private searchSubject = new Subject<string>();
    private searchSubscription?: Subscription;

    // ── Admin transcript-edit mode ────────────────────────────────────────────
    get adminAuthenticated(): boolean {
        return this.rdioScannerService.isSystemAdmin();
    }
    editingCallId: number | null = null;
    editText = '';
    editSaving = false;
    editApproving = false;
    editAudioSrc = '';
    editAudioLoading = false;
    private editAudioObjectUrl: string | null = null;

    // Transcript collector (global server setting)
    collectorConnected = false;
    collectorHasApiKey = false;
    collectorServerName = '';
    collectorLoading = false;
    collectorConnecting = false;
    collectorStats: { submissions: number; formatted: string; hours: number; minutes: number; seconds: number } | null = null;

    globalTrainingProgress: {
        goalHours: number;
        hoursDecimal: number;
        percentOfGoal: number;
        formatted: string;
        hours: number;
        minutes: number;
        seconds: number;
        submissions: number;
        serverAccounts: number;
    } | null = null;
    globalTrainingLoading = false;
    showTrainingTips = false;

    constructor(
        private rdioScannerService: RdioScannerService,
        private alertsService: AlertsService,
        private alertSoundService: AlertSoundService,
        private settingsService: SettingsService,
        private http: HttpClient,
        private adminService: RdioScannerAdminService,
        private reviewService: TranscriptReviewService,
        private snackBar: MatSnackBar,
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
                if (!this.boardEmbed && this.panelMode === 'alertsAndPreferences') {
                    this.loadSystemAlerts();
                }
            }
            if (!this.boardEmbed && this.panelMode === 'transcripts') {
                this.loadTranscripts();
                void this.loadCollectorSettings();
                void this.loadGlobalTrainingProgress();
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
                        // Full refresh — incremental since= can miss a just-persisted
                        // alerting-talkgroup / tone row (issue #229).
                        this.loadAlerts(true);
                    }
                    if (!this.boardEmbed && this.panelMode === 'transcripts') {
                        this.onTranscriptsMayHaveChanged();
                    }
                    if (this.boardEmbed || this.panelMode === 'alertsAndPreferences') {
                        this.showNotification(event.alert);
                        this.playAlertSound();
                    }
                }
                if (event.incident) {
                    this.alertsService.patchIncidentUpdate(event.incident);
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
        // Deduplicate by callId — keep the alert with the most keywords for each call.
        const byCall = new Map<number, RdioScannerAlert>();
        for (const alert of this.alerts) {
            if (alert?.createdAt == null) continue;
            const callId = Number(alert.callId);
            if (!Number.isFinite(callId)) continue;
            const existing = byCall.get(callId);
            if (!existing) {
                byCall.set(callId, alert);
            } else {
                const existingCount = this.countKeywordsMatched(existing);
                const newCount = this.countKeywordsMatched(alert);
                if (newCount > existingCount || (!existing.transcript && alert.transcript)) {
                    byCall.set(callId, alert);
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
        this.transcriptsStale = false;
        this.loadTranscripts();
    }

    hasActiveTranscriptFilters(): boolean {
        return !!(
            this.filterSystemId ||
            this.filterTalkgroupId ||
            this.filterDateFrom ||
            this.filterDateTo ||
            this.filterSearch?.trim()
        );
    }

    /** True when a full list reload would disrupt the user's current transcripts view. */
    private shouldDeferTranscriptRefreshOnAlert(): boolean {
        return this.hasActiveTranscriptFilters()
            || this.editingCallId != null
            || this.transcriptOffset > 0;
    }

    private onTranscriptsMayHaveChanged(): void {
        if (this.shouldDeferTranscriptRefreshOnAlert()) {
            this.transcriptsStale = true;
            return;
        }
        this.loadTranscripts({ silent: true });
    }

    refreshStaleTranscripts(): void {
        this.transcriptsStale = false;
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
        this.revokeEditAudio();
    }

    // ── Admin edit mode methods ───────────────────────────────────────────────

    toggleEdit(transcript: RdioScannerTranscript): void {
        if (this.isTrainingSubmitted(transcript)) {
            return;
        }
        if (this.editingCallId === transcript.callId) {
            this.cancelEdit();
            return;
        }
        this.cancelEdit();
        this.editingCallId = transcript.callId ?? null;
        this.editText = transcript.reviewedTranscript?.trim() || transcript.transcript || '';
        if (transcript.callId != null) {
            void this.ensureAdminToken().then((ok) => {
                if (ok && transcript.callId != null) {
                    void this.loadEditAudio(transcript.callId);
                }
            });
        }
    }

    cancelEdit(): void {
        this.editingCallId = null;
        this.editText = '';
        this.revokeEditAudio();
    }

    isTrainingSubmitted(transcript: RdioScannerTranscript): boolean {
        return transcript.trainingReviewStatus === 'submitted';
    }

    hasTrainingDraft(transcript: RdioScannerTranscript): boolean {
        return transcript.trainingReviewStatus === 'pending';
    }

    async saveEditDraft(): Promise<void> {
        if (this.editingCallId == null || !this.editText.trim()) return;
        if (!(await this.ensureAdminToken())) {
            this.snackBar.open('Could not authorize — sign in as system admin', '', { duration: 5000 });
            return;
        }
        this.editSaving = true;
        try {
            await this.reviewService.save(this.editingCallId, this.editText.trim());
            const callId = this.editingCallId;
            const idx = this.transcripts.findIndex((t) => t.callId === callId);
            if (idx >= 0) {
                this.transcripts[idx] = {
                    ...this.transcripts[idx],
                    reviewedTranscript: this.editText.trim(),
                    trainingReviewStatus: 'pending',
                };
            }
            this.snackBar.open('Draft saved', '', { duration: 2500 });
        } catch (e: any) {
            this.snackBar.open(e?.error?.error || 'Save failed', '', { duration: 5000 });
        } finally {
            this.editSaving = false;
        }
    }

    async approveEdit(): Promise<void> {
        if (this.editingCallId == null || !this.editText.trim()) return;
        if (!(await this.ensureAdminToken())) {
            this.snackBar.open('Could not authorize — sign in as system admin', '', { duration: 5000 });
            return;
        }
        if (!this.collectorHasApiKey || !this.collectorConnected) {
            this.snackBar.open('Request a transcript collector API key first (see setup above)', '', { duration: 5000 });
            return;
        }
        this.editApproving = true;
        try {
            const approvedText = this.editText.trim().toUpperCase();
            const res = await this.reviewService.approve(this.editingCallId, approvedText);
            const callId = this.editingCallId;
            const idx = this.transcripts.findIndex((t) => t.callId === callId);
            if (idx >= 0) {
                this.transcripts[idx] = {
                    ...this.transcripts[idx],
                    transcript: approvedText,
                    reviewedTranscript: approvedText,
                    trainingReviewStatus: 'submitted',
                    transcriptAnnotations: undefined,
                };
            }
            this.snackBar.open(res.message || 'Approved & sent to collector', '', { duration: 4000 });
            this.cancelEdit();
            void this.loadCollectorSettings();
            void this.loadGlobalTrainingProgress();
        } catch (e: any) {
            this.snackBar.open(e?.error?.error || 'Approve failed', '', { duration: 6000 });
        } finally {
            this.editApproving = false;
        }
    }

    private async loadEditAudio(callId: number): Promise<void> {
        this.revokeEditAudio();
        this.editAudioLoading = true;
        try {
            const res = await fetch(this.reviewService.audioUrl(callId), {
                headers: this.reviewService.getAudioFetchHeaders(),
            });
            if (!res.ok) throw new Error(`HTTP ${res.status}`);
            const blob = await res.blob();
            if (!blob.size) throw new Error('Empty audio');
            this.editAudioObjectUrl = URL.createObjectURL(blob);
            this.editAudioSrc = this.editAudioObjectUrl;
        } catch {
            this.editAudioSrc = '';
        } finally {
            this.editAudioLoading = false;
        }
    }

    private revokeEditAudio(): void {
        if (this.editAudioObjectUrl) {
            URL.revokeObjectURL(this.editAudioObjectUrl);
            this.editAudioObjectUrl = null;
        }
        this.editAudioSrc = '';
        this.editAudioLoading = false;
    }

    // ── Transcript collector (global server config) ─────────────────────────────

    async loadCollectorSettings(): Promise<void> {
        if (!this.adminAuthenticated) {
            return;
        }
        if (!(await this.ensureAdminToken())) {
            this.collectorConnected = false;
            this.collectorHasApiKey = false;
            return;
        }
        this.collectorLoading = true;
        try {
            const settings = await this.reviewService.getCollectorSettings();
            this.collectorHasApiKey = !!settings?.hasApiKey;
            this.collectorConnected = !!settings?.connected;
            this.collectorServerName = settings?.serverName || '';
            if (this.collectorHasApiKey && this.collectorConnected) {
                try {
                    const stats = await this.reviewService.getCollectorStats();
                    const dur = stats?.audioDuration;
                    this.collectorStats = {
                        submissions: stats?.submissions ?? 0,
                        formatted: dur?.formatted || '0s',
                        hours: dur?.hours ?? 0,
                        minutes: dur?.minutes ?? 0,
                        seconds: dur?.seconds ?? 0,
                    };
                } catch {
                    this.collectorStats = null;
                }
            } else {
                this.collectorStats = null;
            }
        } catch {
            this.collectorConnected = false;
            this.collectorHasApiKey = false;
            this.collectorServerName = '';
            this.collectorStats = null;
        } finally {
            this.collectorLoading = false;
        }
    }

    async loadGlobalTrainingProgress(): Promise<void> {
        const pin = this.rdioScannerService.readPin();
        if (!pin) {
            this.globalTrainingProgress = null;
            return;
        }
        this.globalTrainingLoading = true;
        try {
            const progress = await firstValueFrom(this.alertsService.getTrainingProgress(pin));
            const dur = progress?.audioDuration;
            const goalHours = progress?.goalHours ?? 5000;
            const hoursDecimal = progress?.hoursDecimal ?? 0;
            this.globalTrainingProgress = {
                goalHours,
                hoursDecimal,
                percentOfGoal: Math.min(100, progress?.percentOfGoal ?? 0),
                formatted: dur?.formatted || '0s',
                hours: dur?.hours ?? 0,
                minutes: dur?.minutes ?? 0,
                seconds: dur?.seconds ?? 0,
                submissions: progress?.submissions ?? 0,
                serverAccounts: progress?.serverAccounts ?? 0,
            };
        } catch {
            this.globalTrainingProgress = null;
        } finally {
            this.globalTrainingLoading = false;
        }
    }

    async requestCollectorKey(): Promise<void> {
        if (!(await this.ensureAdminToken())) {
            this.snackBar.open('Could not authorize — sign in as system admin', '', { duration: 5000 });
            return;
        }
        this.collectorConnecting = true;
        try {
            const res = await this.reviewService.requestCollectorKey();
            this.collectorServerName = res.serverName || this.collectorServerName;
            await this.loadCollectorSettings();
            this.snackBar.open(res.message || 'Connected to transcript collector', '', { duration: 4000 });
        } catch (e: any) {
            this.snackBar.open(e?.message || e?.error?.error || 'Could not request API key', '', { duration: 6000 });
        } finally {
            this.collectorConnecting = false;
        }
    }

    private async ensureAdminToken(): Promise<boolean> {
        if (this.reviewService.hasAdminToken()) {
            return true;
        }
        const pin = this.rdioScannerService.readPin();
        if (!pin) {
            return false;
        }
        try {
            const res = await fetch('/api/admin/sso', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ pin }),
            });
            if (!res.ok) {
                return false;
            }
            const data = await res.json();
            if (data?.token) {
                this.adminService.setTokenFromExternal(data.token);
                return true;
            }
        } catch {
            // ignore
        }
        return false;
    }

    setTab(tab: 'alerts' | 'preferences'): void {
        if (this.panelMode !== 'alertsAndPreferences') {
            return;
        }
        this.activeTab = tab;
        if (tab === 'alerts') {
            // Full refresh so a just-fired push (pre-alert) is not missed by since=
            this.loadAlerts(true);
            this.loadSystemAlerts();
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

    loadSystemAlerts(): void {
        this.pin = this.rdioScannerService.readPin();
        if (!this.pin) {
            this.systemAlerts = [];
            this.canViewSystemAlerts = false;
            this.loadingSystemAlerts = false;
            return;
        }

        this.loadingSystemAlerts = true;
        this.alertsService.getSystemAlerts(this.limit, this.pin).subscribe({
            next: (res) => {
                this.systemAlerts = res?.alerts || [];
                this.isSystemAdmin = !!res?.isSystemAdmin;
                this.canViewSystemAlerts = !!res?.canViewSystemAlerts;
                if (!this.canViewSystemAlerts && this.alertsViewMode !== 'mine') {
                    this.alertsViewMode = 'mine';
                }
                this.loadingSystemAlerts = false;
            },
            error: (error) => {
                console.error('Error loading system alerts:', error);
                this.systemAlerts = [];
                this.loadingSystemAlerts = false;
            },
        });
    }

    setAlertsViewMode(mode: RdioScannerAlertsViewMode): void {
        if (mode !== 'mine' && !this.canViewSystemAlerts) {
            return;
        }
        this.alertsViewMode = mode;
        if (mode === 'system' || mode === 'all') {
            this.loadSystemAlerts();
        }
        if (mode === 'mine' || mode === 'all') {
            this.loadAlerts(false);
        }
    }

    refreshAlertsTab(): void {
        if (this.alertsViewMode === 'system') {
            this.loadSystemAlerts();
            return;
        }
        if (this.alertsViewMode === 'all') {
            this.loadAlerts(true);
            this.loadSystemAlerts();
            return;
        }
        this.loadAlerts(true);
    }

    get showMyAlertsSection(): boolean {
        return this.alertsViewMode === 'mine' || this.alertsViewMode === 'all';
    }

    get showSystemAlertsSection(): boolean {
        return this.canViewSystemAlerts && (this.alertsViewMode === 'system' || this.alertsViewMode === 'all');
    }

    get alertsTabLoading(): boolean {
        if (this.alertsViewMode === 'system') {
            return this.loadingSystemAlerts;
        }
        if (this.alertsViewMode === 'all') {
            return this.loading || this.loadingSystemAlerts;
        }
        return this.loading;
    }

    get hasActiveAlertSearch(): boolean {
        return !!this.alertSearch.trim();
    }

    get filteredAlertGroups(): Array<{ key: string; alerts: RdioScannerAlert[]; latestTimestamp: number; groupType: 'tone' | 'channel' }> {
        const q = this.alertSearch.trim().toLowerCase();
        if (!q) {
            return this.allAlertGroups;
        }
        return this.allAlertGroups
            .map((group) => {
                const groupKeyMatches = group.key.toLowerCase().includes(q);
                const alerts = groupKeyMatches
                    ? group.alerts
                    : group.alerts.filter((alert) => this.alertMatchesSearch(alert, q));
                if (alerts.length === 0) {
                    return null;
                }
                return {
                    ...group,
                    alerts,
                    latestTimestamp: Math.max(...alerts.map((a) => a.createdAt || 0)),
                };
            })
            .filter((group): group is NonNullable<typeof group> => group != null)
            .sort((a, b) => b.latestTimestamp - a.latestTimestamp);
    }

    get filteredSystemAlerts(): RdioScannerSystemAlert[] {
        const q = this.alertSearch.trim().toLowerCase();
        if (!q) {
            return this.systemAlerts;
        }
        return this.systemAlerts.filter((alert) => this.systemAlertMatchesSearch(alert, q));
    }

    clearAlertSearch(): void {
        this.alertSearch = '';
    }

    private alertMatchesSearch(alert: RdioScannerAlert, q: string): boolean {
        const parts: string[] = [
            this.getAlertTypeLabel(alert),
            alert.alertType || '',
            alert.systemLabel || '',
            alert.talkgroupLabel || '',
            alert.talkgroupName || '',
            alert.transcript || '',
            alert.transcriptSnippet || '',
            alert.alertSummary || '',
            alert.matchedToneSetName || '',
            String(alert.callId ?? ''),
            String(alert.systemId ?? ''),
            String(alert.talkgroupId ?? ''),
            ...(alert.matchedToneSetNames || []),
            ...this.getKeywordsMatched(alert),
        ];
        return parts.filter(Boolean).join(' ').toLowerCase().includes(q);
    }

    private systemAlertMatchesSearch(alert: RdioScannerSystemAlert, q: string): boolean {
        const parts = [
            alert.title,
            alert.message,
            alert.alertType,
            alert.severity,
            this.getSystemAlertTypeLabel(alert),
        ];
        return parts.filter(Boolean).join(' ').toLowerCase().includes(q);
    }

    getSystemAlertTypeLabel(alert: RdioScannerSystemAlert): string {
        switch (alert.alertType) {
            case 'no_audio':
            case 'no_audio_received':
                return 'No audio';
            case 'api_key_no_audio':
                return 'API key';
            case 'tone_detection_issue':
                return 'Tone detection';
            case 'transcription_failure':
                return 'Transcription';
            case 'manual':
                return 'Notice';
            default:
                return 'System';
        }
    }

    getSystemAlertSeverityIcon(severity: string): string {
        switch (severity) {
            case 'critical':
                return '🚨';
            case 'error':
                return '❌';
            case 'warning':
                return '⚠️';
            case 'info':
                return 'ℹ️';
            default:
                return '🔔';
        }
    }

    trackBySystemAlertId(_index: number, alert: RdioScannerSystemAlert): number {
        return alert.id;
    }

    loadTranscripts(opts?: { silent?: boolean }): void {
        this.pin = this.rdioScannerService.readPin();
        if (!this.pin) {
            this.transcripts = [];
            return;
        }
        if (!opts?.silent) {
            this.loadingTranscripts = true;
            this.transcriptsStale = false;
        }
        
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
            case 'transcript':
                return 'Transcript';
            default:
                return 'Alert';
        }
    }

    getKeywordsMatched(alert: RdioScannerAlert): string[] {
        if (alert.keywordsMatched == null || alert.keywordsMatched === '') {
            return [];
        }

        let raw: unknown;
        if (typeof alert.keywordsMatched === 'string') {
            const trimmed = alert.keywordsMatched.trim();
            if (!trimmed || trimmed === '[]') {
                return [];
            }
            try {
                raw = JSON.parse(trimmed);
            } catch {
                raw = trimmed.split(/[,;]+/).map((s) => s.trim()).filter(Boolean);
            }
        } else {
            raw = alert.keywordsMatched;
        }

        if (!Array.isArray(raw)) {
            return [];
        }

        const seen = new Set<string>();
        const unique: string[] = [];
        for (const entry of raw) {
            if (typeof entry !== 'string') {
                continue;
            }
            const keyword = entry.trim();
            if (!keyword) {
                continue;
            }
            const key = keyword.toLowerCase();
            if (seen.has(key)) {
                continue;
            }
            seen.add(key);
            unique.push(keyword);
        }
        return unique;
    }

    private countKeywordsMatched(alert: RdioScannerAlert): number {
        return this.getKeywordsMatched(alert).length;
    }

    formatTimestamp(timestamp: number): string {
        const date = new Date(timestamp);
        const datePart = date.toLocaleDateString();
        const timePart = date.toLocaleTimeString();
        const spacer = '\u00A0\u00A0\u00A0'; // three non-breaking spaces
        return `${datePart}${spacer}${timePart}`;
    }

    formatAlertDate(timestamp: number): string {
        return new Date(timestamp).toLocaleDateString(undefined, {
            month: 'numeric',
            day: 'numeric',
            year: '2-digit',
        });
    }

    formatAlertTime(timestamp: number): string {
        return new Date(timestamp).toLocaleTimeString(undefined, {
            hour: 'numeric',
            minute: '2-digit',
        });
    }

    /** Single-line date + time for group headers (avoids year/time mashup). */
    formatAlertDateTime(timestamp: number): string {
        if (!timestamp) return '';
        return `${this.formatAlertDate(timestamp)} ${this.formatAlertTime(timestamp)}`;
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
        
        this.alerts.filter(alert => alert.alertType === 'keyword' || alert.alertType === 'transcript').forEach(alert => {
            const callId = Number(alert.callId);
            if (!Number.isFinite(callId)) {
                return;
            }
            // Create channel key from system + talkgroup
            const channelKey = `${alert.systemLabel || `System ${alert.systemId}`} / ${alert.talkgroupLabel || alert.talkgroupName || `Talkgroup ${alert.talkgroupId}`}`;
            
            if (!channelGrouped.has(channelKey)) {
                channelGrouped.set(channelKey, []);
            }
            const list = channelGrouped.get(channelKey)!;
            if (!list.some(a => Number(a.callId) === callId)) {
                list.push(alert);
            }
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

    hasIncidentLocation(alert: RdioScannerAlert): boolean {
        return typeof alert.incidentLat === 'number' &&
            alert.incidentLat !== 0 &&
            !!alert.incidentNature?.trim();
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

