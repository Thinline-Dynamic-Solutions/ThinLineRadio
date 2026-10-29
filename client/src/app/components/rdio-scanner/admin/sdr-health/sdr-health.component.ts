/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, ElementRef, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { DomSanitizer, SafeUrl } from '@angular/platform-browser';
import { RdioScannerAdminService } from '../admin.service';

export interface SdrHealthSource {
    apiKeyId: number;
    apiKeyIdent: string;
    systemRef: number;
    systemLabel: string;
    receivedAt: number;
    ageSeconds: number;
    stale: boolean;
    reporting: boolean;
    role: string;
    version: string;
    uploaderState: string;
    controlLost: boolean;
    tunerError: boolean;
    uploaderError: boolean;
    status: string;
    connected: boolean;
    report: any;
}

export type SdrHealthTab = 'overview' | 'tuners' | 'channels' | 'streams' | 'control';

export type SdrLiveView = 'tuners' | 'channels' | 'streams' | 'playlist' | 'preferences' | 'desktop';

export interface SdrSettingsField {
    name: string;
    label: string;
    kind: 'boolean' | 'integer' | 'number' | 'text' | 'enum' | 'enumList' | 'list' | 'json' | 'object';
    readOnly: boolean;
    value?: any;
    options?: { value: string; label: string }[];
    fields?: SdrSettingsField[];
    type?: string;
    edit?: any;
}

export interface SdrSettingsPanel {
    scope: 'tuner' | 'stream' | 'channel';
    id: string;
    title: string;
    streamType?: string;
    fields: SdrSettingsField[];
}

export interface SdrPreferenceSection {
    id: string;
    title: string;
    fields: SdrSettingsField[];
    open?: boolean;
}

export const SDR_REMOTE_LABELS: Record<string, string> = {
    desktop: 'Remote desktop',
    spectrum: 'Spectrum',
    tuners: 'Tuner settings',
    channels: 'Channels',
    aliases: 'Aliases and talkgroups',
    streams: 'Streams',
    preferences: 'User preferences',
};

export type SdrHealthTone = 'good' | 'warn' | 'bad' | 'idle';

export interface SdrHealthStats {
    tuners: number;
    tunersOk: number;
    channels: number;
    channelsOk: number;
    controlLost: number;
    streams: number;
    streamsOk: number;
}

