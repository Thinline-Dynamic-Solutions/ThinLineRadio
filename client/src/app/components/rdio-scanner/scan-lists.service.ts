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

    createList(name: string): void {
        const newList: ScanList = {
            id: `list-${Date.now()}`,
            name,
            channels: [],
        };
        this.lists = [...this.lists, newList];
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

    updateChannelEnabled(systemId: string, talkgroupId: string, isEnabled: boolean): void {
        this.lists = this.lists.map(l => ({
            ...l,
            channels: l.channels.map(c =>
                c.systemId === systemId && c.talkgroupId === talkgroupId ? { ...c, isEnabled } : c
            ),
        }));
        this.lists$.next([...this.lists]);
    }

    private mergeLists(serverLists: ScanList[]): void {
        // Merge server lists: server is authoritative for non-favorites lists
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
                const updated = { ...current, scanLists: toSave };
                this.settingsService.saveSettings(updated).subscribe({
                    error: (e) => console.error('Error saving scan lists:', e),
                });
            },
            error: (e) => console.error('Error loading settings for scan lists save:', e),
        });
    }
}
