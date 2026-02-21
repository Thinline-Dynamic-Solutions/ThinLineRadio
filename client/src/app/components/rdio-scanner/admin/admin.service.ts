/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

import { HttpClient, HttpErrorResponse, HttpHeaders } from '@angular/common/http';
import { EventEmitter, Injectable, OnDestroy } from '@angular/core';
import { AbstractControl, FormArray, FormBuilder, FormGroup, ValidationErrors, ValidatorFn, Validators } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { firstValueFrom, timer, timeout, Observable, race } from 'rxjs';
import { AppUpdateService } from '../../../shared/update/update.service';
import { RdioScannerToneSet } from '../rdio-scanner';

export interface Alert {
    begin: number;
    end: number;
    frequency: number;
    type: OscillatorType;
}

export interface Alerts {
    [key: string]: Alert[];
}

export interface AdminEvent {
    authenticated?: boolean;
    config?: Config;
    docker?: boolean;
    passwordNeedChange?: boolean;
    libraryTalkgroupsUpdated?: {
        libraryId: number;
    };
    libraryListChanged?: {
        action: 'create' | 'update' | 'delete';
        library: any;
    };
}

export interface Apikey {
    id?: string;
    disabled?: boolean;
    ident?: string;
    key?: string;
    order?: number;
    systems?: {
        id: number;
        talkgroups: number[] | '*';
    }[] | number[] | '*';
}

export interface User {
    id?: number;
    email?: string;
    firstName?: string;
    lastName?: string;
    password?: string;
    pin?: string;
    verified?: boolean;
    systemAdmin?: boolean;
    isGroupAdmin?: boolean;
    userGroupId?: number;
    connectionLimit?: number;
    delay?: number;
    zipCode?: string;
    accountExpiresAt?: string;
    pinExpiresAt?: string;
    lastLogin?: string;
    createdAt?: string;
    stripeCustomerId?: string;
    stripeSubscriptionId?: string;
    subscriptionStatus?: string;
    settings?: any;
    systems?: any[];
    systemDelays?: any[];
    talkgroupDelays?: any[];
}

export interface KeywordList {
    id?: number;
    label?: string;
    description?: string;
    keywords?: string[];
    order?: number;
    createdAt?: number;
}

export interface UserGroup {
    id?: number;
    name?: string;
    description?: string;
    connectionLimit?: number;
    delay?: number;
    maxUsers?: number;
    allowAddExistingUsers?: boolean;
    isPublicRegistration?: boolean;
    billingEnabled?: boolean;
    billingMode?: string;
    stripePriceId?: string;
    pricingOptions?: any;
    collectSalesTax?: boolean;
    createdAt?: string | number;
    systemAccess?: any[] | string;
    systemDelays?: any[] | string;
    talkgroupDelays?: any[] | string;
}

export interface Config {
    apikeys?: Apikey[];
    branding?: string;
    dirwatch?: Dirwatch[];
    downstreams?: Downstream[];
    groups?: Group[];
    options?: Options;
    systems?: System[];
    tags?: Tag[];
    users?: User[];
    userGroups?: UserGroup[];
    keywordLists?: KeywordList[];
    version?: string;
}

export interface Dirwatch {
    id?: string;
    delay?: number;
    deleteAfter?: boolean;
    directory?: string;
    disabled?: boolean;
    extension?: string;
    frequency?: number;
    mask?: string;
    order?: number;
    siteId?: number;
    systemId?: number;
    talkgroupId?: number;
    type?: string;
}

export interface Downstream {
    id?: string;
    apikey?: string;
    disabled?: boolean;
    name?: string;
    order?: number;
    systems?: {
        id?: number;
        id_as?: number;
        talkgroups?: {
            id: number;
            id_as?: number;
        }[] | number[] | '*';
    }[] | number[] | '*';
    url?: string;
}

export interface Group {
    id?: number;
    alert?: string;
    label?: string;
    led?: string;
    order?: number;
}

export interface Log {
    id?: number;
    dateTime: Date;
    level: number;
    message: string;
}

export interface LogsQuery {
    count: number;
    dateStart: Date;
    dateStop: Date;
    options: LogsQueryOptions;
    logs: Log[];
}

export interface LogsQueryOptions {
    date?: Date;
    level?: 'error' | 'info' | 'warn';
    limit: number;
    offset: number;
    sort: number;
}

export interface CallSearchResult {
    id: number;
    dateTime: Date;
    system: number;
    talkgroup: number;
    frequency?: number;
    source?: number;
    site?: number;
}

export interface CallsQuery {
    count: number;
    hasMore: boolean;
    dateStart: Date;
    dateStop: Date;
    options: CallsQueryOptions;
    results: CallSearchResult[];
}

export interface CallsQueryOptions {
    date?: string | Date;
    group?: string;
    limit: number;
    offset: number;
    sort: number;
    system?: number;
    tag?: string;
    talkgroup?: number;
}

export interface Options {
	audioConversion?: 0 | 1 | 2 | 3;
	audioCodec?: string;
	audioBitrate?: number;
	autoPopulate?: boolean;
	branding?: string;
	defaultSystemDelay?: number;
	dimmerDelay?: number;
	disableDuplicateDetection?: boolean;
	duplicateDetectionMode?: string;
	duplicateDetectionTimeFrame?: number;
	advancedDetectionTimeFrame?: number;
	email?: string;
	keypadBeeps?: string;
	maxClients?: number;
	playbackGoesLive?: boolean;
	pruneDays?: number;
	showListenersCount?: boolean;
	sortTalkgroups?: boolean;
	time12hFormat?: boolean;
    radioReferenceEnabled?: boolean;
    radioReferenceUsername?: string;
    radioReferencePassword?: string;
    radioReferenceAPIKey?: string;
    userRegistrationEnabled?: boolean;
    publicRegistrationEnabled?: boolean;
    publicRegistrationMode?: string;
    stripePaywallEnabled?: boolean;
    emailServiceEnabled?: boolean;
    emailProvider?: string;
    emailSmtpFromEmail?: string;
    emailSmtpFromName?: string;
    emailSendGridApiKey?: string;
    emailMailgunApiKey?: string;
    emailMailgunDomain?: string;
    emailMailgunApiBase?: string;
    emailSmtpHost?: string;
    emailSmtpPort?: number;
    emailSmtpUsername?: string;
    emailSmtpPassword?: string;
    emailSmtpUseTLS?: boolean;
    emailSmtpSkipVerify?: boolean;
    emailLogoFilename?: string;
    emailLogoBorderRadius?: string;
    faviconFilename?: string;
    stripePublishableKey?: string;
    stripeSecretKey?: string;
    stripeWebhookSecret?: string;
    stripeGracePeriodDays?: number;
    stripePriceId?: string;
    baseUrl?: string;
    transcriptionEnabled?: boolean;
    transcriptionConfig?: {
        enabled?: boolean;
        provider?: string;
        language?: string;
        prompt?: string;
        workerPoolSize?: number;
        minCallDuration?: number;
        whisperAPIURL?: string;
        whisperAPIKey?: string;
        azureKey?: string;
        azureRegion?: string;
        googleAPIKey?: string;
        googleCredentials?: string;
        assemblyAIKey?: string;
        assemblyAIWordBoost?: string[];
        hallucinationPatterns?: string[];
        hallucinationDetectionMode?: string;
        hallucinationMinOccurrences?: number;
    };
    alertRetentionDays?: number;
    relayServerURL?: string;
    relayServerAPIKey?: string;
    adminLocalhostOnly?: boolean;
    configSyncEnabled?: boolean;
    configSyncPath?: string;
    turnstileEnabled?: boolean;
    turnstileSiteKey?: string;
    turnstileSecretKey?: string;
    // Centralized Management Integration
    centralManagementEnabled?: boolean;
    centralManagementURL?: string;
    centralManagementAPIKey?: string;
    centralManagementServerName?: string;
}

