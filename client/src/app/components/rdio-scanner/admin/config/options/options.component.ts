/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

import { Component, Input, OnInit, OnDestroy, OnChanges, SimpleChanges, AfterViewInit, ChangeDetectorRef, ChangeDetectionStrategy } from '@angular/core';
import { FormGroup, FormArray, Validators } from '@angular/forms';
import { Subscription } from 'rxjs';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatDialog } from '@angular/material/dialog';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { RequestAPIKeyDialogComponent } from './request-api-key-dialog.component';
import { RecoverAPIKeyDialogComponent } from './recover-api-key-dialog.component';
import { LocationDataService } from 'src/app/services/location-data.service';
import { OPENAI_CHAT_MODEL_OPTIONS, OpenAIChatModelOption, RdioScannerAdminService } from '../../admin.service';

export type OptionsPanelId =
    | 'alerts' | 'security' | 'branding' | 'notifications'
    | 'integrations' | 'general' | 'stripe' | 'transcription' | 'userRegistration';

interface OptionsPanelDef {
    keys: string[];
    systemsNoAudio?: boolean;
}

const OPTIONS_PANEL_DEFS: Record<OptionsPanelId, OptionsPanelDef> = {
    alerts: {
        keys: [
            'alertRetentionDays', 'systemHealthAlertsEnabled',
            'transcriptionFailureAlertsEnabled', 'transcriptionFailureThreshold',
            'transcriptionFailureTimeWindow', 'transcriptionFailureRepeatMinutes',
            'toneDetectionAlertsEnabled', 'toneDetectionIssueThreshold',
            'toneDetectionTimeWindow', 'toneDetectionRepeatMinutes',
            'autoLearnToneSetConfig',
            'noAudioAlertsEnabled', 'noAudioThresholdMinutes', 'noAudioRepeatMinutes',
        ],
        systemsNoAudio: true,
    },
    security: {
        keys: [
            'audioConversion', 'disableDuplicateDetection', 'duplicateTimestampWindow',
            'duplicateDetectionTimeFrame', 'audioEncryptionEnabled', 'rateLimitingEnabled',
            'maxDownloadsPerWindow', 'downloadWindowMinutes',
        ],
    },
    branding: {
        keys: ['branding', 'baseUrl', 'email', 'emailLogoBorderRadius', 'faviconFilename', 'emailLogoFilename'],
    },
    notifications: {
        keys: [
            'emailServiceEnabled', 'emailProvider', 'emailSendGridApiKey',
            'emailMailgunApiKey', 'emailMailgunDomain', 'emailMailgunApiBase',
            'emailSmtpHost', 'emailSmtpPort', 'emailSmtpUsername', 'emailSmtpPassword',
            'emailSmtpUseTLS', 'emailSmtpSkipVerify', 'emailSmtpFromEmail', 'emailSmtpFromName',
        ],
    },
    integrations: {
        keys: [
            'openAIIntegration', 'radioReferenceEnabled', 'radioReferenceUsername',
            'radioReferencePassword', 'relayServerAPIKey',
        ],
    },
    general: {
        keys: [
            'time12hFormat', 'autoPopulate', 'defaultSystemDelay', 'playbackGoesLive',
            'keypadBeeps', 'maxClients', 'pruneDays', 'showListenersCount', 'sortTalkgroups',
            'reconnectionGracePeriod', 'reconnectionMaxBufferSize', 'configSyncEnabled', 'configSyncPath',
        ],
    },
    stripe: {
        keys: [
            'stripePaywallEnabled', 'stripePublishableKey', 'stripeSecretKey',
            'stripeWebhookSecret', 'stripeGracePeriodDays',
        ],
    },
    transcription: {
        keys: ['transcriptionEnabled', 'transcriptionEnhancement', 'transcriptionConfig'],
    },
    userRegistration: {
        keys: [
            'userRegistrationEnabled', 'publicRegistrationEnabled', 'publicRegistrationMode',
            'emailVerificationRequired', 'turnstileEnabled', 'turnstileSiteKey', 'turnstileSecretKey',
        ],
    },
};

const OPTIONS_PANEL_LABELS: Record<OptionsPanelId, string> = {
    alerts: 'Alert & Health Monitoring',
    security: 'Audio Settings',
    branding: 'Branding',
    notifications: 'Email',
    integrations: 'External Integrations',
    general: 'General Settings',
    stripe: 'Stripe Payments',
    transcription: 'Transcription Settings',
    userRegistration: 'User Registration',
};

