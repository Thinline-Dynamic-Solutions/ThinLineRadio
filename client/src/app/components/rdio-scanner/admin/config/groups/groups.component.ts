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

import { CdkDragDrop, moveItemInArray } from '@angular/cdk/drag-drop';
import { Component, Input, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { FormArray, FormGroup } from '@angular/forms';
import { PageEvent } from '@angular/material/paginator';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-groups',
    templateUrl: './groups.component.html',
    styleUrls: ['./groups.component.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAdminGroupsComponent {
    @Input() form: FormArray | undefined;
    @Input() originalConfig: any;

    displayedColumns: string[] = ['drag', 'label', 'usage', 'id', 'actions'];

    /** Rows per column; page shows left+right (= 20). */
    readonly columnSize = 10;
    readonly pageSize = 20;
    pageIndex = 0;
    searchQuery = '';

    saving = false;

    get groups(): FormGroup[] {
        return (this.form?.controls as FormGroup[] | undefined)
            ?.slice()
            .sort((a, b) => (a.value.order || 0) - (b.value.order || 0)) ?? [];
    }

    get filteredGroups(): FormGroup[] {
        const q = this.searchQuery.trim().toLowerCase();
        if (!q) {
            return this.groups;
        }
        return this.groups.filter((group) => {
            const label = String(group.value.label || '').toLowerCase();
            const id = String(group.value.id ?? '');
            return label.includes(q) || id.includes(q);
        });
    }

    get leftGroups(): FormGroup[] {
        const start = this.pageIndex * this.pageSize;
        return this.filteredGroups.slice(start, start + this.columnSize);
    }

    get rightGroups(): FormGroup[] {
        const start = this.pageIndex * this.pageSize + this.columnSize;
        return this.filteredGroups.slice(start, start + this.columnSize);
    }

    get showDualColumns(): boolean {
        return this.rightGroups.length > 0;
    }

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
        private cdr: ChangeDetectorRef,
    ) { }

    onSearch(value: string): void {
        this.searchQuery = value ?? '';
        this.pageIndex = 0;
        this.cdr.markForCheck();
    }

    onPage(event: PageEvent): void {
        this.pageIndex = event.pageIndex;
        this.cdr.markForCheck();
    }

    private clampPage(): void {
        const maxPage = Math.max(0, Math.ceil(this.filteredGroups.length / this.pageSize) - 1);
        if (this.pageIndex > maxPage) {
            this.pageIndex = maxPage;
        }
    }

    private columnOffset(listId: string): number {
        return listId.endsWith('-right') ? this.columnSize : 0;
    }

    isGroupUnused(groupId: number): boolean {
        if (!this.originalConfig || !this.originalConfig.systems) return false;

        // Check original config data instead of FormArray
        for (const system of this.originalConfig.systems) {
            if (system.talkgroups && Array.isArray(system.talkgroups)) {
                for (const talkgroup of system.talkgroups) {
                    const groupIds = talkgroup.groupIds || talkgroup.group;
                    if (Array.isArray(groupIds) && groupIds.includes(groupId)) {
                        return false;
                    }
                }
            }
        }

        return true;
    }

    add(): void {
        const group = this.adminService.newGroupForm();

        group.markAsDirty({ onlySelf: false });

        this.form?.insert(0, group);

        this.form?.markAsDirty();
        this.pageIndex = 0;
        this.cdr.markForCheck();
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (this.searchQuery.trim()) {
            return;
        }
        const all = this.groups;
        const pageStart = this.pageIndex * this.pageSize;
        const from = pageStart
            + this.columnOffset(event.previousContainer.id)
            + event.previousIndex;
        const to = pageStart
            + this.columnOffset(event.container.id)
            + event.currentIndex;
        if (from === to || from < 0 || to < 0 || from >= all.length) {
            return;
        }
        const clampedTo = Math.min(to, all.length - 1);
        moveItemInArray(all, from, clampedTo);
        all.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));

        this.form?.markAsDirty();
        this.saveAll(false);
    }

    remove(group: FormGroup): void {
        const index = this.form?.controls.indexOf(group) ?? -1;
        if (index < 0) {
            return;
        }
        const label = (group.get('label')?.value || '').toString().trim() || 'this group';
        if (!confirm(`Are you sure you want to delete group "${label}"?`)) {
            return;
        }
        this.form?.removeAt(index);

        this.form?.markAsDirty();
        this.clampPage();
        this.saveAll(false);
    }

    /**
     * API-driven save: PUT /api/admin/talkgroup-groups with the full list.
     * Auto-invoked for reorder/remove/cleanup; the Save button covers label text edits.
     */
    async saveAll(showToast = true): Promise<void> {
        if (!this.form) return;
        if (this.form.invalid) {
            if (showToast) {
                this.snackBar.open('Fix the highlighted fields before saving.', 'Close', { duration: 4000 });
            }
            return;
        }

        this.saving = true;
        const updated = await this.adminService.saveGroups(this.form.getRawValue());
        this.saving = false;

        if (updated) {
            this.form.markAsPristine();
            if (showToast) {
                this.snackBar.open('Talkgroup groups saved', 'Close', { duration: 1500 });
            }
        } else if (showToast) {
            this.snackBar.open('Failed to save talkgroup groups. Please try again.', 'Close', { duration: 4000 });
        }
    }

    cleanupUnused(): void {
        if (!this.form || !this.originalConfig?.systems) return;

        const usedGroupIds = new Set<number>();
        for (const system of this.originalConfig.systems) {
            if (!system.talkgroups || !Array.isArray(system.talkgroups)) {
                continue;
            }
            for (const talkgroup of system.talkgroups) {
                const groupIds = talkgroup.groupIds || talkgroup.group;
                if (Array.isArray(groupIds)) {
                    groupIds.forEach((id: number) => usedGroupIds.add(id));
                }
            }
        }

        const unusedIndexes: number[] = [];
        for (let i = 0; i < this.form.controls.length; i++) {
            const groupId = this.form.at(i).get('id')?.value;
            if (groupId && !usedGroupIds.has(groupId)) {
                unusedIndexes.push(i);
            }
        }
        if (unusedIndexes.length === 0) {
            this.snackBar.open('No unused groups to remove.', 'Close', { duration: 2500 });
            return;
        }
        if (!confirm(`Are you sure you want to delete ${unusedIndexes.length} unused group${unusedIndexes.length > 1 ? 's' : ''}?`)) {
            return;
        }

        for (let i = unusedIndexes.length - 1; i >= 0; i--) {
            this.form.removeAt(unusedIndexes[i]);
        }

        this.form.markAsDirty();
        this.clampPage();
        this.saveAll(false);
    }
}
