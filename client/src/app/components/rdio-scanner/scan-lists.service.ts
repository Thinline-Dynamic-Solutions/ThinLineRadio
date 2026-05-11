/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 * ****************************************************************************
 */

import { Injectable, OnDestroy } from '@angular/core';
import { BehaviorSubject, Observable, Subscription } from 'rxjs';
import { SettingsService } from './settings/settings.service';
import { RdioScannerEvent } from './rdio-scanner';
import { RdioScannerService } from './rdio-scanner.service';

export interface ScanListChannel {
    systemId: string;
    talkgroupId: string;
    /** Server DB talkgroup row id (`talkgroupId` in config); avoids wrong labels when TGID/ref repeats across systems. */
    talkgroupDbId?: string;
    talkgroupLabel: string;
    talkgroupName: string;
    systemLabel: string;
    tag: string;
    /**
     * Legacy metadata only — left in the schema so older saved settings load
     * without losing data. The actual scanning state of a channel is now read
     * from the live-feed map, so this flag is no longer enforced anywhere.
     */
    isEnabled: boolean;
}

export interface ScanList {
    id: string;
    name: string;
    isFavoritesSource?: boolean;
    channels: ScanListChannel[];
}

@Injectable()
export class ScanListsService implements OnDestroy {
    private lists$ = new BehaviorSubject<ScanList[]>([]);
    private lists: ScanList[] = [];
    private configSubscription?: Subscription;
    private saveDebounceTimer?: ReturnType<typeof setTimeout>;