const OPTIONS_FIELD_LABELS: Record<string, string> = {
    alertRetentionDays: 'Alert retention days',
    systemHealthAlertsEnabled: 'System health alerts',
    transcriptionFailureAlertsEnabled: 'Transcription failure alerts',
    transcriptionFailureThreshold: 'Transcription failure threshold',
    transcriptionFailureTimeWindow: 'Transcription failure time window',
    transcriptionFailureRepeatMinutes: 'Transcription failure repeat interval',
    toneDetectionAlertsEnabled: 'Tone detection alerts',
    toneDetectionIssueThreshold: 'Tone detection issue threshold',
    toneDetectionTimeWindow: 'Tone detection time window',
    toneDetectionRepeatMinutes: 'Tone detection repeat interval',
    'autoLearnToneSetConfig.aToneMinDuration': 'A-tone min duration',
    'autoLearnToneSetConfig.aToneMaxDuration': 'A-tone max duration',
    'autoLearnToneSetConfig.bToneMinDuration': 'B-tone min duration',
    'autoLearnToneSetConfig.bToneMaxDuration': 'B-tone max duration',
    'autoLearnToneSetConfig.longToneMinDuration': 'Long-tone min duration',
    'autoLearnToneSetConfig.longToneMaxDuration': 'Long-tone max duration',
    'autoLearnToneSetConfig.callsRequired': 'Calls required',
    'autoLearnToneSetConfig.frequencyToleranceHz': 'Frequency tolerance (Hz)',
    noAudioAlertsEnabled: 'No-audio alerts',
    noAudioThresholdMinutes: 'No-audio threshold (minutes)',
    noAudioRepeatMinutes: 'No-audio repeat interval',
    audioConversion: 'Audio conversion',
    disableDuplicateDetection: 'Disable duplicate detection',
    duplicateTimestampWindow: 'Duplicate timestamp window',
    duplicateDetectionTimeFrame: 'Duplicate cache retention',
    audioEncryptionEnabled: 'Audio encryption',
    rateLimitingEnabled: 'Download rate limiting',
    maxDownloadsPerWindow: 'Max downloads per window',
    downloadWindowMinutes: 'Download window (minutes)',
    branding: 'Branding label',
    baseUrl: 'Base URL',
    email: 'Support email',
    emailLogoBorderRadius: 'Logo border radius',
    faviconFilename: 'Favicon',
    emailLogoFilename: 'Server logo',
    emailServiceEnabled: 'Email service',
    emailProvider: 'Email provider',
    emailSendGridApiKey: 'SendGrid API key',
    emailMailgunApiKey: 'Mailgun API key',
    emailMailgunDomain: 'Mailgun domain',
    emailMailgunApiBase: 'Mailgun API base',
    emailSmtpHost: 'SMTP host',
    emailSmtpPort: 'SMTP port',
    emailSmtpUsername: 'SMTP username',
    emailSmtpPassword: 'SMTP password',
    emailSmtpUseTLS: 'SMTP TLS',
    emailSmtpSkipVerify: 'SMTP skip certificate verify',
    emailSmtpFromEmail: 'From email address',
    emailSmtpFromName: 'From name',
    'openAIIntegration.baseUrl': 'OpenAI API base URL',
    'openAIIntegration.apiKey': 'OpenAI API key',
    'openAIIntegration.model': 'OpenAI chat model',
    radioReferenceEnabled: 'Radio Reference integration',
    radioReferenceUsername: 'Radio Reference username',
    radioReferencePassword: 'Radio Reference password',
    relayServerAPIKey: 'Relay server API key',
    time12hFormat: '12-hour time format',
    autoPopulate: 'Auto-populate',
    defaultSystemDelay: 'Default system delay',
    playbackGoesLive: 'Playback goes live',
    keypadBeeps: 'Keypad beeps',
    maxClients: 'Max clients',
    pruneDays: 'Prune days',
    showListenersCount: 'Show listeners count',
    sortTalkgroups: 'Sort talkgroups',
    reconnectionGracePeriod: 'Reconnection grace period',
    reconnectionMaxBufferSize: 'Reconnection max buffer size',
    configSyncEnabled: 'Config sync',
    configSyncPath: 'Config sync path',
    stripePaywallEnabled: 'Stripe paywall',
    stripePublishableKey: 'Stripe publishable key',
    stripeSecretKey: 'Stripe secret key',
    stripeWebhookSecret: 'Stripe webhook secret',
    stripeGracePeriodDays: 'Stripe grace period (days)',
    transcriptionEnabled: 'Transcription enabled',
    transcriptionEnhancement: 'Transcription audio enhancement',
    'transcriptionConfig.provider': 'Transcription provider',
    'transcriptionConfig.whisperAPIURL': 'Whisper API URL',
    'transcriptionConfig.whisperAPIKey': 'Whisper API key',
    'transcriptionConfig.whisperAPIModel': 'Whisper model',
    'transcriptionConfig.azureKey': 'Azure key',
    'transcriptionConfig.azureRegion': 'Azure region',
    'transcriptionConfig.googleAPIKey': 'Google API key',
    'transcriptionConfig.googleCredentials': 'Google credentials',
    'transcriptionConfig.assemblyAIKey': 'AssemblyAI key',
    'transcriptionConfig.assemblyAISpeechModel': 'AssemblyAI speech model',
    'transcriptionConfig.assemblyAIWordBoost': 'AssemblyAI word boost',
    'transcriptionConfig.cloudflareAccountID': 'Cloudflare account ID',
    'transcriptionConfig.cloudflareAPIToken': 'Cloudflare API token',
    'transcriptionConfig.cloudflareModel': 'Cloudflare model',
    'transcriptionConfig.language': 'Transcription language',
    'transcriptionConfig.prompt': 'Transcription prompt',
    'transcriptionConfig.timeoutSeconds': 'Transcription timeout',
    'transcriptionConfig.minCallDuration': 'Minimum call duration',
    'transcriptionConfig.workerPoolSize': 'Worker pool size',
    'transcriptionConfig.hallucinationPatterns': 'Hallucination removal patterns',
    'transcriptionConfig.hallucinationDetectionMode': 'Hallucination detection mode',
    'transcriptionConfig.hallucinationMinOccurrences': 'Hallucination min occurrences',
    userRegistrationEnabled: 'User registration',
    publicRegistrationEnabled: 'Public registration',
    publicRegistrationMode: 'Public registration mode',
    emailVerificationRequired: 'Email verification required',
    turnstileEnabled: 'Cloudflare Turnstile',
    turnstileSiteKey: 'Turnstile site key',
    turnstileSecretKey: 'Turnstile secret key',
};

export interface UnsavedPanelChanges {
    panelId: OptionsPanelId;
    panelLabel: string;
    fields: string[];
}

@Component({
    selector: 'rdio-scanner-admin-options',
    templateUrl: './options.component.html',
    styleUrls: ['./options.component.scss'],
})
export class RdioScannerAdminOptionsComponent implements OnInit, AfterViewInit, OnDestroy, OnChanges {
    @Input() form: FormGroup | undefined;
    @Input() systemsForm: FormArray | undefined;
    private radioReferenceSubscription?: Subscription;
    private formChangeSubscription?: Subscription;
    private systemsChangeSubscription?: Subscription;
    private initialLoadComplete = false;
    private panelBaselines: Partial<Record<OptionsPanelId, string>> = {};
    public isEditingRadioReference = false;
    panelsReady = false;
    private originalRadioReferenceUsername = '';
    private originalRadioReferencePassword = '';
    faviconUrl: string = '';
    window = window;
    
    // Expansion panel state - all collapsed by default
    generalExpanded = false;
    brandingExpanded = false;
    transcriptionExpanded = false;
    alertsExpanded = false;
    emailExpanded = false;
    notificationsExpanded = false;
    userRegistrationExpanded = false;
    stripeExpanded = false;
    integrationsExpanded = false;
    securityExpanded = false;
    
    // Central Management Integration
    centralConnectionStatus: 'success' | 'error' | null = null;
    centralConnectionMessage: string = '';
    showExternalAPIKey: boolean = false;
    readonly openAIChatModels = OPENAI_CHAT_MODEL_OPTIONS;

    get selectedOpenAIModel(): OpenAIChatModelOption | undefined {
        const id = this.form?.get('openAIIntegration')?.get('model')?.value || 'gpt-5.4-mini';
        return this.openAIChatModels.find(m => m.id === id) || this.openAIChatModels[0];
    }

    get isCentrallyManaged(): boolean {
        return this.form?.get('centralManagementEnabled')?.value === true;
    }

    /** Populated from GET /api/admin/relay-suspension when relay has fully suspended this scanner. */
    relaySuspensionStatus: {
        fully_suspended: boolean;
        suspend_message?: string;
        relay_owner_unlocked_public?: boolean;
        public_listener_blocked?: boolean;
        push_notifications_blocked?: boolean;
    } | null = null;

    get relaySuspensionBannerVisible(): boolean {
        const s = this.relaySuspensionStatus;
        return !!s && s.fully_suspended === true && s.push_notifications_blocked === true;
    }

    constructor(
        private snackBar: MatSnackBar,
        private dialog: MatDialog,
        private locationService: LocationDataService,
        private http: HttpClient,
        private cdr: ChangeDetectorRef,
        private adminService: RdioScannerAdminService,
    ) {}

    /** Per-panel save state for inline feedback. */
    savingPanel: OptionsPanelId | null = null;

    isPanelDirty(panelId: OptionsPanelId): boolean {
        const baseline = this.panelBaselines[panelId];
        if (!baseline || !this.form) {
            return false;
        }

        return JSON.stringify(this.snapshotPanel(panelId)) !== baseline;
    }

    get hasUnsavedChanges(): boolean {
        return this.unsavedChanges.length > 0;
    }

    get unsavedChanges(): UnsavedPanelChanges[] {
        if (!this.form || !this.initialLoadComplete) {
            return [];
        }

        const groups: UnsavedPanelChanges[] = [];
        (Object.keys(OPTIONS_PANEL_DEFS) as OptionsPanelId[]).forEach((panelId) => {
            const fields = this.getPanelFieldChanges(panelId);
            if (fields.length) {
                groups.push({
                    panelId,
                    panelLabel: OPTIONS_PANEL_LABELS[panelId],
                    fields,
                });
            }
        });

        return groups;
    }

