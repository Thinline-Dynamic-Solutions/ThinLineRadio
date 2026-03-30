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

import { Component, OnDestroy, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Subscription } from 'rxjs';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { RdioScannerService } from '../rdio-scanner.service';
import { SettingsService } from './settings.service';
import { TagColorService, TagColorConfig } from '../tag-color.service';
import { AlertSoundService, AlertSound } from '../alert-sound.service';
import { AlertsService } from '../alerts/alerts.service';
import { RdioScannerAlertPreference } from '../rdio-scanner';

@Component({
    selector: 'rdio-scanner-settings',
    styleUrls: ['./settings.component.scss'],
    templateUrl: './settings.component.html',
})
export class RdioScannerSettingsComponent implements OnDestroy, OnInit {
    settings: any = {};
    availableTags: string[] = [];
    tagColors: TagColorConfig = {};
    availableColors: Array<{name: string, value: string}> = [];
    private tagColorsSubscription?: Subscription;
    tagsExpanded: boolean = false;
    livefeedBacklogMinutes: number = 0;
    autoLivefeed: boolean = false;
    isPWA: boolean = false;
    alertSound: string = 'alert';
    availableAlertSounds: AlertSound[] = [];
    
    // Font selection
    appFont: string = 'Roboto';
    availableFonts: Array<{name: string, value: string, displayName: string}> = [
        { name: 'Roboto', value: 'Roboto, sans-serif', displayName: 'Roboto (Default)' },
        { name: 'Rajdhani', value: 'Rajdhani, sans-serif', displayName: 'Rajdhani (Modern Technical)' },
        { name: 'ShareTechMono', value: '"Share Tech Mono", monospace', displayName: 'Share Tech Mono (Terminal)' },
        { name: 'Audiowide', value: 'Audiowide, cursive', displayName: 'Audiowide (Digital Display)' },
    ];

    // Per-channel sounds
    channelPreferences: RdioScannerAlertPreference[] = [];
    loadingChannelSounds: boolean = false;
    savingChannelSound: Set<string> = new Set();
    expandedSystems: Set<number> = new Set();
    expandedTags: Set<string> = new Set();

    // Account info
    accountInfo: any = null;
    loadingAccount: boolean = false;
    
    // Subscription management
    config: any = null;
    showCheckout: boolean = false;
    showChangeSubscription: boolean = false;
    userEmail: string = '';
    currentPriceId: string | null = null;

    // Forms
    emailForm: FormGroup;
    passwordForm: FormGroup;
    emailVerificationForm: FormGroup;
    passwordVerificationForm: FormGroup;
    updatingEmail: boolean = false;
    updatingPassword: boolean = false;
    
    // Email change verification state
    emailChangeVerified: boolean = false;
    requestingVerification: boolean = false;
    verificationCodeSent: boolean = false;
    verifyingCode: boolean = false;
    emailChangeCode: string = '';
    
    // Password change verification state
    passwordChangeVerified: boolean = false;
    requestingPasswordVerification: boolean = false;
    passwordVerificationCodeSent: boolean = false;
    verifyingPasswordCode: boolean = false;
    passwordChangeCode: string = '';

    constructor(
        private rdioScannerService: RdioScannerService,
        private settingsService: SettingsService,
        private tagColorService: TagColorService,
        private alertSoundService: AlertSoundService,
        private alertsService: AlertsService,
        private http: HttpClient,
        private fb: FormBuilder,
        private snackBar: MatSnackBar,
    ) {
        this.emailForm = this.fb.group({
            newEmail: ['', [Validators.required, Validators.email]],
            password: ['', [Validators.required]],
            code: ['', [Validators.required]], // Email change verification code
        });

        this.emailVerificationForm = this.fb.group({
            code: ['', [Validators.required, Validators.pattern(/^\d{6}$/)]],
        });

        this.passwordVerificationForm = this.fb.group({
            code: ['', [Validators.required, Validators.pattern(/^\d{6}$/)]],
        });

        this.passwordForm = this.fb.group({
            currentPassword: ['', [Validators.required]],
            newPassword: ['', [Validators.required, Validators.minLength(6)]],
            confirmPassword: ['', [Validators.required]],
            code: ['', [Validators.required]], // Password change verification code
        }, { validators: this.passwordMatchValidator });
    }

    ngOnInit(): void {
        this.checkIfPWA();
        this.loadSettings();
        this.loadTagColors();
        this.loadAccountInfo();
        this.loadConfig();
        this.loadAlertSounds();
        this.loadChannelPreferences();
    }

