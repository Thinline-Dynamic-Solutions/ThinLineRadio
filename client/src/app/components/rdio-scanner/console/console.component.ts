/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 *
 * Console — the modern scanner client view.
 *
 * Replaces the legacy `main.component`. Renders the hybrid Thinline skin
 * (LCD chassis around the live now-playing strip; clean dashboard for the
 * tab content) and hosts the seven primary tabs:
 *
 *     Current · Archive · Channels · Alerts · Transcripts · Stats · Settings
 *
 * Wires all the same business behaviour as the legacy main view (auth,
 * livefeed transport, subscription/checkout, audio playback/replay, call
 * history, scanning animation) but trims the dead display state that older
 * iterations of the LCD readout used to populate.
 * ****************************************************************************
 */

import {
    ChangeDetectorRef,
    Component,
    EventEmitter,
    Input,
    OnChanges,
    OnDestroy,
    OnInit,
    Output,
    SimpleChanges,
    ViewChild,
} from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MatInput } from '@angular/material/input';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Title } from '@angular/platform-browser';
import { Subscription, timer } from 'rxjs';

import packageInfo from '../../../../../package.json';
import {
    RdioScannerAvoidOptions,
    RdioScannerBeepStyle,
    RdioScannerCall,
    RdioScannerConfig,
    RdioScannerEvent,
    RdioScannerLivefeedMap,
    RdioScannerLivefeedMode,
} from '../rdio-scanner';
import { RdioScannerService } from '../rdio-scanner.service';
import { RdioScannerSearchComponent } from '../search/search.component';
import { RdioScannerSupportComponent } from '../main/support/support.component';
import { SettingsService } from '../settings/settings.service';
import { TagColorService } from '../tag-color.service';
import { TranscriptAnnotation, renderAnnotatedTranscript } from '../transcript-utils';
import { findUnitLabelForSrc, resolveUnitLabelForSrc as resolveUnitLabel } from '../unit-utils';

/** Stable index per board tab. Order MUST match the template's `<mat-tab>` list. */
const TAB = {
    Current: 0,
    Archive: 1,
    Channels: 2,
    Alerts: 3,
    Transcripts: 4,
    Stats: 5,
    Settings: 6,
} as const;

@Component({
    selector: 'rdio-scanner-console',
    templateUrl: './console.component.html',
    styleUrls: ['./console.component.scss'],
})
export class RdioScannerConsoleComponent implements OnChanges, OnDestroy, OnInit {

    // ────────────────────────────────────────────────────────────────────────
    // OUTPUTS — bubbled to the parent rdio-scanner.component
    // ────────────────────────────────────────────────────────────────────────

    @Output() toggleFullscreen = new EventEmitter<void>();
    @Output() signOut = new EventEmitter<void>();
    @Output() toggleClassicViewRequest = new EventEmitter<void>();

    /**
     * True when this view is the one currently shown to the user. The parent
     * keeps BOTH views mounted at all times for instant view switching, so
     * each view must skip user-visible side effects (snackbar errors, PWA
     * auto-livefeed) when it is the hidden one — otherwise events fire twice.
     */
    @Input() viewActive = true;

    @ViewChild('password', { read: MatInput }) private authPassword: MatInput | undefined;
    @ViewChild('archiveSearch') archiveSearch: RdioScannerSearchComponent | undefined;

    // ────────────────────────────────────────────────────────────────────────
    // BRANDING / CHROME
    // ────────────────────────────────────────────────────────────────────────

    branding = '';
    toolbarLogoUrl = '';
    toolbarLogoError = false;
    version = packageInfo.version;
    email = '';

    // ────────────────────────────────────────────────────────────────────────
    // LIVE CALL STATE
    // ────────────────────────────────────────────────────────────────────────

    /** Currently playing call (live feed) — undefined between calls. */
    call: RdioScannerCall | undefined;
    /** Last call that just finished — used briefly during transitions. */
    callPrevious: RdioScannerCall | undefined;
    /** Finished calls newest-first (pruned at ~2h). */
    callHistoryStore: RdioScannerCall[] = [];
    /** Pre-filtered slice of `callHistoryStore` for the "last hour" table. */
    callsLastHour: RdioScannerCall[] = [];
    /** Currently highlighted history row (for detail-pane). */
    selectedHistoryCall: RdioScannerCall | undefined;
    /** Source-unit label captured during audio playback (used by Now Playing). */
    callUnit = '';

    // ────────────────────────────────────────────────────────────────────────
    // LIVEFEED / TRANSPORT
    // ────────────────────────────────────────────────────────────────────────

    livefeedOffline = true;
    livefeedOnline = false;
    livefeedPaused = false;
    playbackMode = false;

    linked = false;
    listeners = 0;
    callQueue = 0;

    holdSys = false;
    holdTg = false;
    isFavorite = false;
    avoided = false;
    delayed = false;
    patched = false;

    volume = 100;
    clock = new Date();
    timeFormat: 'HH:mm' | 'h:mm a' = 'HH:mm';

    // ────────────────────────────────────────────────────────────────────────
    // AUTH (legacy unlock-code path — only active when user-registration is OFF)
    // ────────────────────────────────────────────────────────────────────────

    auth = false;
    authForm: FormGroup;
    userRegistrationEnabled = false;

