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

import { Component, ElementRef, OnDestroy, ViewChild, ViewEncapsulation } from '@angular/core';
import { MatTabChangeEvent } from '@angular/material/tabs';
import { Title } from '@angular/platform-browser';
import { AdminEvent, RdioScannerAdminService } from './admin.service';
import { RdioScannerAdminLogsComponent } from './logs/logs.component';
import { RdioScannerAdminConfigComponent } from './config/config.component';
import { RdioScannerAdminToolsComponent } from './tools/tools.component';

export interface SearchResult {
    label: string;
    keywords: string;
    breadcrumb: string;
    icon: string;
    tab: number;          // 0=Config, 1=Logs, 2=SystemHealth, 3=Tools
    configSection?: string;
    optionPanel?: string;
    toolSection?: string;
}

const SETTINGS_INDEX: SearchResult[] = [
    // ── General ──────────────────────────────────────────────────────────────
    { label: 'Time Format', keywords: 'time clock 12 24 hour format', breadcrumb: 'Config → Options → General', icon: 'schedule', tab: 0, configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Keypad Beeps', keywords: 'keypad beep sound audio', breadcrumb: 'Config → Options → General', icon: 'volume_up', tab: 0, configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Max Clients', keywords: 'max clients connections limit', breadcrumb: 'Config → Options → General', icon: 'people', tab: 0, configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Playback Goes Live', keywords: 'playback live auto play', breadcrumb: 'Config → Options → General', icon: 'live_tv', tab: 0, configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Sort Talkgroups', keywords: 'sort talkgroups order', breadcrumb: 'Config → Options → General', icon: 'sort', tab: 0, configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Show Listeners Count', keywords: 'listeners count display', breadcrumb: 'Config → Options → General', icon: 'people_outline', tab: 0, configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Auto Populate', keywords: 'auto populate talkgroup system', breadcrumb: 'Config → Options → General', icon: 'auto_fix_high', tab: 0, configSection: 'options', optionPanel: 'generalExpanded' },
    // ── Branding ─────────────────────────────────────────────────────────────
    { label: 'Branding Label', keywords: 'branding label name title', breadcrumb: 'Config → Options → Branding', icon: 'label', tab: 0, configSection: 'options', optionPanel: 'brandingExpanded' },
    { label: 'Base URL', keywords: 'base url domain address server', breadcrumb: 'Config → Options → Branding', icon: 'link', tab: 0, configSection: 'options', optionPanel: 'brandingExpanded' },
    { label: 'Server Logo', keywords: 'server logo image email logo upload', breadcrumb: 'Config → Options → Branding', icon: 'image', tab: 0, configSection: 'options', optionPanel: 'brandingExpanded' },
    { label: 'Favicon', keywords: 'favicon icon browser tab logo generate', breadcrumb: 'Config → Options → Branding', icon: 'web', tab: 0, configSection: 'options', optionPanel: 'brandingExpanded' },
    // ── Transcription ─────────────────────────────────────────────────────────
    { label: 'Transcription', keywords: 'transcription enable provider whisper deepgram openai', breadcrumb: 'Config → Options → Transcription', icon: 'transcribe', tab: 0, configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Transcription Provider', keywords: 'whisper deepgram openai provider api', breadcrumb: 'Config → Options → Transcription', icon: 'smart_toy', tab: 0, configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Transcription Language', keywords: 'language locale transcription', breadcrumb: 'Config → Options → Transcription', icon: 'language', tab: 0, configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Worker Pool Size', keywords: 'worker pool threads concurrent transcription', breadcrumb: 'Config → Options → Transcription', icon: 'memory', tab: 0, configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Hallucination Detection', keywords: 'hallucination detect filter transcription', breadcrumb: 'Config → Options → Transcription', icon: 'psychology', tab: 0, configSection: 'options', optionPanel: 'transcriptionExpanded' },
    // ── Alerts ────────────────────────────────────────────────────────────────
    { label: 'System Health Alerts', keywords: 'system health alerts enable monitoring', breadcrumb: 'Config → Options → Alert & Health', icon: 'health_and_safety', tab: 0, configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'No Audio Alerts', keywords: 'no audio alert silence threshold minutes', breadcrumb: 'Config → Options → Alert & Health', icon: 'volume_off', tab: 0, configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'Transcription Failure Alerts', keywords: 'transcription failure alert threshold', breadcrumb: 'Config → Options → Alert & Health', icon: 'error_outline', tab: 0, configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'Tone Detection Alerts', keywords: 'tone detection alert monitoring', breadcrumb: 'Config → Options → Alert & Health', icon: 'notifications', tab: 0, configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'Alert Retention', keywords: 'alert retention days keep history', breadcrumb: 'Config → Options → Alert & Health', icon: 'history', tab: 0, configSection: 'options', optionPanel: 'alertsExpanded' },
    // ── Email ─────────────────────────────────────────────────────────────────
    { label: 'Email Service', keywords: 'email service enable sendgrid mailgun smtp push notifications', breadcrumb: 'Config → Options → Email', icon: 'email', tab: 0, configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'SendGrid API Key', keywords: 'sendgrid api key email service provider', breadcrumb: 'Config → Options → Email', icon: 'vpn_key', tab: 0, configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'Mailgun API Key', keywords: 'mailgun api key email domain provider', breadcrumb: 'Config → Options → Email', icon: 'vpn_key', tab: 0, configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'SMTP Settings', keywords: 'smtp server host port email tls username password provider', breadcrumb: 'Config → Options → Email', icon: 'dns', tab: 0, configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'From Email / Name', keywords: 'from email sender name address', breadcrumb: 'Config → Options → Email', icon: 'alternate_email', tab: 0, configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'Push Notifications', keywords: 'push notifications relay server api key thinline enable', breadcrumb: 'Config → Options → Email', icon: 'notifications_active', tab: 0, configSection: 'options', optionPanel: 'notificationsExpanded' },
    // ── User Registration ─────────────────────────────────────────────────────
    { label: 'User Registration', keywords: 'user registration enable public invite invite-only signup', breadcrumb: 'Config → Options → User Registration', icon: 'how_to_reg', tab: 0, configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    { label: 'Email Verification', keywords: 'email verification required signup verify', breadcrumb: 'Config → Options → User Registration', icon: 'mark_email_read', tab: 0, configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    { label: 'Cloudflare Turnstile', keywords: 'turnstile cloudflare captcha site key secret', breadcrumb: 'Config → Options → User Registration', icon: 'security', tab: 0, configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    { label: 'Invitation Codes', keywords: 'invitation invite code access registration', breadcrumb: 'Config → Options → User Registration', icon: 'card_giftcard', tab: 0, configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    // ── Stripe ────────────────────────────────────────────────────────────────
    { label: 'Stripe Paywall', keywords: 'stripe paywall enable payments billing', breadcrumb: 'Config → Options → Stripe', icon: 'payment', tab: 0, configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Publishable Key', keywords: 'stripe publishable key pk live test', breadcrumb: 'Config → Options → Stripe', icon: 'vpn_key', tab: 0, configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Secret Key', keywords: 'stripe secret key sk live test', breadcrumb: 'Config → Options → Stripe', icon: 'lock', tab: 0, configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Webhook Secret', keywords: 'stripe webhook secret whsec', breadcrumb: 'Config → Options → Stripe', icon: 'webhook', tab: 0, configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Grace Period', keywords: 'stripe grace period days subscription lapse', breadcrumb: 'Config → Options → Stripe', icon: 'timer', tab: 0, configSection: 'options', optionPanel: 'stripeExpanded' },
    // ── Integrations ─────────────────────────────────────────────────────────
    { label: 'Radio Reference', keywords: 'radio reference rr login username password premium account', breadcrumb: 'Config → Options → Integrations', icon: 'cloud_download', tab: 0, configSection: 'options', optionPanel: 'integrationsExpanded' },
    { label: 'Config Sync', keywords: 'config sync remote server upstream synchronize', breadcrumb: 'Config → Options → Integrations', icon: 'cloud_sync', tab: 0, configSection: 'options', optionPanel: 'integrationsExpanded' },
    { label: 'Relay Server', keywords: 'relay server thinline push notifications audio encryption connect', breadcrumb: 'Config → Options → Integrations', icon: 'cell_tower', tab: 0, configSection: 'options', optionPanel: 'integrationsExpanded' },
    { label: 'Relay Server API Key', keywords: 'relay server api key push notifications audio encryption request thinline tlr', breadcrumb: 'Config → Options → Integrations', icon: 'vpn_key', tab: 0, configSection: 'options', optionPanel: 'integrationsExpanded' },
    // ── Audio Settings ────────────────────────────────────────────────────────
    { label: 'Audio Conversion', keywords: 'audio conversion enable convert format', breadcrumb: 'Config → Options → Audio Settings', icon: 'graphic_eq', tab: 0, configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Duplicate Detection', keywords: 'duplicate detection call fingerprint similarity', breadcrumb: 'Config → Options → Audio Settings', icon: 'content_copy', tab: 0, configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Audio Encryption', keywords: 'audio encryption key aes', breadcrumb: 'Config → Options → Audio Settings', icon: 'enhanced_encryption', tab: 0, configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Rate Limiting', keywords: 'rate limit download restrict', breadcrumb: 'Config → Options → Audio Settings', icon: 'speed', tab: 0, configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Reconnection Manager', keywords: 'reconnection manager grace period buffer', breadcrumb: 'Config → Options → Audio Settings', icon: 'sync', tab: 0, configSection: 'options', optionPanel: 'securityExpanded' },
    // ── Config Sections ───────────────────────────────────────────────────────
    { label: 'Systems', keywords: 'systems radio system p25 dmr configure', breadcrumb: 'Config → Systems', icon: 'radio', tab: 0, configSection: 'systems' },
    { label: 'Users', keywords: 'users user manage accounts admin', breadcrumb: 'Config → Users', icon: 'people', tab: 0, configSection: 'users' },
    { label: 'User Groups', keywords: 'user groups roles access permissions', breadcrumb: 'Config → User Groups', icon: 'group', tab: 0, configSection: 'user-groups' },
    { label: 'API Keys', keywords: 'api keys tokens access ingest', breadcrumb: 'Config → API Keys', icon: 'key', tab: 0, configSection: 'apikeys' },
    { label: 'Directory Watch', keywords: 'dirwatch directory watch folder monitor ingest', breadcrumb: 'Config → Dirwatch', icon: 'folder_open', tab: 0, configSection: 'dirwatch' },
    { label: 'Downstreams', keywords: 'downstream forward stream relay', breadcrumb: 'Config → Downstreams', icon: 'call_made', tab: 0, configSection: 'downstreams' },
    { label: 'Groups', keywords: 'groups talkgroup organize category', breadcrumb: 'Config → Groups', icon: 'folder', tab: 0, configSection: 'groups' },
    { label: 'Tags', keywords: 'tags labels talkgroup filter', breadcrumb: 'Config → Tags', icon: 'label', tab: 0, configSection: 'tags' },
    { label: 'Keyword Lists', keywords: 'keyword list alert word filter transcription', breadcrumb: 'Config → Keyword Lists', icon: 'list', tab: 0, configSection: 'keyword-lists' },
    { label: 'User Registration Settings', keywords: 'user registration options config', breadcrumb: 'Config → User Registration', icon: 'how_to_reg', tab: 0, configSection: 'user-registration' },
    // ── Tools ─────────────────────────────────────────────────────────────────
    { label: 'Import Talkgroups', keywords: 'import talkgroups csv json file upload', breadcrumb: 'Tools → Import Talkgroups', icon: 'description', tab: 3, toolSection: 'import-talkgroups' },
    { label: 'Import Units', keywords: 'import units csv json file upload', breadcrumb: 'Tools → Import Units', icon: 'description', tab: 3, toolSection: 'import-units' },
    { label: 'Radio Reference Import', keywords: 'radio reference import download rr', breadcrumb: 'Tools → Radio Reference', icon: 'cloud_download', tab: 3, toolSection: 'radio-reference' },
    { label: 'Admin Password', keywords: 'admin password change reset', breadcrumb: 'Tools → Admin Password', icon: 'password', tab: 3, toolSection: 'admin-password' },
    { label: 'Import / Export Config', keywords: 'import export backup restore config json', breadcrumb: 'Tools → Import/Export Config', icon: 'sync_alt', tab: 3, toolSection: 'import-export-config' },
    { label: 'Config Sync Tool', keywords: 'config sync remote server tool', breadcrumb: 'Tools → Config Sync', icon: 'cloud_sync', tab: 3, toolSection: 'config-sync' },
    { label: 'Stripe Customer Sync', keywords: 'stripe customer sync subscription billing', breadcrumb: 'Tools → Stripe Sync', icon: 'payment', tab: 3, toolSection: 'stripe-sync' },
    { label: 'Purge Data', keywords: 'purge delete data audio calls records', breadcrumb: 'Tools → Purge Data', icon: 'delete_forever', tab: 3, toolSection: 'purge-data' },
];

@Component({
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin',
    styleUrls: ['./admin.component.scss'],
    templateUrl: './admin.component.html',
})
export class RdioScannerAdminComponent implements OnDestroy {
    authenticated = false;

    /** Controls which top-level tab is active (0=Config, 1=Logs, 2=System Health, 3=Tools) */
    selectedTabIndex = 0;

    // ── Search ────────────────────────────────────────────────────────────────
    searchQuery = '';
    searchResults: SearchResult[] = [];
    searchResultsVisible = false;
    searchActiveIndex = -1;

    @ViewChild('searchInput') private searchInputEl: ElementRef<HTMLInputElement> | undefined;
    @ViewChild('logsComponent') private logsComponent: RdioScannerAdminLogsComponent | undefined;
    @ViewChild('configComponent') configComponent: RdioScannerAdminConfigComponent | undefined;
    @ViewChild('toolsComponent') private toolsComponent: RdioScannerAdminToolsComponent | undefined;

    get formDirty(): boolean { return !!(this.configComponent?.form?.dirty); }
    get formValid(): boolean { return !!(this.configComponent?.form?.valid); }
    get configLoading(): boolean { return !!(this.configComponent?.loading); }

    saveConfig(): void { this.configComponent?.save(); }
    resetConfig(): void { this.configComponent?.reset(); }

    onSearch(): void {
        const q = this.searchQuery.trim().toLowerCase();
        if (!q) { this.searchResults = []; return; }
        this.searchResults = SETTINGS_INDEX.filter(item =>
            item.label.toLowerCase().includes(q) ||
            item.keywords.toLowerCase().includes(q) ||
            item.breadcrumb.toLowerCase().includes(q)
        ).slice(0, 10);
        this.searchActiveIndex = -1;
    }

    onSearchFocus(): void {
        this.searchResultsVisible = true;
        if (this.searchQuery) this.onSearch();
    }

    onSearchBlur(): void {
        setTimeout(() => { this.searchResultsVisible = false; }, 150);
    }

    closeSearch(): void {
        this.searchQuery = '';
        this.searchResults = [];
        this.searchResultsVisible = false;
        this.searchInputEl?.nativeElement.blur();
    }

    clearSearch(): void {
        this.searchQuery = '';
        this.searchResults = [];
        this.searchInputEl?.nativeElement.focus();
    }

    navigateToResult(result: SearchResult): void {
        this.closeSearch();
        this.selectedTabIndex = result.tab;

        if (result.tab === 0) {
            // Config tab
            if (result.optionPanel) {
                // Navigate to options section then open the specific panel
                setTimeout(() => this.configComponent?.navigateToOption(result.optionPanel!), 60);
            } else if (result.configSection) {
                setTimeout(() => this.configComponent?.setSection(result.configSection!), 60);
            }
        } else if (result.tab === 3 && result.toolSection) {
            // Tools tab
            setTimeout(() => this.toolsComponent?.setSection(result.toolSection!), 60);
        }
    }

    private eventSubscription;

    constructor(
        private adminService: RdioScannerAdminService,
        private titleService: Title,
    ) {
        // Initialize authenticated state from admin service
        // (cm_token auto-login is handled by the page-level component before this runs)
        this.authenticated = this.adminService.authenticated;

        // Set initial title if already authenticated
        if (this.authenticated) {
            this.updateTitle();
        }

        this.eventSubscription = this.adminService.event.subscribe(async (event: AdminEvent) => {
            if ('authenticated' in event) {
                this.authenticated = event.authenticated || false;

                if (this.authenticated) {
                    this.updateTitle();
                }
            }

            if ('config' in event && event.config) {
                const branding = event.config.branding?.trim() || 'TLR';
                this.titleService.setTitle(`Admin-${branding}`);
            }
        });
    }

    /** Called when the top-level tab changes — auto-reloads Logs when selected. */
    onTabChange(event: MatTabChangeEvent): void {
        if (event.index === 1 && this.logsComponent) {
            this.logsComponent.reload();
        }
    }

    private async updateTitle(): Promise<void> {
        try {
            const config = await this.adminService.getConfig();
            const branding = config.branding?.trim() || 'TLR';
            this.titleService.setTitle(`Admin-${branding}`);
        } catch {
            this.titleService.setTitle('Admin-TLR');
        }
    }

    ngOnDestroy(): void {
        this.eventSubscription.unsubscribe();
    }

    async logout(): Promise<void> {
        await this.adminService.logout();
    }
}