    loadAlertSounds(): void {
        this.availableAlertSounds = this.alertSoundService.getAvailableSounds();
    }

    loadChannelPreferences(): void {
        const pin = this.getPin();
        if (!pin) return;
        this.loadingChannelSounds = true;
        this.alertsService.getPreferences(pin).subscribe({
            next: (prefs) => {
                this.channelPreferences = (prefs || []).filter(p => p.alertEnabled);
                this.loadingChannelSounds = false;
            },
            error: () => { this.loadingChannelSounds = false; },
        });
    }

    /** Returns active channel preferences for a given system, sorted by talkgroup label */
    getChannelPrefsForSystem(systemRef: number): RdioScannerAlertPreference[] {
        return this.channelPreferences.filter(p => (p.systemRef ?? p.systemId) === systemRef);
    }

    /** Returns all unique systems that have at least one active channel preference */
    getSystemsWithActivePrefs(): any[] {
        if (!this.config?.systems) return [];
        return (this.config.systems as any[]).filter(s =>
            this.channelPreferences.some(p => (p.systemRef ?? p.systemId) === s.id)
        );
    }

    getTalkgroupLabel(systemRef: number, talkgroupRef: number): string {
        const system = (this.config?.systems as any[] | undefined)?.find((s: any) => s.id === systemRef);
        if (!system) return `Channel ${talkgroupRef}`;
        const tg = (system.talkgroups as any[] | undefined)?.find((t: any) => t.talkgroupRef === talkgroupRef || t.id === talkgroupRef);
        return tg?.label || tg?.name || `Channel ${talkgroupRef}`;
    }

    getToneSetsForPref(pref: RdioScannerAlertPreference): any[] {
        if (!pref.toneAlerts) return [];
        const sysRef = pref.systemRef ?? pref.systemId;
        const tgRef = pref.talkgroupRef ?? pref.talkgroupId;
        const system = (this.config?.systems as any[] | undefined)?.find((s: any) => s.id === sysRef);
        if (!system) return [];
        const tg = (system.talkgroups as any[] | undefined)?.find((t: any) => t.talkgroupRef === tgRef || t.id === tgRef);
        return tg?.toneSets || [];
    }

    /** Convert web sound name ('alert') to filename ('alert.wav') */
    soundNameToFilename(name: string): string {
        if (!name || name === 'none') return '';
        const sound = this.availableAlertSounds.find(s => s.name === name);
        if (!sound?.file) return '';
        // Extract filename from path: 'assets/sounds/alert.wav' → 'alert.wav'
        return sound.file.split('/').pop() || '';
    }

    /** Convert filename ('alert.wav') to web sound name ('alert') */
    filenameToSoundName(filename: string): string {
        if (!filename) return 'none';
        const base = filename.replace(/\.(wav|mp3|m4a)$/i, '').toLowerCase();
        const sound = this.availableAlertSounds.find(s => s.name === base || s.name === base.replace(/-/g, '_'));
        return sound?.name || 'none';
    }

    getChannelSoundName(pref: RdioScannerAlertPreference): string {
        return this.filenameToSoundName(pref.notificationSound || '');
    }

    getToneSetSoundName(pref: RdioScannerAlertPreference, toneSetId: string): string {
        return this.filenameToSoundName((pref.toneSetSounds || {})[toneSetId] || '');
    }

    setChannelSound(pref: RdioScannerAlertPreference, soundName: string): void {
        const filename = this.soundNameToFilename(soundName);
        pref.notificationSound = filename;
        if (soundName && soundName !== 'none') {
            this.alertSoundService.previewSound(soundName);
        }
        this.saveChannelPref(pref);
    }

    setToneSetSound(pref: RdioScannerAlertPreference, toneSetId: string, soundName: string): void {
        if (!pref.toneSetSounds) pref.toneSetSounds = {};
        const filename = this.soundNameToFilename(soundName);
        if (!filename) {
            delete pref.toneSetSounds[toneSetId];
        } else {
            pref.toneSetSounds[toneSetId] = filename;
        }
        if (soundName && soundName !== 'none') {
            this.alertSoundService.previewSound(soundName);
        }
        this.saveChannelPref(pref);
    }