    // ────────────────────────────────────────────────────────────────────────
    // SUBSCRIPTION / CHECKOUT
    // ────────────────────────────────────────────────────────────────────────

    showCheckout = false;
    subscriptionActive = false;
    subscriptionChecked = false;
    userEmail = '';
    isGroupAdminManaged = false;
    transferring = false;
    private subscriptionCheckInProgress = false;

    // ────────────────────────────────────────────────────────────────────────
    // TAB / CONFIG
    // ────────────────────────────────────────────────────────────────────────

    boardTabIndex: number = TAB.Current;
    config: RdioScannerConfig | undefined;
    map: RdioScannerLivefeedMap = {};

    /** Cycles through `getEnabledSystems()` while scanning banner is shown. */
    currentScanningSystemIndex = 0;

    // ────────────────────────────────────────────────────────────────────────
    // SUBSCRIPTIONS / STORED AUTH STATE
    // ────────────────────────────────────────────────────────────────────────

    private clockTimer: Subscription | undefined;
    private scanningSystemTimer: Subscription | undefined;
    private eventSubscription: Subscription;
    private storedPinAttempts = 0;
    private lastStoredPin: string | null = null;

    // ────────────────────────────────────────────────────────────────────────
    // CTOR / LIFECYCLE
    // ────────────────────────────────────────────────────────────────────────

    constructor(
        private rdioScannerService: RdioScannerService,
        private matSnackBar: MatSnackBar,
        private cdr: ChangeDetectorRef,
        private fb: FormBuilder,
        private tagColorService: TagColorService,
        private settingsService: SettingsService,
        private titleService: Title,
    ) {
        this.authForm = this.fb.group<{ password: string | null }>({ password: null });

        this.eventSubscription = this.rdioScannerService.event.subscribe(
            (event: RdioScannerEvent) => this.eventHandler(event),
        );

        if (typeof this.rdioScannerService.isLinked === 'function') {
            this.linked = this.rdioScannerService.isLinked();
        }

        const savedVolume = window?.localStorage?.getItem('rdio-scanner-volume');
        if (savedVolume) {
            const v = parseInt(savedVolume, 10);
            this.volume = (isNaN(v) || v < 0 || v > 100) ? 100 : v;
        }
        this.rdioScannerService.setVolume(this.volume / 100);
    }

    ngOnInit(): void {
        this.syncClock();

        if (!this.toolbarLogoUrl) {
            this.toolbarLogoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
        }

        const currentConfig = this.rdioScannerService.getConfig();
        if (currentConfig) {
            this.config = currentConfig;
        }

        this.syncPageScrollMode();

        this.checkAutoStartLivefeed();
    }

    ngOnChanges(changes: SimpleChanges): void {
        if ('viewActive' in changes) {
            this.syncPageScrollMode();
        }
    }

    ngOnDestroy(): void {
        this.clockTimer?.unsubscribe();
        this.stopScanningAnimation();
        this.eventSubscription.unsubscribe();
        // Always strip the document-scroll overrides when the view is gone so
        // the classic view (or any other consumer) doesn't inherit a stale
        // body class that would relax its viewport-locked layout.
        try {
            document.body.classList.remove('tlr-page-scroll');
            document.body.classList.remove('tlr-channels-scroll');
            // Legacy class kept around briefly during refactor — strip it too
            // so older bundles never leave it dangling on the body.
            document.body.classList.remove('tlr-archive-scroll');
        } catch { /* SSR / detached DOM */ }
    }

    // ────────────────────────────────────────────────────────────────────────
    // DERIVED STATE
    // ────────────────────────────────────────────────────────────────────────

    get showListenersCount(): boolean {
        return this.config?.showListenersCount || false;
    }

    get isTranscriptionEnabled(): boolean {
        return this.config?.options?.transcriptionEnabled || false;
    }

    get isUserAuthenticated(): boolean {
        return !!this.rdioScannerService.readPin();
    }

    get isSystemAdmin(): boolean {
        return this.rdioScannerService.isSystemAdmin();
    }

    /** Settings is always the last tab; its index shifts when transcription tabs hide. */
    get settingsBoardTabIndex(): number {
        return this.isTranscriptionEnabled ? TAB.Settings : TAB.Alerts;
    }

    get showScanningAnimation(): boolean {
        return this.livefeedOnline && !this.call && !this.livefeedPaused && this.callQueue === 0;
    }

    /** Detail card source: live call wins; otherwise whichever history row is selected. */
    get detailCall(): RdioScannerCall | undefined {
        return this.call ?? this.selectedHistoryCall;
    }

    // ────────────────────────────────────────────────────────────────────────
    // TRANSPORT BUTTON ACTIONS
    // ────────────────────────────────────────────────────────────────────────

    executeButtonAction(key: string): void {
        switch (key) {
            case 'liveFeed':     this.livefeed(); break;
            case 'pause':        this.pause(); break;
            case 'replayLast':   this.replay(); break;
            case 'skipNext':     this.skip(); break;
            case 'avoid':        this.avoid(); break;
            case 'favorite':     this.toggleFavorite(); break;
            case 'holdSystem':   this.holdSystem(); break;
            case 'holdTalkgroup':this.holdTalkgroup(); break;
        }
    }