export interface ToneImportResponse {
    format: string;
    count: number;
    toneSets: RdioScannerToneSet[];
    warnings?: string[];
}

export interface Site {
    id?: number | null;
    label?: string;
    order?: number;
    siteRef?: string;        // Site ID as string to preserve leading zeros (e.g., "001", "021")
    rfss?: number;           // Radio Frequency Sub-System ID
    frequencies?: number[];  // MHz frequencies for this site
    preferred?: boolean;     // Is this the preferred site?
}

export interface System {
    id?: number | null;
    systemId?: number;      // Database primary key
    systemRef?: number;     // Radio reference ID
    alert?: string;
    autoPopulate?: boolean;
    blacklists?: string;
    delay?: number;
    label?: string;
    led?: string | null;
    order?: number | null;
    sites?: Site[];
    talkgroups?: Talkgroup[];
    type?: string;
    units?: Unit[];
    preferredApiKeyId?: number | null;  // Optional preferred API key for uploads
    noAudioAlertsEnabled?: boolean;     // Enable no-audio alerts for this system
    noAudioThresholdMinutes?: number;   // Minutes without audio before alerting
}

export interface Tag {
    id?: number;
    alert?: string;
    label?: string;
    led?: string;
    order?: number;
    color?: string;
}

export interface Talkgroup {
    id?: number | null;
    talkgroupId?: number;    // Database primary key
    talkgroupRef?: number;   // Radio reference ID
    alert?: string;
    delay?: number;
    frequency?: number | null;
    groupIds?: number[];
    label?: string;
    led?: string | null;
    name?: string;
    order?: number;
    tagId?: number;
    type?: string;
    toneDetectionEnabled?: boolean;
    toneSets?: any[];
    preferredApiKeyId?: number | null;  // Optional preferred API key for uploads
    excludeFromPreferredSite?: boolean; // Exclude from preferred site detection (for interop/patched talkgroups)
}

export interface Unit {
    id?: number | null;
    label?: string;
    order?: number;
    unitRef?: number;
    unitFrom?: number;
    unitTo?: number;
}

enum url {
    alerts = 'alerts',
    alertRetentionDays = 'alert-retention-days',
    calls = 'calls',
    config = 'config',
    login = 'login',
    logout = 'logout',
    logs = 'logs',
    password = 'password',
    purge = 'purge',
    systemhealth = 'systemhealth',
    systemNoAudioSettings = 'system-no-audio-settings',
    toneDetectionIssueThreshold = 'tone-detection-issue-threshold',
    noAudioThresholdMinutes = 'no-audio-threshold-minutes',
    noAudioMultiplier = 'no-audio-multiplier',
    systemHealthAlertsEnabled = 'system-health-alerts-enabled',
    systemHealthAlertSettings = 'system-health-alert-settings',
}

const SESSION_STORAGE_KEY = 'rdio-scanner-admin-token';

declare global {
    interface Window {
        webkitAudioContext: typeof AudioContext;
    }
}

@Injectable()
export class RdioScannerAdminService implements OnDestroy {
    Alerts: Alerts | undefined;

    event = new EventEmitter<AdminEvent>();

    private audioContext: AudioContext | undefined;

    private configWebSocket: WebSocket | undefined;

    private _docker = false;
    private _passwordNeedChange = false;

    get authenticated() {
        return !!this.token;
    }

    get docker() {
        return this._docker;
    }

    get passwordNeedChange() {
        return this._passwordNeedChange;
    }

    getToken(): string {
        return this.token;
    }

    private get token(): string {
        return window?.sessionStorage?.getItem(SESSION_STORAGE_KEY) || '';
    }

    private set token(token: string) {
        if (token) {
            window?.sessionStorage?.setItem(SESSION_STORAGE_KEY, token);
        } else {
            window?.sessionStorage?.removeItem(SESSION_STORAGE_KEY);
        }
    }

    constructor(
        appUpdateService: AppUpdateService,
        private matSnackBar: MatSnackBar,
        private ngFormBuilder: FormBuilder,
        private ngHttpClient: HttpClient,
    ) {
        this.configWebSocketOpen();
    }

    ngOnDestroy(): void {
        this.event.complete();

        this.configWebSocketClose();
    }