    constructor(
        private settingsService: SettingsService,
        private rdioScannerService: RdioScannerService,
    ) {
        this.loadLists();

        this.configSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => {
            if (event.config?.userSettings?.['scanLists']) {
                const serverLists = event.config.userSettings['scanLists'] as ScanList[];
                this.mergeLists(serverLists);
            }
        });
    }

    ngOnDestroy(): void {
        this.configSubscription?.unsubscribe();
        if (this.saveDebounceTimer) clearTimeout(this.saveDebounceTimer);
    }

    getLists(): Observable<ScanList[]> {
        return this.lists$.asObservable();
    }

    getListsSnapshot(): ScanList[] {
        return [...this.lists];
    }

    /**
     * Mobile-parity bulk toggle: flips every channel in `list` to `active` in the live feed.
     *
     * This does NOT take over the live-feed map — channels outside the list keep whatever
     * the user already had selected. Turning a list ON enables every channel inside it;
     * turning it OFF disables every channel inside it (even ones that another source
     * had on), which matches the mobile app's `_toggleAllInList` behavior.
     */
    bulkToggleList(listId: string, active: boolean): void {
        const list = this.lists.find((l) => l.id === listId);
        if (!list || list.channels.length === 0) {
            return;
        }
        this.rdioScannerService.setLivefeedActiveForChannels(
            list.channels.map((c) => ({ systemId: c.systemId, talkgroupId: c.talkgroupId })),
            active,
        );
    }

    createList(name: string): ScanList {
        const newList: ScanList = {
            id: `list-${Date.now()}`,
            name,
            channels: [],
        };
        this.lists = [...this.lists, newList];
        this.lists$.next([...this.lists]);
        this.scheduleSave();
        return newList;
    }

    reorderLists(fromIndex: number, toIndex: number): void {
        const lists = [...this.lists];
        const [moved] = lists.splice(fromIndex, 1);
        lists.splice(toIndex, 0, moved);
        this.lists = lists;
        this.lists$.next([...this.lists]);
        this.scheduleSave();
    }

    renameList(listId: string, name: string): void {
        this.lists = this.lists.map(l => l.id === listId ? { ...l, name } : l);
        this.lists$.next([...this.lists]);
        this.scheduleSave();
    }

    deleteList(listId: string): void {
        this.lists = this.lists.filter(l => l.id !== listId);
        this.lists$.next([...this.lists]);
        this.scheduleSave();
    }

    addChannel(listId: string, channel: ScanListChannel): void {
        this.lists = this.lists.map(l => {
            if (l.id !== listId) return l;
            if (l.channels.some(c => c.systemId === channel.systemId && c.talkgroupId === channel.talkgroupId)) return l;
            return { ...l, channels: [...l.channels, channel] };
        });
        this.lists$.next([...this.lists]);
        this.scheduleSave();
    }

    removeChannel(listId: string, systemId: string, talkgroupId: string): void {
        this.lists = this.lists.map(l => {
            if (l.id !== listId) return l;
            return { ...l, channels: l.channels.filter(c => !(c.systemId === systemId && c.talkgroupId === talkgroupId)) };
        });
        this.lists$.next([...this.lists]);
        this.scheduleSave();
    }

    /** Add many channels in one update (e.g. whole tag from the Edit dialog). Skips duplicates. */
    addChannels(listId: string, channels: ScanListChannel[]): void {
        if (channels.length === 0) {
            return;
        }
        this.lists = this.lists.map((l) => {
            if (l.id !== listId) {
                return l;
            }
            const existing = new Set(l.channels.map((c) => `${c.systemId}:${c.talkgroupId}`));
            const merged = [...l.channels];
            for (const c of channels) {
                const k = `${c.systemId}:${c.talkgroupId}`;
                if (!existing.has(k)) {
                    existing.add(k);
                    merged.push(c);
                }
            }
            return { ...l, channels: merged };
        });
        this.lists$.next([...this.lists]);
        this.scheduleSave();
    }

    /** Remove many channels by system/talkgroup ref (e.g. clear a tag from a list). */
    removeChannelsByRefs(listId: string, refs: { systemId: string; talkgroupId: string }[]): void {
        if (refs.length === 0) {
            return;
        }
        const rset = new Set(refs.map((r) => `${r.systemId}:${r.talkgroupId}`));
        this.lists = this.lists.map((l) => {
            if (l.id !== listId) {
                return l;
            }
            return {
                ...l,
                channels: l.channels.filter((c) => !rset.has(`${c.systemId}:${c.talkgroupId}`)),
            };
        });
        this.lists$.next([...this.lists]);
        this.scheduleSave();
    }

    private mergeLists(serverLists: ScanList[]): void {
        // Server is authoritative for non-favorites lists; locally-derived favorites stay put.
        const favorites = this.lists.find(l => l.isFavoritesSource);
        const serverNonFav = serverLists.filter(l => !l.isFavoritesSource);
        this.lists = [...(favorites ? [favorites] : []), ...serverNonFav];
        this.lists$.next([...this.lists]);
    }

    private loadLists(): void {
        const currentConfig = this.rdioScannerService.getConfig();
        if (currentConfig?.userSettings?.['scanLists']) {
            this.lists = currentConfig.userSettings['scanLists'] as ScanList[];
            this.lists$.next([...this.lists]);
            return;
        }

        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                if (settings?.scanLists && Array.isArray(settings.scanLists)) {
                    this.lists = settings.scanLists;
                } else {
                    this.lists = [];
                }
                this.lists$.next([...this.lists]);
            },
            error: () => {
                this.lists = [];
                this.lists$.next([]);
            },
        });
    }

    private scheduleSave(): void {
        if (this.saveDebounceTimer) clearTimeout(this.saveDebounceTimer);
        this.saveDebounceTimer = setTimeout(() => this.saveLists(), 800);
    }

    private saveLists(): void {
        const toSave = this.lists.filter(l => !l.isFavoritesSource);

        this.settingsService.getSettings().subscribe({
            next: (current) => {
                const updated = {
                    ...current,
                    scanLists: toSave,
                    // Wipe legacy "active scan list" persistence — scan lists are now pure
                    // bulk toggles, not a continuously-enforced filter.
                    activeScanListIds: [],
                    activeScanListId: null,
                };
                this.settingsService.saveSettings(updated).subscribe({
                    error: (e) => console.error('Error saving scan lists:', e),
                });
            },
            error: (e) => console.error('Error loading settings for scan lists save:', e),
        });
    }
}