    private saveChannelPref(pref: RdioScannerAlertPreference): void {
        const pin = this.getPin();
        if (!pin) return;
        const key = `${pref.systemRef ?? pref.systemId}:${pref.talkgroupRef ?? pref.talkgroupId}`;
        this.savingChannelSound.add(key);
        // Send the full list so nothing else gets wiped
        this.alertsService.updatePreferences(this.channelPreferences, pin).subscribe({
            next: () => this.savingChannelSound.delete(key),
            error: () => {
                this.savingChannelSound.delete(key);
                this.snackBar.open('Failed to save sound setting', 'Close', { duration: 3000 });
            },
        });
    }

    isChannelSoundSaving(pref: RdioScannerAlertPreference): boolean {
        const key = `${pref.systemRef ?? pref.systemId}:${pref.talkgroupRef ?? pref.talkgroupId}`;
        return this.savingChannelSound.has(key);
    }

    // ── Collapse helpers ──────────────────────────────────────────────────────

    toggleSystem(systemId: number): void {
        if (this.expandedSystems.has(systemId)) {
            this.expandedSystems.delete(systemId);
        } else {
            this.expandedSystems.add(systemId);
        }
    }

    isSystemExpanded(systemId: number): boolean {
        return this.expandedSystems.has(systemId);
    }

    toggleTag(key: string): void {
        if (this.expandedTags.has(key)) {
            this.expandedTags.delete(key);
        } else {
            this.expandedTags.add(key);
        }
    }

    isTagExpanded(key: string): boolean {
        return this.expandedTags.has(key);
    }

    // ── Tag-grouping helpers ──────────────────────────────────────────────────

    private getTalkgroupTag(systemRef: number, talkgroupRef: number): string {
        const system = (this.config?.systems as any[] | undefined)
            ?.find((s: any) => s.id === systemRef);
        if (!system) return 'Untagged';
        const tg = (system.talkgroups as any[] | undefined)
            ?.find((t: any) => t.talkgroupRef === talkgroupRef || t.id === talkgroupRef);
        return tg?.tag || 'Untagged';
    }

    getTagsForSystem(systemId: number): string[] {
        const tags = new Set<string>();
        for (const pref of this.getChannelPrefsForSystem(systemId)) {
            tags.add(this.getTalkgroupTag(systemId, pref.talkgroupRef ?? pref.talkgroupId ?? 0));
        }
        return Array.from(tags).sort((a, b) => {
            if (a === 'Untagged') return 1;
            if (b === 'Untagged') return -1;
            return a.localeCompare(b);
        });
    }

    getChannelPrefsForSystemAndTag(systemId: number, tag: string): RdioScannerAlertPreference[] {
        return this.getChannelPrefsForSystem(systemId).filter(pref =>
            this.getTalkgroupTag(systemId, pref.talkgroupRef ?? pref.talkgroupId ?? 0) === tag
        );
    }

    checkIfPWA(): void {
        // Check if app is installed as PWA
        // Method 1: Check display-mode media query (works on most browsers)
        const isStandalone = window.matchMedia('(display-mode: standalone)').matches;
        // Method 2: Check if running in standalone mode (iOS Safari)
        const isIOSStandalone = (window.navigator as any).standalone === true;
        // Method 3: Check if running in fullscreen mode
        const isFullscreen = window.matchMedia('(display-mode: fullscreen)').matches;
        
        this.isPWA = isStandalone || isIOSStandalone || isFullscreen;
    }
    
    loadConfig(): void {
        // Subscribe to config updates from the service
        this.rdioScannerService.event.subscribe((event: any) => {
            if (event.config) {
                this.config = event.config;
            }
        });
    }

    ngOnDestroy(): void {
        if (this.tagColorsSubscription) {
            this.tagColorsSubscription.unsubscribe();
        }
    }

