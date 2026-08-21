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

import { Component, ElementRef, OnDestroy, ViewChild, ViewEncapsulation, ChangeDetectionStrategy } from '@angular/core';
import { Title } from '@angular/platform-browser';
import { AdminEvent, RdioScannerAdminService } from './admin.service';
import { RdioScannerAdminConfigComponent } from './config/config.component';

export interface SearchResult {
    label: string;
    keywords: string;
    breadcrumb: string;
    icon: string;
    configSection?: string;
    optionPanel?: string;
    toolSection?: string;
}

const SETTINGS_INDEX: SearchResult[] = [
    // ── General ──────────────────────────────────────────────────────────────
    { label: 'Time Format', keywords: 'time clock 12 24 hour format', breadcrumb: 'Options → General', icon: 'schedule', configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Keypad Beeps', keywords: 'keypad beep sound audio', breadcrumb: 'Options → General', icon: 'volume_up', configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Max Clients', keywords: 'max clients connections limit', breadcrumb: 'Options → General', icon: 'people', configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Playback Goes Live', keywords: 'playback live auto play', breadcrumb: 'Options → General', icon: 'live_tv', configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Sort Talkgroups', keywords: 'sort talkgroups order', breadcrumb: 'Options → General', icon: 'sort', configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Show Listeners Count', keywords: 'listeners count display', breadcrumb: 'Options → General', icon: 'people_outline', configSection: 'options', optionPanel: 'generalExpanded' },
    { label: 'Auto Populate', keywords: 'auto populate talkgroup system', breadcrumb: 'Options → General', icon: 'auto_fix_high', configSection: 'options', optionPanel: 'generalExpanded' },
    // ── Branding ─────────────────────────────────────────────────────────────
    { label: 'Branding Label', keywords: 'branding label name title', breadcrumb: 'Options → Branding', icon: 'label', configSection: 'options', optionPanel: 'brandingExpanded' },
    { label: 'Base URL', keywords: 'base url domain address server', breadcrumb: 'Options → Branding', icon: 'link', configSection: 'options', optionPanel: 'brandingExpanded' },
    { label: 'Server Logo', keywords: 'server logo image email logo upload', breadcrumb: 'Options → Branding', icon: 'image', configSection: 'options', optionPanel: 'brandingExpanded' },
    { label: 'Favicon', keywords: 'favicon icon browser tab logo generate', breadcrumb: 'Options → Branding', icon: 'web', configSection: 'options', optionPanel: 'brandingExpanded' },
    // ── Transcription ─────────────────────────────────────────────────────────
    { label: 'Transcription', keywords: 'transcription enable provider whisper deepgram openai', breadcrumb: 'Options → Transcription', icon: 'transcribe', configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Transcription Provider', keywords: 'whisper deepgram openai gemini flash lite cloudflare workers ai provider api', breadcrumb: 'Options → Transcription', icon: 'smart_toy', configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Gemini Flash-Lite', keywords: 'gemini flash lite transcription google ai studio', breadcrumb: 'Options → Transcription', icon: 'auto_awesome', configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Cloudflare Workers AI', keywords: 'cloudflare workers ai whisper account id api token transcription', breadcrumb: 'Options → Transcription', icon: 'cloud', configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Transcription Language', keywords: 'language locale transcription', breadcrumb: 'Options → Transcription', icon: 'language', configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Worker Pool Size', keywords: 'worker pool threads concurrent transcription', breadcrumb: 'Options → Transcription', icon: 'memory', configSection: 'options', optionPanel: 'transcriptionExpanded' },
    { label: 'Hallucination Detection', keywords: 'hallucination detect filter transcription', breadcrumb: 'Options → Transcription', icon: 'psychology', configSection: 'options', optionPanel: 'transcriptionExpanded' },
    // ── Alerts ────────────────────────────────────────────────────────────────
    { label: 'System Health Alerts', keywords: 'system health alerts enable monitoring', breadcrumb: 'Options → Alert & Health', icon: 'health_and_safety', configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'No Audio Alerts', keywords: 'no audio alert silence threshold minutes', breadcrumb: 'Options → Alert & Health', icon: 'volume_off', configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'Transcription Failure Alerts', keywords: 'transcription failure alert threshold', breadcrumb: 'Options → Alert & Health', icon: 'error_outline', configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'Tone Detection Alerts', keywords: 'tone detection alert monitoring', breadcrumb: 'Options → Alert & Health', icon: 'notifications', configSection: 'options', optionPanel: 'alertsExpanded' },
    { label: 'Alert Retention', keywords: 'alert retention days keep history', breadcrumb: 'Options → Alert & Health', icon: 'history', configSection: 'options', optionPanel: 'alertsExpanded' },
    // ── Email ─────────────────────────────────────────────────────────────────
    { label: 'Email Service', keywords: 'email service enable sendgrid mailgun smtp push notifications', breadcrumb: 'Options → Email', icon: 'email', configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'SendGrid API Key', keywords: 'sendgrid api key email service provider', breadcrumb: 'Options → Email', icon: 'vpn_key', configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'Mailgun API Key', keywords: 'mailgun api key email domain provider', breadcrumb: 'Options → Email', icon: 'vpn_key', configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'SMTP Settings', keywords: 'smtp server host port email tls username password provider', breadcrumb: 'Options → Email', icon: 'dns', configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'From Email / Name', keywords: 'from email sender name address', breadcrumb: 'Options → Email', icon: 'alternate_email', configSection: 'options', optionPanel: 'notificationsExpanded' },
    { label: 'Push Notifications', keywords: 'push notifications relay server api key thinline enable', breadcrumb: 'Options → Email', icon: 'notifications_active', configSection: 'options', optionPanel: 'notificationsExpanded' },
    // ── User Registration ─────────────────────────────────────────────────────
    { label: 'User Registration', keywords: 'user registration enable public invite invite-only signup', breadcrumb: 'Options → User Registration', icon: 'how_to_reg', configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    { label: 'Email Verification', keywords: 'email verification required signup verify', breadcrumb: 'Options → User Registration', icon: 'mark_email_read', configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    { label: 'Cloudflare Turnstile', keywords: 'turnstile cloudflare captcha site key secret', breadcrumb: 'Options → User Registration', icon: 'security', configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    { label: 'Invitation Codes', keywords: 'invitation invite code access registration', breadcrumb: 'Options → User Registration', icon: 'card_giftcard', configSection: 'options', optionPanel: 'userRegistrationExpanded' },
    { label: 'Leave Central Management', keywords: 'central management leave unlink removal code disconnect hub', breadcrumb: 'Operations → Central Management', icon: 'link_off', configSection: 'central-management' },
    // ── Stripe ────────────────────────────────────────────────────────────────
    { label: 'Stripe Paywall', keywords: 'stripe paywall enable payments billing', breadcrumb: 'Options → Stripe', icon: 'payment', configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Publishable Key', keywords: 'stripe publishable key pk live test', breadcrumb: 'Options → Stripe', icon: 'vpn_key', configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Secret Key', keywords: 'stripe secret key sk live test', breadcrumb: 'Options → Stripe', icon: 'lock', configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Webhook Secret', keywords: 'stripe webhook secret whsec', breadcrumb: 'Options → Stripe', icon: 'webhook', configSection: 'options', optionPanel: 'stripeExpanded' },
    { label: 'Stripe Grace Period', keywords: 'stripe grace period days subscription lapse', breadcrumb: 'Options → Stripe', icon: 'timer', configSection: 'options', optionPanel: 'stripeExpanded' },
    // ── Thinline Radio Services ──────────────────────────────────────────────
    { label: 'Relay Server', keywords: 'relay server thinline push notifications audio encryption connect geocoding', breadcrumb: 'Options → Thinline Radio Services', icon: 'cell_tower', configSection: 'options', optionPanel: 'thinlineServicesExpanded' },
    { label: 'Relay Server API Key', keywords: 'relay server api key push notifications audio encryption request thinline tlr geocoding', breadcrumb: 'Options → Thinline Radio Services', icon: 'vpn_key', configSection: 'options', optionPanel: 'thinlineServicesExpanded' },
    { label: 'Relay Account', keywords: 'relay account create sign in username password thinline', breadcrumb: 'Options → Thinline Radio Services', icon: 'person_add', configSection: 'options', optionPanel: 'thinlineServicesExpanded' },
    { label: 'Relay Add-On Plans', keywords: 'relay billing plans geocoding subscribe thinline add-on', breadcrumb: 'Options → Thinline Radio Services', icon: 'credit_card', configSection: 'options', optionPanel: 'thinlineServicesExpanded' },
    // ── Integrations ─────────────────────────────────────────────────────────
    { label: 'OpenAI', keywords: 'openai api key chat model integration mapping', breadcrumb: 'Options → Integrations', icon: 'smart_toy', configSection: 'options', optionPanel: 'integrationsExpanded' },
    { label: 'Gemini API Key', keywords: 'gemini google ai studio api key integration mapping suggest', breadcrumb: 'Options → Integrations', icon: 'auto_awesome', configSection: 'options', optionPanel: 'integrationsExpanded' },
    { label: 'Radio Reference', keywords: 'radio reference rr login username password premium account', breadcrumb: 'Options → Integrations', icon: 'cloud_download', configSection: 'options', optionPanel: 'integrationsExpanded' },
    { label: 'Config Sync', keywords: 'config sync filesystem backup gitops path', breadcrumb: 'Options → General → Config sync', icon: 'cloud_sync', configSection: 'options', optionPanel: 'general:sync' },
    // ── Audio Settings ────────────────────────────────────────────────────────
    { label: 'Audio Conversion', keywords: 'audio conversion enable convert format', breadcrumb: 'Options → Audio Settings', icon: 'graphic_eq', configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Duplicate Detection', keywords: 'duplicate detection call time window', breadcrumb: 'Options → Audio Settings', icon: 'content_copy', configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Audio Encryption', keywords: 'audio encryption key aes', breadcrumb: 'Options → Audio Settings', icon: 'enhanced_encryption', configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Rate Limiting', keywords: 'rate limit download restrict', breadcrumb: 'Options → Audio Settings', icon: 'speed', configSection: 'options', optionPanel: 'securityExpanded' },
    { label: 'Reconnection Manager', keywords: 'reconnection manager grace period buffer', breadcrumb: 'Options → Audio Settings', icon: 'sync', configSection: 'options', optionPanel: 'securityExpanded' },
    // ── Config Sections ───────────────────────────────────────────────────────
    { label: 'Systems', keywords: 'systems radio system p25 dmr configure', breadcrumb: 'Systems', icon: 'radio', configSection: 'systems' },
    { label: 'Users', keywords: 'users user manage accounts admin', breadcrumb: 'Users', icon: 'people', configSection: 'users' },
    { label: 'User Groups', keywords: 'user groups roles access permissions', breadcrumb: 'User Groups', icon: 'group', configSection: 'user-groups' },
    { label: 'API Keys', keywords: 'api keys tokens access ingest', breadcrumb: 'API Keys', icon: 'key', configSection: 'apikeys' },
    { label: 'Directory Watch', keywords: 'dirwatch directory watch folder monitor ingest', breadcrumb: 'Dirwatch', icon: 'folder_open', configSection: 'dirwatch' },
    { label: 'Downstreams', keywords: 'downstream forward stream relay', breadcrumb: 'Downstreams', icon: 'call_made', configSection: 'downstreams' },
    { label: 'Groups', keywords: 'groups talkgroup organize category', breadcrumb: 'Groups & Tags', icon: 'folder', configSection: 'groups' },
    { label: 'Tags', keywords: 'tags labels talkgroup filter', breadcrumb: 'Groups & Tags', icon: 'label', configSection: 'tags' },
    { label: 'Keyword Lists', keywords: 'keyword list alert word filter transcription', breadcrumb: 'Options → Keyword Lists', icon: 'list', configSection: 'keyword-lists' },
    // ── Operations ────────────────────────────────────────────────────────────
    { label: 'Logs', keywords: 'logs errors warnings info system log', breadcrumb: 'Logs', icon: 'article', configSection: 'logs' },
    { label: 'System Health', keywords: 'system health status disk cpu memory alerts', breadcrumb: 'System Health', icon: 'health_and_safety', configSection: 'system-health' },
    { label: 'Import Talkgroups', keywords: 'import talkgroups csv json file upload', breadcrumb: 'Tools → Import Talkgroups', icon: 'description', configSection: 'tools', toolSection: 'import-talkgroups' },
    { label: 'Import Units', keywords: 'import units csv json file upload', breadcrumb: 'Tools → Import Units', icon: 'description', configSection: 'tools', toolSection: 'import-units' },
    { label: 'Radio Reference Import', keywords: 'radio reference import download rr', breadcrumb: 'Tools → Radio Reference', icon: 'cloud_download', configSection: 'tools', toolSection: 'radio-reference' },
    { label: 'Admin Password', keywords: 'admin password change reset', breadcrumb: 'Tools → Admin Password', icon: 'password', configSection: 'tools', toolSection: 'admin-password' },
    { label: 'Import / Export Config', keywords: 'import export backup restore config json', breadcrumb: 'Tools → Import/Export Config', icon: 'sync_alt', configSection: 'tools', toolSection: 'import-export-config' },
    { label: 'Stripe Customer Sync', keywords: 'stripe customer sync subscription billing', breadcrumb: 'Tools → Stripe Sync', icon: 'payment', configSection: 'tools', toolSection: 'stripe-sync' },
    { label: 'Purge Data', keywords: 'purge delete data audio calls records', breadcrumb: 'Tools → Purge Data', icon: 'delete_forever', configSection: 'tools', toolSection: 'purge-data' },
    { label: 'Admin Assistant', keywords: 'assistant copilot ai help diagnose chat openai', breadcrumb: 'Assistant', icon: 'smart_toy', configSection: 'assistant' },
    { label: 'Central Management', keywords: 'central management leave unlink removal code disconnect hub alertpage', breadcrumb: 'Operations → Central Management', icon: 'hub', configSection: 'central-management' },
];

@Component({
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin',
    styleUrls: ['./admin.component.scss'],
    templateUrl: './admin.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAdminComponent implements OnDestroy {
    authenticated = false;

    // ── Search ────────────────────────────────────────────────────────────────
    searchQuery = '';
    searchResults: SearchResult[] = [];
    searchResultsVisible = false;
    searchActiveIndex = -1;

    @ViewChild('searchInput') private searchInputEl: ElementRef<HTMLInputElement> | undefined;
    @ViewChild('configComponent') configComponent: RdioScannerAdminConfigComponent | undefined;

    get formDirty(): boolean { return !!(this.configComponent?.form?.dirty); }
    get formValid(): boolean { return !!(this.configComponent?.form?.valid); }
    get configLoading(): boolean { return !!(this.configComponent?.loading); }

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

        if (result.toolSection) {
            setTimeout(() => this.configComponent?.selectTool(result.toolSection!), 60);
        } else if (result.optionPanel) {
            setTimeout(() => this.configComponent?.navigateToOption(result.optionPanel!), 60);
        } else if (result.configSection) {
            setTimeout(() => this.configComponent?.setSection(result.configSection!), 60);
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