@Component({
    selector: 'rdio-scanner-admin-sdr-health',
    styleUrls: ['./sdr-health.component.scss'],
    templateUrl: './sdr-health.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAdminSdrHealthComponent implements OnInit, OnDestroy {
    @ViewChild('spectrum') spectrum?: ElementRef<HTMLCanvasElement>;
    @ViewChild('desktopStage') desktopStage?: ElementRef<HTMLElement>;

    sources: SdrHealthSource[] = [];
    loadedAt = 0;
    selectedId: number | null = null;
    showingUploads = false;
    tab: SdrHealthTab = 'overview';
    loading = false;
    error: string | null = null;
    commandError: string | null = null;
    commanding = false;
    liveId: number | null = null;
    liveConnected = false;
    tuners: any[] = [];
    channels: any[] = [];
    streams: any[] = [];
    spectrumTunerId = '';
    desktopOpen = false;
    desktopFullscreen = false;
    desktopMuted = false;
    desktopUrl: SafeUrl | null = null;
    playlistOpen = false;
    playlistTab: 'channels' | 'aliases' | 'maps' = 'channels';
    playlistChannels: any[] = [];
    aliasLists: any[] = [];
    channelMaps: any[] = [];
    playlistOptions: any = {};
    channelQuery = '';
    draft: any = null;
    draftMode: 'new' | 'edit' | null = null;
    aliasListName = '';
    aliasQuery = '';
    aliasOffset = 0;
    aliasTotal = 0;
    aliases: any[] = [];
    aliasDraft: any = null;
    mapDraft: any = null;
    notice = '';
    liveView: SdrLiveView = 'tuners';
    permissions: Record<string, boolean> | null = null;
    streamTypes: { value: string; label: string }[] = [];
    settingsPanel: SdrSettingsPanel | null = null;
    settingsError = '';
    preferenceSections: SdrPreferenceSection[] = [];
    preferenceQuery = '';
    newStreamType = 'THINLINE_RADIO_CORE';
    newStreamName = '';
    streamAliasesFor = '';
    streamAliasList = '';
    streamAliasQuery = '';
    streamAliases: any[] = [];
    streamAliasTotal = 0;
    streamAliasOffset = 0;
    spectrumMarkers: any[] = [];
    readonly remoteLabels = SDR_REMOTE_LABELS;

    private refreshInterval: any;
    private spectrumAverage: number[] = [];
    private socket?: WebSocket;
    private spectrumBins: number[] = [];
    private spectrumRows: number[][] = [];
    private spectrumCenterHz = 0;
    private spectrumBandwidthHz = 0;
    private spectrumCursorX: number | null = null;
    private desktopObjectUrl = '';
    private desktopScreen = { originX: 0, originY: 0, width: 1, height: 1 };
    private desktopMoveAt = 0;
    private audioContext?: AudioContext;
    private audioNextTime = 0;
    private readonly onFullscreenChange = () => {
        const stage = this.desktopStage?.nativeElement;
        this.desktopFullscreen = !!stage && document.fullscreenElement === stage;
        this.cdr.markForCheck();
    };

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        private sanitizer: DomSanitizer
    ) {}

    ngOnInit(): void {
        document.addEventListener('fullscreenchange', this.onFullscreenChange);
        this.load();
        this.refreshInterval = setInterval(() => this.load(), 60000);
    }

    ngOnDestroy(): void {
        document.removeEventListener('fullscreenchange', this.onFullscreenChange);
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
        }
        this.closeLive();
    }

    get sites(): SdrHealthSource[] {
        return this.sources.filter((source) => source.role !== 'upload' && source.report?.role !== 'upload');
    }

    get uploads(): SdrHealthSource[] {
        return this.sources.filter((source) => source.role === 'upload' || source.report?.role === 'upload');
    }

    get uploadsOk(): number {
        return this.uploads.filter((source) => source.status === 'ok').length;
    }

    get selected(): SdrHealthSource | null {
        const sites = this.sites;
        return sites.find((source) => source.apiKeyId === this.selectedId) || sites[0] || null;
    }

    get totals(): SdrHealthStats & { sites: number; sitesOk: number; live: number } {
        const totals = { sites: 0, sitesOk: 0, live: 0, tuners: 0, tunersOk: 0, channels: 0, channelsOk: 0, controlLost: 0, streams: 0, streamsOk: 0 };
        for (const source of this.sites) {
            const stats = this.stats(source);
            totals.sites++;
            totals.sitesOk += this.statusTone(source.status) === 'good' ? 1 : 0;
            totals.live += source.connected ? 1 : 0;
            totals.tuners += stats.tuners;
            totals.tunersOk += stats.tunersOk;
            totals.channels += stats.channels;
            totals.channelsOk += stats.channelsOk;
            totals.controlLost += stats.controlLost;
            totals.streams += stats.streams;
            totals.streamsOk += stats.streamsOk;
        }
        return totals;
    }

    selectSite(source: SdrHealthSource): void {
        this.showingUploads = false;
        if (this.selected?.apiKeyId === source.apiKeyId) {
            this.selectedId = source.apiKeyId;
            return;
        }
        this.closeLive();
        this.selectedId = source.apiKeyId;
        if (this.tab === 'control') {
            this.tab = 'overview';
        }
    }

    showUploads(): void {
        this.closeLive();
        this.showingUploads = true;
        if (this.tab === 'control') {
            this.tab = 'overview';
        }
    }

    setTab(tab: SdrHealthTab, source: SdrHealthSource): void {
        if (this.tab === 'control' && tab !== 'control') {
            this.closeLive();
        }
        this.tab = tab;
        if (tab === 'control' && source.connected && this.liveId !== source.apiKeyId) {
            this.openLive(source);
        }
    }

    stats(source: SdrHealthSource): SdrHealthStats {
        const tuners = this.list(source, 'tuners');
        const channels = this.list(source, 'channels');
        const streams = this.list(source, 'streams');
        return {
            tuners: tuners.length,
            tunersOk: tuners.filter((tuner) => this.tunerTone(tuner) === 'good').length,
            channels: channels.length,
            channelsOk: channels.filter((channel) => this.channelTone(channel) === 'good').length,
            controlLost: channels.filter((channel) => channel.controlLost).length,
            streams: streams.length,
            streamsOk: streams.filter((stream) => this.streamTone(stream) === 'good').length,
        };
    }

    issues(source: SdrHealthSource): { tone: SdrHealthTone; text: string }[] {
        const issues: { tone: SdrHealthTone; text: string }[] = [];
        if (!source.connected) {
            issues.push({ tone: 'bad', text: 'SDRTrunk is not connected to this server.' });
        }
        if (source.stale) {
            issues.push({ tone: 'warn', text: `The last report is ${this.age(source)}.` });
        }
        for (const tuner of this.list(source, 'tuners')) {
            if (this.tunerTone(tuner) === 'bad') {
                issues.push({ tone: 'bad', text: `Tuner ${tuner.name || tuner.id}: ${tuner.error || 'error'}` });
            }
        }
        for (const channel of this.list(source, 'channels')) {
            if (channel.controlLost) {
                issues.push({ tone: 'warn', text: `${this.channelLabel(channel)} lost the control channel ${channel.secondsSinceControl}s ago.` });
            }
            if (channel.noTuner) {
                issues.push({ tone: 'bad', text: `${this.channelLabel(channel)} has no tuner.` });
            }
            if (channel.overflow) {
                issues.push({ tone: 'warn', text: `${this.channelLabel(channel)} is dropping samples.` });
            }
        }
        for (const stream of this.list(source, 'streams')) {
            if (stream.error) {
                issues.push({ tone: 'bad', text: `Stream ${stream.name}: ${stream.stateLabel || stream.state || 'error'}` });
            } else if (stream.enabled === false) {
                issues.push({ tone: 'idle', text: `Stream ${stream.name} is turned off.` });
            }
        }
        return issues;
    }

    statusTone(status: string): SdrHealthTone {
        switch (status) {
            case 'ok':
            case 'healthy':
                return 'good';
            case 'control_lost':
            case 'not_reporting':
            case 'not_checking_in':
            case 'sample_overflow':
                return 'warn';
            case 'offline':
            case 'tuner_error':
            case 'uploader_error':
                return 'bad';
            default:
                return 'idle';
        }
    }

    tunerTone(tuner: any): SdrHealthTone {
        if (tuner?.status === 'ERROR' || tuner?.error) {
            return 'bad';
        }
        if (tuner?.status === 'ENABLED') {
            return 'good';
        }
        return 'idle';
    }

    channelTone(channel: any): SdrHealthTone {
        if (channel?.noTuner) {
            return 'bad';
        }
        if (channel?.controlLost || channel?.overflow) {
            return 'warn';
        }
        return channel?.processing ? 'good' : 'idle';
    }

    streamTone(stream: any): SdrHealthTone {
        if (stream?.error) {
            return 'bad';
        }
        if (stream?.enabled === false) {
            return 'idle';
        }
        return stream?.state === 'CONNECTED' ? 'good' : 'warn';
    }

    ratioTone(ok: number, total: number): SdrHealthTone {
        if (!total) {
            return 'idle';
        }
        return ok === total ? 'good' : ok === 0 ? 'bad' : 'warn';
    }

    async load(): Promise<void> {
        this.loading = true;
        this.error = null;
        this.cdr.markForCheck();

        try {
            const response = await this.adminService.getSdrtrunkHealth();
            this.sources = response.sources || [];
            this.loadedAt = Date.now();
        } catch (error: any) {
            this.error = error.message || 'Failed to load SDR health';
            this.sources = [];
        } finally {
            this.loading = false;
            this.cdr.markForCheck();
        }
    }

    statusLabel(status: string): string {
        switch (status) {
            case 'ok':
                return 'Upload OK';
            case 'healthy':
                return 'Healthy';
            case 'offline':
                return 'Offline';
            case 'not_reporting':
                return 'Not reporting';
            case 'not_checking_in':
                return 'Not checking in';
            case 'control_lost':
                return 'Control lost';
            case 'tuner_error':
                return 'Tuner error';
            case 'uploader_error':
                return 'Uploader error';
            case 'sample_overflow':
                return 'Sample overflow';
            default:
                return status || 'Unknown';
        }
    }

    age(source: SdrHealthSource): string {
        const seconds = Math.max(0, source.ageSeconds || 0);
        if (seconds < 60) {
            return `${seconds}s ago`;
        }
        if (seconds < 3600) {
            return `${Math.floor(seconds / 60)}m ago`;
        }
        return `${Math.floor(seconds / 3600)}h ago`;
    }

    uptime(source: SdrHealthSource): string {
        const seconds = Number(source.report?.uptimeSeconds || 0);
        if (!seconds) {
            return '—';
        }
        const hours = Math.floor(seconds / 3600);
        const minutes = Math.floor((seconds % 3600) / 60);
        if (hours > 0) {
            return `${hours}h ${minutes}m`;
        }
        return `${minutes}m`;
    }

    mhz(hz: number): string {
        if (!hz) {
            return '—';
        }
        return `${(hz / 1000000).toFixed(4)} MHz`;
    }

    ppm(value: any): string {
        const ppm = Number(value);
        if (!Number.isFinite(ppm) || value === '' || value == null) {
            return '—';
        }
        return ppm.toFixed(2);
    }

    list(source: SdrHealthSource, key: string): any[] {
        const value = source.report?.[key];
        return Array.isArray(value) ? value : [];
    }

    channelLabel(channel: any): string {
        return [channel?.system, channel?.site, channel?.name].filter((part) => !!part).join(' / ') || 'Channel';
    }

    openLive(source: SdrHealthSource): void {
        this.closeLive();
        this.liveId = source.apiKeyId;
        this.clearSpectrum();
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const port = /^42\d{2}$/.test(window.location.port) ? '3000' : window.location.port;
        const host = port ? `${window.location.hostname}:${port}` : window.location.hostname;
        this.socket = new WebSocket(`${protocol}//${host}/api/admin/sdrtrunk-live`);
        this.socket.onopen = () => {
            this.socket?.send(JSON.stringify({ token: this.adminService.getToken(), apiKeyId: source.apiKeyId }));
        };
        this.socket.onmessage = (event) => this.onLiveMessage(event.data);
        this.socket.onclose = () => {
            this.liveConnected = false;
            this.cdr.markForCheck();
        };
    }

    closeLive(): void {
        this.socket?.close();
        this.socket = undefined;
        this.liveId = null;
        this.liveConnected = false;
        this.tuners = [];
        this.channels = [];
        this.streams = [];
        this.clearSpectrum();
        this.clearDesktop();
        this.playlistOpen = false;
        this.draft = null;
        this.aliasDraft = null;
        this.mapDraft = null;
        this.playlistChannels = [];
        this.aliases = [];
        this.notice = '';
        this.liveView = 'tuners';
        this.permissions = null;
        this.settingsPanel = null;
        this.settingsError = '';
        this.preferenceSections = [];
        this.streamAliasesFor = '';
        this.streamAliases = [];
    }

    allowed(key: string): boolean {
        return !this.permissions || this.permissions[key] !== false;
    }

    get blockedRemote(): string[] {
        if (!this.permissions) {
            return [];
        }
        return Object.keys(this.permissions).filter((key) => this.permissions?.[key] === false).map((key) => this.remoteLabels[key] || key);
    }

    setLiveView(view: SdrLiveView): void {
        if (this.liveView === 'desktop' && view !== 'desktop' && this.desktopOpen) {
            this.toggleDesktop();
        }
        if (view !== 'tuners' && this.spectrumTunerId) {
            this.liveCommand('spectrum', { tunerId: this.spectrumTunerId, enabled: false });
            this.clearSpectrum();
        }
        this.liveView = view;
        if (view === 'desktop' && !this.desktopOpen && this.allowed('desktop')) {
            this.toggleDesktop();
        }
        if (view === 'playlist' && !this.playlistOpen) {
            this.openPlaylist();
        }
        if (view === 'preferences') {
            this.liveCommand('preferences');
        }
    }

    openTunerSettings(tuner: any): void {
        this.settingsError = '';
        this.liveCommand('tunerSettings', { tunerId: tuner.id });
    }

    openStreamSettings(stream: any): void {
        this.settingsError = '';
        this.liveCommand('streamSettings', { name: stream.name });
    }

    openChannelSettings(channel: any): void {
        this.settingsError = '';
        this.liveCommand('channelSettings', { channelId: channel.id });
    }

    closeSettings(): void {
        this.settingsPanel = null;
        this.settingsError = '';
    }

    settingsAllowed(panel: SdrSettingsPanel | null): boolean {
        if (!panel) {
            return false;
        }
        return this.allowed(panel.scope === 'tuner' ? 'tuners' : panel.scope === 'stream' ? 'streams' : 'channels');
    }

    saveSettings(): void {
        const panel = this.settingsPanel;
        if (!panel) {
            return;
        }
        let values: Record<string, any>;
        try {
            values = this.collectSettings(panel.fields);
        } catch (error: any) {
            this.settingsError = error?.message || 'Check the highlighted values';
            return;
        }
        this.settingsError = '';
        this.notice = '';
        if (panel.scope === 'tuner') {
            this.liveCommand('saveTunerSettings', { tunerId: panel.id, values });
        } else if (panel.scope === 'stream') {
            this.liveCommand('saveStreamSettings', { name: panel.id, values });
        } else {
            this.liveCommand('saveChannelSettings', { channelId: Number(panel.id), values });
        }
    }

    deleteStream(): void {
        const panel = this.settingsPanel;
        if (panel?.scope !== 'stream' || !confirm(`Delete the stream "${panel.id}" from this SDRTrunk?`)) {
            return;
        }
        this.liveCommand('deleteStream', { name: panel.id });
        this.settingsPanel = null;
    }

    addStream(): void {
        const name = this.newStreamName.trim();
        if (!name) {
            return;
        }
        this.liveCommand('addStream', { streamType: this.newStreamType, name });
        this.newStreamName = '';
    }

    savePreferenceSection(section: SdrPreferenceSection): void {
        let values: Record<string, any>;
        try {
            values = this.collectSettings(section.fields);
        } catch (error: any) {
            this.commandError = error?.message || 'Check the highlighted values';
            return;
        }
        this.liveCommand('savePreferences', { section: section.id, values });
    }

    get visiblePreferenceSections(): SdrPreferenceSection[] {
        const query = this.preferenceQuery.trim().toLowerCase();
        if (!query) {
            return this.preferenceSections;
        }
        return this.preferenceSections.filter((section) =>
            section.title.toLowerCase().includes(query) || section.fields.some((field) => field.label.toLowerCase().includes(query))
        );
    }

    secretField(field: SdrSettingsField): boolean {
        return /password|apikey|secret|token/i.test(field.name);
    }

    toggleEnumList(field: SdrSettingsField, value: string, checked: boolean): void {
        const current: string[] = Array.isArray(field.edit) ? field.edit : [];
        field.edit = checked ? [...current.filter((item) => item !== value), value] : current.filter((item) => item !== value);
    }

    private prepareFields(fields: SdrSettingsField[]): SdrSettingsField[] {
        for (const field of fields || []) {
            if (field.kind === 'object') {
                this.prepareFields(field.fields || []);
            } else if (field.kind === 'list') {
                field.edit = (Array.isArray(field.value) ? field.value : []).join('\n');
            } else if (field.kind === 'json') {
                field.edit = JSON.stringify(field.value ?? null, null, 2);
            } else if (field.kind === 'enumList') {
                field.edit = Array.isArray(field.value) ? [...field.value] : [];
            } else {
                field.edit = field.value;
            }
        }
        return fields || [];
    }

    private collectSettings(fields: SdrSettingsField[]): Record<string, any> {
        const values: Record<string, any> = {};
        for (const field of fields) {
            if (field.kind === 'object') {
                values[field.name] = this.collectSettings(field.fields || []);
                continue;
            }
            if (field.readOnly) {
                continue;
            }
            switch (field.kind) {
                case 'integer':
                case 'number': {
                    const number = Number(field.edit);
                    if (field.edit === '' || field.edit === null || !Number.isFinite(number)) {
                        throw new Error(`${field.label} needs a number`);
                    }
                    values[field.name] = field.kind === 'integer' ? Math.round(number) : number;
                    break;
                }
                case 'list': {
                    const numeric = Array.isArray(field.value) && field.value.length > 0 && field.value.every((item: any) => typeof item === 'number');
                    values[field.name] = String(field.edit || '').split(/[\n,]+/).map((item) => item.trim()).filter((item) => item.length > 0)
                        .map((item) => numeric ? Number(item) : item);
                    break;
                }
                case 'json':
                    try {
                        values[field.name] = JSON.parse(field.edit || 'null');
                    } catch {
                        throw new Error(`${field.label} is not valid JSON`);
                    }
                    break;
                default:
                    values[field.name] = field.edit;
            }
        }
        return values;
    }

    openStreamAliases(stream: any): void {
        this.streamAliasesFor = stream.name;
        this.streamAliases = [];
        this.streamAliasTotal = 0;
        this.streamAliasOffset = 0;
        if (!this.streamAliasList && this.aliasLists.length) {
            this.streamAliasList = this.aliasLists[0].name;
        }
        this.loadStreamAliases(0);
    }

    loadStreamAliases(offset = 0): void {
        this.liveCommand('aliases', { purpose: 'streams', list: this.streamAliasList, query: this.streamAliasQuery, offset });
    }

    streamed(alias: any): boolean {
        return (alias.streams || []).includes(this.streamAliasesFor);
    }

    setAliasStreamed(alias: any, enabled: boolean): void {
        alias.streams = enabled ? [...(alias.streams || []), this.streamAliasesFor] : (alias.streams || []).filter((name: string) => name !== this.streamAliasesFor);
        this.liveCommand('setAliasStream', {
            purpose: 'streams', id: alias.id, stream: this.streamAliasesFor, enabled,
            list: this.streamAliasList, query: this.streamAliasQuery, offset: this.streamAliasOffset,
        });
    }

    setAllAliasesStreamed(enabled: boolean): void {
        const scope = this.streamAliasQuery ? `the ${this.streamAliasTotal} matching aliases` : `all ${this.streamAliasTotal} aliases in ${this.streamAliasList || 'every list'}`;
        if (!confirm(`${enabled ? 'Upload' : 'Stop uploading'} ${scope} on ${this.streamAliasesFor}?`)) {
            return;
        }
        this.liveCommand('setAliasStreamAll', {
            purpose: 'streams', stream: this.streamAliasesFor, enabled,
            list: this.streamAliasList, query: this.streamAliasQuery, offset: this.streamAliasOffset,
        });
    }

    aliasStreamChecked(name: string): boolean {
        return (this.aliasDraft?.streams || []).includes(name);
    }

    toggleAliasDraftStream(name: string, checked: boolean): void {
        const current: string[] = this.aliasDraft?.streams || [];
        this.aliasDraft.streams = checked ? [...current.filter((item) => item !== name), name] : current.filter((item) => item !== name);
    }

    toggleChannel(channel: any): void {
        const enabled = !channel.processing;
        const apply = (row: any) => {
            if (row && row.id === channel.id) {
                row.processing = enabled;
            }
        };
        apply(channel);
        this.channels.forEach(apply);
        this.playlistChannels.forEach(apply);
        apply(this.draft);
        this.liveCommand('setChannel', {
            channelId: channel.id,
            system: channel.system,
            site: channel.site,
            name: channel.name,
            enabled,
        });
        this.cdr.markForCheck();
    }

    liveCommand(command: string, extra: Record<string, unknown> = {}): void {
        if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
            return;
        }
        this.socket.send(JSON.stringify({ type: 'command', command, ...extra }));
    }

    showSpectrum(tuner: any): void {
        if (this.spectrumTunerId === tuner.id) {
            this.liveCommand('spectrum', { tunerId: tuner.id, enabled: false });
            this.clearSpectrum();
            return;
        }
        this.clearSpectrum();
        this.spectrumTunerId = tuner.id;
        this.liveCommand('spectrum', { tunerId: tuner.id, enabled: true });
    }

    moveSpectrumCursor(event: MouseEvent): void {
        const canvas = this.spectrum?.nativeElement;
        if (!canvas) {
            return;
        }
        const bounds = canvas.getBoundingClientRect();
        this.spectrumCursorX = event.clientX - bounds.left;
        this.drawSpectrum();
    }

    clearSpectrumCursor(): void {
        this.spectrumCursorX = null;
        this.drawSpectrum();
    }

    toggleDesktop(): void {
        this.desktopOpen = !this.desktopOpen;
        if (!this.desktopOpen) {
            this.clearDesktop();
        } else {
            this.ensureDesktopAudio();
        }
        this.liveCommand('desktop', { enabled: this.desktopOpen });
    }

    toggleDesktopMute(): void {
        this.desktopMuted = !this.desktopMuted;
        if (this.desktopMuted) {
            this.audioNextTime = 0;
        } else {
            this.ensureDesktopAudio();
        }
    }

    toggleDesktopFullscreen(): void {
        const stage = this.desktopStage?.nativeElement;
        if (!stage) {
            return;
        }
        if (document.fullscreenElement === stage) {
            document.exitFullscreen();
            return;
        }
        stage.requestFullscreen();
    }

    desktopPointer(event: PointerEvent, kind: 'down' | 'up' | 'move'): void {
        const image = event.currentTarget as HTMLElement;
        if (kind === 'down') {
            image.focus();
            image.setPointerCapture?.(event.pointerId);
        }
        if (kind === 'move') {
            const now = Date.now();
            if (now - this.desktopMoveAt < 40) {
                return;
            }
            this.desktopMoveAt = now;
        }
        event.preventDefault();
        const point = this.desktopPoint(event);
        if (!point) {
            return;
        }
        this.desktopSend({ kind, x: point.x, y: point.y, button: event.button });
    }

    desktopWheel(event: WheelEvent): void {
        event.preventDefault();
        const point = this.desktopPoint(event);
        if (!point) {
            return;
        }
        this.desktopSend({ kind: 'wheel', x: point.x, y: point.y, delta: event.deltaY });
    }

    desktopKey(event: KeyboardEvent, down: boolean): void {
        if (event.repeat) {
            return;
        }
        event.preventDefault();
        this.desktopSend({ kind: 'key', code: event.code, down });
    }

    private desktopPoint(event: MouseEvent): { x: number; y: number } | null {
        const image = event.currentTarget as HTMLImageElement;
        const bounds = image.getBoundingClientRect();
        const naturalWidth = image.naturalWidth || this.desktopScreen.width;
        const naturalHeight = image.naturalHeight || this.desktopScreen.height;
        if (!bounds.width || !bounds.height || !naturalWidth || !naturalHeight) {
            return null;
        }
        const scale = Math.min(bounds.width / naturalWidth, bounds.height / naturalHeight);
        const drawnWidth = naturalWidth * scale;
        const drawnHeight = naturalHeight * scale;
        const localX = event.clientX - bounds.left - (bounds.width - drawnWidth) / 2;
        const localY = event.clientY - bounds.top - (bounds.height - drawnHeight) / 2;
        if (localX < 0 || localY < 0 || localX > drawnWidth || localY > drawnHeight) {
            return null;
        }
        return {
            x: localX / drawnWidth * this.desktopScreen.width,
            y: localY / drawnHeight * this.desktopScreen.height,
        };
    }

    private desktopSend(body: Record<string, unknown>): void {
        if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
            return;
        }
        this.socket.send(JSON.stringify({ type: 'input', ...body }));
    }

    private ensureDesktopAudio(): void {
        if (!this.audioContext) {
            this.audioContext = new AudioContext();
        }
        void this.audioContext.resume();
    }

    private stopDesktopAudio(): void {
        this.audioNextTime = 0;
        const context = this.audioContext;
        this.audioContext = undefined;
        if (context) {
            void context.close();
        }
    }

    private playDesktopAudio(pcm: string, rate: number, channels: number): void {
        if (this.desktopMuted) {
            return;
        }
        this.ensureDesktopAudio();
        const context = this.audioContext;
        if (!context) {
            return;
        }
        const bytes = Uint8Array.from(atob(pcm), (char) => char.charCodeAt(0));
        const frameSize = Math.max(1, channels) * 2;
        const frames = Math.floor(bytes.length / frameSize);
        if (!frames) {
            return;
        }
        const buffer = context.createBuffer(channels, frames, rate);
        const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
        for (let channel = 0; channel < channels; channel++) {
            const data = buffer.getChannelData(channel);
            for (let index = 0; index < frames; index++) {
                data[index] = view.getInt16((index * channels + channel) * 2, true) / 32768;
            }
        }
        const source = context.createBufferSource();
        source.buffer = buffer;
        source.connect(context.destination);
        const now = context.currentTime;
        if (this.audioNextTime < now + 0.02) {
            this.audioNextTime = now + 0.08;
        }
        source.start(this.audioNextTime);
        this.audioNextTime += buffer.duration;
        if (this.audioNextTime > now + 1) {
            this.audioNextTime = now + 0.08;
        }
    }

    private clearDesktop(): void {
        if (this.desktopStage && document.fullscreenElement === this.desktopStage.nativeElement) {
            document.exitFullscreen();
        }
        this.stopDesktopAudio();
        this.desktopOpen = false;
        this.desktopFullscreen = false;
        this.desktopUrl = null;
        if (this.desktopObjectUrl) {
            URL.revokeObjectURL(this.desktopObjectUrl);
            this.desktopObjectUrl = '';
        }
    }

    private clearSpectrum(): void {
        this.spectrumTunerId = '';
        this.spectrumBins = [];
        this.spectrumAverage = [];
        this.spectrumMarkers = [];
        this.spectrumRows = [];
        this.spectrumCenterHz = 0;
        this.spectrumBandwidthHz = 0;
        this.spectrumCursorX = null;
    }

    applyTuner(tuner: any, field: 'ppm' | 'frequency'): void {
        if (field === 'ppm') {
            this.liveCommand('setPpm', { tunerId: tuner.id, ppm: Number(tuner.ppm) });
            return;
        }
        this.liveCommand('setFrequency', { tunerId: tuner.id, frequencyHz: Math.round(Number(tuner.frequencyMHz) * 1000000) });
    }

    get visibleChannels(): any[] {
        const query = this.channelQuery.trim().toLowerCase();
        if (!query) {
            return this.playlistChannels;
        }
        return this.playlistChannels.filter((channel) =>
            `${channel.system} ${channel.site} ${channel.name} ${channel.decoderLabel || channel.decoder}`.toLowerCase().includes(query)
        );
    }

    openPlaylist(): void {
        this.playlistOpen = !this.playlistOpen;
        if (this.playlistOpen) {
            this.liveCommand('playlist');
        }
    }

    shows(field: string): boolean {
        const decoder = this.draft?.decoder;
        const fields: Record<string, string[]> = {
            modulation: ['P25_PHASE1'],
            ignoreData: ['P25_PHASE1', 'P25_PHASE2', 'DMR'],
            pool: ['P25_PHASE1', 'P25_PHASE2', 'DMR', 'NXDN', 'MPT1327'],
            scramble: ['P25_PHASE2'],
            bandwidth: ['NBFM', 'AM'],
            talkgroup: ['NBFM', 'AM'],
            squelch: ['AM'],
            audioFilter: ['NBFM'],
            timeslots: ['DMR'],
            nxdn: ['NXDN'],
            channelMap: ['MPT1327'],
            direction: ['LTR', 'LTR_NET'],
            nxdnMode: ['NXDN'],
        };
        return (fields[field] || []).includes(decoder);
    }

    bandwidthChoices(): any[] {
        const key = this.draft?.decoder === 'AM' ? 'AM' : 'NBFM';
        return this.playlistOptions?.bandwidths?.[key] || [];
    }

    newChannel(): void {
        this.draftMode = 'new';
        this.notice = '';
        this.draft = {
            id: 0,
            system: '',
            site: '',
            name: '',
            aliasList: '',
            autoStart: false,
            order: 0,
            decoder: 'P25_PHASE1',
            frequencyText: '',
            preferredTuner: '',
            rotationDelay: 500,
            modulation: 'C4FM',
            ignoreDataCalls: false,
            trafficChannelPoolSize: 20,
            autoDetectScramble: true,
            bandwidth: 'BW_12_5',
            talkgroup: 1,
            squelchThreshold: -60,
            squelchAutoTrack: false,
            audioFilter: true,
            channelMap: '',
            messageDirection: 'OSW',
            sync: 'NORMAL',
            callTimeout: 10,
            transmissionMode: 'M9600',
            timeslots: [],
            nxdnChannels: [],
        };
    }

    editChannel(channel: any): void {
        this.draftMode = 'edit';
        this.notice = '';
        this.draft = {
            ...JSON.parse(JSON.stringify(channel)),
            frequencyText: (channel.frequencies || []).map((hz: number) => (hz / 1000000).toFixed(4)).join('\n'),
            timeslots: (channel.timeslots || []).map((row: any) => ({
                number: row.number,
                downlinkMHz: row.downlinkHz ? (row.downlinkHz / 1000000).toFixed(4) : '',
                uplinkMHz: row.uplinkHz ? (row.uplinkHz / 1000000).toFixed(4) : '',
            })),
            nxdnChannels: (channel.nxdnChannels || []).map((row: any) => ({
                channel: row.channel,
                downlinkMHz: row.downlinkHz ? (row.downlinkHz / 1000000).toFixed(4) : '',
                uplinkMHz: row.uplinkHz ? (row.uplinkHz / 1000000).toFixed(4) : '',
            })),
        };
    }

    saveDraft(): void {
        if (!this.draft) {
            return;
        }
        const frequencies = String(this.draft.frequencyText || '').split(/[\s,]+/)
            .map((part) => part.trim())
            .filter((part) => part.length > 0)
            .map((part) => Math.round(Number(part) * 1000000))
            .filter((hz) => Number.isFinite(hz) && hz > 0);
        this.liveCommand(this.draftMode === 'new' ? 'addChannel' : 'saveChannel', {
            channelId: this.draft.id,
            system: this.draft.system || '',
            site: this.draft.site || '',
            name: this.draft.name || '',
            aliasList: this.draft.aliasList || '',
            autoStart: !!this.draft.autoStart,
            order: Number(this.draft.order) || 0,
            decoder: this.draft.decoder,
            frequencies,
            preferredTuner: this.draft.preferredTuner || '',
            rotationDelay: Number(this.draft.rotationDelay) || 500,
            modulation: this.draft.modulation,
            ignoreDataCalls: !!this.draft.ignoreDataCalls,
            trafficChannelPoolSize: Number(this.draft.trafficChannelPoolSize) || 0,
            autoDetectScramble: !!this.draft.autoDetectScramble,
            bandwidth: this.draft.bandwidth,
            talkgroup: Number(this.draft.talkgroup) || 1,
            squelchThreshold: Number(this.draft.squelchThreshold) || 0,
            squelchAutoTrack: !!this.draft.squelchAutoTrack,
            audioFilter: !!this.draft.audioFilter,
            channelMap: this.draft.channelMap || '',
            messageDirection: this.draft.messageDirection,
            sync: this.draft.sync,
            callTimeout: Number(this.draft.callTimeout) || 0,
            transmissionMode: this.draft.transmissionMode,
            timeslots: (this.draft.timeslots || []).map((row: any) => ({
                number: Number(row.number) || 1,
                downlinkHz: Math.round(Number(row.downlinkMHz) * 1000000) || 0,
                uplinkHz: Math.round(Number(row.uplinkMHz) * 1000000) || 0,
            })),
            nxdnChannels: (this.draft.nxdnChannels || []).map((row: any) => ({
                channel: Number(row.channel) || 1,
                downlinkHz: Math.round(Number(row.downlinkMHz) * 1000000) || 0,
                uplinkHz: Math.round(Number(row.uplinkMHz) * 1000000) || 0,
            })),
        });
    }

    deleteDraft(): void {
        if (this.draft?.id) {
            this.liveCommand('deleteChannel', { channelId: this.draft.id });
        }
    }

    cloneDraft(): void {
        if (this.draft?.id) {
            this.liveCommand('cloneChannel', { channelId: this.draft.id });
        }
    }

    addRow(kind: 'timeslots' | 'nxdnChannels'): void {
        if (!this.draft) {
            return;
        }
        this.draft[kind] = [...(this.draft[kind] || []), kind === 'timeslots'
            ? { number: (this.draft.timeslots?.length || 0) + 1, downlinkMHz: '', uplinkMHz: '' }
            : { channel: (this.draft.nxdnChannels?.length || 0) + 1, downlinkMHz: '', uplinkMHz: '' }];
    }

    removeRow(kind: 'timeslots' | 'nxdnChannels', index: number): void {
        this.draft[kind] = this.draft[kind].filter((_: any, row: number) => row !== index);
    }

    loadAliases(offset = 0): void {
        if (!this.aliasListName) {
            return;
        }
        this.liveCommand('aliases', { list: this.aliasListName, query: this.aliasQuery, offset });
    }

    newAlias(): void {
        this.aliasDraft = {
            id: '',
            list: this.aliasListName,
            name: '',
            group: '',
            record: true,
            priority: 100,
            talkgroups: [{ kind: 'talkgroup', protocol: 'APCO25', value: '', min: '', max: '' }],
            streams: [],
            extras: 0,
        };
    }

    editAlias(alias: any): void {
        this.aliasDraft = JSON.parse(JSON.stringify(alias));
        if (!this.aliasDraft.talkgroups?.length) {
            this.aliasDraft.talkgroups = [{ kind: 'talkgroup', protocol: 'APCO25', value: '', min: '', max: '' }];
        }
    }

    saveAlias(): void {
        if (!this.aliasDraft) {
            return;
        }
        this.liveCommand(this.aliasDraft.id ? 'saveAlias' : 'saveAlias', {
            id: this.aliasDraft.id || '',
            list: this.aliasDraft.list || this.aliasListName,
            name: this.aliasDraft.name || '',
            group: this.aliasDraft.group || '',
            record: !!this.aliasDraft.record,
            priority: Number(this.aliasDraft.priority) || 100,
            streams: this.aliasDraft.streams || [],
            query: this.aliasQuery,
            offset: this.aliasOffset,
            talkgroups: (this.aliasDraft.talkgroups || []).filter((row: any) => row.value || row.min).map((row: any) => ({
                kind: row.kind || 'talkgroup',
                protocol: row.protocol || 'APCO25',
                value: Number(row.value) || 0,
                min: Number(row.min) || Number(row.value) || 0,
                max: Number(row.max) || Number(row.value) || 0,
            })),
        });
        if (!this.aliasListName && this.aliasDraft.list) {
            this.aliasListName = this.aliasDraft.list;
        }
    }

    deleteAlias(): void {
        if (this.aliasDraft?.id) {
            this.liveCommand('deleteAlias', { id: this.aliasDraft.id, list: this.aliasListName, query: this.aliasQuery, offset: this.aliasOffset });
        }
    }

    newMap(): void {
        this.mapDraft = { originalName: '', name: '', ranges: [{ first: 1, last: 511, baseMHz: '851.0000', stepKHz: 12.5 }] };
    }

    editMap(map: any): void {
        this.mapDraft = {
            originalName: map.name,
            name: map.name,
            ranges: (map.ranges || []).map((range: any) => ({
                first: range.first,
                last: range.last,
                baseMHz: (range.baseHz / 1000000).toFixed(4),
                stepKHz: range.stepHz / 1000,
            })),
        };
    }

    saveMap(): void {
        if (!this.mapDraft) {
            return;
        }
        this.liveCommand('saveChannelMap', {
            originalName: this.mapDraft.originalName || '',
            name: this.mapDraft.name || '',
            ranges: (this.mapDraft.ranges || []).map((range: any) => ({
                first: Number(range.first) || 0,
                last: Number(range.last) || 0,
                baseHz: Math.round(Number(range.baseMHz) * 1000000) || 0,
                stepHz: Math.round(Number(range.stepKHz) * 1000) || 0,
            })),
        });
    }

    deleteMap(): void {
        if (this.mapDraft?.originalName) {
            this.liveCommand('deleteChannelMap', { name: this.mapDraft.originalName });
        }
    }

    talkgroupLabel(alias: any): string {
        return (alias.talkgroups || []).map((row: any) => row.kind === 'range' ? `${row.min}-${row.max}` : `${row.value}`).join(', ') || '—';
    }

    private onLiveMessage(raw: string): void {
        let message: any;
        try {
            message = JSON.parse(raw);
        } catch {
            return;
        }
        if (message.type === 'welcome') {
            this.liveConnected = true;
            this.liveCommand('playlist');
        } else if (message.type === 'state') {
            this.tuners = (message.tuners || []).map((tuner: any) => ({
                ...tuner,
                frequencyMHz: tuner.frequencyHz ? (tuner.frequencyHz / 1000000).toFixed(4) : '',
            }));
            this.channels = message.channels || [];
            this.streams = message.streams || [];
            this.permissions = message.permissions || null;
            this.streamTypes = message.streamTypes || this.streamTypes;
            this.liveConnected = true;
        } else if (message.type === 'spectrum' && message.tunerId === this.spectrumTunerId) {
            const raw: number[] = message.bins || [];
            this.spectrumBins = message.format === 'db' ? raw : raw.map((value) => value / 255 * 120 - 120);
            this.spectrumMarkers = message.channels || [];
            if (this.spectrumAverage.length !== this.spectrumBins.length) {
                this.spectrumAverage = [...this.spectrumBins];
            } else {
                for (let index = 0; index < this.spectrumBins.length; index++) {
                    this.spectrumAverage[index] += (this.spectrumBins[index] - this.spectrumAverage[index]) * 0.35;
                }
            }
            this.spectrumRows.unshift(this.spectrumBins);
            if (this.spectrumRows.length > 200) {
                this.spectrumRows.pop();
            }
            this.spectrumCenterHz = Number(message.centerHz) || 0;
            this.spectrumBandwidthHz = Number(message.bandwidthHz) || 0;
            this.drawSpectrum();
        } else if (message.type === 'desktop' && message.image) {
            this.desktopScreen = {
                originX: Number(message.originX) || 0,
                originY: Number(message.originY) || 0,
                width: Number(message.screenWidth) || 1,
                height: Number(message.screenHeight) || 1,
            };
            const bytes = Uint8Array.from(atob(message.image), (char) => char.charCodeAt(0));
            const next = URL.createObjectURL(new Blob([bytes], { type: 'image/jpeg' }));
            if (this.desktopObjectUrl) {
                URL.revokeObjectURL(this.desktopObjectUrl);
            }
            this.desktopObjectUrl = next;
            this.desktopUrl = this.sanitizer.bypassSecurityTrustUrl(next);
        } else if (message.type === 'audio' && this.desktopOpen && message.pcm) {
            this.playDesktopAudio(message.pcm, Number(message.rate) || 8000, Number(message.channels) || 1);
        } else if (message.type === 'playlist') {
            this.playlistChannels = message.channels || [];
            this.aliasLists = message.aliasLists || [];
            this.channelMaps = message.channelMaps || [];
            this.playlistOptions = message;
        } else if (message.type === 'settings') {
            if (message.scope === 'preferences') {
                const open = new Set(this.preferenceSections.filter((section) => section.open).map((section) => section.id));
                this.preferenceSections = (message.sections || []).map((section: any) => ({
                    ...section,
                    open: open.has(section.id),
                    fields: this.prepareFields(section.fields || []),
                }));
            } else {
                this.settingsPanel = {
                    scope: message.scope,
                    id: String(message.id),
                    title: message.title || String(message.id),
                    streamType: message.streamType,
                    fields: this.prepareFields(message.fields || []),
                };
            }
        } else if (message.type === 'aliases' && message.purpose === 'streams') {
            this.streamAliases = message.aliases || [];
            this.streamAliasTotal = message.total || 0;
            this.streamAliasOffset = message.offset || 0;
        } else if (message.type === 'aliases') {
            this.aliases = message.aliases || [];
            this.aliasTotal = message.total || 0;
            this.aliasOffset = message.offset || 0;
            if (message.list) {
                this.aliasListName = message.list;
            }
        } else if (message.type === 'result' && message.ok === false) {
            this.notice = '';
            this.commandError = message.error || 'SDRTrunk rejected the command';
            if (/Settings$/.test(message.command || '')) {
                this.settingsError = this.commandError || '';
            }
        } else if (message.type === 'result' && message.ok === true) {
            this.commandError = null;
            if (['saveTunerSettings', 'saveStreamSettings', 'saveChannelSettings', 'savePreferences', 'addStream', 'deleteStream'].includes(message.command)) {
                this.notice = message.command === 'savePreferences' ? 'Preferences saved on SDRTrunk' : 'Saved on SDRTrunk';
            }
            if (['saveChannel', 'addChannel', 'deleteChannel', 'cloneChannel', 'saveAlias', 'deleteAlias', 'saveChannelMap', 'deleteChannelMap'].includes(message.command)) {
                this.notice = 'Saved to the SDRTrunk playlist';
                if (message.command === 'deleteChannel') {
                    this.draft = null;
                    this.draftMode = null;
                }
                if (message.command === 'deleteAlias') {
                    this.aliasDraft = null;
                }
                if (message.command === 'deleteChannelMap') {
                    this.mapDraft = null;
                }
            }
        } else if (message.type === 'error') {
            this.commandError = message.error || 'Live session failed';
        }
        this.cdr.markForCheck();
    }

    private drawSpectrum(): void {
        const canvas = this.spectrum?.nativeElement;
        const bins = this.spectrumAverage.length === this.spectrumBins.length ? this.spectrumAverage : this.spectrumBins;
        if (!canvas || bins.length < 2) {
            return;
        }
        const dpr = window.devicePixelRatio || 1;
        const width = Math.floor(canvas.clientWidth);
        if (width < 10) {
            return;
        }
        const height = 380;
        const spectrumHeight = 160;
        const axis = 22;
        const waterfallTop = spectrumHeight;
        const waterfallHeight = height - spectrumHeight - axis;
        const plotBottom = height - axis;
        canvas.width = Math.floor(width * dpr);
        canvas.height = Math.floor(height * dpr);
        const context = canvas.getContext('2d');
        if (!context) {
            return;
        }
        context.setTransform(dpr, 0, 0, dpr, 0, 0);
        context.fillStyle = '#000';
        context.fillRect(0, 0, width, height);
        this.drawWaterfall(context, width, waterfallTop, waterfallHeight);

        const centerHz = this.spectrumCenterHz;
        const bandwidthHz = this.spectrumBandwidthHz;
        const ticks = centerHz && bandwidthHz ? this.frequencyTicks(centerHz, bandwidthHz, width) : [];
        context.strokeStyle = 'rgba(192,192,192,0.3)';
        context.lineWidth = 1;
        ticks.forEach((tick) => {
            context.beginPath();
            context.moveTo(tick.x + 0.5, 0);
            context.lineTo(tick.x + 0.5, spectrumHeight);
            context.stroke();
        });
        this.drawSpectrumLine(context, bins, width, spectrumHeight);
        this.drawChannelMarkers(context, width, spectrumHeight, plotBottom, centerHz, bandwidthHz);

        context.fillStyle = '#000';
        context.fillRect(0, plotBottom, width, axis);
        context.fillStyle = '#d0d0d0';
        context.font = '11px sans-serif';
        context.textBaseline = 'middle';
        ticks.forEach((tick) => {
            const labelX = Math.min(width - 2, Math.max(2, tick.x));
            context.textAlign = tick.x < 32 ? 'left' : tick.x > width - 32 ? 'right' : 'center';
            context.fillText(tick.label, labelX, plotBottom + axis / 2);
        });
        this.drawSpectrumCursor(context, width, plotBottom, centerHz, bandwidthHz);
    }

    private drawSpectrumLine(context: CanvasRenderingContext2D, bins: number[], width: number, spectrumHeight: number): void {
        const sorted = [...bins].sort((left, right) => left - right);
        const noise = sorted[Math.floor(sorted.length * 0.25)] ?? -100;
        const bottomDb = Math.floor((noise - 10) / 10) * 10;
        const topDb = bottomDb + 70;
        const top = 4;
        const bottom = spectrumHeight - 1;
        const toY = (db: number) => bottom - Math.max(0, Math.min(1, (db - bottomDb) / (topDb - bottomDb))) * (bottom - top);

        context.font = '10px sans-serif';
        context.textAlign = 'left';
        context.textBaseline = 'bottom';
        for (let db = bottomDb + 10; db < topDb; db += 10) {
            const y = Math.round(toY(db)) + 0.5;
            context.strokeStyle = 'rgba(192,192,192,0.18)';
            context.beginPath();
            context.moveTo(0, y);
            context.lineTo(width, y);
            context.stroke();
            context.fillStyle = 'rgba(208,208,208,0.7)';
            context.fillText(`${db} dB`, 4, y - 1);
        }

        context.beginPath();
        context.moveTo(0, bottom);
        bins.forEach((value, index) => context.lineTo(index * width / (bins.length - 1), toY(value)));
        context.lineTo(width, bottom);
        context.closePath();
        context.fillStyle = 'rgba(0,160,0,0.28)';
        context.fill();

        context.beginPath();
        bins.forEach((value, index) => {
            const x = index * width / (bins.length - 1);
            if (index === 0) {
                context.moveTo(x, toY(value));
            } else {
                context.lineTo(x, toY(value));
            }
        });
        context.strokeStyle = '#00ff00';
        context.lineWidth = 1;
        context.stroke();
    }

    private drawChannelMarkers(context: CanvasRenderingContext2D, width: number, spectrumHeight: number, plotBottom: number,
                               centerHz: number, bandwidthHz: number): void {
        if (!centerHz || !bandwidthHz || !this.spectrumMarkers.length) {
            return;
        }
        const low = centerHz - bandwidthHz / 2;
        const toX = (hz: number) => ((hz - low) / bandwidthHz) * width;
        context.font = 'bold 11px sans-serif';
        context.textAlign = 'center';
        context.textBaseline = 'top';
        const labelled: { left: number; right: number; row: number }[] = [];
        for (const marker of this.spectrumMarkers) {
            const frequency = Number(marker.frequencyHz) || 0;
            const markerWidth = Math.max(3, toX(frequency + (Number(marker.bandwidthHz) || 12500) / 2) - toX(frequency - (Number(marker.bandwidthHz) || 12500) / 2));
            const x = toX(frequency);
            if (x < -markerWidth || x > width + markerWidth) {
                continue;
            }
            const control = !!marker.control;
            context.fillStyle = control ? 'rgba(0,200,255,0.22)' : 'rgba(255,170,0,0.2)';
            context.fillRect(x - markerWidth / 2, 0, markerWidth, spectrumHeight);
            context.strokeStyle = control ? 'rgba(0,200,255,0.8)' : 'rgba(255,170,0,0.75)';
            context.beginPath();
            context.moveTo(Math.round(x) + 0.5, 0);
            context.lineTo(Math.round(x) + 0.5, plotBottom);
            context.stroke();

            const label = String(marker.label || (control ? 'Control' : 'Traffic'));
            const textWidth = context.measureText(label).width + 8;
            let row = 0;
            while (labelled.some((item) => item.row === row && x - textWidth / 2 < item.right && x + textWidth / 2 > item.left) && row < 4) {
                row++;
            }
            labelled.push({ left: x - textWidth / 2, right: x + textWidth / 2, row });
            const labelX = Math.max(textWidth / 2, Math.min(width - textWidth / 2, x));
            const labelY = 4 + row * 16;
            context.fillStyle = 'rgba(0,0,0,0.7)';
            context.fillRect(labelX - textWidth / 2, labelY - 1, textWidth, 15);
            context.fillStyle = control ? '#7fe3ff' : '#ffc966';
            context.fillText(label, labelX, labelY);
        }
    }

    private drawWaterfall(context: CanvasRenderingContext2D, width: number, top: number, height: number): void {
        const dpr = window.devicePixelRatio || 1;
        const pixelWidth = Math.max(1, Math.floor(width * dpr));
        const pixelHeight = Math.max(1, Math.floor(height * dpr));
        const image = context.createImageData(pixelWidth, pixelHeight);
        const pixels = image.data;
        const rows = this.spectrumRows;
        for (let y = 0; y < pixelHeight; y++) {
            const row = rows[Math.floor(y / dpr)];
            const offset = y * pixelWidth * 4;
            if (!row || row.length < 2) {
                for (let x = 0; x < pixelWidth; x++) {
                    const index = offset + x * 4;
                    pixels[index] = 0;
                    pixels[index + 1] = 0;
                    pixels[index + 2] = 127;
                    pixels[index + 3] = 255;
                }
                continue;
            }
            let sum = 0;
            for (let binIndex = 0; binIndex < row.length; binIndex++) {
                sum += row[binIndex];
            }
            const average = sum / row.length;
            const scale = 256 / Math.max(1, Math.abs(average));
            for (let x = 0; x < pixelWidth; x++) {
                const bin = row[Math.min(row.length - 1, Math.floor(x * row.length / pixelWidth))];
                const level = Math.max(0, Math.min(255, Math.round((bin - average) * scale)));
                const colorIndex = level * 3;
                const index = offset + x * 4;
                pixels[index] = waterfallPalette[colorIndex];
                pixels[index + 1] = waterfallPalette[colorIndex + 1];
                pixels[index + 2] = waterfallPalette[colorIndex + 2];
                pixels[index + 3] = 255;
            }
        }
        context.save();
        context.setTransform(1, 0, 0, 1, 0, 0);
        context.putImageData(image, 0, Math.floor(top * dpr));
        context.restore();
    }

    private drawSpectrumCursor(context: CanvasRenderingContext2D, width: number, plotBottom: number, centerHz: number, bandwidthHz: number): void {
        if (this.spectrumCursorX == null || !centerHz || !bandwidthHz) {
            return;
        }
        const x = Math.max(0, Math.min(width, this.spectrumCursorX));
        context.strokeStyle = '#ff9f1a';
        context.lineWidth = 1;
        context.beginPath();
        context.moveTo(x + 0.5, 0);
        context.lineTo(x + 0.5, plotBottom);
        context.stroke();
        const hz = centerHz - bandwidthHz / 2 + (x / width) * bandwidthHz;
        const label = (hz / 1000000).toFixed(5);
        context.font = '12px sans-serif';
        context.textBaseline = 'top';
        context.textAlign = x > width - 78 ? 'right' : 'left';
        context.fillStyle = '#fff';
        context.fillText(label, x > width - 78 ? x - 6 : x + 6, 168);
    }

    private frequencyTicks(centerHz: number, bandwidthHz: number, width: number): { x: number; label: string }[] {
        const low = centerHz - bandwidthHz / 2;
        const high = centerHz + bandwidthHz / 2;
        const steps = [25000, 50000, 100000, 125000, 200000, 250000, 500000, 1000000, 2000000, 5000000];
        let step = steps[steps.length - 1];
        for (const candidate of steps) {
            if (width / (bandwidthHz / candidate) >= 76) {
                step = candidate;
                break;
            }
        }
        const ticks: { x: number; label: string }[] = [];
        for (let hz = Math.ceil(low / step) * step; hz <= high; hz += step) {
            ticks.push({
                x: ((hz - low) / bandwidthHz) * width,
                label: (hz / 1000000).toFixed(3),
            });
        }
        return ticks;
    }

    async command(source: SdrHealthSource, command: string, name?: string, enabled?: boolean): Promise<void> {
        this.commanding = true;
        this.commandError = null;
        this.cdr.markForCheck();
        try {
            const result = await this.adminService.sendSdrtrunkCommand({
                apiKeyId: source.apiKeyId,
                command,
                name,
                enabled,
            });
            if (result && result.ok === false) {
                this.commandError = result.error || 'SDRTrunk rejected the command';
            }
            await this.load();
        } catch (error: any) {
            this.commandError = error?.message || 'Command failed';
        } finally {
            this.commanding = false;
            this.cdr.markForCheck();
        }
    }

    formatDate(value: number): string {
        if (!value) {
            return '—';
        }
        return new Date(value).toLocaleString();
    }
}

const waterfallPalette = buildWaterfallPalette();

function buildWaterfallPalette(): Uint8ClampedArray {
    const rgb = new Uint8ClampedArray(256 * 3);
    const set = (index: number, red: number, green: number, blue: number) => {
        rgb[index * 3] = red;
        rgb[index * 3 + 1] = green;
        rgb[index * 3 + 2] = blue;
    };
    for (let index = 0; index < 16; index++) {
        set(index, 0, 0, 127);
    }
    for (let index = 16; index < 32; index++) {
        set(index, 0, 0, 191);
    }
    let red = 0;
    let green = 0;
    let blue = 191;
    for (let index = 32; index < 60; index++) {
        set(index, red, green, blue);
        red += 9;
        green += 9;
        blue -= 6;
    }
    red = 255;
    green = 255;
    blue = 0;
    for (let index = 60; index < 188; index++) {
        set(index, red, green, blue);
        green -= 2;
    }
    for (let index = 188; index < 256; index++) {
        set(index, 255, 0, 0);
    }
    return rgb;
}
