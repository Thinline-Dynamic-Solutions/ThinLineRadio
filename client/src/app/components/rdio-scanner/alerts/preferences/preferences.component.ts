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

import { ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { RdioScannerAlertPreference, RdioScannerConfig, RdioScannerKeywordList, RdioScannerService, RdioScannerSystem, RdioScannerTalkgroup, RdioScannerToneSet } from '../../rdio-scanner';
import { AlertsService } from '../alerts.service';
import { Subscription } from 'rxjs';
import { TagColorService } from '../../tag-color.service';

type StoredPreference = RdioScannerAlertPreference & {
    systemRef?: number;
    talkgroupRef?: number;
    toneSetIds?: string[];
};

@Component({
    selector: 'rdio-scanner-alert-preferences',
    styleUrls: [
        '../../common.scss',
        './preferences.component.scss',
    ],
    templateUrl: './preferences.component.html',
})
export class RdioScannerAlertPreferencesComponent implements OnDestroy, OnInit {
    preferences: Map<string, StoredPreference> = new Map();
    keywordLists: RdioScannerKeywordList[] = [];
    systems: RdioScannerSystem[] = [];
    loading = false;
    saving = false;
    searchQuery = '';
    isSearchFocused = false;
    detailSystemId: number | null = null;
    expandedTags: Map<string, boolean> = new Map();
    
    private pin?: string;
    private config?: RdioScannerConfig;
    private eventSubscription?: Subscription;
    private preferencesLoaded = false;
    private pendingPreferenceLoad = false;
    private keywordListsLoaded = false;
    private systemIndexById: Map<number, RdioScannerSystem> = new Map();
    private systemIndexByRef: Map<number, RdioScannerSystem> = new Map();

    constructor(
        private rdioScannerService: RdioScannerService,
        private alertsService: AlertsService,
        private tagColorService: TagColorService,
        private cdRef: ChangeDetectorRef,
    ) {
        this.config = this.rdioScannerService.getConfig();
        if (this.config?.systems?.length) {
            this.systems = this.config.systems || [];
            this.rebuildSystemIndexes();
        }
        // Get PIN from localStorage using the service method
        this.pin = this.rdioScannerService.readPin();
    }

    ngOnInit(): void {
        this.pin = this.rdioScannerService.readPin();

        // Subscribe to tag color updates
        this.tagColorService.getTagColors().subscribe();

        if (this.config?.systems?.length) {
            this.systems = this.config.systems || [];
            this.rebuildSystemIndexes();
            this.syncDetailSystemSelection();
        }

        if (this.pin && this.systems.length > 0) {
            this.loadPreferences(true);
            this.loadKeywordLists();
        } else {
            this.pendingPreferenceLoad = true;
            if (this.pin) {
                this.loadKeywordLists();
            }
        }

        // Listen for real-time config/auth updates to refresh data
        this.eventSubscription = this.rdioScannerService.event.subscribe((event: any) => {
            if (event.config) {
                this.config = event.config;
                this.systems = this.config?.systems || [];
                this.rebuildSystemIndexes();
                this.syncDetailSystemSelection();
                this.pin = this.rdioScannerService.readPin();
                if (this.pin) {
                    this.loadPreferences(true);
                    if (!this.keywordListsLoaded) {
                        this.loadKeywordLists();
                    }
                } else {
                    this.pendingPreferenceLoad = true;
                }
            } else if (event.auth) {
                this.pin = this.rdioScannerService.readPin();
                if (this.pin && (this.pendingPreferenceLoad || !this.preferencesLoaded)) {
                    this.loadPreferences(true);
                }
                if (this.pin && !this.keywordListsLoaded) {
                    this.loadKeywordLists();
                }
            }
        });
    }

    ngOnDestroy(): void {
        if (this.eventSubscription) {
            this.eventSubscription.unsubscribe();
        }
    }

    loadPreferences(reset: boolean = false): void {
        // Refresh PIN before each request
        this.pin = this.rdioScannerService.readPin();

        if (!this.pin) {
            console.warn('No PIN available for loading preferences');
            this.loading = false;
            this.pendingPreferenceLoad = true;
            return;
        }

        if (!this.systems || this.systems.length === 0) {
            console.warn('Systems not ready for loading preferences');
            this.loading = false;
            this.pendingPreferenceLoad = true;
            return;
        }

        if (reset) {
            this.preferences = new Map();
            this.preferencesLoaded = false;
        }

        this.pendingPreferenceLoad = false;
        this.loading = true;

        this.alertsService.getPreferences(this.pin).subscribe({
            next: (preferences) => {
                if (reset) {
                    this.preferences = new Map();
                }
                (preferences || []).forEach(pref => this.storePreference(pref));
                this.preferencesLoaded = true;
                this.loading = false;
                this.syncDetailSystemSelection();
            },
            error: (error) => {
                console.error('Error loading preferences:', error);
                this.loading = false;
            },
        });
    }

    loadKeywordLists(): void {
        // Refresh PIN before each request
        this.pin = this.rdioScannerService.readPin();
        
        if (!this.pin) {
            console.warn('No PIN available for loading keyword lists');
            return;
        }
        
        this.alertsService.getKeywordLists(this.pin).subscribe({
            next: (lists) => {
                this.keywordLists = lists || [];
                this.keywordListsLoaded = true;
            },
            error: (error) => {
                console.error('Error loading keyword lists:', error);
            },
        });
    }

    getFilteredTalkgroups(system: RdioScannerSystem): RdioScannerTalkgroup[] {
        const alertable = (system.talkgroups || []).filter(tg => tg.alertsEnabled !== false);
        if (!this.searchQuery.trim()) return alertable;
        const q = this.searchQuery.toLowerCase();
        return alertable.filter(tg => {
            const label = (tg.label || '').toLowerCase();
            const name = (tg.name || '').toLowerCase();
            const id = tg.id.toString();
            return label.includes(q) || name.includes(q) || id.includes(q);
        });
    }

    getSystemsForSidebar(): RdioScannerSystem[] {
        if (!this.systems?.length) return [];
        // Exclude systems where admin has disabled alerts
        const alertable = this.systems.filter(s => s.alertsEnabled !== false);
        const q = this.searchQuery.trim().toLowerCase();
        if (!q) return alertable;
        return alertable.filter(system => {
            if ((system.label || '').toLowerCase().includes(q)) return true;
            return (system.talkgroups || []).filter(tg => tg.alertsEnabled !== false).some(tg => {
                const label = (tg.label || '').toLowerCase();
                const name = (tg.name || '').toLowerCase();
                const id = tg.id.toString();
                return label.includes(q) || name.includes(q) || id.includes(q);
            });
        });
    }

    selectDetailSystem(systemId: number): void {
        this.detailSystemId = systemId;
        this.cdRef.markForCheck();
    }

    getDetailSystem(): RdioScannerSystem | undefined {
        if (this.detailSystemId == null) return undefined;
        return this.systems.find(s => s.id === this.detailSystemId);
    }

    onSearchChange(): void {
        this.syncDetailSystemSelection();
        this.cdRef.markForCheck();
    }

    clearSearch(): void {
        this.searchQuery = '';
        this.syncDetailSystemSelection();
        this.cdRef.markForCheck();
    }

    private syncDetailSystemSelection(): void {
        const list = this.getSystemsForSidebar();
        if (list.length === 0) {
            this.detailSystemId = null;
            return;
        }
        if (this.detailSystemId == null || !list.some(s => s.id === this.detailSystemId)) {
            this.detailSystemId = list[0].id;
        }
    }

    groupTalkgroupsByTag(system: RdioScannerSystem): Array<{tag: string, talkgroups: RdioScannerTalkgroup[]}> {
        const groups: Map<string, RdioScannerTalkgroup[]> = new Map();

        this.getFilteredTalkgroups(system).forEach(tg => {
            const tag = tg.tag || 'Untagged';
            if (!groups.has(tag)) {
                groups.set(tag, []);
            }
            groups.get(tag)!.push(tg);
        });

        const sorted = Array.from(groups.entries()).sort((a, b) => {
            if (a[0] === 'Untagged') return 1;
            if (b[0] === 'Untagged') return -1;
            return a[0].localeCompare(b[0]);
        });

        return sorted.map(([tag, talkgroups]) => ({ tag, talkgroups }));
    }

    isTagExpanded(systemId: number, tag: string): boolean {
        const key = `${systemId}-${tag}`;
        return this.expandedTags.get(key) || false;
    }

    /** Ignore clicks on alert toggle / enable buttons so expand/collapse stays predictable. */
    onTagHeaderClick(event: Event, systemId: number, tag: string): void {
        const t = event.target as HTMLElement | null;
        if (t?.closest?.('.tag-toggle') || t?.closest?.('.tag-actions')) {
            return;
        }
        this.toggleTag(systemId, tag, event);
    }

    toggleTag(systemId: number, tag: string, event?: Event): void {
        event?.stopPropagation();
        const key = `${systemId}-${tag}`;
        const current = this.expandedTags.get(key) || false;
        this.expandedTags = new Map(this.expandedTags).set(key, !current);
        this.cdRef.markForCheck();
    }

    trackTagGroup(_index: number, item: { tag: string }): string {
        return item.tag;
    }

    trackTalkgroup(_index: number, tg: RdioScannerTalkgroup): number {
        return tg.id;
    }

    getPreference(systemId: number, talkgroupId: number): RdioScannerAlertPreference {
        const key = this.buildPreferenceKey(systemId, talkgroupId);
        const existing = this.preferences.get(key) || this.getPreferenceFromAlternateKeys(systemId, talkgroupId);
        if (existing) {
            return existing;
        }

        const pref: StoredPreference = {
            userId: 0,
            systemId,
            talkgroupId,
            alertEnabled: false,
            toneAlerts: true,
            keywordAlerts: true,
            keywords: [],
            keywordListIds: [],
            toneSetIds: [],
        };

        this.storePreference(pref);
        return this.preferences.get(key) || pref;
    }

    savePreferences(): void {
        // Refresh PIN before saving
        this.pin = this.rdioScannerService.readPin();
        
        if (!this.pin) {
            alert('No PIN available. Please log in first.');
            return;
        }
        
        this.saving = true;
        // FIXED: Send systemRef and talkgroupRef (not systemId/talkgroupId)
        // The config uses system.id = systemRef, so pref.systemId actually contains systemRef
        // Send as systemRef/talkgroupRef so backend resolves correctly without collision
        const prefsArray = Array.from(this.preferences.values()).map(pref => ({
            userId: pref.userId,
            systemRef: pref.systemId,        // system.id from config is actually systemRef
            talkgroupRef: pref.talkgroupId,  // talkgroup.id from config is actually talkgroupRef
            alertEnabled: pref.alertEnabled,
            toneAlerts: pref.toneAlerts,
            keywordAlerts: pref.keywordAlerts,
            keywords: pref.keywords,
            keywordListIds: pref.keywordListIds,
            toneSetIds: pref.toneSetIds,
        }));
        
        // DEBUG: Log what we're sending to the API
        console.log('🚀 [TONE SET DEBUG] Saving preferences to API:');
        prefsArray.forEach(pref => {
            if (pref.toneAlerts) {
                console.log(`   System ${pref.systemRef}, Talkgroup ${pref.talkgroupRef}:`, {
                    toneAlerts: pref.toneAlerts,
                    toneSetIds: pref.toneSetIds,
                    isEmpty: !pref.toneSetIds || pref.toneSetIds.length === 0,
                    meaning: (!pref.toneSetIds || pref.toneSetIds.length === 0) ? 'ALL TONE SETS' : 'SPECIFIC TONE SETS'
                });
            }
        });
        
        this.alertsService.updatePreferences(prefsArray, this.pin).subscribe({
            next: () => {
                this.saving = false;
                console.log('✅ [TONE SET DEBUG] Preferences saved successfully');
                alert('Preferences saved successfully');
            },
            error: (error) => {
                console.error('❌ [TONE SET DEBUG] Error saving preferences:', error);
                this.saving = false;
                alert('Error saving preferences');
            },
        });
    }

    addKeyword(pref: RdioScannerAlertPreference, keyword: string): void {
        if (keyword && keyword.trim() && !pref.keywords.includes(keyword.trim())) {
            pref.keywords.push(keyword.trim());
        }
    }

    removeKeyword(pref: RdioScannerAlertPreference, keyword: string): void {
        const index = pref.keywords.indexOf(keyword);
        if (index >= 0) {
            pref.keywords.splice(index, 1);
        }
    }

    toggleKeywordListSelection(pref: RdioScannerAlertPreference, listId: number | string, event: Event): void {
        event.stopPropagation();
        if (!pref.keywordListIds) {
            pref.keywordListIds = [];
        }
        
        const normalizedListId = typeof listId === 'string' ? parseInt(listId, 10) : listId;
        
        // Normalize existing IDs to numbers for comparison
        const normalizedIds = pref.keywordListIds.map(id => typeof id === 'string' ? parseInt(id, 10) : id);
        
        const index = normalizedIds.indexOf(normalizedListId);
        if (index >= 0) {
            // Remove it
            pref.keywordListIds.splice(index, 1);
        } else {
            // Add it
            pref.keywordListIds.push(normalizedListId);
        }
    }

    showKeywordListInfo(list: RdioScannerKeywordList, event: Event): void {
        event.stopPropagation();
        const keywords = list.keywords || [];
        if (keywords.length === 0) {
            alert(`${list.label}\n\nNo keywords in this list.`);
        } else {
            const keywordsText = keywords.join(', ');
            const message = `${list.label}\n\nKeywords (${keywords.length}):\n${keywordsText}`;
            alert(message);
        }
    }

    isKeywordListSelected(pref: RdioScannerAlertPreference, listId: number): boolean {
        if (!Array.isArray(pref.keywordListIds)) {
            return false;
        }
        const normalizedIds = pref.keywordListIds.map(id => typeof id === 'string' ? parseInt(id, 10) : id);
        const normalizedListId = typeof listId === 'string' ? parseInt(listId, 10) : listId;
        return normalizedIds.includes(normalizedListId);
    }

    toggleAlertEnabled(systemId: number, talkgroupId: number, event: Event): void {
        event.stopPropagation();
        const system = this.findSystemByAnyId(systemId);
        if (!system) {
            console.error('System not found:', systemId);
            return;
        }
        
        const pref = this.getPreference(system.id, talkgroupId);
        pref.alertEnabled = !pref.alertEnabled;
        // Only disable tone alerts when main alert is disabled
        // Keyword alerts remain independent
        if (!pref.alertEnabled) {
            pref.toneAlerts = false;
            // Don't disable keywordAlerts - they work independently
        }
        
        // Store the updated preference back in the map to trigger change detection
        const key = this.buildPreferenceKey(system.id, talkgroupId);
        this.preferences.set(key, pref);
    }

    toggleToneAlerts(systemId: number, talkgroupId: number, event: Event): void {
        const system = this.findSystemByAnyId(systemId);
        if (!system) {
            console.error('System not found:', systemId);
            event.preventDefault();
            return;
        }
        
        const talkgroup = this.findTalkgroupInSystem(system, talkgroupId);
        if (!talkgroup) {
            console.error('Talkgroup not found:', talkgroupId, 'in system:', systemId);
            event.preventDefault();
            return;
        }
        
        this.toggleToneAlertsWithTalkgroup(systemId, talkgroupId, talkgroup, event);
    }

    toggleToneAlertsWithTalkgroup(systemId: number, talkgroupId: number, talkgroup: RdioScannerTalkgroup, event: Event): void {
        event.stopPropagation();
        
        // Check if tone detection is enabled - log for debugging
        console.log('toggleToneAlerts - talkgroup:', talkgroup.label, 'toneDetectionEnabled:', talkgroup.toneDetectionEnabled, 'talkgroup object:', talkgroup);
        
        if (!talkgroup.toneDetectionEnabled) {
            event.preventDefault();
            alert('Tone detection must be enabled by an admin for this talkgroup before you can enable tone alerts.\n\nIn the Admin UI, go to Systems → [Your System] → Talkgroups → [This Talkgroup] and enable "Tone Detection" there first.');
            return;
        }
        
        const system = this.findSystemByAnyId(systemId);
        if (!system) {
            console.error('System not found:', systemId);
            event.preventDefault();
            return;
        }
        
        const pref = this.getPreference(system.id, talkgroup.id);
        
        // Prevent default checkbox behavior and manually toggle
        event.preventDefault();
        pref.toneAlerts = !pref.toneAlerts;
        
        // Store the updated preference back in the map to trigger change detection
        const key = this.buildPreferenceKey(system.id, talkgroup.id);
        this.preferences.set(key, pref);
    }

    toggleKeywordAlerts(systemId: number, talkgroupId: number, event: Event): void {
        event.stopPropagation();
        event.preventDefault();
        
        const pref = this.getPreference(systemId, talkgroupId);
        
        // Manually toggle the value
        pref.keywordAlerts = !pref.keywordAlerts;
        
        // Store the updated preference back in the map to trigger change detection
        const key = this.buildPreferenceKey(systemId, talkgroupId);
        this.preferences.set(key, pref);
    }

    getToneSetsForTalkgroup(systemId: number, talkgroupId: number): RdioScannerToneSet[] {
        const system = this.findSystemByAnyId(systemId);
        if (!system) {
            return [];
        }
        const talkgroup = this.findTalkgroupInSystem(system, talkgroupId);
        return talkgroup?.toneSets || [];
    }

    toggleToneSetSelection(pref: RdioScannerAlertPreference, toneSetId: string, event: Event): void {
        event.stopPropagation();
        if (!pref.toneSetIds) {
            pref.toneSetIds = [];
        }
        const index = pref.toneSetIds.indexOf(toneSetId);
        if (index >= 0) {
            pref.toneSetIds.splice(index, 1);
            console.log(`🔧 [TONE SET DEBUG] Removed tone set "${toneSetId}". Current selection:`, pref.toneSetIds);
        } else {
            pref.toneSetIds.push(toneSetId);
            console.log(`🔧 [TONE SET DEBUG] Added tone set "${toneSetId}". Current selection:`, pref.toneSetIds);
        }
    }

    isToneSetSelected(pref: RdioScannerAlertPreference, toneSetId: string): boolean {
        return Array.isArray(pref.toneSetIds) ? pref.toneSetIds.includes(toneSetId) : false;
    }

    getTagColor(tag: string): string {
        return this.tagColorService.getTagColor(tag);
    }

    getTagIcon(tag: string): string {
        // Return appropriate icon based on tag
        const lowerTag = tag.toLowerCase();
        if (lowerTag.includes('fire')) return 'local_fire_department';
        if (lowerTag.includes('law') || lowerTag.includes('police')) return 'security';
        if (lowerTag.includes('ems') || lowerTag.includes('medical')) return 'medical_services';
        if (lowerTag.includes('public works') || lowerTag.includes('works')) return 'build';
        return 'label';
    }

    getEnabledCountInSystem(system: RdioScannerSystem): number {
        return this.getFilteredTalkgroups(system).filter(tg => {
            const pref = this.getPreference(system.id, tg.id);
            return pref.alertEnabled;
        }).length;
    }

    isAllEnabledInSystem(system: RdioScannerSystem): boolean {
        const talkgroups = this.getFilteredTalkgroups(system);
        if (talkgroups.length === 0) return false;
        return talkgroups.every(tg => this.getPreference(system.id, tg.id).alertEnabled);
    }

    isSomeEnabledInSystem(system: RdioScannerSystem): boolean {
        const talkgroups = this.getFilteredTalkgroups(system);
        if (talkgroups.length === 0) return false;
        const enabled = talkgroups.filter(tg => this.getPreference(system.id, tg.id).alertEnabled).length;
        return enabled > 0 && enabled < talkgroups.length;
    }

    toggleSystemAlerts(system: RdioScannerSystem, event: Event): void {
        event.stopPropagation();
        const allEnabled = this.isAllEnabledInSystem(system);
        this.getFilteredTalkgroups(system).forEach(tg => {
            const pref = this.getPreference(system.id, tg.id);
            pref.alertEnabled = !allEnabled;
            if (!pref.alertEnabled) {
                pref.toneAlerts = false;
                // Keyword alerts remain independent - don't disable them
            }
            // Store the updated preference back in the map to trigger change detection
            const key = this.buildPreferenceKey(system.id, tg.id);
            this.preferences.set(key, pref);
        });
    }

    isAllEnabledInTag(systemId: number, tag: string): boolean {
        const system = this.systems.find(s => s.id === systemId);
        if (!system) return false;
        const talkgroups = this.getFilteredTalkgroups(system).filter(tg => (tg.tag || 'Untagged') === tag);
        if (talkgroups.length === 0) return false;
        return talkgroups.every(tg => this.getPreference(systemId, tg.id).alertEnabled);
    }

    isSomeEnabledInTag(systemId: number, tag: string): boolean {
        const system = this.systems.find(s => s.id === systemId);
        if (!system) return false;
        const talkgroups = this.getFilteredTalkgroups(system).filter(tg => (tg.tag || 'Untagged') === tag);
        if (talkgroups.length === 0) return false;
        const enabled = talkgroups.filter(tg => this.getPreference(systemId, tg.id).alertEnabled).length;
        return enabled > 0 && enabled < talkgroups.length;
    }

    toggleTagAlerts(systemId: number, tag: string, talkgroups: RdioScannerTalkgroup[], event: Event): void {
        event.stopPropagation();
        const allEnabled = this.isAllEnabledInTag(systemId, tag);
        talkgroups.forEach(tg => {
            const pref = this.getPreference(systemId, tg.id);
            pref.alertEnabled = !allEnabled;
            if (!pref.alertEnabled) {
                pref.toneAlerts = false;
                // Keyword alerts remain independent - don't disable them
            }
            // Store the updated preference back in the map to trigger change detection
            const key = this.buildPreferenceKey(systemId, tg.id);
            this.preferences.set(key, pref);
        });
    }

    isKeywordListSelectedForTag(systemId: number, tag: string, listId: number): boolean {
        const system = this.systems.find(s => s.id === systemId);
        if (!system) return false;
        const talkgroups = this.getFilteredTalkgroups(system).filter(tg => (tg.tag || 'Untagged') === tag);
        if (talkgroups.length === 0) return false;
        
        // Check if ALL talkgroups in this tag have this keyword list selected
        return talkgroups.every(tg => {
            const pref = this.getPreference(systemId, tg.id);
            return this.isKeywordListSelected(pref, listId);
        });
    }

    toggleKeywordListForTag(systemId: number, tag: string, talkgroups: RdioScannerTalkgroup[], listId: number, event: Event): void {
        event.stopPropagation();
        const allSelected = this.isKeywordListSelectedForTag(systemId, tag, listId);
        
        talkgroups.forEach(tg => {
            const pref = this.getPreference(systemId, tg.id);
            if (!pref.keywordListIds) {
                pref.keywordListIds = [];
            }
            
            // Normalize IDs to numbers
            const normalizedIds = pref.keywordListIds.map(id => typeof id === 'string' ? parseInt(id, 10) : id);
            const normalizedListId = typeof listId === 'string' ? parseInt(listId, 10) : listId;
            const index = normalizedIds.indexOf(normalizedListId);
            
            if (allSelected) {
                // Remove from all
                if (index >= 0) {
                    pref.keywordListIds.splice(index, 1);
                }
            } else {
                // Add to all and enable keyword alerts
                if (index === -1) {
                    pref.keywordListIds.push(normalizedListId);
                }
                // Enable keyword alerts when adding a list
                pref.keywordAlerts = true;
            }
            
            // Trigger change detection
            pref.keywordListIds = [...pref.keywordListIds];
        });
    }

    setTagAlertsStatus(systemId: number, tag: string, talkgroups: RdioScannerTalkgroup[], status: boolean, event: Event): void {
        event.stopPropagation();
        talkgroups.forEach(tg => {
            const pref = this.getPreference(systemId, tg.id);
            pref.alertEnabled = status;
            if (!status) {
                pref.toneAlerts = false;
                // Keyword alerts remain independent - don't disable them
            }
            // Store the updated preference back in the map to trigger change detection
            const key = this.buildPreferenceKey(systemId, tg.id);
            this.preferences.set(key, pref);
        });
    }

    private buildPreferenceKey(systemKey: number, talkgroupKey: number): string {
        return `${systemKey}-${talkgroupKey}`;
    }

    private storePreference(pref: RdioScannerAlertPreference): void {
        const normalized = this.normalizePreference(pref);
        const systemKeys = this.getSystemKeys(normalized);
        const talkgroupKeys = this.getTalkgroupKeys(normalized);

        systemKeys.forEach(systemKey => {
            talkgroupKeys.forEach(talkgroupKey => {
                const key = this.buildPreferenceKey(systemKey, talkgroupKey);
                // Create a new object reference to trigger change detection
                const newPref: StoredPreference = {
                    ...normalized,
                    keywordListIds: normalized.keywordListIds ? [...normalized.keywordListIds] : [],
                    toneSetIds: normalized.toneSetIds ? [...normalized.toneSetIds] : [],
                    keywords: normalized.keywords ? [...normalized.keywords] : [],
                };
                this.preferences.set(key, newPref);
            });
        });
    }

    private normalizePreference(pref: RdioScannerAlertPreference): StoredPreference {
        const normalized: StoredPreference = {
            ...pref,
            keywords: pref.keywords || [],
            keywordListIds: pref.keywordListIds || [],
            toneSetIds: Array.isArray(pref.toneSetIds) ? [...pref.toneSetIds] : [],
        };

        // If preference already has systemRef/talkgroupRef (from server), use those
        // Otherwise, try to look them up from the config
        if (pref.systemRef !== undefined && pref.talkgroupRef !== undefined) {
            // Preference from server - keep the ref values as the primary keys
            // system.id and talkgroup.id in config are actually systemRef and talkgroupRef
            normalized.systemId = pref.systemRef;
            normalized.talkgroupId = pref.talkgroupRef;
            normalized.systemRef = pref.systemRef;
            normalized.talkgroupRef = pref.talkgroupRef;
        } else {
            // New preference being created - use config values
            const system = this.findSystemByAnyId(pref.systemId) || this.findSystemByAnyId(pref.systemRef);
            if (system) {
                normalized.systemId = system.id;
                normalized.systemRef = system.systemRef ?? pref.systemRef;
                const talkgroup = this.findTalkgroupInSystem(system, pref.talkgroupId, pref.talkgroupRef);
                if (talkgroup) {
                    normalized.talkgroupId = talkgroup.id;
                    normalized.talkgroupRef = talkgroup.talkgroupRef ?? pref.talkgroupRef;
                }
            }
        }

        return normalized;
    }

    private getSystemKeys(pref: StoredPreference): number[] {
        // FIXED: Only use systemId to avoid collision where systemRef matches another system's systemId
        // Example: OH Geauga has systemRef=28, OH Statewide MA has systemId=28
        if (typeof pref.systemId === 'number') {
            return [pref.systemId];
        }
        return [];
    }

    private getTalkgroupKeys(pref: StoredPreference): number[] {
        // FIXED: Only use talkgroupId to avoid collision
        if (typeof pref.talkgroupId === 'number') {
            return [pref.talkgroupId];
        }
        return [];
    }

    private rebuildSystemIndexes(): void {
        this.systemIndexById.clear();
        this.systemIndexByRef.clear();
        (this.systems || []).forEach(system => {
            this.systemIndexById.set(system.id, system);
            if (typeof system.systemRef === 'number') {
                this.systemIndexByRef.set(system.systemRef, system);
            }
        });
    }

    private findSystemByAnyId(id?: number): RdioScannerSystem | undefined {
        if (id === undefined || id === null) {
            return undefined;
        }
        return this.systemIndexById.get(id) || this.systemIndexByRef.get(id);
    }

    private findTalkgroupInSystem(system: RdioScannerSystem, identifier?: number, alt?: number): RdioScannerTalkgroup | undefined {
        const talkgroups = system.talkgroups || [];
        return talkgroups.find(tg => {
            return tg.id === identifier ||
                tg.id === alt ||
                tg.talkgroupRef === identifier ||
                tg.talkgroupRef === alt;
        });
    }

    private getPreferenceFromAlternateKeys(systemId: number, talkgroupId: number): StoredPreference | undefined {
        const system = this.findSystemByAnyId(systemId);
        if (!system) {
            return undefined;
        }

        const candidateKeys = new Set<string>();
        candidateKeys.add(this.buildPreferenceKey(system.id, talkgroupId));
        if (typeof system.systemRef === 'number') {
            candidateKeys.add(this.buildPreferenceKey(system.systemRef, talkgroupId));
        }

        const talkgroup = this.findTalkgroupInSystem(system, talkgroupId);
        if (talkgroup?.talkgroupRef !== undefined) {
            candidateKeys.add(this.buildPreferenceKey(system.id, talkgroup.talkgroupRef));
            candidateKeys.add(this.buildPreferenceKey(systemId, talkgroup.talkgroupRef));
            if (typeof system.systemRef === 'number') {
                candidateKeys.add(this.buildPreferenceKey(system.systemRef, talkgroup.talkgroupRef));
            }
        }

        for (const key of candidateKeys) {
            if (this.preferences.has(key)) {
                return this.preferences.get(key)!;
            }
        }

        return undefined;
    }
}
