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
    selector: 'rdio-scanner-admin-tags',
    templateUrl: './tags.component.html',
    styleUrls: ['./tags.component.scss'],
})
export class RdioScannerAdminTagsComponent {
    @Input() form: FormArray | undefined;

    displayedColumns = ['drag', 'color', 'label', 'usage', 'delete'];

    readonly colorOptions = [
        { value: '',        label: 'None (White)',  hex: '#ffffff' },
        { value: '#ff1744', label: 'Red',           hex: '#ff1744' },
        { value: '#ff9100', label: 'Orange',        hex: '#ff9100' },
        { value: '#ffea00', label: 'Yellow',        hex: '#ffea00' },
        { value: '#00e676', label: 'Green',         hex: '#00e676' },
        { value: '#00e5ff', label: 'Cyan',          hex: '#00e5ff' },
        { value: '#2979ff', label: 'Blue',          hex: '#2979ff' },
        { value: '#d500f9', label: 'Magenta',       hex: '#d500f9' },
        { value: '#9e9e9e', label: 'Gray',          hex: '#9e9e9e' },
        { value: '#ffffff', label: 'White',         hex: '#ffffff' },
    ];

    constructor(private adminService: RdioScannerAdminService) {}

    get tags(): FormGroup[] {
        if (!this.form) return [];
        return (this.form.controls as FormGroup[])
            .slice()
            .sort((a, b) => (a.value.order || 0) - (b.value.order || 0));
    }

    isTagUnused(tagId: number): boolean {
        if (!this.form) return false;
        const systemsArray = this.form.root.get('systems') as FormArray;
        if (!systemsArray) return true;
        for (const sys of systemsArray.controls) {
            const tgs = sys.get('talkgroups') as FormArray;
            if (tgs) {
                for (const tg of tgs.controls) {
                    if (tg.get('tagId')?.value === tagId) return false;
                }
            }
        }
        return true;
    }

    getColorLabel(hex: string): string {
        return this.colorOptions.find(c => c.value === hex)?.label ?? (hex || 'None (White)');
    }

    add(): void {
        const tag = this.adminService.newTagForm();
        tag.markAllAsTouched();
        tag.markAsDirty();
        this.form?.insert(0, tag);
        this.form?.markAsDirty();
    }

    remove(index: number): void {
        this.form?.removeAt(index);
        this.form?.markAsDirty();
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);
        event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));
        this.form?.markAsDirty();
    }

    cleanupUnused(): void {
        if (!this.form) return;
        const systemsArray = this.form.root.get('systems') as FormArray;
        if (!systemsArray) return;
        const usedTagIds = new Set<number>();
        systemsArray.controls.forEach(sys => {
            const tgs = sys.get('talkgroups') as FormArray;
            if (tgs) {
                tgs.controls.forEach(tg => {
                    const id = tg.get('tagId')?.value;
                    if (id) usedTagIds.add(id);
                });
            }
        });
        for (let i = this.form.controls.length - 1; i >= 0; i--) {
            const id = this.form.at(i).get('id')?.value;
            if (id && !usedTagIds.has(id)) this.form.removeAt(i);
        }
        this.form.markAsDirty();
    }
}