    loadSettings(): void {
        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                this.settings = settings || {};
                // Load livefeed backlog setting
                this.livefeedBacklogMinutes = this.settings.livefeedBacklogMinutes || 0;
                // Load auto livefeed setting
                this.autoLivefeed = this.settings.autoLivefeed || false;
                // Load alert sound setting
                this.alertSound = this.settings.alertSound || 'alert';
                // Load font setting
                this.appFont = this.settings.appFont || 'Roboto';
                // Apply font
                this.applyFont(this.appFont);
            },
            error: (error) => {
                console.error('Error loading settings:', error);
                this.settings = {};
                this.livefeedBacklogMinutes = 0;
                this.autoLivefeed = false;
                this.alertSound = 'alert';
                this.appFont = 'Roboto';
            },
        });
    }

    loadTagColors(): void {
        // Get all available tags from the service
        this.availableTags = this.tagColorService.getAllTags();
        
        // Get available color options
        this.availableColors = this.tagColorService.getAvailableColors();
        
        // Subscribe to tag color updates
        this.tagColorsSubscription = this.tagColorService.getTagColors().subscribe({
            next: (colors) => {
                this.tagColors = colors;
                // Update available tags in case new ones were added
                this.availableTags = this.tagColorService.getAllTags();
            },
        });
        
        // Get current colors
        this.tagColors = this.tagColorService.getAllTagColors();
    }

    setTagColor(tag: string, color: string): void {
        this.tagColorService.setTagColor(tag, color);
        // Colors are automatically saved by TagColorService
    }

    resetTagColor(tag: string): void {
        this.tagColorService.resetTagColor(tag);
    }

    resetAllColors(): void {
        this.tagColorService.resetAllColors();
    }

    getColorFieldId(tag: string): string {
        const normalized = tag
            .toLowerCase()
            .replace(/[^a-z0-9]+/g, '-')
            .replace(/-+/g, '-')
            .replace(/^-|-$/g, '');

        return `color-${normalized || 'untagged'}`;
    }

    toggleTagsExpanded(): void {
        this.tagsExpanded = !this.tagsExpanded;
    }

    getSelectedColorValue(tag: string): string {
        return this.tagColors[tag.toLowerCase()] || '#ffffff';
    }

    getSelectedColorName(tag: string): string {
        const colorValue = this.getSelectedColorValue(tag);
        const color = this.availableColors.find(c => c.value === colorValue);
        return color ? color.name : 'White';
    }

    saveSettings(): void {
        // Update settings with current values
        this.settings.livefeedBacklogMinutes = this.livefeedBacklogMinutes;
        this.settings.autoLivefeed = this.autoLivefeed;
        this.settings.alertSound = this.alertSound;
        this.settings.appFont = this.appFont;
        this.settingsService.saveSettings(this.settings).subscribe({
            next: () => {
                console.log('Settings saved successfully');
                this.snackBar.open('Settings saved successfully', 'Close', {
                    duration: 3000,
                });
            },
            error: (error) => {
                console.error('Error saving settings:', error);
                this.snackBar.open('Error saving settings', 'Close', {
                    duration: 3000,
                });
            },
        });
    }

    onBacklogChange(): void {
        // Auto-save when backlog setting changes
        this.saveSettings();
    }

    onAutoLivefeedChange(): void {
        // Auto-save when auto livefeed setting changes
        this.saveSettings();
    }

    onAlertSoundChange(): void {
        // Auto-save when alert sound changes
        this.saveSettings();
    }

    previewAlertSound(soundName: string): void {
        this.alertSoundService.previewSound(soundName);
    }
    
    onFontChange(): void {
        // Apply font and auto-save
        this.applyFont(this.appFont);
        this.saveSettings();
    }
    
    applyFont(fontName: string): void {
        const font = this.availableFonts.find(f => f.name === fontName);
        if (font) {
            // Apply font to body element
            document.body.style.fontFamily = font.value;
            
            // Adjust font size for Audiowide (15% smaller)
            if (fontName === 'Audiowide') {
                document.documentElement.style.fontSize = '14.45px'; // 85% of 17px (default)
            } else {
                document.documentElement.style.fontSize = ''; // Reset to default
            }
        }
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
        const pin = window?.localStorage?.getItem('rdio-scanner-pin');
        return pin ? window.atob(pin) : undefined;
    }

    loadAccountInfo(): void {
        this.loadingAccount = true;
        const pin = this.getPin();
        if (!pin) {
            this.loadingAccount = false;
            return;
        }

        const headers = this.getAuthHeaders();
        this.http.get<any>('/api/account', { 
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: (account) => {
                this.accountInfo = account;
                this.emailForm.patchValue({ newEmail: account.email });
                this.userEmail = account.email || '';
                // Store current subscription price ID if available
                this.currentPriceId = account.currentPriceId || null;
                this.loadingAccount = false;
            },
            error: (error) => {
                console.error('Error loading account info:', error);
                this.loadingAccount = false;
            },
        });
    }

    requestEmailChangeVerification(): void {
        this.requestingVerification = true;
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to change your email', 'Close', { duration: 3000 });
            this.requestingVerification = false;
            return;
        }

        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/account/email/request-verification', {}, {
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: () => {
                this.snackBar.open('Verification code sent to your email', 'Close', { duration: 5000 });
                this.requestingVerification = false;
                this.verificationCodeSent = true;
            },
            error: (error) => {
                const message = error.error?.error || 'Failed to send verification code';
                this.snackBar.open(message, 'Close', { duration: 5000 });
                this.requestingVerification = false;
                this.verificationCodeSent = false;
            },
        });
    }

    verifyEmailChangeCode(): void {
        if (this.emailVerificationForm.invalid) {
            return;
        }

        this.verifyingCode = true;
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to verify your email', 'Close', { duration: 3000 });
            this.verifyingCode = false;
            return;
        }

        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/account/email/verify-code', this.emailVerificationForm.value, {
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: (response) => {
                if (response.verified) {
                    this.emailChangeVerified = true;
                    this.emailChangeCode = this.emailVerificationForm.value.code;
                    this.emailForm.patchValue({ code: this.emailChangeCode });
                    this.verificationCodeSent = false; // Reset after successful verification
                    this.snackBar.open('Email verified. You can now change your email address.', 'Close', { duration: 5000 });
                }
                this.verifyingCode = false;
            },
            error: (error) => {
                const message = error.error?.error || 'Invalid verification code';
                this.snackBar.open(message, 'Close', { duration: 5000 });
                this.verifyingCode = false;
            },
        });
    }

    updateEmail(): void {
        if (this.emailForm.invalid) {
            return;
        }

        if (!this.emailChangeVerified) {
            this.snackBar.open('Please verify your current email first', 'Close', { duration: 3000 });
            return;
        }

        this.updatingEmail = true;
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to update your email', 'Close', { duration: 3000 });
            this.updatingEmail = false;
            return;
        }

        const formValue = this.emailForm.value;
        formValue.code = this.emailChangeCode; // Include verification code

        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/account/email', formValue, {
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: (response) => {
                if (response.requiresVerification) {
                    this.snackBar.open('Email change initiated. Please check your new email for verification.', 'Close', { duration: 7000 });
                    // Reset forms and state
                    this.emailChangeVerified = false;
                    this.emailChangeCode = '';
                    this.emailForm.reset();
                    this.emailVerificationForm.reset();
                    // Reload account info
                    this.loadAccountInfo();
                } else {
                    this.snackBar.open('Email updated successfully', 'Close', { duration: 3000 });
                    this.accountInfo.email = response.email;
                    this.emailForm.reset({ newEmail: response.email });
                    this.emailChangeVerified = false;
                    this.emailChangeCode = '';
                }
                this.updatingEmail = false;
            },
            error: (error) => {
                const message = error.error?.error || 'Failed to update email';
                this.snackBar.open(message, 'Close', { duration: 5000 });
                this.updatingEmail = false;
            },
        });
    }

    requestPasswordChangeVerification(): void {
        this.requestingPasswordVerification = true;
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to change your password', 'Close', { duration: 3000 });
            this.requestingPasswordVerification = false;
            return;
        }

        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/account/password/request-verification', {}, {
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: () => {
                this.snackBar.open('Verification code sent to your email', 'Close', { duration: 5000 });
                this.requestingPasswordVerification = false;
                this.passwordVerificationCodeSent = true;
            },
            error: (error) => {
                const message = error.error?.error || 'Failed to send verification code';
                this.snackBar.open(message, 'Close', { duration: 5000 });
                this.requestingPasswordVerification = false;
                this.passwordVerificationCodeSent = false;
            },
        });
    }

    verifyPasswordChangeCode(): void {
        if (this.passwordVerificationForm.invalid) {
            return;
        }

        this.verifyingPasswordCode = true;
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to verify your password change', 'Close', { duration: 3000 });
            this.verifyingPasswordCode = false;
            return;
        }

        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/account/password/verify-code', this.passwordVerificationForm.value, {
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: (response) => {
                if (response.verified) {
                    this.passwordChangeVerified = true;
                    this.passwordChangeCode = this.passwordVerificationForm.value.code;
                    this.passwordForm.patchValue({ code: this.passwordChangeCode });
                    this.snackBar.open('Email verified. You can now change your password.', 'Close', { duration: 5000 });
                }
                this.verifyingPasswordCode = false;
            },
            error: (error) => {
                const message = error.error?.error || 'Invalid verification code';
                this.snackBar.open(message, 'Close', { duration: 5000 });
                this.verifyingPasswordCode = false;
            },
        });
    }

    updatePassword(): void {
        if (this.passwordForm.invalid) {
            return;
        }

        if (!this.passwordChangeVerified) {
            this.snackBar.open('Please verify your email first', 'Close', { duration: 3000 });
            return;
        }

        this.updatingPassword = true;
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to update your password', 'Close', { duration: 3000 });
            this.updatingPassword = false;
            return;
        }

        const { confirmPassword, ...passwordData } = this.passwordForm.value;
        passwordData.code = this.passwordChangeCode; // Include verification code
        const headers = this.getAuthHeaders();
        this.http.post<any>('/api/account/password', passwordData, {
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: () => {
                this.snackBar.open('Password updated successfully', 'Close', { duration: 3000 });
                this.passwordForm.reset();
                this.passwordChangeVerified = false;
                this.passwordChangeCode = '';
                this.passwordVerificationCodeSent = false;
                this.passwordVerificationForm.reset();
                this.updatingPassword = false;
            },
            error: (error) => {
                const message = error.error?.error || 'Failed to update password';
                this.snackBar.open(message, 'Close', { duration: 5000 });
                this.updatingPassword = false;
            },
        });
    }

    openBillingPortal(): void {
        const pin = this.getPin();
        if (!pin) {
            this.snackBar.open('Please log in to manage billing', 'Close', { duration: 3000 });
            return;
        }

        const headers = this.getAuthHeaders();
        const returnUrl = window.location.href;
        this.http.post<any>('/api/billing/portal', { returnUrl }, {
            headers,
            params: { pin: encodeURIComponent(pin) }
        }).subscribe({
            next: (response) => {
                if (response.url) {
                    window.location.href = response.url;
                } else {
                    this.snackBar.open('Failed to open billing portal', 'Close', { duration: 3000 });
                }
            },
            error: (error) => {
                const message = error.error?.error || 'Failed to open billing portal';
                this.snackBar.open(message, 'Close', { duration: 5000 });
            },
        });
    }
    
    openCheckout(): void {
        if (!this.accountInfo?.email) {
            this.snackBar.open('Unable to get your email address', 'Close', { duration: 3000 });
            return;
        }
        this.userEmail = this.accountInfo.email;
        this.showCheckout = true;
        this.showChangeSubscription = false;
    }
    
    openChangeSubscription(): void {
        if (!this.accountInfo?.email) {
            this.snackBar.open('Unable to get your email address', 'Close', { duration: 3000 });
            return;
        }
        this.userEmail = this.accountInfo.email;
        this.showChangeSubscription = true;
        this.showCheckout = true; // Reuse the same checkout modal
    }
    
    onCheckoutSuccess(event: any): void {
        console.log('Checkout successful:', event);
        this.showCheckout = false;
        this.showChangeSubscription = false;
        // Reload account info to get updated subscription status
        this.loadAccountInfo();
        // Reload page to get updated subscription status
        window.location.reload();
    }
    
    onCheckoutError(event: any): void {
        console.error('Checkout error:', event);
        // Keep checkout open on error
    }
    
    onCheckoutCancel(): void {
        this.showCheckout = false;
        this.showChangeSubscription = false;
    }
    
    getSubscriptionStatusDisplay(): string {
        if (!this.accountInfo) {
            return 'N/A';
        }
        
        const status = this.accountInfo.subscriptionStatusDisplay || this.accountInfo.subscriptionStatus;
        
        // Map status codes to user-friendly messages
        switch (status) {
            case 'not_billed':
                return 'This account is not billed';
            case 'group_admin_managed':
                return 'Billing is managed by your group admin';
            case 'active':
                return 'Active';
            case 'trialing':
                return 'Trialing';
            case 'canceled':
                return 'Canceled';
            case 'past_due':
                return 'Past Due';
            case 'unpaid':
                return 'Unpaid';
            case 'incomplete':
                return 'Incomplete';
            case 'incomplete_expired':
                return 'Incomplete - Expired';
            default:
                return status || 'N/A';
        }
    }
    
    isGroupAdminManaged(): boolean {
        if (!this.accountInfo) {
            return false;
        }
        const status = this.accountInfo.subscriptionStatusDisplay || this.accountInfo.subscriptionStatus;
        return status === 'group_admin_managed' || 
               (this.accountInfo.billingRequired && !this.accountInfo.isGroupAdmin && 
                this.accountInfo.subscriptionStatus === 'group_admin_managed');
    }

    passwordMatchValidator(form: FormGroup): { [key: string]: boolean } | null {
        const newPassword = form.get('newPassword');
        const confirmPassword = form.get('confirmPassword');
        
        if (!newPassword || !confirmPassword) {
            return null;
        }
        
        return newPassword.value === confirmPassword.value ? null : { passwordMismatch: true };
    }
}