    async changePassword(currentPassword: string, newPassword: string): Promise<void> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.post<{ passwordNeedChange: boolean }>(
                this.getUrl(url.password),
                { currentPassword, newPassword },
                { headers: this.getHeaders(), responseType: 'json' },
            ));

            this._passwordNeedChange = res.passwordNeedChange;

            this.event.next({ passwordNeedChange: this.passwordNeedChange });

        } catch (error) {
            this.errorHandler(error);

            throw error;

        }
    }

    // Deduplication: if a getConfig HTTP request is already in-flight, reuse the same promise
    // instead of firing a second parallel request that returns the same data.
    private _getConfigPromise: Promise<Config> | undefined;

    async getConfig(): Promise<Config> {
        if (this._getConfigPromise) {
            return this._getConfigPromise;
        }
        this._getConfigPromise = this._fetchConfig().finally(() => {
            this._getConfigPromise = undefined;
        });
        return this._getConfigPromise;
    }

    private async _fetchConfig(): Promise<Config> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{
                config: Config;
                docker: boolean;
                passwordNeedChange: boolean;
            }>(
                this.getUrl(url.config),
                { headers: this.getHeaders(), responseType: 'json' },
            ));

            if (res.docker !== this._docker) {
                this._docker = res.docker;

                this.event.emit({ docker: this.docker })
            }

            if (res.passwordNeedChange !== this._passwordNeedChange) {
                this._passwordNeedChange = res.passwordNeedChange;

                this.event.emit({ passwordNeedChange: this.passwordNeedChange });
            }

            return res.config;

        } catch (error) {
            this.errorHandler(error);
        }

        return {};
    } // end _fetchConfig

    async getLogs(options: LogsQueryOptions): Promise<LogsQuery | undefined> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.post<LogsQuery>(
                this.getUrl(url.logs),
                options,
                { headers: this.getHeaders(), responseType: 'json' },
            ));

            return res;

        } catch (error) {
            this.errorHandler(error);

            return undefined;
        }
    }

    async getCalls(options: CallsQueryOptions): Promise<CallsQuery | undefined> {
        try {
            // Convert Date to ISO string if present
            const payload: any = { ...options };
            if (payload.date instanceof Date) {
                payload.date = payload.date.toISOString();
            }

            const res = await firstValueFrom(this.ngHttpClient.post<CallsQuery>(
                this.getUrl(url.calls),
                payload,
                { headers: this.getHeaders(), responseType: 'json' },
            ));

            // Convert dateTime strings to Date objects
            if (res && res.results) {
                res.results = res.results.map(result => ({
                    ...result,
                    dateTime: new Date(result.dateTime)
                }));
            }

            return res;

        } catch (error) {
            this.errorHandler(error);

            return undefined;
        }
    }

    async loadAlerts(): Promise<void> {
        // Alerts are hardcoded oscillator beep patterns that NEVER change at runtime.
        // In-memory cache: skip if already loaded this session.
        if (this.Alerts) {
            return;
        }
        // Persistent cache: they're baked into the server binary so the data
        // is identical on every fetch. Serve from localStorage to avoid the
        // HTTP round-trip entirely on subsequent page loads.
        const ALERTS_CACHE_KEY = 'rdio-scanner-admin-alerts-cache';
        try {
            const cached = localStorage.getItem(ALERTS_CACHE_KEY);
            if (cached) {
                this.Alerts = JSON.parse(cached);
                return;
            }
        } catch (_) { /* ignore localStorage errors */ }

        try {
            this.Alerts = await firstValueFrom(this.ngHttpClient.get<Alerts>(
                this.getUrl(url.alerts),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            // Persist for future page loads
            try { localStorage.setItem(ALERTS_CACHE_KEY, JSON.stringify(this.Alerts)); } catch (_) { /* ignore */ }
        } catch (error) {
            this.errorHandler(error);
        }
    }

    async getSystemHealth(limit: number = 100, includeDismissed: boolean = false): Promise<{ alerts: any[], count: number }> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ alerts: any[], count: number }>(
                this.getUrl(url.systemhealth) + `?limit=${limit}&includeDismissed=${includeDismissed}`,
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async dismissSystemAlert(alertId: number): Promise<void> {
        try {
            // Use admin endpoint for dismissing alerts (uses admin token authentication)
            // Backend expects PUT to /admin/systemhealth with alertId in body
            await firstValueFrom(this.ngHttpClient.put(
                this.getUrl(url.systemhealth),
                { alertId },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getTranscriptionFailures(): Promise<{ calls: any[], count: number }> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ calls: any[], count: number }>(
                this.getUrl('transcription-failures'),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async resetTranscriptionFailures(callIds?: number[]): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl('transcription-failures'),
                { callIds: callIds || [] },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getTranscriptionFailureThreshold(): Promise<number> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ threshold: number }>(
                this.getUrl('transcription-failure-threshold'),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res.threshold || 10;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async setTranscriptionFailureThreshold(threshold: number): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl('transcription-failure-threshold'),
                { threshold },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getAlertRetentionDays(): Promise<number> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ retentionDays: number }>(
                this.getUrl(url.alertRetentionDays),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res.retentionDays || 5;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async setAlertRetentionDays(retentionDays: number): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl(url.alertRetentionDays),
                { retentionDays },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getToneDetectionIssueThreshold(): Promise<number> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ threshold: number }>(
                this.getUrl(url.toneDetectionIssueThreshold),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res.threshold || 5;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async setToneDetectionIssueThreshold(threshold: number): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl(url.toneDetectionIssueThreshold),
                { threshold },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getNoAudioThresholdMinutes(): Promise<number> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ threshold: number }>(
                this.getUrl(url.noAudioThresholdMinutes),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res.threshold || 30;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async setNoAudioThresholdMinutes(threshold: number): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl(url.noAudioThresholdMinutes),
                { threshold },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getNoAudioMultiplier(): Promise<number> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ multiplier: number }>(
                this.getUrl(url.noAudioMultiplier),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res.multiplier || 1.5;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async setNoAudioMultiplier(multiplier: number): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl(url.noAudioMultiplier),
                { multiplier },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getSystemHealthAlertsEnabled(): Promise<boolean> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<{ enabled: boolean }>(
                this.getUrl(url.systemHealthAlertsEnabled),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res.enabled !== false; // Default to true if not set
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async setSystemHealthAlertsEnabled(enabled: boolean): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl(url.systemHealthAlertsEnabled),
                { enabled },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async getSystemHealthAlertSettings(): Promise<any> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.get<any>(
                this.getUrl(url.systemHealthAlertSettings),
                { headers: this.getHeaders(), responseType: 'json' },
            ));
            return res;
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    async updateSystemHealthAlertSettings(settings: any): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl(url.systemHealthAlertSettings),
                settings,
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    getCallAudioUrl(callId: number): string {
        return this.getUrl(`call-audio/${callId}`);
    }

    async login(password: string): Promise<boolean> {
        try {
            const res = await firstValueFrom(this.ngHttpClient.post<{
                passwordNeedChange: boolean,
                token: string
            }>(
                this.getUrl(url.login),
                { password },
                { headers: this.getHeaders(), responseType: 'json' },
            ));

            this.token = res.token;

            this._passwordNeedChange = res.passwordNeedChange;

            this.event.emit({
                authenticated: this.authenticated,
                passwordNeedChange: res.passwordNeedChange,
            });

            this.configWebSocketOpen();

            return !!this.token;

        } catch (error: any) {
            // Check if IP is blocked due to too many failed attempts
            // Must check BEFORE calling errorHandler to avoid processing the error object
            if (error?.error?.blocked && error?.error?.retryAfter) {
                // Reload page with query params to show countdown
                const currentUrl = window.location.pathname;
                window.location.href = `${currentUrl}?seconds=${error.error.retryAfter}`;
                return false;
            }
            
            this.errorHandler(error);

            return false;
        }
    }

    async logout(): Promise<boolean> {
        try {
            await this.ngHttpClient.post(
                this.getUrl(url.logout),
                null,
                { headers: this.getHeaders(), responseType: 'text' },
            ).toPromise();

            this.configWebSocketClose();

            this.token = '';

            this.event.emit({ authenticated: this.authenticated });

            return true;

        } catch (error) {
            this.errorHandler(error);

            return false;
        }
    }

    async playAlert(name: string): Promise<void> {
        return new Promise((resolve) => {
            if (this.audioContext === undefined) {
                this.audioContext = new (window.AudioContext || window.webkitAudioContext)({ latencyHint: 'playback' });
            }

            if (this.Alerts !== undefined && name in this.Alerts) {
                const ctx = this.audioContext;

                const seq = this.Alerts[name];

                const gn = ctx.createGain();

                gn.gain.value = .1;

                gn.connect(ctx.destination);

                seq.forEach((beep, index) => {
                    const osc = ctx.createOscillator();

                    osc.connect(gn);

                    osc.frequency.value = beep.frequency;

                    osc.type = beep.type;

                    if (index === seq.length - 1) {
                        osc.onended = () => resolve();
                    }

                    osc.start(ctx.currentTime + beep.begin);

                    osc.stop(ctx.currentTime + beep.end);
                });

            } else {
                resolve();
            }
        });
    }

    async saveConfig(config: Config, isFullImport: boolean = false): Promise<Config> {
        try {
            let headers = this.getHeaders();
            if (isFullImport) {
                headers = headers.set('X-Full-Import', 'true');
            }
            
            const res = await firstValueFrom(this.ngHttpClient.put<{ config: Config }>(
                this.getUrl(url.config),
                config,
                { headers: headers, responseType: 'json' },
            ));

            return res.config;

        } catch (error) {
            this.errorHandler(error);

            return config;
        }
    }

    async saveSystemNoAudioSettings(systemId: number, noAudioAlertsEnabled: boolean, noAudioThresholdMinutes: number): Promise<void> {
        try {
            await firstValueFrom(this.ngHttpClient.post(
                this.getUrl(url.systemNoAudioSettings),
                { 
                    systemId,
                    noAudioAlertsEnabled,
                    noAudioThresholdMinutes
                },
                { headers: this.getHeaders(), responseType: 'json' },
            ));
        } catch (error) {
            this.errorHandler(error);
            throw error;
        }
    }

    newApikeyForm(apikey?: Apikey): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(apikey?.id),
            disabled: this.ngFormBuilder.control(apikey?.disabled),
            ident: this.ngFormBuilder.control(apikey?.ident, Validators.required),
            key: this.ngFormBuilder.control(apikey?.key, [Validators.required, this.validateApikey()]),
            order: this.ngFormBuilder.control(apikey?.order),
            systems: this.ngFormBuilder.control(apikey?.systems, Validators.required),
        });
    }

    newConfigForm(config?: Config): FormGroup {
        return this.ngFormBuilder.group({
            apikeys: this.ngFormBuilder.array(config?.apikeys?.map((apikey) => this.newApikeyForm(apikey)) || []),
            dirwatch: this.ngFormBuilder.array(config?.dirwatch?.map((dirwatch) => this.newDirwatchForm(dirwatch)) || []),
            downstreams: this.ngFormBuilder.array(config?.downstreams?.map((downstream) => this.newDownstreamForm(downstream)) || []),
            groups: this.ngFormBuilder.array(config?.groups?.map((group) => this.newGroupForm(group)) || []),
            options: this.newOptionsForm(config?.options),
            systems: this.ngFormBuilder.array(config?.systems?.map((system) => this.newSystemForm(system)) || []),
            tags: this.ngFormBuilder.array(config?.tags?.map((tag) => this.newTagForm(tag)) || []),
            users: this.ngFormBuilder.array(config?.users?.map((user) => this.newUserForm(user)) || []),
            userGroups: this.ngFormBuilder.array(config?.userGroups?.map((userGroup) => this.newUserGroupForm(userGroup)) || []),
            keywordLists: this.ngFormBuilder.array(config?.keywordLists?.map((list) => this.newKeywordListForm(list)) || []),
            version: this.ngFormBuilder.control(config?.version),
        });
    }

    newDirwatchForm(dirwatch?: Dirwatch): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(dirwatch?.id),
            delay: this.ngFormBuilder.control(typeof dirwatch?.delay === 'number' ? Math.max(2000, dirwatch?.delay) : 2000),
            deleteAfter: this.ngFormBuilder.control(dirwatch?.deleteAfter),
            directory: this.ngFormBuilder.control(dirwatch?.directory, [Validators.required, this.validateDirectory()]),
            disabled: this.ngFormBuilder.control(dirwatch?.disabled),
            extension: this.ngFormBuilder.control(dirwatch?.extension, this.validateExtension()),
            frequency: this.ngFormBuilder.control(dirwatch?.frequency, Validators.min(1)),
            mask: this.ngFormBuilder.control(dirwatch?.mask, this.validateMask()),
            order: this.ngFormBuilder.control(dirwatch?.order),
            siteId: this.ngFormBuilder.control(dirwatch?.siteId),
            systemId: this.ngFormBuilder.control(dirwatch?.systemId, this.validateDirwatchSystemId()),
            talkgroupId: this.ngFormBuilder.control(dirwatch?.talkgroupId, this.validateDirwatchTalkgroupId()),
            type: this.ngFormBuilder.control(dirwatch?.type ?? 'default'),
        });
    }

    newDownstreamForm(downstream?: Downstream): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(downstream?.id),
            apikey: this.ngFormBuilder.control(downstream?.apikey, [Validators.required, this.validateApikey()]),
            disabled: this.ngFormBuilder.control(downstream?.disabled),
            name: this.ngFormBuilder.control(downstream?.name),
            order: this.ngFormBuilder.control(downstream?.order),
            systems: this.ngFormBuilder.control(downstream?.systems, Validators.required),
            url: this.ngFormBuilder.control(downstream?.url, [Validators.required, this.validateUrl(), this.validateDownstreamUrl()]),
        });
    }

    newGroupForm(group?: Group): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(group?.id),
            alert: this.ngFormBuilder.control(group?.alert || ''),
            label: this.ngFormBuilder.control(group?.label, Validators.required),
            led: this.ngFormBuilder.control(group?.led || ''),
            order: this.ngFormBuilder.control(group?.order),
        });
    }

    newUserForm(user?: User): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(user?.id),
            email: this.ngFormBuilder.control(user?.email, [Validators.required, Validators.email]),
            firstName: this.ngFormBuilder.control(user?.firstName || ''),
            lastName: this.ngFormBuilder.control(user?.lastName || ''),
            password: this.ngFormBuilder.control(user?.password || ''),
            pin: this.ngFormBuilder.control(user?.pin || ''),
            verified: this.ngFormBuilder.control(user?.verified),
            systemAdmin: this.ngFormBuilder.control(user?.systemAdmin),
            isGroupAdmin: this.ngFormBuilder.control(user?.isGroupAdmin),
            userGroupId: this.ngFormBuilder.control(user?.userGroupId),
            connectionLimit: this.ngFormBuilder.control(user?.connectionLimit),
            delay: this.ngFormBuilder.control(user?.delay),
            zipCode: this.ngFormBuilder.control(user?.zipCode || ''),
            accountExpiresAt: this.ngFormBuilder.control(user?.accountExpiresAt),
            pinExpiresAt: this.ngFormBuilder.control(user?.pinExpiresAt),
            lastLogin: this.ngFormBuilder.control(user?.lastLogin),
            createdAt: this.ngFormBuilder.control(user?.createdAt),
            stripeCustomerId: this.ngFormBuilder.control(user?.stripeCustomerId || ''),
            stripeSubscriptionId: this.ngFormBuilder.control(user?.stripeSubscriptionId || ''),
            subscriptionStatus: this.ngFormBuilder.control(user?.subscriptionStatus || ''),
            settings: this.ngFormBuilder.control(user?.settings),
            systems: this.ngFormBuilder.control(user?.systems),
            systemDelays: this.ngFormBuilder.control(user?.systemDelays),
            talkgroupDelays: this.ngFormBuilder.control(user?.talkgroupDelays),
        });
    }

    newUserGroupForm(userGroup?: UserGroup): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(userGroup?.id),
            name: this.ngFormBuilder.control(userGroup?.name, Validators.required),
            description: this.ngFormBuilder.control(userGroup?.description || ''),
            connectionLimit: this.ngFormBuilder.control(userGroup?.connectionLimit),
            delay: this.ngFormBuilder.control(userGroup?.delay),
            maxUsers: this.ngFormBuilder.control(userGroup?.maxUsers),
            allowAddExistingUsers: this.ngFormBuilder.control(userGroup?.allowAddExistingUsers),
            isPublicRegistration: this.ngFormBuilder.control(userGroup?.isPublicRegistration),
            billingEnabled: this.ngFormBuilder.control(userGroup?.billingEnabled),
            billingMode: this.ngFormBuilder.control(userGroup?.billingMode || ''),
            stripePriceId: this.ngFormBuilder.control(userGroup?.stripePriceId || ''),
            pricingOptions: this.ngFormBuilder.control(userGroup?.pricingOptions),
            createdAt: this.ngFormBuilder.control(userGroup?.createdAt),
            systemAccess: this.ngFormBuilder.control(userGroup?.systemAccess),
            systemDelays: this.ngFormBuilder.control(userGroup?.systemDelays),
            talkgroupDelays: this.ngFormBuilder.control(userGroup?.talkgroupDelays),
        });
    }

    newKeywordListForm(list?: KeywordList): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(list?.id),
            label: this.ngFormBuilder.control(list?.label || '', Validators.required),
            description: this.ngFormBuilder.control(list?.description || ''),
            keywords: this.ngFormBuilder.control(list?.keywords || []),
            order: this.ngFormBuilder.control(list?.order || 0),
            createdAt: this.ngFormBuilder.control(list?.createdAt),
        });
    }

    	newOptionsForm(options?: Options): FormGroup {
        // Build transcription config
        const transcriptionConfig = options?.transcriptionConfig || {
            enabled: false,
            provider: 'whisper-api',
            language: 'en',
            prompt: '',
            workerPoolSize: 1, // Default 1 for safety; users with adequate VRAM can increase
            minCallDuration: 0, // 0 = transcribe all calls
            whisperAPIURL: 'http://localhost:8000',
            whisperAPIKey: '',
            azureKey: '',
            azureRegion: 'eastus',
            googleAPIKey: '',
            googleCredentials: '',
            assemblyAIKey: '',
            assemblyAIWordBoost: [],
        };
        
		return this.ngFormBuilder.group({
		audioConversion: this.ngFormBuilder.control(options?.audioConversion),
		audioCodec: this.ngFormBuilder.control(options?.audioCodec || 'm4a'),
		audioBitrate: this.ngFormBuilder.control(options?.audioBitrate || 24, [Validators.min(16), Validators.max(320)]),
		autoPopulate: this.ngFormBuilder.control(options?.autoPopulate),
		branding: this.ngFormBuilder.control(options?.branding),
			defaultSystemDelay: this.ngFormBuilder.control(options?.defaultSystemDelay, [Validators.required, Validators.min(0)]),
			dimmerDelay: this.ngFormBuilder.control(options?.dimmerDelay, [Validators.required, Validators.min(0)]),
            disableDuplicateDetection: this.ngFormBuilder.control(options?.disableDuplicateDetection),
            duplicateDetectionMode: this.ngFormBuilder.control(options?.duplicateDetectionMode || 'legacy'),
            duplicateDetectionTimeFrame: this.ngFormBuilder.control(options?.duplicateDetectionTimeFrame, [Validators.required, Validators.min(0)]),
            advancedDetectionTimeFrame: this.ngFormBuilder.control(options?.advancedDetectionTimeFrame, [Validators.required, Validators.min(0)]),
            email: this.ngFormBuilder.control(options?.email),
            keypadBeeps: this.ngFormBuilder.control(options?.keypadBeeps, Validators.required),
            maxClients: this.ngFormBuilder.control(options?.maxClients, [Validators.required, Validators.min(1)]),
            playbackGoesLive: this.ngFormBuilder.control(options?.playbackGoesLive),
            pruneDays: this.ngFormBuilder.control(options?.pruneDays, [Validators.required, Validators.min(0)]),
            showListenersCount: this.ngFormBuilder.control(options?.showListenersCount),
            sortTalkgroups: this.ngFormBuilder.control(options?.sortTalkgroups),
            time12hFormat: this.ngFormBuilder.control(options?.time12hFormat),
            radioReferenceEnabled: this.ngFormBuilder.control(options?.radioReferenceEnabled),
            radioReferenceUsername: this.ngFormBuilder.control(options?.radioReferenceUsername, 
                options?.radioReferenceEnabled ? [Validators.required] : []),
            radioReferencePassword: this.ngFormBuilder.control(options?.radioReferencePassword, 
                options?.radioReferenceEnabled ? [Validators.required] : []),
            radioReferenceAPIKey: this.ngFormBuilder.control(options?.radioReferenceAPIKey || ''),
            userRegistrationEnabled: this.ngFormBuilder.control(options?.userRegistrationEnabled),
            publicRegistrationEnabled: this.ngFormBuilder.control(options?.publicRegistrationEnabled ?? true),
            publicRegistrationMode: this.ngFormBuilder.control(options?.publicRegistrationMode || 'both'),
            stripePaywallEnabled: this.ngFormBuilder.control(options?.stripePaywallEnabled),
            emailServiceEnabled: this.ngFormBuilder.control(options?.emailServiceEnabled),
            emailProvider: this.ngFormBuilder.control(options?.emailProvider || 'sendgrid'),
            emailSmtpFromEmail: this.ngFormBuilder.control(options?.emailSmtpFromEmail || ''),
            emailSmtpFromName: this.ngFormBuilder.control(options?.emailSmtpFromName || ''),
            emailSendGridApiKey: this.ngFormBuilder.control(options?.emailSendGridApiKey || ''),
            emailMailgunApiKey: this.ngFormBuilder.control(options?.emailMailgunApiKey || ''),
            emailMailgunDomain: this.ngFormBuilder.control(options?.emailMailgunDomain || ''),
            emailMailgunApiBase: this.ngFormBuilder.control(options?.emailMailgunApiBase || 'https://api.mailgun.net'),
            emailSmtpHost: this.ngFormBuilder.control(options?.emailSmtpHost || ''),
            emailSmtpPort: this.ngFormBuilder.control(options?.emailSmtpPort || 587),
            emailSmtpUsername: this.ngFormBuilder.control(options?.emailSmtpUsername || ''),
            emailSmtpPassword: this.ngFormBuilder.control(options?.emailSmtpPassword || ''),
            emailSmtpUseTLS: this.ngFormBuilder.control(options?.emailSmtpUseTLS ?? true),
            emailSmtpSkipVerify: this.ngFormBuilder.control(options?.emailSmtpSkipVerify || false),
            emailLogoFilename: this.ngFormBuilder.control(options?.emailLogoFilename || ''),
            emailLogoBorderRadius: this.ngFormBuilder.control(options?.emailLogoBorderRadius || '0px'),
            faviconFilename: this.ngFormBuilder.control(options?.faviconFilename || ''),
            stripePublishableKey: this.ngFormBuilder.control(options?.stripePublishableKey),
            stripeSecretKey: this.ngFormBuilder.control(options?.stripeSecretKey),
            stripeWebhookSecret: this.ngFormBuilder.control(options?.stripeWebhookSecret),
            stripeGracePeriodDays: this.ngFormBuilder.control(options?.stripeGracePeriodDays || 0, [Validators.min(0)]),
            stripePriceId: this.ngFormBuilder.control(options?.stripePriceId),
            baseUrl: this.ngFormBuilder.control(options?.baseUrl),
            adminLocalhostOnly: this.ngFormBuilder.control(options?.adminLocalhostOnly ?? false),
            transcriptionEnabled: this.ngFormBuilder.control(transcriptionConfig?.enabled || false),
            transcriptionConfig: this.ngFormBuilder.group({
                enabled: this.ngFormBuilder.control(transcriptionConfig?.enabled || false),
                provider: this.ngFormBuilder.control(transcriptionConfig?.provider || 'whisper-api'),
                language: this.ngFormBuilder.control(transcriptionConfig?.language || 'en'),
                prompt: this.ngFormBuilder.control(transcriptionConfig?.prompt || ''),
                workerPoolSize: this.ngFormBuilder.control(transcriptionConfig?.workerPoolSize || 1, [Validators.min(1), Validators.max(10)]), // Default 1 for safety; configurable by user
                minCallDuration: this.ngFormBuilder.control(transcriptionConfig?.minCallDuration || 0, [Validators.min(0)]),
                whisperAPIURL: this.ngFormBuilder.control(transcriptionConfig?.whisperAPIURL || 'http://localhost:8000'),
                whisperAPIKey: this.ngFormBuilder.control(transcriptionConfig?.whisperAPIKey || ''),
                azureKey: this.ngFormBuilder.control(transcriptionConfig?.azureKey || ''),
                azureRegion: this.ngFormBuilder.control(transcriptionConfig?.azureRegion || 'eastus'),
                googleAPIKey: this.ngFormBuilder.control(transcriptionConfig?.googleAPIKey || ''),
                googleCredentials: this.ngFormBuilder.control(transcriptionConfig?.googleCredentials || ''),
                assemblyAIKey: this.ngFormBuilder.control(transcriptionConfig?.assemblyAIKey || ''),
                assemblyAIWordBoost: this.ngFormBuilder.control(
                    (transcriptionConfig?.assemblyAIWordBoost || []).join('\n')
                ),
                hallucinationPatterns: this.ngFormBuilder.control(
                    (transcriptionConfig?.hallucinationPatterns || []).join('\n')
                ),
                hallucinationDetectionMode: this.ngFormBuilder.control(transcriptionConfig?.hallucinationDetectionMode || 'off'),
                hallucinationMinOccurrences: this.ngFormBuilder.control(transcriptionConfig?.hallucinationMinOccurrences || 5, [Validators.min(1)]),
            }),
            alertRetentionDays: this.ngFormBuilder.control(options?.alertRetentionDays || 30, [Validators.min(0)]),
            relayServerURL: this.ngFormBuilder.control('https://tlradioserver.thinlineds.com'), // Hardcoded
            relayServerAPIKey: this.ngFormBuilder.control(options?.relayServerAPIKey || ''),
            configSyncEnabled: this.ngFormBuilder.control(options?.configSyncEnabled || false),
            configSyncPath: this.ngFormBuilder.control(options?.configSyncPath || ''),
            turnstileEnabled: this.ngFormBuilder.control(options?.turnstileEnabled || false),
            turnstileSiteKey: this.ngFormBuilder.control(options?.turnstileSiteKey || ''),
            turnstileSecretKey: this.ngFormBuilder.control(options?.turnstileSecretKey || ''),
            // Centralized Management Integration
            centralManagementEnabled: this.ngFormBuilder.control(options?.centralManagementEnabled || false),
            centralManagementURL: this.ngFormBuilder.control(options?.centralManagementURL || ''),
            centralManagementAPIKey: this.ngFormBuilder.control(options?.centralManagementAPIKey || ''),
            centralManagementServerName: this.ngFormBuilder.control(options?.centralManagementServerName || ''),
        });
    }

    newSiteForm(site?: Site): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(site?.id),
            label: this.ngFormBuilder.control(site?.label, Validators.required),
            order: this.ngFormBuilder.control(site?.order),
            siteRef: this.ngFormBuilder.control(site?.siteRef, [Validators.required, this.validateSiteRef()]),
            rfss: this.ngFormBuilder.control(site?.rfss, [Validators.min(0)]),
            frequencies: this.ngFormBuilder.array(site?.frequencies?.map(f => this.ngFormBuilder.control(f, [Validators.min(0)])) || []),
            preferred: this.ngFormBuilder.control(site?.preferred || false),
        });
    }

    newSystemForm(system?: System): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(system?.id),
            alert: this.ngFormBuilder.control(system?.alert),
            autoPopulate: this.ngFormBuilder.control(system?.autoPopulate),
            blacklists: this.ngFormBuilder.control(system?.blacklists, this.validateBlacklists()),
            delay: this.ngFormBuilder.control(system?.delay),
            label: this.ngFormBuilder.control(system?.label, Validators.required),
            led: this.ngFormBuilder.control(system?.led || ''),
            order: this.ngFormBuilder.control(system?.order),
            sites: this.ngFormBuilder.array(system?.sites?.map((site) => this.newSiteForm(site)) || []),
            systemRef: this.ngFormBuilder.control(system?.systemRef, [Validators.required, Validators.min(1), this.validateSystemRef()]),
            talkgroups: this.ngFormBuilder.array(system?.talkgroups?.map((talkgroup) => this.newTalkgroupForm(talkgroup)) || []),
            type: this.ngFormBuilder.control(system?.type || ''),
            units: this.ngFormBuilder.array(system?.units?.map((unit) => this.newUnitForm(unit)) || []),
            preferredApiKeyId: this.ngFormBuilder.control(system?.preferredApiKeyId),
            noAudioAlertsEnabled: this.ngFormBuilder.control(system?.noAudioAlertsEnabled !== false), // Default to true
            noAudioThresholdMinutes: this.ngFormBuilder.control(system?.noAudioThresholdMinutes || 30), // Default to 30
        });
    }

    newTagForm(tag?: Tag): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(tag?.id),
            alert: this.ngFormBuilder.control(tag?.alert || ''),
            label: this.ngFormBuilder.control(tag?.label, Validators.required),
            led: this.ngFormBuilder.control(tag?.led || ''),
            order: this.ngFormBuilder.control(tag?.order),
            color: this.ngFormBuilder.control(tag?.color || ''),
        });
    }

    newTalkgroupForm(talkgroup?: Talkgroup): FormGroup {
        // Build tone sets FormArray
        const toneSetsArray: FormArray = this.ngFormBuilder.array([]);
        if (talkgroup?.toneSets && Array.isArray(talkgroup.toneSets)) {
            talkgroup.toneSets.forEach((toneSet: any) => {
                const toneSetForm = this.ngFormBuilder.group({
                    id: this.ngFormBuilder.control(toneSet.id || this.generateToneSetId()),
                    label: this.ngFormBuilder.control(toneSet.label || ''),
                    aToneFrequency: this.ngFormBuilder.control(toneSet.aTone?.frequency || null),
                    aToneMinDuration: this.ngFormBuilder.control(toneSet.aTone?.minDuration || null),
                    aToneMaxDuration: this.ngFormBuilder.control(toneSet.aTone?.maxDuration || null),
                    bToneFrequency: this.ngFormBuilder.control(toneSet.bTone?.frequency || null),
                    bToneMinDuration: this.ngFormBuilder.control(toneSet.bTone?.minDuration || null),
                    bToneMaxDuration: this.ngFormBuilder.control(toneSet.bTone?.maxDuration || null),
                    longToneFrequency: this.ngFormBuilder.control(toneSet.longTone?.frequency || null),
                    longToneMinDuration: this.ngFormBuilder.control(toneSet.longTone?.minDuration || null),
                    longToneMaxDuration: this.ngFormBuilder.control(toneSet.longTone?.maxDuration || null),
                    tolerance: this.ngFormBuilder.control(toneSet.tolerance || 10),
                    minDuration: this.ngFormBuilder.control(toneSet.minDuration || null),
                });
                toneSetsArray.push(toneSetForm as any);
            });
        }
        
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(talkgroup?.id),
            alert: this.ngFormBuilder.control(talkgroup?.alert),
            delay: this.ngFormBuilder.control(talkgroup?.delay),
            frequency: this.ngFormBuilder.control(talkgroup?.frequency, Validators.min(0)),
            groupIds: this.ngFormBuilder.control(talkgroup?.groupIds, [Validators.required, this.validateGroup()]),
            label: this.ngFormBuilder.control(talkgroup?.label, Validators.required),
            led: this.ngFormBuilder.control(talkgroup?.led || ''),
            name: this.ngFormBuilder.control(talkgroup?.name, Validators.required),
            order: this.ngFormBuilder.control(talkgroup?.order),
            tagId: this.ngFormBuilder.control(talkgroup?.tagId, [Validators.required, this.validateTag()]),
            talkgroupRef: this.ngFormBuilder.control(talkgroup?.talkgroupRef, [Validators.required, Validators.min(1), this.validateTalkgroupRef()]),
            type: this.ngFormBuilder.control(talkgroup?.type || ''),
            toneDetectionEnabled: this.ngFormBuilder.control(talkgroup?.toneDetectionEnabled || false),
            toneSets: toneSetsArray,
            preferredApiKeyId: this.ngFormBuilder.control(talkgroup?.preferredApiKeyId),
            excludeFromPreferredSite: this.ngFormBuilder.control(talkgroup?.excludeFromPreferredSite || false),
        });
    }

    importToneSets(format: 'twotone' | 'csv', content: string): Observable<ToneImportResponse> {
        return this.ngHttpClient.post<ToneImportResponse>(
            '/api/admin/tone-import',
            { format, content },
            { headers: this.getHeaders() },
        );
    }

    private generateToneSetId(): string {
        return `tone-set-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
    }

    newUnitForm(unit?: Unit): FormGroup {
        return this.ngFormBuilder.group({
            id: this.ngFormBuilder.control(unit?.id),
            label: this.ngFormBuilder.control(unit?.label, Validators.required),
            order: this.ngFormBuilder.control(unit?.order),
            unitRef: this.ngFormBuilder.control(unit?.unitRef, [Validators.min(1), this.validateUnitRef()]),
            unitFrom: this.ngFormBuilder.control(unit?.unitFrom, [Validators.min(1), this.validateUnitFrom()]),
            unitTo: this.ngFormBuilder.control(unit?.unitTo, [Validators.min(1), this.validateUnitTo()])
        });
    }

    private configWebSocketClose(): void {
        if (this.configWebSocket instanceof WebSocket) {
            this.configWebSocket.onclose = null;
            this.configWebSocket.onmessage = null;
            this.configWebSocket.onopen = null;

            this.configWebSocket.close();

            this.configWebSocket = undefined;
        }
    }

    private configWebSocketReconnect(): void {
        this.configWebSocketClose();

        this.configWebSocketOpen();
    }

    private configWebSocketOpen(): void {
        if (!this.token) {
            return;
        }

        const webSocketUrl = new URL(this.getUrl(url.config), window.location.href).href.replace(/^http/, 'ws');

        this.configWebSocket = new WebSocket(webSocketUrl);

        this.configWebSocket.onclose = (ev: CloseEvent) => {
            if (ev.code === 1000) {
                this.token = '';

                this.event.emit({ authenticated: this.authenticated });
            } else {
                timer(2000).subscribe(() => this.configWebSocketReconnect());
            }
        };

        this.configWebSocket.onopen = () => {
            this.configWebSocket?.send(this.token);

            if (this.configWebSocket instanceof WebSocket) {
                this.configWebSocket.onmessage = (ev: MessageEvent<string>) => {
                    this.event.emit({ config: JSON.parse(ev.data) });
                }
            }
        }
    }

    private errorHandler(error: unknown): void {
        if (!(error instanceof HttpErrorResponse)) {
            return;
        }

        if (error.status === 401) {
            this.token = '';

            this.event.emit({ authenticated: this.authenticated });

            this.configWebSocketClose();

        } else {
            this.matSnackBar.open(error.message, '', { duration: 5000 });
        }
    }

    private getHeaders(): HttpHeaders {
        return new HttpHeaders({
            Authorization: this.token || '',
        });
    }

    public getAuthHeaders(): HttpHeaders {
        return this.getHeaders();
    }

    public getFetchHeaders(): Record<string, string> {
        const headers = this.getHeaders();
        const result: Record<string, string> = {};
        headers.keys().forEach(key => {
            result[key] = headers.get(key) || '';
        });
        return result;
    }

    private getUrl(path: string): string {
        // FORCE REBUILD - URL duplication fix
        // Get the base URL without the current path
        const baseUrl = window.location.origin;
        
        // Construct the final URL
        let finalUrl;
        if (path.startsWith('/api/admin')) {
            finalUrl = `${baseUrl}${path}`;
        } else if (path.charAt(0) === '/') {
            finalUrl = `${baseUrl}/api/admin${path}`;
        } else {
            finalUrl = `${baseUrl}/api/admin/${path}`;
        }
        
        // Add timestamp to force cache refresh
        const timestamp = Date.now();
        // Check if the URL already has query parameters
        const separator = finalUrl.includes('?') ? '&' : '?';
        finalUrl = `${finalUrl}${separator}_t=${timestamp}`;
        
        return finalUrl;
    }

    private validateApikey(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (typeof control.value !== 'string' || !control.value.length) {
                return null;
            }

            const apikeys: Apikey[] = control.parent?.parent?.getRawValue() || [];

            const count = apikeys.reduce((c, a) => c += a.key === control.value ? 1 : 0, 0);

            return count > 1 ? { duplicate: true } : null;
        };
    }

    private validateBlacklists(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            return typeof control.value === 'string' && control.value.length ? /^[0-9]+(,[0-9]+)*$/.test(control.value) ? null : { invalid: true } : null;
        };
    }

    private validateDirectory(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (typeof control.value !== 'string' || !control.value.length) {
                return null;
            }

            if (control.value.startsWith('\\')) {
                return { network: true }
            }

            const dirwatch: Dirwatch[] = control.parent?.parent?.getRawValue() || [];

            const count = dirwatch.reduce((c, a) => c += a.directory === control.value ? 1 : 0, 0);

            return count > 1 ? { duplicate: true } : null;
        };
    }

    private validateDirwatchSystemId(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            const dirwatch = control.parent?.getRawValue() || {};

            const mask = dirwatch.mask || '';

            const type = dirwatch.type;

            return ['dsdplus', 'sdr-trunk', 'trunk-recorder'].includes(type) || control.value !== null || /#SYS/.test(mask) ? null : { required: true };
        };
    }

    private validateDirwatchTalkgroupId(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            const dirwatch = control.parent?.getRawValue() || {};

            const mask = dirwatch.mask || '';

            const type = dirwatch.type;

            return ['dsdplus', 'sdr-trunk', 'trunk-recorder'].includes(type) || control.value !== null || /#TG/.test(mask) ? null : { required: true };
        };
    }

    private validateDownstreamUrl(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (typeof control.value !== 'string' || !control.value.length) {
                return null;
            }

            const downstream: Downstream[] = control.parent?.parent?.getRawValue() || [];

            const count = downstream.reduce((c, a) => c += a.url === control.value ? 1 : 0, 0);

            return count > 1 ? { duplicate: true } : null;
        };
    }

    private validateExtension(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (typeof control.value !== 'string' || !control.value.length) {
                return null;
            }

            return /^[0-9a-zA-Z]+$/.test(control.value) ? null : { invalid: true };
        };
    }

    private validateGroup(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (typeof control.value !== 'number') {
                return null;
            }

            const groupIds = control.root.get('groups')?.value.map((group: Group) => group.id);

            return groupIds ? groupIds.includes(control.value) ? null : { required: true } : null;
        };
    }

    private validateMask(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (typeof control.value !== 'string') {
                return null;
            }

            const masks = ['#DATE', '#GROUP', '#HZ', '#KHZ', '#MHZ', '#SITE', '#SITELBL', '#SYS', '#SYSLBL', '#TAG', '#TG', '#TGAFS', '#TGHZ', '#TGKHZ', '#TGLBL', '#TGMHZ', '#TIME', '#UNIT', '#UNITLBL', '#ZTIME'];

            const metas = control.value.match(/(#[A-Z]+)/g) || [] as string[];

            const count = metas.reduce((c, m) => {
                if (masks.includes(m)) {
                    c++;
                }

                return c;
            }, 0);

            return count ? null : { invalid: true };
        };
    }

    private validateSiteRef(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (control.value === null || control.value === '') {
                return null;
            }

            const sites: Site[] = control.parent?.parent?.getRawValue() || [];

            const count = sites.reduce((c, s) => c += s.siteRef === control.value ? 1 : 0, 0);

            return count > 1 ? { duplicate: true } : null;
        };
    }

    private validateSystemRef(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (control.value === null || typeof control.value !== 'number') {
                return null;
            }

            const systems: System[] = control.parent?.parent?.getRawValue() || [];

            const count = systems.reduce((c, s) => c += control.value !== null && control.value > 0 && s.systemRef === control.value ? 1 : 0, 0);

            return count > 1 ? { duplicate: true } : null;
        };
    }

    private validateTag(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            // If no value selected, let the required validator handle it
            if (control.value === null || control.value === undefined) {
                return null;
            }

            // If value is not a number, it's invalid (but let other validators handle it)
            if (typeof control.value !== 'number') {
                return null;
            }

            // Get all tag IDs from the config, filtering out undefined IDs (newly created tags)
            const tagsFormArray = control.root.get('tags');
            if (!tagsFormArray) {
                // If tags array doesn't exist yet, allow any value
                return null;
            }
            
            const tags = tagsFormArray.value || [];
            const tagIds = tags.map((tag: Tag) => tag.id).filter((id: number | undefined) => id !== undefined && id !== null);

            // If tagIds array is empty, allow any value (tags not loaded yet)
            if (!tagIds || tagIds.length === 0) {
                return null;
            }

            // Check if the selected tagId exists in the list of valid tag IDs
            return tagIds.includes(control.value) ? null : { required: true };
        };
    }

    private validateTalkgroupRef(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (control.value === null || typeof control.value !== 'number') {
                return null;
            }

            const talkgroups: Talkgroup[] = control.parent?.parent?.getRawValue() || [];

            const count = talkgroups.reduce((c, t) => c += t.talkgroupRef === control.value ? 1 : 0, 0);

            return count > 1 ? { duplicate: true } : null;
        };
    }

    private validateUnitRef(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            const unitRef = control.parent?.get('unitRef')?.value;

            const unitFrom = control.parent?.get('unitFrom')?.value;

            const unitTo = control.parent?.get('unitTo')?.value;

            const units: Unit[] = control.parent?.parent?.getRawValue() || [];

            const count = units.reduce((c, u) => c += u.unitRef === unitRef ? 1 : 0, 0);

            return unitRef === null && (unitFrom === null || unitTo === null)
                ? { required: true }
                : count > 1
                    ? { duplicate: true }
                    : null;
        };
    }

    private validateUnitFrom(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            const unitFrom = control.value;

            const unitTo = control.parent?.get('unitTo')?.value;

            if (typeof unitFrom === 'number' && typeof unitTo === 'number' && unitFrom >= unitTo) {
                return { range: true };
            }

            setTimeout(() => {
                control.parent?.get('unitRef')?.updateValueAndValidity();
                control.parent?.get('unitTo')?.updateValueAndValidity();
            });

            return null;
        }
    }

    private validateUnitTo(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            const unitFrom = control.parent?.get('unitFrom')?.value;

            const unitTo = control.value;

            if (typeof unitFrom === 'number' && typeof unitTo === 'number' && unitFrom >= unitTo) {
                return { range: true };
            }

            setTimeout(() => {
                control.parent?.get('unitRef')?.updateValueAndValidity();
                control.parent?.get('unitFrom')?.updateValueAndValidity();
            });

            return null;
        }
    }

    private validateUrl(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (typeof control.value !== 'string' || !control.value.length) {
                return null;
            }

            return /^https?:\/\/.+$/.test(control.value) ? null : { invalid: true }
        };
    }


	async testRadioReferenceConnection(username: string): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.post<any>(
				this.getUrl('/radioreference/test'),
				{ username },
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async searchRadioReferenceSystems(query: string): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.post<any>(
				this.getUrl('/radioreference/search'),
				{ query },
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async importRadioReferenceData(systemID: number, importType: string, categoryID?: number, categoryName?: string): Promise<any> {
		try {
			const payload: any = { 
				systemID, 
				importType, 
				destinationType: 'system',
				loadAll: true  // Use non-streaming mode (streaming has flusher compatibility issues)
			};
			
			if (categoryID && categoryName) {
				payload.categoryID = categoryID;
				payload.categoryName = categoryName;
			}
			
			// Use a much longer timeout for large imports (15 minutes)
			const res = await firstValueFrom(this.ngHttpClient.post<any>(
				this.getUrl('/radioreference/import'),
				payload,
				{ 
					headers: this.getHeaders(), 
					responseType: 'json'
				},
			).pipe(
				// Set timeout to 15 minutes (900000ms) to match backend processing time
				timeout(900000)
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	// ---- Radio Reference dropdown endpoints ----
	async rrGetCountries(): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl('/radioreference/countries'),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async rrGetStates(countryId: number): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl(`/radioreference/states?countryId=${countryId}`),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async rrGetCounties(stateId: number): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl(`/radioreference/counties?stateId=${stateId}`),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async rrGetSystems(countyId: number): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl(`/radioreference/systems?countyId=${countyId}`),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	// Alias methods for the talkgroup manager component
	async getRadioReferenceCountries(): Promise<any> {
		return this.rrGetCountries();
	}

	async getRadioReferenceStates(countryId: number): Promise<any> {
		return this.rrGetStates(countryId);
	}

	async getRadioReferenceCounties(stateId: number): Promise<any> {
		return this.rrGetCounties(stateId);
	}

	async getRadioReferenceSystems(countyId: number): Promise<any> {
		return this.rrGetSystems(countyId);
	}

	async getRadioReferenceTalkgroups(systemId: number): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl(`/radioreference/talkgroups?systemId=${systemId}`),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async getRadioReferenceTalkgroupCategories(systemId: number): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl(`/radioreference/talkgroup-categories?systemId=${systemId}`),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async getRadioReferenceTalkgroupsByCategory(systemId: number, categoryId: number, categoryName: string): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl(`/radioreference/talkgroups-by-category?systemId=${systemId}&categoryId=${categoryId}&categoryName=${encodeURIComponent(categoryName)}`),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async getRadioReferenceSites(systemId: number): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.get<any>(
				this.getUrl(`/radioreference/sites?systemId=${systemId}`),
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

	async reloadConfig(): Promise<any> {
		try {
			const res = await firstValueFrom(this.ngHttpClient.post<any>(
				this.getUrl('/config/reload'),
				{},
				{ headers: this.getHeaders(), responseType: 'json' },
			));
			return res;
		} catch (error: any) {
			this.errorHandler(error);
			return { success: false, error: error.message };
		}
	}

  async getAllUsers(): Promise<any> {
    try {
      const response = await firstValueFrom(this.ngHttpClient.get<any>(
        this.getUrl('/users'),
        { headers: this.getHeaders(), responseType: 'json' }
      ));
      return response;
    } catch (error: any) {
      console.error('Failed to get users:', error);
      throw error;
    }
  }

  async getAllGroups(): Promise<any> {
    try {
      const url = this.getUrl('/groups');
      
      const response = await firstValueFrom(
        this.ngHttpClient.get<any>(
          url,
          { headers: this.getHeaders(), responseType: 'json' }
        ).pipe(timeout(10000))
      );
      
      // The backend returns { groups: [...] }, so extract groups array
      if (response && response.groups) {
        return response.groups;
      } else if (Array.isArray(response)) {
        return response;
      } else {
        return [];
      }
    } catch (error: any) {
      console.error('Failed to get groups:', error);
      console.error('Error status:', error.status);
      console.error('Error message:', error.message);
      console.error('Error body:', error.error);
      throw error;
    }
  }

  async deleteUser(userId: number): Promise<any> {
    try {
      const response = await firstValueFrom(this.ngHttpClient.delete<any>(
        this.getUrl(`/users/${userId}`),
        { headers: this.getHeaders(), responseType: 'json' }
      ));
      return response;
    } catch (error: any) {
      console.error('Failed to delete user:', error);
      throw error;
    }
  }

  async updateUser(userId: number, userData: any): Promise<any> {
    try {
      const response = await firstValueFrom(this.ngHttpClient.put<any>(
        this.getUrl(`/users/${userId}`),
        userData,
        { headers: this.getHeaders(), responseType: 'json' }
      ));
      return response;
    } catch (error: any) {
      console.error('Failed to update user:', error);
      throw error;
    }
  }

  async syncStripeCustomers(): Promise<any> {
    try {
      const response = await firstValueFrom(this.ngHttpClient.post<any>(
        this.getUrl('/stripe-sync'),
        {},
        { headers: this.getHeaders(), responseType: 'json' }
      ));
      return response;
    } catch (error: any) {
      console.error('Failed to sync with Stripe:', error);
      throw error;
    }
  }

  async purgeData(type: 'calls' | 'logs', ids?: number[]): Promise<any> {
    try {
      const payload: any = { type };
      if (ids && ids.length > 0) {
        payload.ids = ids;
      }
      const response = await firstValueFrom(this.ngHttpClient.post<any>(
        this.getUrl(url.purge),
        payload,
        { headers: this.getHeaders(), responseType: 'json' }
      ));
      return response;
    } catch (error: any) {
      this.errorHandler(error);
      throw error;
    }
  }
}