    goToUnsavedPanel(panelId: OptionsPanelId): void {
        this.openPanel(`${panelId}Expanded`);
    }

    /**
     * Save one options section. Only sends keys belonging to that panel; toggles in
     * other sections are unaffected. Per-system no-audio overrides save separately.
     */
    async savePanel(panelId: OptionsPanelId): Promise<void> {
        if (!this.form || !this.isPanelDirty(panelId)) {
            return;
        }

        const def = OPTIONS_PANEL_DEFS[panelId];
        const payload = this.buildPayloadForKeys(def.keys);

        this.savingPanel = panelId;
        this.cdr.markForCheck();

        const updated = await this.adminService.updateOptions(payload);
        let ok = !!updated;

        if (ok && def.systemsNoAudio) {
            ok = await this.saveDirtySystemsNoAudio();
        }

        this.savingPanel = null;
        if (ok) {
            this.refreshPanelBaseline(panelId);
            this.snackBar.open(`${OPTIONS_PANEL_LABELS[panelId]} saved`, 'Close', { duration: 1500 });
        } else {
            this.snackBar.open('Failed to save. Please try again.', 'Close', { duration: 4000 });
        }
        this.cdr.markForCheck();
    }

    private buildPayloadForKeys(keys: string[]): Record<string, any> {
        const raw = this.form!.getRawValue();
        const slice: Record<string, any> = {};
        for (const key of keys) {
            if (raw[key] !== undefined) {
                slice[key] = raw[key];
            }
        }
        return this.normalizeOptionsPayload(slice);
    }

    private normalizeOptionsPayload(payload: Record<string, any>): Record<string, any> {
        const result = { ...payload };

        if ('rateLimitingEnabled' in result) {
            if (!result['rateLimitingEnabled']) {
                result['maxDownloadsPerWindow'] = 0;
            }
            delete result['rateLimitingEnabled'];
        }

        if (result['transcriptionConfig']) {
            if (result['transcriptionEnabled'] !== undefined) {
                result['transcriptionConfig'] = {
                    ...result['transcriptionConfig'],
                    enabled: result['transcriptionEnabled'],
                };
            }

            const patterns = result['transcriptionConfig'].hallucinationPatterns;
            if (typeof patterns === 'string') {
                result['transcriptionConfig'].hallucinationPatterns = patterns
                    .split('\n').map((l: string) => l.trim()).filter((l: string) => l.length > 0);
            }
            const wordBoost = result['transcriptionConfig'].assemblyAIWordBoost;
            if (typeof wordBoost === 'string') {
                result['transcriptionConfig'].assemblyAIWordBoost = wordBoost
                    .split('\n').map((l: string) => l.trim()).filter((l: string) => l.length > 0);
            }
        }

        if ('relayServerAPIKey' in result) {
            result['relayServerURL'] = 'https://tlradioserver.thinlineds.com';
        }

        return result;
    }

    private snapshotPanel(panelId: OptionsPanelId): unknown {
        const raw = this.form?.getRawValue();
        const def = OPTIONS_PANEL_DEFS[panelId];
        const snapshot: Record<string, unknown> = {};

        for (const key of def.keys) {
            snapshot[key] = raw?.[key];
        }

        if (def.systemsNoAudio && this.systemsForm) {
            snapshot['__systemsNoAudio'] = this.systemsForm.controls.map((ctrl) => ({
                id: ctrl.value.id,
                noAudioAlertsEnabled: ctrl.value.noAudioAlertsEnabled,
                noAudioThresholdMinutes: ctrl.value.noAudioThresholdMinutes,
            }));
        }

        return snapshot;
    }

    private captureAllPanelBaselines(): void {
        (Object.keys(OPTIONS_PANEL_DEFS) as OptionsPanelId[]).forEach((panelId) => {
            this.refreshPanelBaseline(panelId);
        });
    }

    private refreshPanelBaseline(panelId: OptionsPanelId): void {
        this.panelBaselines[panelId] = JSON.stringify(this.snapshotPanel(panelId));
    }

    private getPanelFieldChanges(panelId: OptionsPanelId): string[] {
        const baselineJson = this.panelBaselines[panelId];
        if (!baselineJson || !this.form) {
            return [];
        }

        const baseline = JSON.parse(baselineJson) as Record<string, unknown>;
        const current = this.snapshotPanel(panelId) as Record<string, unknown>;
        const labels: string[] = [];

        for (const key of OPTIONS_PANEL_DEFS[panelId].keys) {
            this.collectFieldChanges(key, baseline[key], current[key], labels);
        }

        if (OPTIONS_PANEL_DEFS[panelId].systemsNoAudio) {
            this.collectSystemsNoAudioChanges(
                baseline['__systemsNoAudio'] as { id: number; noAudioAlertsEnabled: boolean; noAudioThresholdMinutes: number }[] | undefined,
                current['__systemsNoAudio'] as { id: number; noAudioAlertsEnabled: boolean; noAudioThresholdMinutes: number }[] | undefined,
                labels,
            );
        }

        return labels;
    }

    private collectFieldChanges(path: string, oldVal: unknown, newVal: unknown, labels: string[]): void {
        const topKey = path.split('.')[0];
        if (RdioScannerAdminOptionsComponent.TOGGLE_KEYS.includes(topKey)) {
            return;
        }

        if (this.valuesEqual(oldVal, newVal)) {
            return;
        }

        const isObject = (v: unknown): v is Record<string, unknown> =>
            v !== null && typeof v === 'object' && !Array.isArray(v);

        if (isObject(oldVal) || isObject(newVal)) {
            const oldObj = isObject(oldVal) ? oldVal : {};
            const newObj = isObject(newVal) ? newVal : {};
            const keys = new Set([...Object.keys(oldObj), ...Object.keys(newObj)]);
            keys.forEach((key) => {
                this.collectFieldChanges(`${path}.${key}`, oldObj[key], newObj[key], labels);
            });
            return;
        }

        labels.push(this.labelForField(path));
    }

    private collectSystemsNoAudioChanges(
        baseline: { id: number; noAudioAlertsEnabled: boolean; noAudioThresholdMinutes: number }[] | undefined,
        current: { id: number; noAudioAlertsEnabled: boolean; noAudioThresholdMinutes: number }[] | undefined,
        labels: string[],
    ): void {
        if (!current?.length) {
            return;
        }

        for (const entry of current) {
            const saved = baseline?.find((s) => s.id === entry.id);
            if (!saved) {
                continue;
            }

            const systemLabel = this.systemsForm?.controls
                .find((ctrl) => ctrl.value.id === entry.id)?.value?.label || `System ${entry.id}`;

            if (saved.noAudioAlertsEnabled !== entry.noAudioAlertsEnabled) {
                labels.push(`${systemLabel}: no-audio alerts`);
            }
            if (saved.noAudioThresholdMinutes !== entry.noAudioThresholdMinutes) {
                labels.push(`${systemLabel}: no-audio threshold`);
            }
        }
    }

    private valuesEqual(a: unknown, b: unknown): boolean {
        return JSON.stringify(a) === JSON.stringify(b);
    }

