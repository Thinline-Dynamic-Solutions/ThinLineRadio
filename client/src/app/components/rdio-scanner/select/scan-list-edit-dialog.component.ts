/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * Channel-picker dialog for editing the contents of a scan list.
 *
 * Mobile-app parity: matches `_ChannelPickerSheet` in the Flutter app — a
 * search box on top, an expandable per-system / per-tag tree below, and a
 * tap on any talkgroup row toggles whether it's a member of this list. All
 * scan-list editing happens here; the Channels tab no longer hosts add/remove
 * scan-list buttons.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA } from '@angular/material/dialog';

import { RdioScannerSystem, RdioScannerTalkgroup } from '../rdio-scanner';
import { ScanList, ScanListChannel, ScanListsService } from '../scan-lists.service';
import { TagColorService } from '../tag-color.service';

interface PickerSystem {
    system: RdioScannerSystem;
    talkgroups: RdioScannerTalkgroup[];
    talkgroupsByTag: { tag: string; talkgroups: RdioScannerTalkgroup[] }[];
    matches: number;
}

export interface ScanListEditDialogData {
    list: ScanList;
    systems: RdioScannerSystem[];
}

@Component({
    selector: 'rdio-scanner-scan-list-edit-dialog',
    templateUrl: './scan-list-edit-dialog.component.html',
    styleUrls: ['./scan-list-edit-dialog.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ScanListEditDialogComponent {
    list: ScanList;
    searchQuery = '';
    expandedSystems = new Set<number>();

    constructor(
        @Inject(MAT_DIALOG_DATA) public data: ScanListEditDialogData,
        private scanListsService: ScanListsService,
        private tagColorService: TagColorService,
        private cdRef: ChangeDetectorRef,
    ) {
        this.list = data.list;

        // Mirror mobile: auto-expand all systems when there are only a few.
        if (data.systems.length <= 3) {
            for (const s of data.systems) this.expandedSystems.add(s.id);
        }
    }

    isMember(systemId: number, talkgroupId: number): boolean {
        const sid = String(systemId);
        const tid = String(talkgroupId);
        return this.list.channels.some((c) => c.systemId === sid && c.talkgroupId === tid);
    }

    /**
     * Tap a row → flip its membership in the list. Adding a channel does not
     * touch the live-feed map — the user controls scanning state separately
     * via the bulk-toggle checkbox on the list card or per-row toggles.
     */
    toggleChannel(system: RdioScannerSystem, talkgroup: RdioScannerTalkgroup): void {
        const sid = String(system.id);
        const tid = String(talkgroup.id);
        if (this.isMember(system.id, talkgroup.id)) {
            this.scanListsService.removeChannel(this.list.id, sid, tid);
        } else {
            this.scanListsService.addChannel(this.list.id, {
                systemId: sid,
                talkgroupId: tid,
                talkgroupDbId:
                    talkgroup.talkgroupId !== undefined && talkgroup.talkgroupId !== null
                        ? String(talkgroup.talkgroupId)
                        : undefined,
                talkgroupLabel: talkgroup.label || '',
                talkgroupName: talkgroup.name || '',
                systemLabel: system.label || '',
                tag: talkgroup.tag || 'Untagged',
                isEnabled: true,
            });
        }
        // Pick up the updated list from the service so the membership dots refresh.
        const updated = this.scanListsService.getListsSnapshot().find((l) => l.id === this.list.id);
        if (updated) this.list = updated;
        this.cdRef.markForCheck();
    }

    toggleSystemExpanded(systemId: number): void {
        if (this.expandedSystems.has(systemId)) this.expandedSystems.delete(systemId);
        else this.expandedSystems.add(systemId);
        this.cdRef.markForCheck();
    }

    isSystemExpanded(systemId: number): boolean {
        return this.expandedSystems.has(systemId);
    }

    onSearchChange(): void {
        this.cdRef.markForCheck();
    }

    clearSearch(): void {
        this.searchQuery = '';
        this.cdRef.markForCheck();
    }

    /**
     * Tap a tag header → bulk add or remove every TG in that tag, mirroring
     * the way mobile lets you sweep an entire tag in/out of a list at once.
     */
    toggleTag(system: RdioScannerSystem, tag: string, talkgroups: RdioScannerTalkgroup[]): void {
        const sid = String(system.id);
        const refs = talkgroups.map((tg) => ({ systemId: sid, talkgroupId: String(tg.id) }));
        const allIn = talkgroups.every((tg) => this.isMember(system.id, tg.id));
        if (allIn) {
            this.scanListsService.removeChannelsByRefs(this.list.id, refs);
        } else {
            const toAdd: ScanListChannel[] = talkgroups
                .filter((tg) => !this.isMember(system.id, tg.id))
                .map((tg) => ({
                    systemId: sid,
                    talkgroupId: String(tg.id),
                    talkgroupDbId:
                        tg.talkgroupId !== undefined && tg.talkgroupId !== null ? String(tg.talkgroupId) : undefined,
                    talkgroupLabel: tg.label || '',
                    talkgroupName: tg.name || '',
                    systemLabel: system.label || '',
                    tag: tg.tag || tag || 'Untagged',
                    isEnabled: true,
                }));
            this.scanListsService.addChannels(this.list.id, toAdd);
        }
        const updated = this.scanListsService.getListsSnapshot().find((l) => l.id === this.list.id);
        if (updated) this.list = updated;
        this.cdRef.markForCheck();
    }

    tagState(system: RdioScannerSystem, talkgroups: RdioScannerTalkgroup[]): 'all' | 'some' | 'none' {
        if (!talkgroups.length) return 'none';
        let n = 0;
        for (const tg of talkgroups) if (this.isMember(system.id, tg.id)) n++;
        if (n === 0) return 'none';
        if (n === talkgroups.length) return 'all';
        return 'some';
    }

    tagStateIcon(state: 'all' | 'some' | 'none'): string {
        if (state === 'all') return 'check_box';
        if (state === 'some') return 'indeterminate_check_box';
        return 'check_box_outline_blank';
    }

    /**
     * Pre-bake the per-system / per-tag layout, with optional search filtering.
     * Systems and tags with zero matching talkgroups are dropped from the result.
     */
    getPickerSystems(): PickerSystem[] {
        const q = this.searchQuery.trim().toLowerCase();
        const filterTg = (tg: RdioScannerTalkgroup): boolean => {
            if (!q) return true;
            const label = (tg.label || '').toLowerCase();
            const name = (tg.name || '').toLowerCase();
            const id = String(tg.id);
            return label.includes(q) || name.includes(q) || id.includes(q);
        };

        const out: PickerSystem[] = [];
        for (const system of this.data.systems) {
            const tgs = (system.talkgroups || []).filter(filterTg);
            if (q && tgs.length === 0) continue;

            const byTag = new Map<string, RdioScannerTalkgroup[]>();
            for (const tg of tgs) {
                const tag = tg.tag || 'Untagged';
                if (!byTag.has(tag)) byTag.set(tag, []);
                byTag.get(tag)!.push(tg);
            }
            const tagGroups = Array.from(byTag.entries())
                .sort((a, b) => {
                    if (a[0] === 'Untagged') return 1;
                    if (b[0] === 'Untagged') return -1;
                    return a[0].localeCompare(b[0]);
                })
                .map(([tag, talkgroups]) => ({
                    tag,
                    talkgroups: talkgroups.sort((a, b) => (a.label || '').localeCompare(b.label || '')),
                }));

            out.push({
                system,
                talkgroups: tgs,
                talkgroupsByTag: tagGroups,
                matches: tgs.length,
            });
        }
        return out.sort((a, b) => (a.system.label || '').localeCompare(b.system.label || ''));
    }

    getTagColor(tag: string): string {
        return this.tagColorService.getTagColor(tag);
    }

    membershipCount(): number {
        return this.list.channels.length;
    }

    trackBySystemId(_: number, item: PickerSystem): number {
        return item.system.id;
    }

    trackByTag(_: number, item: { tag: string }): string {
        return item.tag;
    }

    trackByTalkgroupId(_: number, tg: RdioScannerTalkgroup): number {
        return tg.id;
    }
}
