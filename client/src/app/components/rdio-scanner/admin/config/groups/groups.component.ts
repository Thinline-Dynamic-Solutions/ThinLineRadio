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
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-groups',
    templateUrl: './groups.component.html',
    styleUrls: ['./groups.component.scss'],
})
export class RdioScannerAdminGroupsComponent {
    @Input() form: FormArray | undefined;

    displayedColumns: string[] = ['drag', 'label', 'usage', 'id', 'actions'];

    get groups(): FormGroup[] {
        return this.form?.controls
            .sort((a, b) => a.value.order - b.value.order) as FormGroup[];
    }

    constructor(private adminService: RdioScannerAdminService) { }

    isGroupUnused(groupId: number): boolean {
        if (!this.form) return false;

        const systemsArray = this.form.root.get('systems') as FormArray;
        if (!systemsArray) return true;

        for (const systemControl of systemsArray.controls) {
            const talkgroupsArray = systemControl.get('talkgroups') as FormArray;
            if (talkgroupsArray) {
                for (const talkgroupControl of talkgroupsArray.controls) {
                    const groupIds = talkgroupControl.get('groupIds')?.value;
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

        group.markAllAsTouched();

        this.form?.insert(0, group);

        this.form?.markAsDirty();
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex !== event.currentIndex) {
            moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);

            event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));

            this.form?.markAsDirty();
        }
    }

    remove(index: number): void {
        this.form?.removeAt(index);

        this.form?.markAsDirty();
    }

    cleanupUnused(): void {
        if (!this.form) return;

        const systemsArray = this.form.root.get('systems') as FormArray;
        if (!systemsArray) return;

        const usedGroupIds = new Set<number>();
        systemsArray.controls.forEach((systemControl) => {
            const talkgroupsArray = systemControl.get('talkgroups') as FormArray;
            if (talkgroupsArray) {
                talkgroupsArray.controls.forEach((talkgroupControl) => {
                    const groupIds = talkgroupControl.get('groupIds')?.value;
                    if (Array.isArray(groupIds)) {
                        groupIds.forEach((id: number) => usedGroupIds.add(id));
                    }
                });
            }
        });

        for (let i = this.form.controls.length - 1; i >= 0; i--) {
            const groupId = this.form.at(i).get('id')?.value;
            if (groupId && !usedGroupIds.has(groupId)) {
                this.form.removeAt(i);
            }
        }

        if (this.form.dirty) {
            this.form.markAsDirty();
        }
    }
}