    toolbarIcon(key: string): string {
        const icons: Record<string, string> = {
            liveFeed:      this.livefeedOnline ? 'radio' : 'radio_button_unchecked',
            pause:         this.livefeedPaused ? 'play_arrow' : 'pause',
            replayLast:    'replay',
            skipNext:      'skip_next',
            avoid:         'block',
            favorite:      this.isFavorite ? 'star' : 'star_border',
            holdSystem:    'keyboard_arrow_down',
            holdTalkgroup: 'keyboard_double_arrow_down',
        };
        return icons[key] || 'help';
    }

    toolbarTooltip(key: string): string {
        const paused = this.livefeedPaused ? 'Resume' : 'Pause';
        const tips: Record<string, string> = {
            liveFeed:      this.livefeedOnline ? 'Stop live feed' : 'Start live feed',
            pause:         paused,
            replayLast:    'Replay last transmission',
            skipNext:      'Skip current call',
            avoid:         'Avoid talkgroup',
            favorite:      'Favorite this talkgroup',
            holdSystem:    'Hold current system',
            holdTalkgroup: 'Hold current talkgroup',
        };
        return tips[key] || key;
    }

    toolbarActionClass(key: string): { active: boolean; inactive: boolean } {
        const o = { active: false, inactive: false };
        switch (key) {
            case 'liveFeed':
                if (this.livefeedOnline) o.active = true;
                if (this.livefeedOffline && !this.playbackMode) o.inactive = true;
                break;
            case 'pause':        if (this.livefeedPaused) o.active = true; break;
            case 'favorite':     if (this.isFavorite) o.active = true; break;
            case 'holdSystem':   if (this.holdSys) o.active = true; break;
            case 'holdTalkgroup':if (this.holdTg) o.active = true; break;
        }
        return o;
    }

    // ────────────────────────────────────────────────────────────────────────
    // INDIVIDUAL TRANSPORT METHODS
    // ────────────────────────────────────────────────────────────────────────

    authenticate(password = this.authForm.get('password')?.value): void {
        if (password) {
            this.authForm.disable();
            this.rdioScannerService.authenticate(password);
        }
    }

    authFocus(): void {
        if (this.auth && this.authPassword instanceof MatInput) {
            this.authPassword.focus();
        }
    }

    livefeed(): void {
        if (this.auth) { this.authFocus(); return; }
        this.rdioScannerService.beep(
            this.livefeedOffline ? RdioScannerBeepStyle.Activate : RdioScannerBeepStyle.Deactivate,
        );
        this.rdioScannerService.livefeed();
    }

    pause(): void {
        if (this.auth) { this.authFocus(); return; }
        this.rdioScannerService.beep(
            this.livefeedPaused ? RdioScannerBeepStyle.Deactivate : RdioScannerBeepStyle.Activate,
        );
        this.rdioScannerService.pause();
    }

    skip(options?: { delay?: boolean }): void {
        if (this.auth) { this.authFocus(); return; }
        this.rdioScannerService.beep(RdioScannerBeepStyle.Activate);
        this.rdioScannerService.skip(options);
    }

