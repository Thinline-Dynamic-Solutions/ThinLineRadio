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

import { CdkDragDrop, moveItemInArray } from '@angular/cdk/drag-drop';
import { Component, Input, QueryList, ViewChildren } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { FormArray, FormGroup } from '@angular/forms';
import { MatExpansionPanel } from '@angular/material/expansion';
import { MatSelectChange } from '@angular/material/select';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-tags',
    templateUrl: './tags.component.html',
})
export class RdioScannerAdminTagsComponent {
    @Input() form: FormArray | undefined;

    get tags(): FormGroup[] {
        return this.form?.controls
            .sort((a, b) => a.value.order - b.value.order) as FormGroup[];
    }

    @ViewChildren(MatExpansionPanel) private panels: QueryList<MatExpansionPanel> | undefined;

    constructor(private adminService: RdioScannerAdminService, private matDialog: MatDialog) {
    }

    isTagUnused(tagId: number): boolean {
        if (!this.form) return false;

        // Get all systems and their talkgroups from the root form
        const systemsArray = this.form.root.get('systems') as FormArray;
        if (!systemsArray) return true;

        // Check if this tag ID is used in any talkgroup
        for (const systemControl of systemsArray.controls) {
            const talkgroupsArray = systemControl.get('talkgroups') as FormArray;
            if (talkgroupsArray) {
                for (const talkgroupControl of talkgroupsArray.controls) {
                    const talkgroupTagId = talkgroupControl.get('tagId')?.value;
                    if (talkgroupTagId === tagId) {
                        return false;
                    }
                }
            }
        }

        return true;
    }

    add(): void {
        const tag = this.adminService.newTagForm();

        tag.markAllAsTouched();

        this.form?.insert(0, tag);

        this.form?.markAsDirty();
    }

    closeAll(): void {
        this.panels?.forEach((panel) => panel.close());
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

        // Get all systems and their talkgroups from the root form
        const systemsArray = this.form.root.get('systems') as FormArray;
        if (!systemsArray) return;

        // Collect all tag IDs that are actually used in talkgroups
        const usedTagIds = new Set<number>();
        systemsArray.controls.forEach((systemControl) => {
            const talkgroupsArray = systemControl.get('talkgroups') as FormArray;
            if (talkgroupsArray) {
                talkgroupsArray.controls.forEach((talkgroupControl) => {
                    const tagId = talkgroupControl.get('tagId')?.value;
                    if (tagId) {
                        usedTagIds.add(tagId);
                    }
                });
            }
        });

        // Remove tags that aren't used, starting from the end to avoid index issues
        for (let i = this.form.controls.length - 1; i >= 0; i--) {
            const tagId = this.form.at(i).get('id')?.value;
            if (tagId && !usedTagIds.has(tagId)) {
                this.form.removeAt(i);
            }
        }

        if (this.form.dirty) {
            this.form.markAsDirty();
        }
    }

    getColorName(hex: string): string {
        const colorMap: { [key: string]: string } = {
            '#ff1744': 'Red',
            '#ff9100': 'Orange',
            '#ffea00': 'Yellow',
            '#00e676': 'Green',
            '#00e5ff': 'Cyan',
            '#2979ff': 'Blue',
            '#d500f9': 'Magenta',
            '#9e9e9e': 'Gray',
            '#ffffff': 'White'
        };
        return colorMap[hex.toLowerCase()] || hex;
    }
}
