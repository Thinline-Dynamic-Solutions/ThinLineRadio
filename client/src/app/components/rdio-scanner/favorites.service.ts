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

import { Injectable, OnDestroy } from '@angular/core';
import { BehaviorSubject, Observable, Subscription } from 'rxjs';
import { SettingsService } from './settings/settings.service';
import { RdioScannerConfig, RdioScannerEvent, RdioScannerSystem } from './rdio-scanner';
import { RdioScannerService } from './rdio-scanner.service';

export interface FavoriteItem {
    type: 'system' | 'tag' | 'talkgroup';
    systemId?: number;
    tag?: string;
    talkgroupId?: number;
}

export interface FavoritesConfig {
    [key: string]: FavoriteItem; // key is like "system:1", "tag:system:1:Fire", "talkgroup:system:1:123"
}

@Injectable()
export class FavoritesService implements OnDestroy {
    private favorites$ = new BehaviorSubject<Set<string>>(new Set());
    private favorites: Set<string> = new Set();
    private configSubscription?: Subscription;

    constructor(
        private settingsService: SettingsService,
        private rdioScannerService: RdioScannerService,
    ) {
        this.loadFavorites();
        
        // Subscribe to config events to get user settings
        this.configSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => {
            if (event.config?.userSettings?.['favorites']) {
                const favoritesList = event.config.userSettings['favorites'] as FavoriteItem[];
                this.applyFavoritesList(favoritesList, event.config.systems);
            }
        });
    }

    ngOnDestroy(): void {
        if (this.configSubscription) {
            this.configSubscription.unsubscribe();
        }
    }

    private getFavoriteKey(item: FavoriteItem): string {
        if (item.type === 'system' && item.systemId !== undefined) {
            return `system:${item.systemId}`;
        } else if (item.type === 'tag' && item.systemId !== undefined && item.tag) {
            return `tag:${item.systemId}:${item.tag}`;
        } else if (item.type === 'talkgroup' && item.systemId !== undefined && item.talkgroupId !== undefined) {
            return `talkgroup:${item.systemId}:${item.talkgroupId}`;
        }
        return '';
    }

    getFavorites(): Observable<Set<string>> {
        return this.favorites$.asObservable();
    }

    getAllFavorites(): Set<string> {
        return new Set(this.favorites);
    }

    isFavorite(item: FavoriteItem): boolean {
        const key = this.getFavoriteKey(item);
        return this.favorites.has(key);
    }

    isSystemFavorite(systemId: number): boolean {
        return this.isFavorite({ type: 'system', systemId });
    }

    isTagFavorite(systemId: number, tag: string): boolean {
        return this.isFavorite({ type: 'tag', systemId, tag });
    }

    isTalkgroupFavorite(systemId: number, talkgroupId: number): boolean {
        return this.isFavorite({ type: 'talkgroup', systemId, talkgroupId });
    }

    addFavorite(item: FavoriteItem): void {
        const key = this.getFavoriteKey(item);
        if (key && !this.favorites.has(key)) {
            this.favorites.add(key);
            this.favorites$.next(new Set(this.favorites));
            this.saveFavorites();
        }
    }

    addFavorites(items: FavoriteItem[]): void {
        let changed = false;
        items.forEach(item => {
            const key = this.getFavoriteKey(item);
            if (key && !this.favorites.has(key)) {
                this.favorites.add(key);
                changed = true;
            }
        });
        if (changed) {
            this.favorites$.next(new Set(this.favorites));
            this.saveFavorites();
        }
    }

    removeFavorite(item: FavoriteItem): void {
        const key = this.getFavoriteKey(item);
        if (this.favorites.has(key)) {
            this.favorites.delete(key);
            this.favorites$.next(new Set(this.favorites));
            this.saveFavorites();
        }
    }

    removeFavorites(items: FavoriteItem[]): void {
        let changed = false;
        items.forEach(item => {
            const key = this.getFavoriteKey(item);
            if (this.favorites.has(key)) {
                this.favorites.delete(key);
                changed = true;
            }
        });
        if (changed) {
            this.favorites$.next(new Set(this.favorites));
            this.saveFavorites();
        }
    }

    toggleFavorite(item: FavoriteItem): void {
        if (this.isFavorite(item)) {
            this.removeFavorite(item);
        } else {
            this.addFavorite(item);
        }
    }

    /**
     * Drop parent tag/system favorites that are not backed by every talkgroup
     * in that tag/system (left over from the old single-TG star bug).
     */
    normalizeFavoriteItems(items: FavoriteItem[], systems: RdioScannerSystem[]): FavoriteItem[] {
        const talkgroupKeys = new Set(
            items
                .filter(i => i.type === 'talkgroup' && i.systemId !== undefined && i.talkgroupId !== undefined)
                .map(i => `${i.systemId}:${i.talkgroupId}`),
        );
        const result: FavoriteItem[] = [
            ...items.filter(i => i.type === 'talkgroup'),
        ];

        for (const item of items) {
            if (item.type !== 'tag' || item.systemId === undefined || !item.tag) {
                continue;
            }
            const system = systems.find(s => s.id === item.systemId);
            if (!system) {
                continue;
            }
            const tagTalkgroups = (system.talkgroups || []).filter(
                tg => (tg.tag || 'Untagged') === item.tag,
            );
            if (tagTalkgroups.length > 0 && tagTalkgroups.every(tg => talkgroupKeys.has(`${item.systemId}:${tg.id}`))) {
                result.push(item);
            }
        }

        for (const item of items) {
            if (item.type !== 'system' || item.systemId === undefined) {
                continue;
            }
            const system = systems.find(s => s.id === item.systemId);
            if (!system) {
                continue;
            }
            const allTalkgroups = system.talkgroups || [];
            if (allTalkgroups.length > 0 && allTalkgroups.every(tg => talkgroupKeys.has(`${item.systemId}:${tg.id}`))) {
                result.push(item);
            }
        }

        return result;
    }

    private applyFavoritesList(favoritesList: FavoriteItem[], systems?: RdioScannerSystem[]): void {
        const normalized = systems?.length
            ? this.normalizeFavoriteItems(favoritesList, systems)
            : favoritesList;
        const oldKeys = new Set(favoritesList.map(f => this.getFavoriteKey(f)).filter(Boolean));
        const newKeys = new Set(normalized.map(f => this.getFavoriteKey(f)).filter(Boolean));
        const changed = oldKeys.size !== newKeys.size || [...oldKeys].some(k => !newKeys.has(k));

        this.favorites = newKeys;
        this.favorites$.next(new Set(this.favorites));

        if (changed && systems?.length) {
            this.saveFavorites();
        }
    }

    getFavoriteItems(): FavoriteItem[] {
        const items: FavoriteItem[] = [];
        this.favorites.forEach(key => {
            const parts = key.split(':');
            if (parts[0] === 'system' && parts.length === 2) {
                items.push({ type: 'system', systemId: parseInt(parts[1]) });
            } else if (parts[0] === 'tag' && parts.length === 3) {
                items.push({ type: 'tag', systemId: parseInt(parts[1]), tag: parts[2] });
            } else if (parts[0] === 'talkgroup' && parts.length === 3) {
                items.push({ type: 'talkgroup', systemId: parseInt(parts[1]), talkgroupId: parseInt(parts[2]) });
            }
        });
        return items;
    }

    private loadFavorites(): void {
        // First, try to load from config (comes with login)
        const currentConfig = this.rdioScannerService.getConfig();
        if (currentConfig?.userSettings?.['favorites']) {
            const favoritesList = currentConfig.userSettings['favorites'] as FavoriteItem[];
            this.applyFavoritesList(favoritesList, currentConfig.systems);
            return;
        }

        // Fallback: try to load from API
        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                if (settings?.favorites && Array.isArray(settings.favorites)) {
                    const favoritesList = settings.favorites as FavoriteItem[];
                    const systems = this.rdioScannerService.getConfig()?.systems;
                    this.applyFavoritesList(favoritesList, systems);
                } else {
                    this.favorites = new Set();
                    this.favorites$.next(new Set(this.favorites));
                }
            },
            error: (error) => {
                console.error('Error loading favorites from settings:', error);
                this.favorites = new Set();
                this.favorites$.next(new Set(this.favorites));
            },
        });
    }

    private saveFavorites(): void {
        const favoriteItems = this.getFavoriteItems();
        
        this.settingsService.getSettings().subscribe({
            next: (currentSettings) => {
                const updatedSettings = {
                    ...currentSettings,
                    favorites: favoriteItems,
                };
                this.settingsService.saveSettings(updatedSettings).subscribe({
                    next: () => {
                        console.log('Favorites saved to settings');
                    },
                    error: (error) => {
                        console.error('Error saving favorites:', error);
                    },
                });
            },
            error: (error) => {
                console.error('Error loading current settings for save:', error);
                // Save just favorites if we can't load current settings
                this.settingsService.saveSettings({ favorites: favoriteItems }).subscribe({
                    error: (saveError) => {
                        console.error('Error saving favorites:', saveError);
                    },
                });
            },
        });
    }
}

