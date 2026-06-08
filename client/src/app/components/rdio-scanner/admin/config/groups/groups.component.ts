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
import { Component, Input } from '@angular/core';
import { FormArray, FormGroup } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-groups',
    templateUrl: './groups.component.html',
    styleUrls: ['./groups.component.scss'],
})
export class RdioScannerAdminGroupsComponent {
    @Input() form: FormArray | undefined;
    @Input() originalConfig: any;

    displayedColumns: string[] = ['drag', 'label', 'usage', 'id', 'actions'];

    saving = false;

    get groups(): FormGroup[] {
        return this.form?.controls
            .sort((a, b) => a.value.order - b.value.order) as FormGroup[];
    }

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
    ) { }

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
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex !== event.currentIndex) {
            moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);

            event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));

            this.form?.markAsDirty();
            this.saveAll(false);
        }
    }

    remove(index: number): void {
        this.form?.removeAt(index);

        this.form?.markAsDirty();
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

        for (let i = this.form.controls.length - 1; i >= 0; i--) {
            const groupId = this.form.at(i).get('id')?.value;
            if (groupId && !usedGroupIds.has(groupId)) {
                this.form.removeAt(i);
            }
        }

        this.form.markAsDirty();
        this.saveAll(false);
    }
}