    replay(): void {
        if (this.auth) { this.authFocus(); return; }
        if (!this.livefeedPaused && (this.call || this.callPrevious)) {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Activate);
            this.rdioScannerService.replay();
        } else {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Denied);
        }
    }

    avoid(options?: RdioScannerAvoidOptions): void {
        if (this.auth) { this.authFocus(); return; }
        const call = this.call || this.callPrevious;

        if (!options && !call) {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Denied);
            return;
        }

        if (options) {
            this.rdioScannerService.avoid(options);
        } else if (call) {
            const isAvoided = this.rdioScannerService.isAvoided(call);
            const minutes = this.rdioScannerService.isAvoidedTimer(call);
            if (!isAvoided) {
                this.rdioScannerService.avoid({ status: false });
            } else if (!minutes) {
                this.rdioScannerService.avoid({ minutes: 30, status: false });
            } else if (minutes === 30) {
                this.rdioScannerService.avoid({ minutes: 60, status: false });
            } else if (minutes === 60) {
                this.rdioScannerService.avoid({ minutes: 120, status: false });
            } else {
                this.rdioScannerService.avoid({ status: true });
            }
        }

        if (call && this.rdioScannerService.isAvoided(call)) {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Activate);
        } else {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Deactivate);
        }
    }

    holdSystem(): void {
        if (this.auth) { this.authFocus(); return; }
        if (this.call || this.callPrevious) {
            this.rdioScannerService.beep(
                this.holdSys ? RdioScannerBeepStyle.Deactivate : RdioScannerBeepStyle.Activate,
            );
            this.rdioScannerService.holdSystem();
        } else {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Denied);
        }
    }

    holdTalkgroup(): void {
        if (this.auth) { this.authFocus(); return; }
        if (this.call || this.callPrevious) {
            this.rdioScannerService.beep(
                this.holdTg ? RdioScannerBeepStyle.Deactivate : RdioScannerBeepStyle.Activate,
            );
            this.rdioScannerService.holdTalkgroup();
        } else {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Denied);
        }
    }

    toggleFavorite(): void {
        if (this.auth) { this.authFocus(); return; }
        if (!this.call && !this.callPrevious) return;
        this.isFavorite = !this.isFavorite;
        this.rdioScannerService.beep();
    }

    stop(): void {
        this.rdioScannerService.stop();
    }

    onVolumeChange(newVolume: number): void {
        this.volume = newVolume;
        window?.localStorage?.setItem('rdio-scanner-volume', String(newVolume));
        this.rdioScannerService.setVolume(newVolume / 100);
    }

    // ────────────────────────────────────────────────────────────────────────
    // TAB NAVIGATION
    // ────────────────────────────────────────────────────────────────────────

    onBoardIndexChange(index: number): void {
        this.applyBoardTab(index, true);
    }

    showSearchPanel(): void  { this.beepThenTab(TAB.Archive,  true); }
    showSelectPanel(): void  { this.beepThenTab(TAB.Channels, false); }
    showSettingsPanel(): void{ this.beepThenTab(this.settingsBoardTabIndex, false); }

    showAlertsPanel(): void {
        if (!this.config || this.auth) {
            if (this.auth) this.authFocus();
            return;
        }
        if (!this.isTranscriptionEnabled) {
            this.matSnackBar.open('Alerts require transcription to be enabled on the server.', 'OK', { duration: 5000 });
            return;
        }
        this.rdioScannerService.beep();
        this.applyBoardTab(TAB.Alerts, false);
    }

    openArchiveSearch(): void { this.showSearchPanel(); }

    private beepThenTab(idx: number, refreshArchive: boolean): void {
        if (!this.config) return;
        if (this.auth) { this.authFocus(); return; }
        this.rdioScannerService.beep();
        this.applyBoardTab(idx, refreshArchive);
    }

    private applyBoardTab(index: number, refreshArchiveSearch: boolean): void {
        const prev = this.boardTabIndex;
        if (prev === TAB.Archive && index !== TAB.Archive) {
            this.rdioScannerService.stopPlaybackMode();
        }
        this.boardTabIndex = index;
        if (refreshArchiveSearch && index === TAB.Archive) {
            setTimeout(() => this.archiveSearch?.searchCalls(), 0);
        }
        this.syncPageScrollMode();
    }

    /**
     * Archive and Channels use the fitted tab viewport: panels stretch to
     * fill the tab body (see `.tab-panel--fill` in console.component.scss).
     * Other tabs keep the same viewport-locked layout.
     */
    private syncPageScrollMode(): void {
        if (typeof document === 'undefined') return;
        document.body.classList.remove('tlr-page-scroll');
        document.body.classList.remove('tlr-channels-scroll');
    }

    // ────────────────────────────────────────────────────────────────────────
    // HEADER ACTIONS
    // ────────────────────────────────────────────────────────────────────────

    requestToggleClassicView(): void {
        this.toggleClassicViewRequest.emit();
    }

    openAdminPanel(): void {
        const pin = this.rdioScannerService.readPin();
        if (!pin) return;

        fetch('/api/admin/sso', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ pin }),
        })
            .then(r => r.ok ? r.json() : Promise.reject(r.status))
            .then((data: any) => {
                if (data?.token) {
                    window.open(`/admin?sso_token=${encodeURIComponent(data.token)}`, '_blank');
                }
            })
            .catch(() => window.open('/admin', '_blank'));
    }

    onSignOut(): void {
        this.rdioScannerService.disconnect();
        this.subscriptionActive = false;
        this.subscriptionChecked = false;
        this.showCheckout = false;
        this.isGroupAdminManaged = false;
        this.signOut.emit();
    }

    showHelp(): void {
        this.matSnackBar.openFromComponent(RdioScannerSupportComponent, { data: { email: this.email } });
    }

    // ────────────────────────────────────────────────────────────────────────
    // BRANDING / LOGO
    // ────────────────────────────────────────────────────────────────────────

    getToolbarLogoBorderRadius(): string {
        const borderRadius = this.config?.options?.emailLogoBorderRadius;
        return borderRadius && borderRadius.trim() !== '' ? borderRadius : '8px';
    }

    onToolbarLogoError(event: Event): void {
        this.toolbarLogoError = true;
        const img = event.target as HTMLImageElement;
        if (img) img.style.display = 'none';
    }

    // ────────────────────────────────────────────────────────────────────────
    // CALL DETAIL HELPERS
    // ────────────────────────────────────────────────────────────────────────

    renderTranscript(transcript: string, annotations?: TranscriptAnnotation[]): string {
        return renderAnnotatedTranscript(transcript, annotations);
    }

    displayTgidForCall(call: RdioScannerCall | undefined): string {
        if (!call) return '—';
        if (this.isAfsSystem(call)) return this.formatAfs(call.talkgroup);
        return String(call.talkgroup ?? '0');
    }

    displayUnitForCall(call: RdioScannerCall | undefined): string {
        if (!call) return '—';
        if (Array.isArray(call.sources) && call.sources.length) {
            const ordered = [...call.sources].sort((a, b) => (a.pos || 0) - (b.pos || 0));
            for (const s of ordered) {
                if (typeof s.tag === 'string' && s.tag.length > 0) return s.tag;
            }
            const first = ordered[0];
            if (typeof first?.src === 'number') return resolveUnitLabel(call.systemData?.units, first.src);
        }
        if (typeof call.source === 'number') return resolveUnitLabel(call.systemData?.units, call.source);
        return '—';
    }

    /** Now-playing row prefers the live `callUnit` if it matches; otherwise falls back to the static unit. */
    displaySourceForNowPlaying(call: RdioScannerCall): string {
        if (this.call?.id != null && call.id === this.call.id) {
            const u = this.callUnit?.trim() ?? '';
            if (u !== '' && u !== '0' && this.displayStringBelongsToCall(call, u)) return u;
        }
        return this.displayUnitForCall(call);
    }

    formatFrequencyMHz(frequency: number | undefined): string {
        if (typeof frequency !== 'number' || frequency === 0) return '';
        const mhz = frequency / 1000000;
        const [integer, decimals] = mhz.toFixed(7).split('.');
        return `${integer.padStart(3, '0')}.${decimals} MHz`;
    }

    getTransmissionHistoryTagColor(call: RdioScannerCall | undefined): string {
        if (!call) return 'transparent';
        if (call.tagData?.led) return this.tagColorService.getTagColor(call.tagData.led);
        if (call.talkgroupData?.tag) return this.tagColorService.getTagColor(call.talkgroupData.tag);
        return 'transparent';
    }

    getTransmissionHistoryBackgroundColor(call: RdioScannerCall | undefined): string {
        const color = this.getTransmissionHistoryTagColor(call);
        if (color === 'transparent') return 'transparent';
        const r = parseInt(color.slice(1, 3), 16);
        const g = parseInt(color.slice(3, 5), 16);
        const b = parseInt(color.slice(5, 7), 16);
        return `rgba(${r}, ${g}, ${b}, 0.2)`;
    }

    nowPlayingRowAccent(call: RdioScannerCall): string {
        const col = this.getTransmissionHistoryTagColor(call);
        if (!col || col === 'transparent') return 'inset 3px 0 0 var(--tlr-primary, #39ff14)';
        return `inset 3px 0 0 ${col}`;
    }

    isRowPlaying(c: RdioScannerCall): boolean {
        return !!this.call?.id && !!c?.id && this.call!.id === c.id;
    }

    /** Suppress the duplicate detail-grid when the selected row IS the live now-playing line. */
    showDetailFieldGrid(dc: RdioScannerCall): boolean {
        if (!this.call || dc.id == null || this.call.id == null) return true;
        return dc.id !== this.call.id;
    }

    onHistoryRowClick(c: RdioScannerCall): void {
        if (!c || this.auth) {
            if (this.auth) this.authFocus();
            return;
        }
        this.selectedHistoryCall = c;
        this.rdioScannerService.beep();
        this.playCallFromHistoryEntry(c, {
            preserveQueue: this.livefeedOnline && !this.playbackMode,
        });
    }

    getDelayedText(): string {
        if (this.playbackMode) return 'PLAYBACK';
        if (this.delayed) return 'DELAYED';
        return 'LIVE';
    }

    getDelayedTooltip(): string {
        if (this.playbackMode) return 'Playing from archive or search';
        if (this.delayed) return 'Feed delay reported for this call';
        return 'Live audio';
    }

    // ────────────────────────────────────────────────────────────────────────
    // SCANNING ANIMATION
    // ────────────────────────────────────────────────────────────────────────

    getEnabledSystems(): Array<{ id: number; label: string }> {
        if (!this.config?.systems || !this.map) return [];

        // Hold modes pin us to a single system.
        if ((this.holdSys || this.holdTg) && (this.call || this.callPrevious)) {
            const heldSystemId = (this.call || this.callPrevious)?.system;
            if (heldSystemId !== undefined) {
                const heldSystem = this.config.systems.find(s => s.id === heldSystemId);
                if (heldSystem) return [{ id: heldSystem.id, label: heldSystem.label }];
            }
        }

        const enabled: Array<{ id: number; label: string }> = [];
        for (const system of this.config.systems) {
            const sysMap = this.map[system.id];
            if (!sysMap) continue;
            const hasActive = Object.keys(sysMap).some(tgId => sysMap[+tgId]?.active === true);
            if (hasActive) enabled.push({ id: system.id, label: system.label });
        }
        return enabled;
    }

    getCurrentScanningSystem(): string {
        const enabled = this.getEnabledSystems();
        if (enabled.length === 0) return '';
        return enabled[this.currentScanningSystemIndex]?.label || enabled[0]?.label || '';
    }

    private updateScanningAnimation(): void {
        if (this.showScanningAnimation) this.startScanningAnimation();
        else this.stopScanningAnimation();
    }

    private startScanningAnimation(): void {
        this.stopScanningAnimation();
        const enabled = this.getEnabledSystems();
        if (enabled.length === 0) return;
        this.currentScanningSystemIndex = 0;

        // 1s cycle. Timer runs inside Angular's zone so CD is triggered automatically.
        this.scanningSystemTimer = timer(0, 1000).subscribe(() => {
            const systems = this.getEnabledSystems();
            if (systems.length > 0) {
                this.currentScanningSystemIndex = (this.currentScanningSystemIndex + 1) % systems.length;
            }
        });
    }

    private stopScanningAnimation(): void {
        this.scanningSystemTimer?.unsubscribe();
        this.scanningSystemTimer = undefined;
        this.currentScanningSystemIndex = 0;
    }

    // ────────────────────────────────────────────────────────────────────────
    // SUBSCRIPTION / CHECKOUT
    // ────────────────────────────────────────────────────────────────────────

    checkSubscriptionStatus(): void {
        if (this.subscriptionCheckInProgress) return;
        if (this.subscriptionChecked && this.subscriptionActive) return;
        if (this.subscriptionChecked && !this.subscriptionActive) this.subscriptionChecked = false;

        this.subscriptionCheckInProgress = true;

        // No paywall configured ⇒ allow access immediately.
        if (!this.config?.options?.stripePaywallEnabled) {
            this.subscriptionActive = true;
            this.subscriptionChecked = true;
            this.showCheckout = false;
            this.subscriptionCheckInProgress = false;
            this.cdr.detectChanges();
            return;
        }

        // No pricing options ⇒ billing not required for this user.
        const pricingOptions = this.config?.options?.pricingOptions;
        if (!pricingOptions || pricingOptions.length === 0) {
            this.subscriptionActive = true;
            this.subscriptionChecked = true;
            this.showCheckout = false;
            this.subscriptionCheckInProgress = false;
            this.cdr.detectChanges();
            return;
        }

        this.checkUserSubscription();
    }

    private checkUserSubscription(): void {
        const pin = this.rdioScannerService.readPin();
        if (!pin) return;

        fetch(`/api/account?pin=${encodeURIComponent(pin)}`)
            .then(r => r.ok ? r.json() : Promise.reject(new Error('Failed to fetch account info')))
            .then(data => {
                if (data.email) this.userEmail = data.email;

                const isAdminManagedNonAdmin =
                    data.subscriptionStatusDisplay === 'group_admin_managed'
                    || data.subscriptionStatus === 'group_admin_managed';
                const billingRequired = data.billingRequired === true;

                // Easiest case — no billing required & not in group_admin mode.
                if (!billingRequired && !isAdminManagedNonAdmin) {
                    this.subscriptionActive = true;
                    this.subscriptionChecked = true;
                    this.showCheckout = false;
                    this.subscriptionCheckInProgress = false;
                    return;
                }

                const isSubActive = data.subscriptionStatus === 'active' || data.subscriptionStatus === 'trialing';
                const isPinValid = !data.pinExpired;
                const hasPricingOptions = !!(this.config?.options?.pricingOptions
                    && this.config.options.pricingOptions.length > 0);
                const isAdminNeedsSubscription = billingRequired && data.isGroupAdmin && !isAdminManagedNonAdmin;

                if (isAdminNeedsSubscription) {
                    if (isSubActive && isPinValid) {
                        this.subscriptionActive = true;
                        this.subscriptionChecked = true;
                        this.showCheckout = false;
                        this.isGroupAdminManaged = false;
                    } else {
                        this.subscriptionActive = false;
                        this.subscriptionChecked = true;
                        this.isGroupAdminManaged = false;
                        this.showCheckout = hasPricingOptions;
                        this.cdr.detectChanges();
                    }
                    this.subscriptionCheckInProgress = false;
                    return;
                }

                if (isAdminManagedNonAdmin) {
                    if (!isSubActive) {
                        this.subscriptionActive = false;
                        this.subscriptionChecked = true;
                        this.isGroupAdminManaged = true;
                        this.showCheckout = false;
                    } else if (isPinValid) {
                        this.subscriptionActive = true;
                        this.subscriptionChecked = true;
                        this.isGroupAdminManaged = false;
                        this.showCheckout = false;
                    } else {
                        this.subscriptionActive = false;
                        this.subscriptionChecked = true;
                        this.isGroupAdminManaged = true;
                        this.showCheckout = false;
                    }
                    this.subscriptionCheckInProgress = false;
                    this.cdr.detectChanges();
                    return;
                }

                // Standard user — PIN-based grace period covers brief billing dips.
                if (isPinValid) {
                    this.subscriptionActive = true;
                    this.subscriptionChecked = true;
                    this.showCheckout = false;
                    this.isGroupAdminManaged = false;
                } else {
                    this.subscriptionActive = false;
                    this.subscriptionChecked = true;
                    this.isGroupAdminManaged = false;
                    this.showCheckout = hasPricingOptions;
                    this.cdr.detectChanges();
                }
                this.subscriptionCheckInProgress = false;
            })
            .catch(() => {
                // Don't lock people out on a transient fetch error.
                this.subscriptionActive = true;
                this.subscriptionChecked = true;
                this.showCheckout = false;
                this.subscriptionCheckInProgress = false;
                this.cdr.detectChanges();
            });
    }

    transferToPersonalSubscription(): void {
        if (this.transferring) return;
        this.transferring = true;
        const pin = this.rdioScannerService.readPin();
        if (!pin) { this.transferring = false; return; }

        fetch('/api/user/transfer-to-public', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ pin }),
        })
            .then(r => {
                if (!r.ok) return r.json().then(err => { throw new Error(err.message || 'Transfer failed'); });
                return r.json();
            })
            .then(() => window.location.reload())
            .catch(err => {
                alert('Failed to transfer to personal subscription: ' + err.message);
                this.transferring = false;
            });
    }

    onCheckoutSuccess(_event: any): void {
        this.showCheckout = false;
        this.subscriptionActive = true;
        window.location.reload();
    }

    onCheckoutError(_event: any): void {
        // Keep checkout open on error — Stripe surface handles inline messaging.
    }

    onCheckoutCancel(): void {
        this.showCheckout = false;
        this.rdioScannerService.disconnect();
        this.subscriptionActive = false;
        this.subscriptionChecked = false;
        this.isGroupAdminManaged = false;
        this.signOut.emit();
    }

    // ────────────────────────────────────────────────────────────────────────
    // EVENT HANDLER (websocket → component state)
    // ────────────────────────────────────────────────────────────────────────

    private eventHandler(event: RdioScannerEvent): void {
        // Legacy unlock-code auth — only when user-registration is OFF.
        if ('auth' in event && event.auth && !this.userRegistrationEnabled) {
            const password = this.rdioScannerService.readPin();
            if (password) {
                if (this.lastStoredPin !== password) { this.lastStoredPin = password; this.storedPinAttempts = 0; }
                if (this.storedPinAttempts < 3) {
                    this.storedPinAttempts++;
                    this.authForm.get('password')?.setValue(password);
                    this.rdioScannerService.authenticate(password);
                } else {
                    this.rdioScannerService.clearPin();
                    this.lastStoredPin = null;
                    this.storedPinAttempts = 0;
                    this.auth = true;
                    this.authForm.reset();
                    if (this.authForm.disabled) this.authForm.enable();
                }
            } else {
                this.auth = event.auth;
                this.authForm.reset();
                if (this.authForm.disabled) this.authForm.enable();
            }
        }

        if ('call' in event) {
            if (this.call) {
                // Move finished call into history.
                const finished = this.call;
                if (finished?.id != null && !this.callHistoryStore.find(c => c?.id === finished.id)) {
                    this.callHistoryStore.unshift(finished);
                    this.pruneCallHistoryStore();
                }
                this.call = undefined;
                this.callPrevious = undefined;
                this.selectedHistoryCall = undefined;
                this.callUnit = '';
                this.avoided = this.delayed = this.patched = false;
                this.isFavorite = false;
            }
            if (event.call) {
                this.call = event.call;
                this.selectedHistoryCall = this.call;
                this.updateLiveCallInStore(this.call);
            }
            this.updateScanningAnimation();
        }

        if ('config' in event) {
            this.config = event.config;
            this.branding = this.config?.branding ?? '';
            this.titleService.setTitle(`TLR-${this.branding.trim() || 'ThinLine Radio'}`);
            this.email = this.config?.email ?? '';
            // Prefer the running server's version over the client's stamped
            // build version, which can drift between releases.
            if (this.config?.version) {
                this.version = this.config.version;
            }
            this.timeFormat = this.config?.time12hFormat ? 'h:mm a' : 'HH:mm';
            this.userRegistrationEnabled = this.config?.options?.userRegistrationEnabled ?? false;

            if (!this.userRegistrationEnabled) {
                const password = this.authForm.get('password')?.value;
                if (password) {
                    this.rdioScannerService.savePin(password);
                    this.authForm.reset();
                }
            }

            this.auth = false;
            this.lastStoredPin = null;
            this.storedPinAttempts = 0;
            this.authForm.reset();
            if (this.authForm.enabled) this.authForm.disable();

            // Re-check subscription whenever config changes.
            this.subscriptionChecked = false;
            if (!this.subscriptionCheckInProgress) {
                setTimeout(() => {
                    this.checkSubscriptionStatus();
                    setTimeout(() => this.cdr.detectChanges(), 100);
                }, 100);
            }
        }

        if ('error' in event && event.error && this.viewActive) {
            this.matSnackBar.open(event.error, 'Close', { duration: 5000, panelClass: ['error-snackbar'] });
        }

        if ('expired' in event && event.expired === true) {
            this.subscriptionActive = false;
            this.subscriptionChecked = false;
            this.showCheckout = false;
            this.authForm.get('password')?.setErrors({ expired: true });
            this.cdr.detectChanges();
            if (this.config) this.checkSubscriptionStatus();
        }

        if ('holdSys' in event)   this.holdSys = event.holdSys || false;
        if ('holdTg' in event)    this.holdTg = event.holdTg || false;
        if ('linked' in event)    this.linked = event.linked || false;
        if ('listeners' in event) this.listeners = event.listeners || 0;
        if ('queue' in event)     this.callQueue = event.queue || 0;
        if ('map' in event)       { this.map = event.map || {}; this.updateScanningAnimation(); }
        if ('pause' in event)     { this.livefeedPaused = event.pause || false; this.updateScanningAnimation(); }

        if ('time' in event && typeof event.time === 'number') {
            // Live audio is playing — refresh the source-unit display.
            this.refreshCallUnitForCurrentTime(event.time);
        }

        if ('tooMany' in event && event.tooMany === true) {
            this.authForm.get('password')?.setErrors({ tooMany: true });
        }

        if ('livefeedMode' in event && event.livefeedMode) {
            this.livefeedOffline = event.livefeedMode === RdioScannerLivefeedMode.Offline;
            this.livefeedOnline  = event.livefeedMode === RdioScannerLivefeedMode.Online;
            this.playbackMode    = event.livefeedMode === RdioScannerLivefeedMode.Playback;
            this.updateScanningAnimation();
            return;
        }

        this.updateCallFlags();
    }

    // ────────────────────────────────────────────────────────────────────────
    // PRIVATE UTILITIES
    // ────────────────────────────────────────────────────────────────────────

    /** Resolves the active source-unit label for the live call as audio progresses. */
    private refreshCallUnitForCurrentTime(time: number): void {
        if (!this.call) return;

        if (Array.isArray(this.call.sources) && this.call.sources.length) {
            const source = this.call.sources.reduce(
                (p, v) => (v.pos || 0) <= time ? v : p,
                {} as { pos?: number; src?: number; tag?: string },
            );
            const firstAlias = this.call.sources
                .find(s => typeof s.tag === 'string' && s.tag.trim().length > 0)
                ?.tag?.trim();

            if (typeof source.src === 'number') {
                if (typeof source.tag === 'string' && source.tag.trim().length > 0) {
                    this.callUnit = source.tag.trim();
                } else if (firstAlias) {
                    this.callUnit = firstAlias;
                } else {
                    this.callUnit = findUnitLabelForSrc(this.call.systemData?.units, source.src) ?? `${source.src}`;
                }
            }
        } else if (typeof this.call.source === 'number') {
            this.callUnit = findUnitLabelForSrc(this.call.systemData?.units, this.call.source) ?? `${this.call.source}`;
        }

        this.updateCallFlags();
    }

    /** Recompute avoided / delayed / patched flags for the active or just-finished call. */
    private updateCallFlags(): void {
        const call = this.call || this.callPrevious;
        if (!call) {
            this.avoided = this.delayed = this.patched = false;
            return;
        }
        this.delayed = !!call.delayed;
        if (this.rdioScannerService.isPatched(call)) {
            this.avoided = false;
            this.patched = true;
        } else {
            this.avoided = this.rdioScannerService.isAvoided(call);
            this.patched = false;
        }
    }

    /** True if `display` is a tag, src number, or resolved unit label belonging to `call`. */
    private displayStringBelongsToCall(call: RdioScannerCall, display: string): boolean {
        const norm = display.trim();
        if (!norm || norm === '0') return false;
        if (Array.isArray(call.sources)) {
            for (const s of call.sources) {
                if (typeof s.src === 'number') {
                    if (norm === String(s.src)) return true;
                    if (norm === resolveUnitLabel(call.systemData?.units, s.src)) return true;
                }
                if (typeof s.tag === 'string' && s.tag.trim() === norm) return true;
            }
        }
        if (typeof call.source === 'number'
            && (norm === String(call.source) || norm === resolveUnitLabel(call.systemData?.units, call.source))) {
            return true;
        }
        return false;
    }

    private isAfsSystem(call: RdioScannerCall): boolean {
        return call.systemData?.type === 'provoice' || call.talkgroupData?.type === 'provoice';
    }

    private formatAfs(n: number): string {
        return `${(n >> 7 & 15).toString().padStart(2, '0')}-${(n >> 3 & 15).toString().padStart(2, '0')}${n & 7}`;
    }

    private syncClock(): void {
        this.clockTimer?.unsubscribe();
        this.clock = new Date();
        this.clockTimer = timer(1000 * (60 - this.clock.getSeconds())).subscribe(() => this.syncClock());
    }

    private refreshCallsLastHour(): void {
        const cutoff = Date.now() - 60 * 60 * 1000;
        this.callsLastHour = this.callHistoryStore.filter(c => {
            if (!c?.dateTime) return false;
            return new Date(c.dateTime).getTime() >= cutoff;
        });
    }

    private pruneCallHistoryStore(): void {
        const cutoff = Date.now() - 2 * 60 * 60 * 1000;
        this.callHistoryStore = this.callHistoryStore.filter(c => {
            const t = c?.dateTime ? new Date(c.dateTime).getTime() : 0;
            return t >= cutoff;
        });
        if (this.callHistoryStore.length > 400) this.callHistoryStore = this.callHistoryStore.slice(0, 400);
        this.refreshCallsLastHour();
    }

    private updateLiveCallInStore(live: RdioScannerCall): void {
        if (live?.id == null) return;
        const i = this.callHistoryStore.findIndex(c => c?.id === live.id);
        if (i >= 0) {
            this.callHistoryStore[i] = { ...this.callHistoryStore[i], ...live };
            this.refreshCallsLastHour();
        }
    }

    private playCallFromHistoryEntry(
        entry: RdioScannerCall | undefined,
        options?: { preserveQueue?: boolean },
    ): void {
        if (!entry) return;
        const d = entry.audio?.data;
        const hasAudio = Array.isArray(d) ? d.length > 0 : false;
        if (hasAudio) {
            this.rdioScannerService.play(entry);
        } else if (entry.id != null) {
            this.rdioScannerService.loadAndPlay(entry.id, options);
        } else {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Denied);
        }
    }

    private checkAutoStartLivefeed(): void {
        setTimeout(async () => {
            if (!this.isUserAuthenticated) return;
            // Only the currently visible view should initiate auto-livefeed, otherwise
            // both mounted views would race to start the singleton service.
            if (!this.viewActive) return;

            this.settingsService.shouldAutoStartLivefeed().subscribe({
                next: async (shouldAutoStart) => {
                    if (shouldAutoStart && this.livefeedOffline) {
                        await this.rdioScannerService.ensureAudioReady();
                        setTimeout(() => {
                            this.rdioScannerService.beep(RdioScannerBeepStyle.Activate);
                            this.rdioScannerService.livefeed();
                        }, 100);
                    }
                },
                error: (err) => console.error('Error checking auto-start livefeed:', err),
            });
        }, 2000);
    }
}