    private labelForField(path: string): string {
        const known = OPTIONS_FIELD_LABELS[path];
        if (known) {
            return known;
        }

        const leaf = path.split('.').pop() || path;
        if (this.isSensitiveField(leaf)) {
            return `${this.humanizeKey(leaf)} (modified)`;
        }

        return this.humanizeKey(leaf);
    }

    private isSensitiveField(key: string): boolean {
        const lower = key.toLowerCase();
        return lower.includes('password')
            || lower.includes('secret')
            || lower.includes('apikey')
            || lower.includes('credentials')
            || lower.includes('token');
    }

    private humanizeKey(key: string): string {
        const spaced = key
            .replace(/([A-Z])/g, ' $1')
            .replace(/_/g, ' ')
            .trim();
        return spaced.charAt(0).toUpperCase() + spaced.slice(1);
    }

    private refreshBaselinesForKey(key: string): void {
        (Object.keys(OPTIONS_PANEL_DEFS) as OptionsPanelId[]).forEach((panelId) => {
            const def = OPTIONS_PANEL_DEFS[panelId];
            if (def.keys.includes(key)
                || (key === 'rateLimitingEnabled' && panelId === 'security')
                || (key === 'maxDownloadsPerWindow' && panelId === 'security')
                || (key === 'transcriptionEnabled' && panelId === 'transcription')) {
                this.refreshPanelBaseline(panelId);
            }
        });
    }

    /** Keep mirrored form fields aligned after a toggle auto-save (before baseline snapshot). */
    private syncRelatedFieldsAfterToggleSave(key: string, value: unknown): void {
        if (key === 'transcriptionEnabled') {
            this.form?.get('transcriptionConfig')?.get('enabled')?.setValue(!!value, { emitEvent: false });
        }
    }

    private async saveDirtySystemsNoAudio(): Promise<boolean> {
        if (!this.systemsForm) {
            return true;
        }

        const baseline = JSON.parse(this.panelBaselines.alerts || '{}').__systemsNoAudio as {
            id: number;
            noAudioAlertsEnabled: boolean;
            noAudioThresholdMinutes: number;
        }[] | undefined;

        let allOk = true;
        for (const ctrl of this.systemsForm.controls) {
            const id = ctrl.value.id;
            const current = {
                noAudioAlertsEnabled: ctrl.value.noAudioAlertsEnabled ?? false,
                noAudioThresholdMinutes: ctrl.value.noAudioThresholdMinutes ?? 0,
            };
            const saved = baseline?.find((s) => s.id === id);
            const changed = !saved
                || saved.noAudioAlertsEnabled !== current.noAudioAlertsEnabled
                || saved.noAudioThresholdMinutes !== current.noAudioThresholdMinutes;

            if (!changed) {
                continue;
            }

            try {
                await this.adminService.saveSystemNoAudioSettings(
                    id,
                    current.noAudioAlertsEnabled,
                    current.noAudioThresholdMinutes || 30,
                );
                ctrl.markAsPristine();
            } catch {
                allOk = false;
            }
        }

        return allOk;
    }

    private setupFormChangeTracking(): void {
        this.formChangeSubscription?.unsubscribe();
        this.systemsChangeSubscription?.unsubscribe();

        if (this.form) {
            this.formChangeSubscription = this.form.valueChanges.subscribe(() => {
                if (this.initialLoadComplete) {
                    this.cdr.markForCheck();
                }
            });
        }

        if (this.systemsForm) {
            this.systemsChangeSubscription = this.systemsForm.valueChanges.subscribe(() => {
                if (this.initialLoadComplete) {
                    this.cdr.markForCheck();
                }
            });
        }
    }

    /** Top-level boolean option controls that auto-save the moment they change. */
    private static readonly TOGGLE_KEYS = [
        'systemHealthAlertsEnabled', 'transcriptionFailureAlertsEnabled', 'toneDetectionAlertsEnabled',
        'noAudioAlertsEnabled', 'disableDuplicateDetection', 'audioEncryptionEnabled', 'rateLimitingEnabled',
        'time12hFormat', 'autoPopulate', 'playbackGoesLive', 'showListenersCount', 'sortTalkgroups',
        'emailServiceEnabled', 'emailSmtpUseTLS', 'emailSmtpSkipVerify', 'radioReferenceEnabled',
        'stripePaywallEnabled', 'transcriptionEnabled', 'transcriptionEnhancement', 'userRegistrationEnabled',
        'publicRegistrationEnabled', 'emailVerificationRequired', 'turnstileEnabled', 'configSyncEnabled',
        'adminLocalhostOnly', 'adminPasswordLoginDisabled',
    ];

    private toggleSubscriptions: Subscription[] = [];

    /** Subscribe each toggle so flipping it immediately persists just that key. */
    private setupToggleAutoSave(): void {
        this.toggleSubscriptions.forEach(s => s.unsubscribe());
        this.toggleSubscriptions = [];
        if (!this.form) return;

        for (const key of RdioScannerAdminOptionsComponent.TOGGLE_KEYS) {
            const ctrl = this.form.get(key);
            if (!ctrl) continue;
            this.toggleSubscriptions.push(ctrl.valueChanges.subscribe((value) => {
                if (!this.initialLoadComplete) return;
                this.saveOptionKey(key, value);
            }));
        }
    }

    /** Persist a single option key (used by toggle auto-save). */
    private async saveOptionKey(key: string, value: any): Promise<void> {
        let partial: { [key: string]: any };

        if (key === 'rateLimitingEnabled') {
            // Synthetic control: maps onto maxDownloadsPerWindow (0 = disabled).
            const max = this.form?.get('maxDownloadsPerWindow')?.value || 100;
            partial = { maxDownloadsPerWindow: value ? max : 0 };
        } else if (key === 'transcriptionEnabled') {
            // Flat toggle drives transcriptionConfig.enabled server-side.
            partial = { transcriptionEnabled: value };
        } else {
            partial = { [key]: value };
        }

        this.syncRelatedFieldsAfterToggleSave(key, value);

        const updated = await this.adminService.updateOptions(partial);
        if (updated) {
            this.syncRelatedFieldsAfterToggleSave(key, value);
            this.refreshBaselinesForKey(key);
            this.snackBar.open('Saved', 'Close', { duration: 1500 });
        } else {
            this.snackBar.open('Failed to save. Please try again.', 'Close', { duration: 4000 });
        }
        this.cdr.markForCheck();
    }

    asFormGroup(ctrl: any): FormGroup {
        return ctrl as FormGroup;
    }

    /** Programmatically open a specific expansion panel (called by global search). */
    openPanel(panelName: string): void {
        const key = panelName as keyof this;
        if (key in this) {
            (this as any)[key] = true;
            this.cdr.markForCheck();
            setTimeout(() => {
                const el = document.getElementById('opt-panel-' + panelName);
                if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
            }, 180);
        }
    }

    get isRadioReferenceLoggedIn(): boolean {
        return this.hasStoredRadioReferenceCredentials();
    }

    get shouldShowLoginForm(): boolean {
        return this.isEditingRadioReference || !this.isRadioReferenceLoggedIn;
    }

