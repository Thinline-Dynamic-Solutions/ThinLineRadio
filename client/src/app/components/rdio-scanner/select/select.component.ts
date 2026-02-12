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

import { ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';
import {
    RdioScannerAvoidOptions,
    RdioScannerBeepStyle,
    RdioScannerCategory,
    RdioScannerCategoryStatus,
    RdioScannerEvent,
    RdioScannerLivefeedMap,
    RdioScannerSystem,
    RdioScannerTalkgroup,
} from '../rdio-scanner';
import { RdioScannerService } from '../rdio-scanner.service';
import { TagColorService } from '../tag-color.service';
import { FavoritesService, FavoriteItem } from '../favorites.service';
import { SystemsVisibilityDialogComponent } from './systems-visibility-dialog.component';

@Component({
    selector: 'rdio-scanner-select',
    styleUrls: [
        '../common.scss',
        './select.component.scss',
    ],
    templateUrl: './select.component.html',
})
export class RdioScannerSelectComponent implements OnDestroy, OnInit {
    categories: RdioScannerCategory[] | undefined;

    map: RdioScannerLivefeedMap = {};

    systems: RdioScannerSystem[] | undefined;

    // New properties for Flutter-style layout
    searchQuery = '';
    isSearchFocused = false;
    expandedSystems: Map<number, boolean> = new Map();
    expandedTags: Map<string, boolean> = new Map();
    favoriteExpandedSystems: Map<number, boolean> = new Map();
    favoriteExpandedTags: Map<string, boolean> = new Map();
    hiddenSystems: Set<number> = new Set();
    private eventSubscription?: Subscription;
    private favoritesSubscription?: Subscription;
    favoriteItems: FavoriteItem[] = [];

    private static readonly LOCAL_STORAGE_KEY_HIDDEN_SYSTEMS = 'rdio-scanner-hidden-systems';

    constructor(
        private rdioScannerService: RdioScannerService,
        private tagColorService: TagColorService,
        private favoritesService: FavoritesService,
        private cdRef: ChangeDetectorRef,
        private matDialog: MatDialog,
    ) {
        this.eventSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => this.eventHandler(event));

        this.favoritesSubscription = this.favoritesService.getFavorites().subscribe(() => {
            this.favoriteItems = this.favoritesService.getFavoriteItems();
            this.cdRef.markForCheck();
        });
        this.favoriteItems = this.favoritesService.getFavoriteItems();
    }

    ngOnInit(): void {
        // Subscribe to tag color updates
        this.tagColorService.getTagColors().subscribe();
        
        // Load hidden systems from localStorage
        this.loadHiddenSystems();
    }

    avoid(options?: RdioScannerAvoidOptions): void {
        if (options?.all == true) {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Activate);

        } else if (options?.all == false) {
            this.rdioScannerService.beep(RdioScannerBeepStyle.Deactivate);

        } else if (options?.system !== undefined && options?.talkgroup !== undefined) {
            this.rdioScannerService.beep(this.map[options.system.id][options.talkgroup.id].active
                ? RdioScannerBeepStyle.Deactivate
                : RdioScannerBeepStyle.Activate
            );

        } else {
            this.rdioScannerService.beep(options?.status ? RdioScannerBeepStyle.Activate : RdioScannerBeepStyle.Deactivate);
        }

        this.rdioScannerService.avoid(options);
    }

    ngOnDestroy(): void {
        this.eventSubscription?.unsubscribe();
        this.favoritesSubscription?.unsubscribe();
    }

    toggle(category: RdioScannerCategory): void {
        if (category.status == RdioScannerCategoryStatus.On)
            this.rdioScannerService.beep(RdioScannerBeepStyle.Deactivate);
        else
            this.rdioScannerService.beep(RdioScannerBeepStyle.Activate);

        this.rdioScannerService.toggleCategory(category);
    }

    clearSearch(): void {
        this.searchQuery = '';
    }

    getVisibleSystems(): RdioScannerSystem[] {
        if (!this.systems) return [];
        return this.systems.filter(system => !this.hiddenSystems.has(system.id));
    }

    getFilteredTalkgroups(system: RdioScannerSystem): RdioScannerTalkgroup[] {
        if (!this.searchQuery) return system.talkgroups || [];
        
        const query = this.searchQuery.toLowerCase();
        return (system.talkgroups || []).filter(tg => {
            const label = (tg.label || '').toLowerCase();
            const name = (tg.name || '').toLowerCase();
            const id = tg.id.toString();
            return label.includes(query) || name.includes(query) || id.includes(query);
        });
    }

    getFavoriteTalkgroupsCount(system: RdioScannerSystem): number {
        // Return count of favorited talkgroups in this system
        return this.favoriteItems
            .filter(f => f.type === 'talkgroup' && f.systemId === system.id && f.talkgroupId !== undefined)
            .length;
    }

    groupTalkgroupsByTag(system: RdioScannerSystem): Array<{tag: string, talkgroups: RdioScannerTalkgroup[]}> {
        const filtered = this.getFilteredTalkgroups(system);
        const groups: Map<string, RdioScannerTalkgroup[]> = new Map();
        
        filtered.forEach(tg => {
            const tag = tg.tag || 'Untagged';
            if (!groups.has(tag)) {
                groups.set(tag, []);
            }
            groups.get(tag)!.push(tg);
        });

        // Sort tags alphabetically, keeping 'Untagged' last
        const sorted = Array.from(groups.entries()).sort((a, b) => {
            if (a[0] === 'Untagged') return 1;
            if (b[0] === 'Untagged') return -1;
            return a[0].localeCompare(b[0]);
        });

        return sorted.map(([tag, talkgroups]) => ({ tag, talkgroups }));
    }

    isSystemExpanded(systemId: number): boolean {
        return this.expandedSystems.get(systemId) || false;
    }

    toggleSystem(systemId: number, event?: Event): void {

        const current = this.expandedSystems.get(systemId) || false;
        this.expandedSystems.set(systemId, !current);
    }

    isTagExpanded(systemId: number, tag: string): boolean {
        const key = `${systemId}-${tag}`;
        return this.expandedTags.get(key) || false;
    }

    toggleTag(systemId: number, tag: string, event?: Event): void {
        const key = `${systemId}-${tag}`;

        const current = this.expandedTags.get(key) || false;
        this.expandedTags.set(key, !current);
    }

    isFavoriteSystemExpanded(systemId: number): boolean {
        return this.favoriteExpandedSystems.get(systemId) || false;
    }

    toggleFavoriteSystemExpand(systemId: number): void {
        const current = this.favoriteExpandedSystems.get(systemId) || false;
        this.favoriteExpandedSystems.set(systemId, !current);
    }

    isFavoriteTagExpanded(systemId: number, tag: string): boolean {
        const key = `${systemId}-${tag}`;
        return this.favoriteExpandedTags.get(key) || false;
    }

    toggleFavoriteTagExpand(systemId: number, tag: string): void {
        const key = `${systemId}-${tag}`;
        const current = this.favoriteExpandedTags.get(key) || false;
        this.favoriteExpandedTags.set(key, !current);
    }

    isTalkgroupEnabled(systemId: number, talkgroupId: number): boolean {
        return !!(this.map[systemId] && this.map[systemId][talkgroupId] && this.map[systemId][talkgroupId].active);
    }

    toggleTalkgroup(systemId: number, talkgroupId: number): void {
        const system = this.systems?.find(s => s.id === systemId);
        const talkgroup = system?.talkgroups?.find(tg => tg.id === talkgroupId);
        
        if (system && talkgroup) {
            this.avoid({ system, talkgroup });
        }
    }

    toggleSystemTalkgroups(system: RdioScannerSystem, event: Event): void {
        event.stopPropagation();
        const allEnabled = (system.talkgroups || []).every(tg => this.isTalkgroupEnabled(system.id, tg.id));
        this.avoid({ system, status: !allEnabled });
    }

    setSystemTalkgroupsStatus(system: RdioScannerSystem, status: boolean, event: Event): void {
        event.stopPropagation();
        this.applySystemStatus(system, status);
    }

    setFavoriteSystemTalkgroupsStatus(system: RdioScannerSystem, status: boolean, event: Event): void {
        event.stopPropagation();
        // Only enable/disable favorited talkgroups in this system
        const favoriteTalkgroups = this.favoriteItems
            .filter(f => f.type === 'talkgroup' && f.systemId === system.id && f.talkgroupId !== undefined)
            .map(f => f.talkgroupId!);
        
        (system.talkgroups || []).forEach(tg => {
            if (favoriteTalkgroups.includes(tg.id)) {
                this.avoid({ system, talkgroup: tg, status });
            }
        });
    }

    toggleTagTalkgroups(systemId: number, tag: string, talkgroups: RdioScannerTalkgroup[], event: Event): void {
        event.stopPropagation();
        const system = this.systems?.find(s => s.id === systemId);
        if (!system) return;
        
        const allEnabled = talkgroups.every(tg => this.isTalkgroupEnabled(systemId, tg.id));
        
        talkgroups.forEach(tg => {
            this.avoid({ system, talkgroup: tg, status: !allEnabled });
        });
    }

    setTagTalkgroupsStatus(systemId: number, tag: string, talkgroups: RdioScannerTalkgroup[], status: boolean, event: Event): void {
        event.stopPropagation();
        this.applyTagStatus(systemId, tag, talkgroups, status);
    }

    enableAllFavorites(event: Event): void {
        event.stopPropagation();
        this.applyFavoritesStatus(true);
    }

    disableAllFavorites(event: Event): void {
        event.stopPropagation();
        this.applyFavoritesStatus(false);
    }

    isAllEnabled(): boolean {
        if (!this.systems) return false;
        const visibleSystems = this.getVisibleSystems();
        if (visibleSystems.length === 0) return false;
        
        let total = 0;
        let enabled = 0;
        
        visibleSystems.forEach(system => {
            (system.talkgroups || []).forEach(tg => {
                total++;
                if (this.isTalkgroupEnabled(system.id, tg.id)) {
                    enabled++;
                }
            });
        });
        
        return total > 0 && enabled === total;
    }

    isPartiallyEnabled(): boolean {
        if (!this.systems) return false;
        const visibleSystems = this.getVisibleSystems();
        if (visibleSystems.length === 0) return false;
        
        let total = 0;
        let enabled = 0;
        
        visibleSystems.forEach(system => {
            (system.talkgroups || []).forEach(tg => {
                total++;
                if (this.isTalkgroupEnabled(system.id, tg.id)) {
                    enabled++;
                }
            });
        });
        
        return enabled > 0 && enabled < total;
    }

    toggleAllTalkgroups(): void {
        const allEnabled = this.isAllEnabled();
        const visibleSystems = this.getVisibleSystems();
        
        // Only toggle visible systems, not hidden ones
        visibleSystems.forEach(system => {
            this.avoid({ system, status: !allEnabled });
        });
    }

    getEnabledCount(): number {
        if (!this.systems) return 0;
        const visibleSystems = this.getVisibleSystems();
        let count = 0;
        
        visibleSystems.forEach(system => {
            (system.talkgroups || []).forEach(tg => {
                if (this.isTalkgroupEnabled(system.id, tg.id)) {
                    count++;
                }
            });
        });
        
        return count;
    }

    getTotalCount(): number {
        if (!this.systems) return 0;
        const visibleSystems = this.getVisibleSystems();
        let count = 0;
        
        visibleSystems.forEach(system => {
            count += (system.talkgroups || []).length;
        });
        
        return count;
    }

    getEnabledCountInSystem(system: RdioScannerSystem): number {
        const filtered = this.getFilteredTalkgroups(system);
        return filtered.filter(tg => this.isTalkgroupEnabled(system.id, tg.id)).length;
    }

    isAllEnabledInSystem(system: RdioScannerSystem): boolean {
        const filtered = this.getFilteredTalkgroups(system);
        if (filtered.length === 0) return false;
        return filtered.every(tg => this.isTalkgroupEnabled(system.id, tg.id));
    }

    isSomeEnabledInSystem(system: RdioScannerSystem): boolean {
        const filtered = this.getFilteredTalkgroups(system);
        const enabled = filtered.filter(tg => this.isTalkgroupEnabled(system.id, tg.id)).length;
        return enabled > 0 && enabled < filtered.length;
    }

    isAllEnabledInTag(systemId: number, tag: string): boolean {
        const system = this.systems?.find(s => s.id === systemId);
        if (!system) return false;
        const tagGroups = this.groupTalkgroupsByTag(system);
        const tagGroup = tagGroups.find(tg => tg.tag === tag);
        if (!tagGroup || tagGroup.talkgroups.length === 0) return false;
        return tagGroup.talkgroups.every(tg => this.isTalkgroupEnabled(systemId, tg.id));
    }

    isSomeEnabledInTag(systemId: number, tag: string): boolean {
        const system = this.systems?.find(s => s.id === systemId);
        if (!system) return false;
        const tagGroups = this.groupTalkgroupsByTag(system);
        const tagGroup = tagGroups.find(tg => tg.tag === tag);
        if (!tagGroup) return false;
        const enabled = tagGroup.talkgroups.filter(tg => this.isTalkgroupEnabled(systemId, tg.id)).length;
        return enabled > 0 && enabled < tagGroup.talkgroups.length;
    }

    getSystemStatusIcon(system: RdioScannerSystem): string {
        if (this.isAllEnabledInSystem(system)) return 'check_circle';
        if (this.isSomeEnabledInSystem(system)) return 'remove_circle';
        return 'circle_outlined';
    }

    getSystemStatusClass(system: RdioScannerSystem): string {
        if (this.isAllEnabledInSystem(system)) return 'status-enabled';
        if (this.isSomeEnabledInSystem(system)) return 'status-partial';
        return 'status-disabled';
    }

    getTagStatusIcon(systemId: number, tag: string): string {
        if (this.isAllEnabledInTag(systemId, tag)) return 'check_circle';
        if (this.isSomeEnabledInTag(systemId, tag)) return 'remove_circle';
        return 'circle_outlined';
    }

    getTagStatusClass(systemId: number, tag: string): string {
        if (this.isAllEnabledInTag(systemId, tag)) return 'status-enabled';
        if (this.isSomeEnabledInTag(systemId, tag)) return 'status-partial';
        return 'status-disabled';
    }

    getTagIcon(tag: string): string {
        const tagNum = this.parseTag(tag);
        switch (tagNum) {
            case 1: return 'local_fire_department'; // Fire
            case 2: return 'local_police'; // Law
            case 3: return 'build'; // Public Works
            case 4: return 'medical_services'; // EMS
            case 5: return 'radio'; // TAC
            case 6: return 'security'; // Corrections
            default: return 'radio';
        }
    }

    getTagColor(tag: string): string {
        return this.tagColorService.getTagColor(tag);
    }

    private parseTag(tag: string): number | null {
        if (!tag) return null;
        const num = parseInt(tag);
        if (!isNaN(num)) return num;
        const lower = tag.toLowerCase();
        if (lower.includes('fire')) return 1;
        if (lower.includes('law') || lower.includes('police')) return 2;
        if (lower.includes('public works') || lower.includes('works')) return 3;
        if (lower.includes('ems') || lower.includes('medical')) return 4;
        if (lower.includes('tac')) return 5;
        if (lower.includes('jail') || lower.includes('correction')) return 6;
        return null;
    }

    isFavorite(systemId: number, talkgroupId: number): boolean {
        return this.favoritesService.isTalkgroupFavorite(systemId, talkgroupId);
    }

    isSystemFavorite(systemId: number): boolean {
        return this.favoritesService.isSystemFavorite(systemId);
    }

    isTagFavorite(systemId: number, tag: string): boolean {
        return this.favoritesService.isTagFavorite(systemId, tag);
    }

    toggleFavoriteSystem(systemId: number, event: Event): void {
        event.stopPropagation();
        const system = this.systems?.find(s => s.id === systemId);
        if (!system) return;

        if (this.favoritesService.isSystemFavorite(systemId)) {
            // Remove system and ALL its tags and talkgroups
            const itemsToRemove: FavoriteItem[] = [{ type: 'system', systemId }];
            
            // Collect all tags in this system
            const tagSet = new Set<string>();
            (system.talkgroups || []).forEach(tg => {
                const tag = tg.tag || 'Untagged';
                tagSet.add(tag);
                itemsToRemove.push({ type: 'tag', systemId, tag });
            });
            
            // Collect all talkgroups in this system
            (system.talkgroups || []).forEach(tg => {
                itemsToRemove.push({ type: 'talkgroup', systemId, talkgroupId: tg.id });
            });
            
            this.favoritesService.removeFavorites(itemsToRemove);
        } else {
            // Add system and ALL its tags and talkgroups
            const itemsToAdd: FavoriteItem[] = [{ type: 'system', systemId }];
            
            // Collect all tags in this system
            const tagSet = new Set<string>();
            (system.talkgroups || []).forEach(tg => {
                const tag = tg.tag || 'Untagged';
                if (!tagSet.has(tag)) {
                    tagSet.add(tag);
                    itemsToAdd.push({ type: 'tag', systemId, tag });
                }
                itemsToAdd.push({ type: 'talkgroup', systemId, talkgroupId: tg.id });
            });
            
            this.favoritesService.addFavorites(itemsToAdd);
        }
    }

    toggleFavoriteTag(systemId: number, tag: string, event: Event): void {
        event.stopPropagation();
        const system = this.systems?.find(s => s.id === systemId);
        if (!system) return;

        const tagTalkgroups = (system.talkgroups || []).filter(tg => (tg.tag || 'Untagged') === tag);

        if (this.favoritesService.isTagFavorite(systemId, tag)) {
            // Remove tag and ALL its talkgroups
            const itemsToRemove: FavoriteItem[] = [{ type: 'tag', systemId, tag }];
            
            // Collect all talkgroups in this tag
            tagTalkgroups.forEach(tg => {
                itemsToRemove.push({ type: 'talkgroup', systemId, talkgroupId: tg.id });
            });
            
            this.favoritesService.removeFavorites(itemsToRemove);
            
            // Check if system should be removed (no other favorites in system)
            const hasOtherFavorites = this.hasOtherFavoritesInSystem(systemId, tag);
            if (!hasOtherFavorites && this.favoritesService.isSystemFavorite(systemId)) {
                this.favoritesService.removeFavorite({ type: 'system', systemId });
            }
        } else {
            // Add tag and ALL its talkgroups
            const itemsToAdd: FavoriteItem[] = [{ type: 'tag', systemId, tag }];
            
            // Add all talkgroups in this tag
            tagTalkgroups.forEach(tg => {
                itemsToAdd.push({ type: 'talkgroup', systemId, talkgroupId: tg.id });
            });
            
            // Ensure parent system is also favorited
            if (!this.favoritesService.isSystemFavorite(systemId)) {
                itemsToAdd.push({ type: 'system', systemId });
            }
            
            this.favoritesService.addFavorites(itemsToAdd);
        }
    }

    toggleFavoriteTalkgroup(systemId: number, talkgroupId: number, event: Event): void {
        event.stopPropagation();
        const system = this.systems?.find(s => s.id === systemId);
        const talkgroup = system?.talkgroups?.find(tg => tg.id === talkgroupId);
        if (!system || !talkgroup) return;

        const tag = talkgroup.tag || 'Untagged';

        if (this.favoritesService.isTalkgroupFavorite(systemId, talkgroupId)) {
            // Remove only this talkgroup
            this.favoritesService.removeFavorite({ type: 'talkgroup', systemId, talkgroupId });
            
            // Check if tag should be removed (no other talkgroups favorited in this tag)
            const tagTalkgroups = (system.talkgroups || []).filter(tg => (tg.tag || 'Untagged') === tag);
            const hasOtherTagFavorites = tagTalkgroups.some(tg => 
                tg.id !== talkgroupId && this.favoritesService.isTalkgroupFavorite(systemId, tg.id)
            );
            
            if (!hasOtherTagFavorites && this.favoritesService.isTagFavorite(systemId, tag)) {
                this.favoritesService.removeFavorite({ type: 'tag', systemId, tag });
            }
            
            // Check if system should be removed (no other favorites in system)
            const hasOtherFavorites = this.hasOtherFavoritesInSystem(systemId);
            if (!hasOtherFavorites && this.favoritesService.isSystemFavorite(systemId)) {
                this.favoritesService.removeFavorite({ type: 'system', systemId });
            }
        } else {
            // Add talkgroup, tag, and system
            this.favoritesService.addFavorite({ type: 'talkgroup', systemId, talkgroupId });
            
            // Ensure parent tag is also favorited
            if (!this.favoritesService.isTagFavorite(systemId, tag)) {
                this.favoritesService.addFavorite({ type: 'tag', systemId, tag });
            }
            
            // Ensure parent system is also favorited
            if (!this.favoritesService.isSystemFavorite(systemId)) {
                this.favoritesService.addFavorite({ type: 'system', systemId });
            }
        }
    }

    private hasOtherFavoritesInSystem(systemId: number, excludeTag?: string): boolean {
        const system = this.systems?.find(s => s.id === systemId);
        if (!system) return false;

        // Check all tags in the system (excluding the one being removed)
        const allTags = new Set<string>();
        (system.talkgroups || []).forEach(tg => {
            const tag = tg.tag || 'Untagged';
            if (!excludeTag || tag !== excludeTag) {
                allTags.add(tag);
            }
        });

        // Check if any of these tags are favorited
        for (const tag of allTags) {
            if (this.favoritesService.isTagFavorite(systemId, tag)) {
                return true;
            }
        }

        // Check if there are any favorited talkgroups (excluding those in the excluded tag)
        const favoriteTalkgroups = (system.talkgroups || []).filter(tg => {
            const tag = tg.tag || 'Untagged';
            // Skip talkgroups in the excluded tag
            if (excludeTag && tag === excludeTag) {
                return false;
            }
            return this.favoritesService.isTalkgroupFavorite(systemId, tg.id);
        });

        return favoriteTalkgroups.length > 0;
    }

    getFavoriteItems(): FavoriteItem[] {
        return this.favoriteItems;
    }

    getFavoriteSystems(): RdioScannerSystem[] {
        if (!this.systems) return [];
        const favoriteSystems = this.getFavoriteItems()
            .filter(f => f.type === 'system' && f.systemId !== undefined)
            .map(f => f.systemId!);
        return this.systems.filter(s => favoriteSystems.includes(s.id));
    }

    getFavoriteTags(system: RdioScannerSystem): Array<{tag: string, talkgroups: RdioScannerTalkgroup[]}> {
        const favoriteTags = this.getFavoriteItems()
            .filter(f => f.type === 'tag' && f.systemId === system.id && f.tag)
            .map(f => f.tag!);
        
        const groups: Map<string, RdioScannerTalkgroup[]> = new Map();
        (system.talkgroups || []).forEach(tg => {
            const tag = tg.tag || 'Untagged';
            if (favoriteTags.includes(tag)) {
                if (!groups.has(tag)) {
                    groups.set(tag, []);
                }
                groups.get(tag)!.push(tg);
            }
        });

        return Array.from(groups.entries()).map(([tag, talkgroups]) => ({ tag, talkgroups }));
    }

    getFavoriteTalkgroups(system: RdioScannerSystem): RdioScannerTalkgroup[] {
        const favoriteTalkgroups = this.getFavoriteItems()
            .filter(f => f.type === 'talkgroup' && f.systemId === system.id && f.talkgroupId !== undefined)
            .map(f => f.talkgroupId!);
        
        return (system.talkgroups || []).filter(tg => favoriteTalkgroups.includes(tg.id));
    }

    getFavoriteSystemsWithFavorites(): RdioScannerSystem[] {
        if (!this.systems) return [];
        
        // Get systems that have any favorites (system, tag, or talkgroup favorites)
        const favoriteSystems = new Set<number>();
        
        this.favoriteItems.forEach(fav => {
            if (fav.systemId !== undefined) {
                favoriteSystems.add(fav.systemId);
            }
        });
        
        return this.systems.filter(s => favoriteSystems.has(s.id));
    }

    getFavoriteTagGroupsForSystem(system: RdioScannerSystem): Array<{tag: string, talkgroups: RdioScannerTalkgroup[]}> {
        const groups: Map<string, RdioScannerTalkgroup[]> = new Map();
        const isSystemFavorite = this.favoritesService.isSystemFavorite(system.id);
        
        // Get all individually favorited tags
        const favoriteTags = new Set<string>();
        this.favoriteItems
            .filter(f => f.type === 'tag' && f.systemId === system.id && f.tag)
            .forEach(f => favoriteTags.add(f.tag!));
        
        // Get all individually favorited talkgroups
        const favoriteTalkgroups = new Set<number>();
        this.favoriteItems
            .filter(f => f.type === 'talkgroup' && f.systemId === system.id && f.talkgroupId !== undefined)
            .forEach(f => favoriteTalkgroups.add(f.talkgroupId!));

        // Only show all tags/talkgroups if system is explicitly favorited AND all are individually favorited
        if (isSystemFavorite) {
            const allTags = new Set<string>();
            (system.talkgroups || []).forEach(tg => {
                const tag = tg.tag || 'Untagged';
                allTags.add(tag);
            });
            
            // Check if ALL tags are favorited
            const allTagsAreFavorited = Array.from(allTags).every(tag => favoriteTags.has(tag));
            
            // Check if ALL talkgroups are favorited
            const allTalkgroupsAreFavorited = (system.talkgroups || []).every(tg => favoriteTalkgroups.has(tg.id));
            
            // Only show all if everything is individually favorited
            if (allTagsAreFavorited && allTalkgroupsAreFavorited) {
                this.groupTalkgroupsByTag(system).forEach(({ tag, talkgroups }) => {
                    groups.set(tag, [...talkgroups]);
                });
            } else {
                // System is favorited but not all children are - only show favorited ones
                favoriteTags.forEach(tag => {
                    const tagTalkgroups = (system.talkgroups || []).filter(tg => (tg.tag || 'Untagged') === tag);
                    if (tagTalkgroups.length > 0) {
                        groups.set(tag, tagTalkgroups.filter(tg => favoriteTalkgroups.has(tg.id)));
                    }
                });
                
                // Add individually favorited talkgroups that might not have their tag favorited
                (system.talkgroups || []).forEach(tg => {
                    if (favoriteTalkgroups.has(tg.id)) {
                        const tag = tg.tag || 'Untagged';
                        if (!groups.has(tag)) {
                            groups.set(tag, []);
                        }
                        if (!groups.get(tag)!.some(t => t.id === tg.id)) {
                            groups.get(tag)!.push(tg);
                        }
                    }
                });
            }
        } else {
            // System is not favorited - only show individually favorited tags and talkgroups
            favoriteTags.forEach(tag => {
                const tagTalkgroups = (system.talkgroups || []).filter(tg => (tg.tag || 'Untagged') === tag);
                if (tagTalkgroups.length > 0) {
                    groups.set(tag, tagTalkgroups.filter(tg => favoriteTalkgroups.has(tg.id)));
                }
            });
            
            // Add individually favorited talkgroups that might not have their tag favorited
            (system.talkgroups || []).forEach(tg => {
                if (favoriteTalkgroups.has(tg.id)) {
                    const tag = tg.tag || 'Untagged';
                    if (!groups.has(tag)) {
                        groups.set(tag, []);
                    }
                    if (!groups.get(tag)!.some(t => t.id === tg.id)) {
                        groups.get(tag)!.push(tg);
                    }
                }
            });
        }

        const sorted = Array.from(groups.entries()).sort((a, b) => {
            if (a[0] === 'Untagged') return 1;
            if (b[0] === 'Untagged') return -1;
            return a[0].localeCompare(b[0]);
        });

        return sorted.map(([tag, talkgroups]) => ({ tag, talkgroups }));
    }

    showSystemsModal(): void {
        if (!this.systems || this.systems.length === 0) {
            return;
        }

        const dialogRef = this.matDialog.open(SystemsVisibilityDialogComponent, {
            width: '400px',
            data: {
                systems: this.systems.map(s => ({
                    id: s.id,
                    label: s.label,
                    hidden: this.hiddenSystems.has(s.id),
                })),
            },
        });

        dialogRef.afterClosed().subscribe((result?: { systemId: number; hidden: boolean }[]) => {
            if (result) {
                // Update hidden systems based on dialog result
                this.hiddenSystems.clear();
                result.forEach(item => {
                    if (item.hidden) {
                        this.hiddenSystems.add(item.systemId);
                    }
                });
                this.saveHiddenSystems();
                this.cdRef.markForCheck();
            }
        });
    }

    private loadHiddenSystems(): void {
        try {
            const stored = window?.localStorage?.getItem(RdioScannerSelectComponent.LOCAL_STORAGE_KEY_HIDDEN_SYSTEMS);
            if (stored) {
                const hiddenIds: number[] = JSON.parse(stored);
                this.hiddenSystems = new Set(hiddenIds);
            }
        } catch (error) {
            console.error('Failed to load hidden systems:', error);
        }
    }

    private saveHiddenSystems(): void {
        try {
            const hiddenIds = Array.from(this.hiddenSystems);
            window?.localStorage?.setItem(
                RdioScannerSelectComponent.LOCAL_STORAGE_KEY_HIDDEN_SYSTEMS,
                JSON.stringify(hiddenIds)
            );
        } catch (error) {
            console.error('Failed to save hidden systems:', error);
        }
    }

    private applySystemStatus(system: RdioScannerSystem, status: boolean): void {
        this.avoid({ system, status });
    }

    private applyTagStatus(systemId: number, tag: string, talkgroups: RdioScannerTalkgroup[], status: boolean): void {
        const system = this.systems?.find(s => s.id === systemId);
        if (!system) return;
        talkgroups.forEach(tg => this.avoid({ system, talkgroup: tg, status }));
    }

    private applyFavoritesStatus(status: boolean): void {
        if (!this.systems || this.favoriteItems.length === 0) return;

        const processedSystems = new Set<number>();
        const processedTags = new Set<string>();

        this.favoriteItems.forEach(fav => {
            if (fav.systemId === undefined) {
                return;
            }

            const system = this.systems!.find(s => s.id === fav.systemId);
            if (!system) return;

            if (fav.type === 'system') {
                if (!processedSystems.has(system.id)) {
                    processedSystems.add(system.id);
                    this.applySystemStatus(system, status);
                }
            } else if (fav.type === 'tag' && fav.tag) {
                const tagKey = `${system.id}-${fav.tag}`;
                if (processedTags.has(tagKey)) {
                    return;
                }
                const tagTalkgroups = (system.talkgroups || []).filter(tg => (tg.tag || 'Untagged') === fav.tag);
                if (tagTalkgroups.length > 0) {
                    processedTags.add(tagKey);
                    this.applyTagStatus(system.id, fav.tag, tagTalkgroups, status);
                }
            } else if (fav.type === 'talkgroup' && fav.talkgroupId !== undefined) {
                const talkgroup = (system.talkgroups || []).find(tg => tg.id === fav.talkgroupId);
                if (talkgroup) {
                    this.avoid({ system, talkgroup, status });
                }
            }
        });
    }

    private getTalkgroupsForTag(system: RdioScannerSystem, tag: string): RdioScannerTalkgroup[] {
        return (system.talkgroups || []).filter(tg => (tg.tag || 'Untagged') === tag);
    }

    private getTalkgroupTag(systemId: number, talkgroupId: number): string | undefined {
        const system = this.systems?.find(s => s.id === systemId);
        const talkgroup = system?.talkgroups?.find(tg => tg.id === talkgroupId);
        return talkgroup?.tag || 'Untagged';
    }

    private eventHandler(event: RdioScannerEvent): void {
        if (event.config) this.systems = event.config.systems;
        if (event.categories) this.categories = event.categories;
        if (event.map) this.map = event.map;
    }
}
