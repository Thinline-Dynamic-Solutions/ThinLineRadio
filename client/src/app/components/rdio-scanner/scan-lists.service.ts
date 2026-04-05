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
    talkgroupLabel: string;
    talkgroupName: string;
    systemLabel: string;
    tag: string;
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
    /** Lists whose enabled channels are unioned to drive the live feed (web client). */
    private activeScanListIds = new Set<string>();
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
            if (event.config?.userSettings && this.userSettingsHasActiveScanKeys(event.config.userSettings)) {
                this.parseActiveScanListIdsFromUserSettings(event.config.userSettings);
                this.pruneActiveScanListIds();
                this.tryApplyActiveScanList();
            }
        });
    }

    private userSettingsHasActiveScanKeys(us: Record<string, unknown>): boolean {
        return 'activeScanListIds' in us || 'activeScanListId' in us;
    }

    private parseActiveScanListIdsFromUserSettings(us: Record<string, unknown>): void {
        const ids = us['activeScanListIds'];
        const legacy = us['activeScanListId'];
        if (Array.isArray(ids)) {
            this.activeScanListIds = new Set(
                ids.filter((x): x is string => typeof x === 'string' && x.length > 0),
            );
            return;
        }
        if (typeof legacy === 'string' && legacy !== '') {
            this.activeScanListIds = new Set([legacy]);
            return;
        }
        this.activeScanListIds = new Set();
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

    getActiveScanListIds(): string[] {
        return [...this.activeScanListIds];
    }

    /**
     * Include or exclude one list from the live-feed scan set. Multiple lists can be on; enabled
     * channels are unioned (a talkgroup is on if it is enabled in any active list).
     */
    setScanListScanningEnabled(listId: string, enabled: boolean): void {
        const had = this.activeScanListIds.has(listId);
        if (enabled === had) {
            return;
        }
        if (enabled) {
            this.activeScanListIds.add(listId);
        } else {
            this.activeScanListIds.delete(listId);
        }
        this.scheduleSave();
        this.tryApplyActiveScanList();
    }

    isActiveScanList(listId: string): boolean {
        return this.activeScanListIds.has(listId);
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
        this.activeScanListIds.delete(listId);
        this.lists$.next([...this.lists]);
        this.scheduleSave();
        this.tryApplyActiveScanList();
    }

    addChannel(listId: string, channel: ScanListChannel): void {
        this.lists = this.lists.map(l => {
            if (l.id !== listId) return l;
            if (l.channels.some(c => c.systemId === channel.systemId && c.talkgroupId === channel.talkgroupId)) return l;
            return { ...l, channels: [...l.channels, channel] };
        });
        this.lists$.next([...this.lists]);
        this.scheduleSave();
        if (this.activeScanListIds.size > 0) {
            this.tryApplyActiveScanList();
        }
    }

    removeChannel(listId: string, systemId: string, talkgroupId: string): void {
        this.lists = this.lists.map(l => {
            if (l.id !== listId) return l;
            return { ...l, channels: l.channels.filter(c => !(c.systemId === systemId && c.talkgroupId === talkgroupId)) };
        });
        this.lists$.next([...this.lists]);
        this.scheduleSave();
        if (this.activeScanListIds.size > 0) {
            this.tryApplyActiveScanList();
        }
    }

    /** Add many channels in one update (e.g. whole tag from Channels UI). Skips duplicates. */
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
        if (this.activeScanListIds.size > 0) {
            this.tryApplyActiveScanList();
        }
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
        if (this.activeScanListIds.size > 0) {
            this.tryApplyActiveScanList();
        }
    }

    updateChannelEnabled(systemId: string, talkgroupId: string, isEnabled: boolean): void {
        this.lists = this.lists.map(l => ({
            ...l,
            channels: l.channels.map(c =>
                c.systemId === systemId && c.talkgroupId === talkgroupId ? { ...c, isEnabled } : c
            ),
        }));
        this.lists$.next([...this.lists]);
        this.scheduleSave();
        this.tryApplyActiveScanList();
    }

    private mergeLists(serverLists: ScanList[]): void {
        // Merge server lists: server is authoritative for non-favorites lists
        const favorites = this.lists.find(l => l.isFavoritesSource);
        const serverNonFav = serverLists.filter(l => !l.isFavoritesSource);
        this.lists = [...(favorites ? [favorites] : []), ...serverNonFav];
        this.pruneActiveScanListIds();
        this.lists$.next([...this.lists]);
        this.tryApplyActiveScanList();
    }

    private pruneActiveScanListIds(): void {
        const valid = new Set(this.lists.map((l) => l.id));
        this.activeScanListIds = new Set([...this.activeScanListIds].filter((id) => valid.has(id)));
    }

    private tryApplyActiveScanList(): void {
        if (!this.rdioScannerService.getConfig()?.systems?.length) {
            return;
        }
        if (this.activeScanListIds.size === 0) {
            return;
        }

        const onKeys = new Set<string>();
        for (const listId of this.activeScanListIds) {
            const list = this.lists.find((l) => l.id === listId);
            if (!list) {
                continue;
            }
            for (const ch of list.channels) {
                if (ch.isEnabled) {
                    onKeys.add(`${ch.systemId}:${ch.talkgroupId}`);
                }
            }
        }

        const merged: ScanListChannel[] = [];
        for (const k of onKeys) {
            let sample: ScanListChannel | undefined;
            for (const listId of this.activeScanListIds) {
                const list = this.lists.find((l) => l.id === listId);
                const ch = list?.channels.find(
                    (c) => `${c.systemId}:${c.talkgroupId}` === k,
                );
                if (ch) {
                    sample = ch;
                    break;
                }
            }
            const [systemId, talkgroupId] = k.split(':');
            merged.push(
                sample ?? {
                    systemId,
                    talkgroupId,
                    talkgroupLabel: '',
                    talkgroupName: '',
                    systemLabel: '',
                    tag: '',
                    isEnabled: true,
                },
            );
        }

        this.rdioScannerService.applyScanListChannelsToLivefeed(merged);
    }

    private loadLists(): void {
        const currentConfig = this.rdioScannerService.getConfig();
        if (currentConfig?.userSettings?.['scanLists']) {
            this.lists = currentConfig.userSettings['scanLists'] as ScanList[];
            this.parseActiveScanListIdsFromUserSettings(currentConfig.userSettings as Record<string, unknown>);
            this.pruneActiveScanListIds();
            this.lists$.next([...this.lists]);
            this.tryApplyActiveScanList();
            return;
        }

        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                if (settings?.scanLists && Array.isArray(settings.scanLists)) {
                    this.lists = settings.scanLists;
                } else {
                    this.lists = [];
                }
                this.parseActiveScanListIdsFromUserSettings(settings as Record<string, unknown>);
                this.pruneActiveScanListIds();
                this.lists$.next([...this.lists]);
                this.tryApplyActiveScanList();
            },
            error: () => {
                this.lists = [];
                this.activeScanListIds = new Set();
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
                    activeScanListIds: [...this.activeScanListIds],
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