    ngOnInit(): void {
        this.setupRadioReferenceValidation();
        this.setInitialRadioReferenceValidation();
        this.storeOriginalCredentials();
        this.setupRelayServerFormListeners();
        this.setupRateLimitingToggle();
        this.setupAudioEncryptionToggle();
        this.setHardcodedRelayServerURL();
        this.updateFaviconUrl();
        this.updateEmailLogoUrl();
        this.setupToggleAutoSave();
        this.setupFormChangeTracking();
        setTimeout(() => {
            this.initialLoadComplete = true;
            this.captureAllPanelBaselines();
            this.cdr.markForCheck();
        }, 100);
    }

    ngAfterViewInit(): void {
        setTimeout(() => {
            this.panelsReady = true;
            this.refreshRelaySuspensionStatus();
            this.cdr.detectChanges();
        }, 80);
    }

    ngOnDestroy(): void {
        this.radioReferenceSubscription?.unsubscribe();
        this.formChangeSubscription?.unsubscribe();
        this.systemsChangeSubscription?.unsubscribe();
        this.toggleSubscriptions.forEach(s => s.unsubscribe());
    }

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['form'] && this.form) {
            // Collapse all panels explicitly before hiding so they are in the right state when re-shown
            this.generalExpanded = false;
            this.brandingExpanded = false;
            this.transcriptionExpanded = false;
            this.alertsExpanded = false;
            this.notificationsExpanded = false;
            this.userRegistrationExpanded = false;
            this.stripeExpanded = false;
            this.integrationsExpanded = false;
            this.securityExpanded = false;

            this.panelsReady = false;
            this.cdr.detectChanges(); // Force the hide to apply immediately

            this.setupRadioReferenceValidation();
            this.setInitialRadioReferenceValidation();
            this.storeOriginalCredentials();
            this.setupRelayServerFormListeners();
            this.setupRateLimitingToggle();
            this.setupAudioEncryptionToggle();
            this.setHardcodedRelayServerURL();
            this.setupToggleAutoSave();
            this.setupFormChangeTracking();
            this.isEditingRadioReference = false;
            this.updateEmailLogoUrl();

            setTimeout(() => {
                this.panelsReady = true;
                this.captureAllPanelBaselines();
                this.cdr.detectChanges();
            }, 80);
        }

        if (changes['systemsForm'] && this.systemsForm) {
            this.setupFormChangeTracking();
            if (this.initialLoadComplete) {
                this.refreshPanelBaseline('alerts');
            }
        }
    }

    private setupRadioReferenceValidation(): void {
        if (!this.form) return;

        const radioReferenceEnabledControl = this.form.get('radioReferenceEnabled');
        const usernameControl = this.form.get('radioReferenceUsername');
        const passwordControl = this.form.get('radioReferencePassword');

        if (radioReferenceEnabledControl && usernameControl && passwordControl) {
            // Listen to enabled toggle changes
            this.radioReferenceSubscription = radioReferenceEnabledControl.valueChanges.subscribe(enabled => {
                if (enabled) {
                    usernameControl.setValidators([Validators.required]);
                    passwordControl.setValidators([Validators.required]);
                } else {
                    usernameControl.clearValidators();
                    passwordControl.clearValidators();
                }
                
                usernameControl.updateValueAndValidity();
                passwordControl.updateValueAndValidity();
                
                // Force form to detect changes
                if (this.form) {
                    this.form.markAsDirty();
                    this.form.updateValueAndValidity();
                }
            });

            // Listen to username changes (only after initial load to avoid marking form dirty on auto-populate)
            usernameControl.valueChanges.subscribe(() => {
                if (this.initialLoadComplete) {
                    if (this.form) {
                        this.form.markAsDirty();
                    }
                }
            });

            // Listen to password changes (only after initial load to avoid marking form dirty on auto-populate)
            passwordControl.valueChanges.subscribe(() => {
                if (this.initialLoadComplete) {
                    if (this.form) {
                        this.form.markAsDirty();
                    }
                }
            });
        }
    }

    private setInitialRadioReferenceValidation(): void {
        if (!this.form) return;

        const radioReferenceEnabledControl = this.form.get('radioReferenceEnabled');
        const usernameControl = this.form.get('radioReferenceUsername');
        const passwordControl = this.form.get('radioReferencePassword');

        if (radioReferenceEnabledControl && usernameControl && passwordControl) {
            const enabled = radioReferenceEnabledControl.value;
            if (enabled) {
                usernameControl.setValidators([Validators.required]);
                passwordControl.setValidators([Validators.required]);
            } else {
                usernameControl.clearValidators();
                passwordControl.clearValidators();
            }
            
            usernameControl.updateValueAndValidity();
            passwordControl.updateValueAndValidity();
        }
    }

    private storeOriginalCredentials(): void {
        if (!this.form) return;
        
        // Store the current values as original values
        this.originalRadioReferenceUsername = this.form.get('radioReferenceUsername')?.value || '';
        this.originalRadioReferencePassword = this.form.get('radioReferencePassword')?.value || '';
    }

    editRadioReferenceLogin(): void {
        if (!this.form) return;
        
        // Store current values as original before editing
        this.storeOriginalCredentials();
        
        // Enter edit mode
        this.isEditingRadioReference = true;
        
        // Keep the username but clear the password for editing
        this.form.get('radioReferencePassword')?.setValue('');
        this.form.markAsDirty();
    }

    cancelEditRadioReference(): void {
        if (!this.form) return;
        
        // Restore the original username and password values
        this.form.get('radioReferenceUsername')?.setValue(this.originalRadioReferenceUsername);
        this.form.get('radioReferencePassword')?.setValue(this.originalRadioReferencePassword);
        
        // Exit edit mode
        this.isEditingRadioReference = false;
        
        // Mark form as pristine since we've restored original values
        this.form.markAsPristine();
    }

    removeRadioReferenceAccount(): void {
        if (!this.form) return;
        
        // Exit edit mode if we were editing
        this.isEditingRadioReference = false;
        
        // Clear credentials and disable Radio Reference
        this.form.get('radioReferenceEnabled')?.setValue(false);
        this.form.get('radioReferenceUsername')?.setValue('');
        this.form.get('radioReferencePassword')?.setValue('');
        this.originalRadioReferenceUsername = '';
        this.originalRadioReferencePassword = '';
        this.form.markAsDirty();
    }

    private hasStoredRadioReferenceCredentials(): boolean {
        return !!(this.originalRadioReferenceUsername && this.originalRadioReferencePassword);
    }

    private setHardcodedRelayServerURL(): void {
        if (!this.form) return;
        
        const relayServerURLControl = this.form.get('relayServerURL');
        if (relayServerURLControl) {
            relayServerURLControl.setValue('https://tlradioserver.thinlineds.com', { emitEvent: false });
        }
    }

    private setupRelayServerFormListeners(): void {
        if (!this.form) return;

        const relayServerURLControl = this.form.get('relayServerURL');
        const relayServerAPIKeyControl = this.form.get('relayServerAPIKey');

        // Don't listen to relayServerURL changes since it's hardcoded
        // if (relayServerURLControl) {
        //     relayServerURLControl.valueChanges.subscribe(() => {
        //         if (this.initialLoadComplete && this.form) {
        //             this.form.markAsDirty();
        //         }
        //     });
        // }

        if (relayServerAPIKeyControl) {
            relayServerAPIKeyControl.valueChanges.subscribe(() => {
                if (this.initialLoadComplete && this.form) {
                    this.form.markAsDirty();
                }
            });
        }
    }

    private setupRateLimitingToggle(): void {
        if (!this.form) return;

        const toggleControl = this.form.get('rateLimitingEnabled');
        const maxControl = this.form.get('maxDownloadsPerWindow');
        const windowControl = this.form.get('downloadWindowMinutes');

        if (!toggleControl || !maxControl || !windowControl) return;

        const applyState = (enabled: boolean) => {
            if (enabled) {
                maxControl.enable({ emitEvent: false });
                windowControl.enable({ emitEvent: false });
            } else {
                maxControl.disable({ emitEvent: false });
                windowControl.disable({ emitEvent: false });
            }
        };

        // Apply initial state
        applyState(toggleControl.value);

        toggleControl.valueChanges.subscribe(enabled => {
            applyState(enabled);
        });
    }

    private setupAudioEncryptionToggle(): void {
        if (!this.form) return;

        const apiKeyControl = this.form.get('relayServerAPIKey');
        const encryptionControl = this.form.get('audioEncryptionEnabled');

        if (!apiKeyControl || !encryptionControl) return;

        const applyState = (apiKey: string) => {
            if (apiKey && apiKey.trim().length > 0) {
                encryptionControl.enable({ emitEvent: false });
            } else {
                // No API key — force off and disable
                encryptionControl.setValue(false, { emitEvent: false });
                encryptionControl.disable({ emitEvent: false });
            }
        };

        applyState(apiKeyControl.value);

        apiKeyControl.valueChanges.subscribe(apiKey => applyState(apiKey));
    }

    hasRelayAPIKey(): boolean {
        if (!this.form) return false;
        const apiKey = this.form.get('relayServerAPIKey')?.value;
        return apiKey && apiKey.trim().length > 0;
    }

    requestRelayAPIKey() {
        this.editRelayAPIKey();
    }

    editRelayAPIKey() {
        if (!this.form) return;

        // Use hardcoded relay server URL
        const relayServerURL = 'https://tlradioserver.thinlineds.com';
        const existingAPIKey = this.form.get('relayServerAPIKey')?.value;
        
        // Ensure the form control has the hardcoded value
        const relayServerURLControl = this.form.get('relayServerURL');
        if (relayServerURLControl) {
            relayServerURLControl.setValue(relayServerURL, { emitEvent: false });
        }

        const dialogRef = this.dialog.open(RequestAPIKeyDialogComponent, {
            width: '600px',
            maxHeight: '90vh',
            data: {
                relayServerURL: relayServerURL,
                existingAPIKey: existingAPIKey || null
            }
        });

        dialogRef.afterClosed().subscribe((apiKey: string | null) => {
            if (apiKey && this.form) {
                this.form.get('relayServerAPIKey')?.setValue(apiKey);
                // Persist immediately via the dedicated options endpoint (no global save / reload).
                this.saveOptionKey('relayServerAPIKey', apiKey);
            }
        });
    }

    recoverRelayAPIKey() {
        if (!this.form) return;

        const relayServerURL = 'https://tlradioserver.thinlineds.com';

        const dialogRef = this.dialog.open(RecoverAPIKeyDialogComponent, {
            width: '600px',
            data: { relayServerURL: relayServerURL }
        });

        dialogRef.afterClosed().subscribe((apiKey: string | null) => {
            if (apiKey && this.form) {
                this.form.get('relayServerAPIKey')?.setValue(apiKey);
                // Persist immediately via the dedicated options endpoint (no global save / reload).
                this.saveOptionKey('relayServerAPIKey', apiKey);
            }
        });
    }

    // Favicon upload methods
    hasFavicon(): boolean {
        return !!(this.form?.get('faviconFilename')?.value);
    }

    getFaviconPreview(): string {
        if (this.faviconUrl) {
            return this.faviconUrl;
        }
        return `${window.location.origin}/favicon?t=${Date.now()}`;
    }

    onFaviconSelected(event: Event): void {
        const input = event.target as HTMLInputElement;
        if (input.files && input.files.length > 0) {
            const file = input.files[0];
            
            // Validate file size (max 2MB)
            if (file.size > 2 * 1024 * 1024) {
                alert('File is too large. Maximum size is 2MB.');
                return;
            }

            // Validate file type
            if (!file.type.startsWith('image/')) {
                alert('Please select an image file.');
                return;
            }

            this.uploadFavicon(file);
        }
    }

    private uploadFavicon(file: File): void {
        const formData = new FormData();
        formData.append('favicon', file);

        // Get auth token from session storage (admin service sends token without "Bearer" prefix)
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        // HttpHeaders is immutable, so create with headers already set
        const headers = new HttpHeaders({
            'Authorization': token
        });

        console.log('Uploading favicon with token:', token ? 'Token present (' + token.substring(0, 20) + '...)' : 'No token');

        this.http.post(`${window.location.origin}/api/admin/favicon`, formData, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success && response.filename) {
                        this.form?.get('faviconFilename')?.setValue(response.filename, { emitEvent: false });
                        this.faviconUrl = `${window.location.origin}/favicon?t=${Date.now()}`;
                        this.cdr.detectChanges();
                        this.snackBar.open('Favicon uploaded successfully', 'Close', { duration: 3000 });
                        this.refreshPanelBaseline('branding');
                    } else {
                        alert('Failed to upload favicon: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Favicon upload error:', error);
                    let errorMsg = 'Failed to upload favicon.';
                    if (error.status === 0) {
                        errorMsg += ' The file may be too large or the connection timed out.';
                    } else if (error.status === 413) {
                        errorMsg += ' The file is too large.';
                    } else if (error.error && error.error.error) {
                        errorMsg += ' ' + error.error.error;
                    }
                    alert(errorMsg);
                }
            });
    }

    removeFavicon(): void {
        // Get auth token from session storage (admin service sends token without "Bearer" prefix)
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        // HttpHeaders is immutable, so create with headers already set
        const headers = new HttpHeaders({
            'Authorization': token
        });

        this.http.delete(`${window.location.origin}/api/admin/favicon/delete`, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success) {
                        this.form?.get('faviconFilename')?.setValue('', { emitEvent: false });
                        this.faviconUrl = '';
                        this.cdr.detectChanges();
                        this.snackBar.open('Favicon removed successfully', 'Close', { duration: 3000 });
                        this.refreshPanelBaseline('branding');
                    } else {
                        alert('Failed to remove favicon: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Favicon removal error:', error);
                    alert('Failed to remove favicon: ' + (error.error?.error || error.message || 'Unknown error'));
                }
            });
    }

    private updateFaviconUrl(): void {
        if (this.hasFavicon()) {
            this.faviconUrl = `${window.location.origin}/favicon?t=${Date.now()}`;
        } else {
            this.faviconUrl = '';
        }
    }

    private updateEmailLogoUrl(): void {
        if (this.hasEmailLogo()) {
            this.emailLogoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
        } else {
            this.emailLogoUrl = '';
        }
    }

    // Email logo methods
    emailLogoUrl: string = '';
    private emailLogoErrorRetryCount: number = 0;
    private readonly MAX_EMAIL_LOGO_RETRIES: number = 1;

    hasEmailLogo(): boolean {
        return !!(this.form?.get('emailLogoFilename')?.value);
    }

    getEmailLogoPreview(): string {
        if (this.emailLogoUrl) {
            return this.emailLogoUrl;
        }
        return `${window.location.origin}/email-logo?t=${Date.now()}`;
    }

    getEmailLogoStyle(): string {
        const borderRadius = this.form?.get('emailLogoBorderRadius')?.value || '0px';
        return `max-width: 100%; max-height: 105px; display: block; border-radius: ${borderRadius};`;
    }

    onEmailLogoSelected(event: Event): void {
        const input = event.target as HTMLInputElement;
        if (input.files && input.files.length > 0) {
            const file = input.files[0];
            
            if (!file.type.match(/^image\/(png|jpeg|jpg|svg\+xml)$/)) {
                alert('Please select a PNG, JPG, or SVG image file.');
                return;
            }

            if (file.size > 5000000) {
                alert('Logo file size must be less than 5MB.');
                return;
            }

            if (file.type === 'image/svg+xml') {
                this.uploadEmailLogo(file);
            } else {
                this.compressAndUploadEmailLogo(file);
            }
        }
    }

    private compressAndUploadEmailLogo(file: File): void {
        const reader = new FileReader();
        reader.onload = (e: any) => {
            const img = new Image();
            img.onload = () => {
                let width = img.width;
                let height = img.height;
                const maxSize = 300;
                
                if (width > maxSize || height > maxSize) {
                    if (width > height) {
                        height = (height / width) * maxSize;
                        width = maxSize;
                    } else {
                        width = (width / height) * maxSize;
                        height = maxSize;
                    }
                }

                const canvas = document.createElement('canvas');
                canvas.width = width;
                canvas.height = height;
                const ctx = canvas.getContext('2d');
                if (!ctx) {
                    alert('Failed to process image.');
                    return;
                }
                ctx.drawImage(img, 0, 0, width, height);

                canvas.toBlob((blob) => {
                    if (!blob) {
                        alert('Failed to compress image.');
                        return;
                    }

                    if (blob.size > 500000 && file.type !== 'image/png') {
                        canvas.toBlob((compressedBlob) => {
                            if (compressedBlob) {
                                this.uploadEmailLogo(compressedBlob, file.name);
                            } else {
                                this.uploadEmailLogo(blob, file.name);
                            }
                        }, 'image/jpeg', 0.7);
                    } else {
                        this.uploadEmailLogo(blob, file.name);
                    }
                }, file.type === 'image/png' ? 'image/png' : 'image/jpeg', file.type === 'image/png' ? 1.0 : 0.85);
            };
            img.onerror = () => alert('Failed to load image.');
            img.src = e.target.result;
        };
        reader.onerror = () => alert('Failed to read file.');
        reader.readAsDataURL(file);
    }

    private uploadEmailLogo(file: File | Blob, originalName?: string): void {
        const formData = new FormData();
        const fileToUpload = file instanceof File ? file : new File([file], originalName || 'logo.jpg', { type: file.type || 'image/jpeg' });
        formData.append('logo', fileToUpload);

        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        const headers = new HttpHeaders({ 'Authorization': token });

        this.http.post(`${window.location.origin}/api/admin/email-logo`, formData, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success && response.filename) {
                        this.form?.get('emailLogoFilename')?.setValue(response.filename, { emitEvent: false });
                        this.emailLogoErrorRetryCount = 0;
                        this.emailLogoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
                        this.cdr.detectChanges();
                        this.snackBar.open('Email logo uploaded successfully', 'Close', { duration: 3000 });
                        this.refreshPanelBaseline('branding');
                    } else {
                        alert('Failed to upload logo: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Logo upload error:', error);
                    let errorMsg = 'Failed to upload logo.';
                    if (error.status === 0) {
                        errorMsg += ' The file may be too large or the connection timed out.';
                    } else if (error.status === 413) {
                        errorMsg += ' The file is too large.';
                    } else if (error.error && error.error.error) {
                        errorMsg += ' ' + error.error.error;
                    }
                    alert(errorMsg);
                }
            });
    }

    removeEmailLogo(): void {
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        const headers = new HttpHeaders({ 'Authorization': token });

        this.http.delete(`${window.location.origin}/api/admin/email-logo/delete`, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success) {
                        this.form?.get('emailLogoFilename')?.setValue('', { emitEvent: false });
                        this.emailLogoUrl = '';
                        this.emailLogoErrorRetryCount = 0;
                        this.cdr.detectChanges();
                        this.snackBar.open('Email logo removed successfully', 'Close', { duration: 3000 });
                        this.refreshPanelBaseline('branding');
                    } else {
                        alert('Failed to remove logo: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Logo removal error:', error);
                    alert('Failed to remove logo: ' + (error.error?.error || error.message || 'Unknown error'));
                }
            });
    }

    onEmailLogoLoad(): void {
        this.emailLogoErrorRetryCount = 0;
        this.cdr.detectChanges();
    }

    onEmailLogoError(): void {
        if (this.emailLogoUrl && this.emailLogoErrorRetryCount < this.MAX_EMAIL_LOGO_RETRIES) {
            this.emailLogoErrorRetryCount++;
            const url = new URL(this.emailLogoUrl);
            url.searchParams.set('t', Date.now().toString());
            this.emailLogoUrl = url.toString();
            this.cdr.detectChanges();
        } else {
            this.emailLogoUrl = '';
            this.emailLogoErrorRetryCount = 0;
            this.cdr.detectChanges();
        }
    }

    // Test email functionality
    testEmailAddress: string = '';
    sendingTestEmail: boolean = false;
    testEmailError: string = '';
    testEmailSuccess: string = '';

    sendTestEmail(): void {
        if (!this.testEmailAddress || !this.testEmailAddress.trim()) {
            this.testEmailError = 'Please enter a recipient email address';
            this.testEmailSuccess = '';
            return;
        }

        const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
        if (!emailRegex.test(this.testEmailAddress)) {
            this.testEmailError = 'Please enter a valid email address';
            this.testEmailSuccess = '';
            return;
        }

        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            this.testEmailError = 'Not authenticated. Please log in again.';
            this.testEmailSuccess = '';
            return;
        }

        this.sendingTestEmail = true;
        this.testEmailError = '';
        this.testEmailSuccess = '';

        const headers = new HttpHeaders({
            'Authorization': token,
            'Content-Type': 'application/json'
        });

        this.http.post(`${window.location.origin}/api/admin/email-test`, 
            { toEmail: this.testEmailAddress.trim() }, 
            { headers })
            .subscribe({
                next: (response: any) => {
                    this.sendingTestEmail = false;
                    if (response.success) {
                        this.testEmailSuccess = response.message || 'Test email sent successfully!';
                        this.testEmailError = '';
                    } else {
                        this.testEmailError = response.error || 'Failed to send test email';
                        this.testEmailSuccess = '';
                    }
                    this.cdr.detectChanges();
                },
                error: (error) => {
                    this.sendingTestEmail = false;
                    console.error('Test email error:', error);
                    let errorMsg = 'Failed to send test email.';
                    
                    if (error.error) {
                        if (typeof error.error === 'string') {
                            errorMsg = error.error;
                        } else if (error.error.error) {
                            errorMsg = error.error.error;
                        } else if (error.error.message) {
                            errorMsg = error.error.message;
                        }
                    } else if (error.message) {
                        errorMsg = error.message;
                    }
                    
                    if (errorMsg === 'Failed to send test email.') {
                        if (error.status === 0) {
                            errorMsg = 'Connection error. Please check your network connection.';
                        } else if (error.status === 401) {
                            errorMsg = 'Authentication failed. Please log in again.';
                        } else if (error.status === 500) {
                            errorMsg = 'Server error occurred. Check server logs for details.';
                        }
                    }
                    
                    this.testEmailError = errorMsg;
                    this.testEmailSuccess = '';
                    this.cdr.detectChanges();
                }
            });
    }

    generateExternalAPIKey(): void {
        if (!this.form) return;
        const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
        let key = '';
        const array = new Uint8Array(48);
        window.crypto.getRandomValues(array);
        array.forEach(b => key += chars[b % chars.length]);
        this.form.get('centralManagementAPIKey')?.setValue(key);
        this.form.markAsDirty();
        this.snackBar.open('New API key generated — save your configuration to apply it.', 'Close', { duration: 5000 });
    }

    async generateFaviconFromLogo(): Promise<void> {
        if (!this.hasEmailLogo() || !this.emailLogoUrl) {
            this.snackBar.open('No server logo found. Please upload a server logo first.', 'Close', { duration: 3000 });
            return;
        }

        try {
            // Create an image element to load the logo
            const img = new Image();
            img.crossOrigin = 'anonymous';
            
            await new Promise<void>((resolve, reject) => {
                img.onload = () => resolve();
                img.onerror = () => reject(new Error('Failed to load server logo'));
                img.src = this.emailLogoUrl;
            });

            // Create a canvas to resize the logo to favicon size (32x32)
            const canvas = document.createElement('canvas');
            canvas.width = 32;
            canvas.height = 32;
            const ctx = canvas.getContext('2d');
            
            if (!ctx) {
                throw new Error('Failed to create canvas context');
            }

            // Get the border radius setting
            const borderRadius = this.form?.get('emailLogoBorderRadius')?.value || '0px';
            
            // Parse border radius (handle px, %, or unitless numbers)
            let radius = 0;
            if (borderRadius.includes('%')) {
                // If percentage, apply to 32x32 canvas
                const percent = parseFloat(borderRadius);
                radius = (32 * percent) / 100;
            } else {
                // Parse as pixels
                radius = parseFloat(borderRadius) || 0;
                // Scale the radius proportionally if the original logo is larger
                // Assume typical logo is ~200px, scale to 32px
                radius = (radius * 32) / 200;
            }

            // Apply border radius clipping if set
            if (radius > 0) {
                ctx.beginPath();
                
                // Create rounded rectangle path
                const x = 0, y = 0, width = 32, height = 32;
                ctx.moveTo(x + radius, y);
                ctx.lineTo(x + width - radius, y);
                ctx.quadraticCurveTo(x + width, y, x + width, y + radius);
                ctx.lineTo(x + width, y + height - radius);
                ctx.quadraticCurveTo(x + width, y + height, x + width - radius, y + height);
                ctx.lineTo(x + radius, y + height);
                ctx.quadraticCurveTo(x, y + height, x, y + height - radius);
                ctx.lineTo(x, y + radius);
                ctx.quadraticCurveTo(x, y, x + radius, y);
                ctx.closePath();
                
                ctx.clip();
            }

            // Draw the image scaled to 32x32
            ctx.drawImage(img, 0, 0, 32, 32);

            // Convert canvas to blob
            const blob = await new Promise<Blob>((resolve, reject) => {
                canvas.toBlob((b) => {
                    if (b) resolve(b);
                    else reject(new Error('Failed to create favicon blob'));
                }, 'image/png');
            });

            // Create a file from the blob
            const file = new File([blob], 'favicon.png', { type: 'image/png' });

            // Upload the favicon
            await this.uploadFavicon(file);

            this.snackBar.open('Favicon generated successfully from server logo!', 'Close', { duration: 3000 });
        } catch (error) {
            console.error('Error generating favicon:', error);
            this.snackBar.open('Failed to generate favicon. Please try uploading manually.', 'Close', { duration: 5000 });
        }
    }

    refreshRelaySuspensionStatus(): void {
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            return;
        }
        const headers = new HttpHeaders({ Authorization: token });
        this.http
            .get<{
                fully_suspended: boolean;
                suspend_message?: string;
                relay_owner_unlocked_public?: boolean;
                public_listener_blocked?: boolean;
                push_notifications_blocked?: boolean;
            }>(`${window.location.origin}/api/admin/relay-suspension`, { headers })
            .subscribe({
                next: (s) => {
                    this.relaySuspensionStatus = s;
                    this.cdr.markForCheck();
                },
                error: () => {
                    this.relaySuspensionStatus = null;
                    this.cdr.markForCheck();
                },
            });
    }

    unlockPublicWebListener(): void {
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            this.snackBar.open('Not authenticated.', 'Close', { duration: 4000 });
            return;
        }
        const headers = new HttpHeaders({ Authorization: token });
        this.http
            .post<{ success: boolean; error?: string }>(
                `${window.location.origin}/api/admin/relay-unlock-public-client`,
                {},
                { headers },
            )
            .subscribe({
                next: (res) => {
                    if (res?.success) {
                        this.snackBar.open('Public web listener unlocked.', 'Close', { duration: 5000 });
                        this.refreshRelaySuspensionStatus();
                    } else {
                        this.snackBar.open(res?.error || 'Unlock failed', 'Close', { duration: 6000 });
                    }
                },
                error: (err) => {
                    const msg = err?.error?.error || err?.message || 'Unlock failed';
                    this.snackBar.open(msg, 'Close', { duration: 6000 });
                },
            });
    }

    testCentralConnection(): void {
        const url = this.form?.get('centralManagementURL')?.value;
        const apiKey = this.form?.get('centralManagementAPIKey')?.value;

        if (!url || !apiKey) {
            this.snackBar.open('Please enter both URL and API key', 'Close', { duration: 3000 });
            return;
        }

        // Test connection to central system
        const testUrl = `${url}/api/webhook/central-test?api_key=${encodeURIComponent(apiKey)}`;
        const headers = new HttpHeaders({
            'X-API-Key': apiKey
        });

        this.centralConnectionStatus = null;
        this.centralConnectionMessage = 'Testing connection...';

        this.http.get(testUrl, { headers }).subscribe({
            next: (response: any) => {
                this.centralConnectionStatus = 'success';
                this.centralConnectionMessage = `Connected successfully! Server: ${response.server || 'Unknown'}`;
                this.snackBar.open('Connection test successful', 'Close', { duration: 3000 });
            },
            error: (error) => {
                this.centralConnectionStatus = 'error';
                this.centralConnectionMessage = `Connection failed: ${error.statusText || 'Unknown error'}`;
                this.snackBar.open('Connection test failed', 'Close', { duration: 5000 });
            }
        });
    }

    // Helper methods for array handling in templates
    getAssemblyAIWordBoostDisplay(): string {
        const wordBoost = this.form?.get('transcriptionConfig')?.get('assemblyAIWordBoost')?.value;
        return Array.isArray(wordBoost) ? wordBoost.join(',') : '';
    }

    setAssemblyAIWordBoost(value: string): void {
        const terms = value.split(',').map(s => s.trim()).filter(s => s);
        this.form?.get('transcriptionConfig')?.get('assemblyAIWordBoost')?.setValue(terms);
    }

}
